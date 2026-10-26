package portal

import (
	"errors"
	"net/http"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

const creditSelect = `SELECT c.user_id,c.account_id,c.plan_id,c.mode,c.remaining_units,c.available_micro,c.settled_units,c.settled_micro,c.revision,
 COALESCE((SELECT sum(credit_units) FROM portal_charges WHERE credit_job_id=c.job_id AND credit_mode=c.mode AND state='reserved' AND funding_source='prepaid'),0),
 COALESCE((SELECT sum(amount_micro) FROM portal_charges WHERE credit_job_id=c.job_id AND credit_mode=c.mode AND state='reserved' AND funding_source='prepaid'),0)
 FROM portal_task_credits c`

func scanTaskCredit(row pgx.Row, c *taskCredit) error {
	e := row.Scan(&c.userID, &c.accountID, &c.planID, &c.Mode, &c.RemainingUnits, &c.AvailableMicro, &c.SettledUnits, &c.SettledMicro, &c.Revision, &c.ReservedUnits, &c.ReservedMicro)
	c.TotalUnits = c.RemainingUnits + c.ReservedUnits + c.SettledUnits
	return e
}

type taskAdjustmentInput struct {
	RequestID        string `json:"request_id"`
	Mode             string `json:"mode"`
	TotalUnits       int64  `json:"total_units"`
	ExpectedRevision int64  `json:"expected_revision"`
	ExpectedDelta    *int64 `json:"expected_delta_micro"`
}
type taskQuote struct {
	Mode         string `json:"mode"`
	TotalUnits   int64  `json:"total_units"`
	MinimumUnits int64  `json:"minimum_units"`
	DeltaUnits   int64  `json:"delta_units"`
	DeltaMicro   int64  `json:"delta_micro"`
	Revision     int64  `json:"revision"`
	BalanceMicro int64  `json:"balance_micro"`
}

func quoteTaskAdjustment(c taskCredit, p Pricing, mode string, target int64) (taskQuote, error) {
	q := taskQuote{Mode: mode, TotalUnits: target, MinimumUnits: c.SettledUnits + c.ReservedUnits, Revision: c.Revision}
	if mode != "per_run" && mode != "per_km" {
		return q, errors.New("请选择按次或按公里")
	}
	if c.Revision > 0 && mode != c.Mode {
		return q, errors.New("已下单任务保持原计费方式，请调整原方式的总量")
	}
	if target < q.MinimumUnits {
		return q, errors.New("下单总量不得低于已成功结算与执行中占用之和；已结算部分不支持退款")
	}
	if target < 0 || target > 1000000000 {
		return q, errors.New("请输入有效总量；公里按 100 m 调整，也可退完全部未执行额度")
	}
	q.DeltaUnits = target - c.TotalUnits
	if mode == "per_km" && q.DeltaUnits%100 != 0 && target != q.MinimumUnits {
		return q, errors.New("公里总量按 100 m 增减，也可退完全部未执行额度")
	}
	if q.DeltaUnits > 0 {
		var e error
		q.DeltaMicro, e = prepayCost(p, mode, q.DeltaUnits)
		return q, e
	}
	if q.DeltaUnits < 0 {
		q.DeltaMicro = -escrowShare(c.AvailableMicro, -q.DeltaUnits, c.RemainingUnits)
	}
	return q, nil
}

func (s *Service) adjustTaskCredit(w http.ResponseWriter, r *http.Request) {
	var in taskAdjustmentInput
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	preview := r.Method == "POST"
	if !preview && (!creditOrderID.MatchString(in.RequestID) || in.ExpectedDelta == nil) {
		fail(w, 400, "缺少确认金额或幂等订单号")
		return
	}
	ctx := r.Context()
	j, e := s.store.GetJobContext(ctx, r.PathValue("id"))
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	u := currentUser(r)
	if u.ID != j.Account.OwnerUserID || u.Role != "customer" {
		fail(w, 403, "仅账号所属客户可确认预扣与退款")
		return
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(ctx)
	var locked string
	if e = tx.QueryRow(ctx, `SELECT j.id FROM automation_jobs j JOIN runner_accounts a ON a.id=j.account_id WHERE j.id=$1 AND j.account_id=$2 AND j.plan_id=$3 AND a.owner_user_id=$4 FOR UPDATE OF j FOR SHARE OF a`, j.Job.ID, j.Account.ID, j.Plan.ID, u.ID).Scan(&locked); e != nil {
		fail(w, 409, "任务绑定已变更，请刷新")
		return
	}
	var balance int64
	if e = tx.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id=$1 AND role='customer' AND enabled FOR UPDATE`, u.ID).Scan(&balance); e != nil {
		fail(w, 403, "用户已停用")
		return
	}
	if !preview {
		var oldUser, oldJob, oldMode string
		var oldTarget, oldAmount, oldRevision int64
		e = tx.QueryRow(ctx, `SELECT user_id,job_id,mode,target_units,delta_micro,expected_revision FROM portal_task_adjustments WHERE id=$1`, in.RequestID).Scan(&oldUser, &oldJob, &oldMode, &oldTarget, &oldAmount, &oldRevision)
		if e == nil {
			if oldUser != u.ID || oldJob != j.Job.ID || oldMode != in.Mode || oldTarget != in.TotalUnits || oldAmount != *in.ExpectedDelta || oldRevision != in.ExpectedRevision {
				fail(w, 409, "订单号已用于其他调整")
				return
			}
			jsonReply(w, 200, map[string]any{"ok": true, "replayed": true, "delta_micro": oldAmount})
			return
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			s.dbError(w, e)
			return
		}
	}
	c := taskCredit{JobID: j.Job.ID, Mode: in.Mode}
	e = scanTaskCredit(tx.QueryRow(ctx, creditSelect+` WHERE c.job_id=$1 FOR UPDATE OF c`, j.Job.ID), &c)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	if c.Revision > 0 && (c.userID != u.ID || c.accountID != j.Account.ID || c.planID != j.Plan.ID) {
		fail(w, 409, "预扣额度与当前账号归属不一致，请联系管理员核对")
		return
	}
	cfg, e := s.Settings(ctx, false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	q, e := quoteTaskAdjustment(c, cfg.Pricing, in.Mode, in.TotalUnits)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	q.BalanceMicro = balance
	if q.DeltaUnits > 0 {
		var owned bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3)`, u.ID, j.Account.ID, j.Plan.ID).Scan(&owned); e != nil {
			s.dbError(w, e)
			return
		}
		if owned {
			fail(w, 409, "已有完整计划套餐，无需增加预扣额度")
			return
		}
		if cfg.Mode != "commercial" || !cfg.Pricing.Enabled {
			fail(w, 403, "当前未开启商业计费；已有未执行额度仍可退回")
			return
		}
	}
	if preview {
		jsonReply(w, 200, q)
		return
	}
	if q.Revision != in.ExpectedRevision || q.DeltaMicro != *in.ExpectedDelta {
		fail(w, 409, "额度或价格已变化，请重新预览并确认")
		return
	}
	if q.DeltaMicro > balance {
		fail(w, 400, "余额不足，请先充值")
		return
	}
	if q.DeltaUnits == 0 {
		jsonReply(w, 200, map[string]any{"ok": true, "unchanged": true})
		return
	}
	if c.Revision == 0 {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_credits(job_id,user_id,account_id,plan_id,mode,remaining_units,available_micro,escrow_version) VALUES($1,$2,$3,$4,$5,$6,$7,1)`, j.Job.ID, u.ID, j.Account.ID, j.Plan.ID, in.Mode, q.DeltaUnits, q.DeltaMicro)
	} else {
		_, e = tx.Exec(ctx, `UPDATE portal_task_credits SET remaining_units=remaining_units+$2,available_micro=available_micro+$3,revision=revision+1,updated_at=now() WHERE job_id=$1`, j.Job.ID, q.DeltaUnits, q.DeltaMicro)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE portal_users SET balance_micro=balance_micro-$2 WHERE id=$1`, u.ID, q.DeltaMicro)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_adjustments(id,user_id,job_id,mode,target_units,delta_units,delta_micro,expected_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, in.RequestID, u.ID, j.Job.ID, in.Mode, in.TotalUnits, q.DeltaUnits, q.DeltaMicro, in.ExpectedRevision)
	}
	if e == nil && q.DeltaUnits > 0 {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_credit_orders(id,user_id,job_id,mode,units,amount_micro) VALUES($1,$2,$3,$4,$5,$6)`, "adjust_"+in.RequestID, u.ID, j.Job.ID, in.Mode, q.DeltaUnits, q.DeltaMicro)
	}
	kind := "task_prepay"
	if q.DeltaUnits < 0 {
		kind = "task_release"
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference,funding_amount_micro) VALUES($1,$2,$3,$4,$5,$6)`, platform.NewID("led"), u.ID, -q.DeltaMicro, kind, "adjust:"+in.RequestID, q.DeltaMicro)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_job_billing(job_id,mode) VALUES($1,$2) ON CONFLICT(job_id) DO UPDATE SET mode=EXCLUDED.mode`, j.Job.ID, in.Mode)
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(ctx, "task.adjust", j.Job.ID)
	jsonReply(w, 200, map[string]any{"ok": true, "delta_micro": q.DeltaMicro})
}
