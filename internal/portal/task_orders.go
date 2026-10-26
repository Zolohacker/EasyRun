package portal

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
	"github.com/jackc/pgx/v5"
)

// A failed checkout leaves neither a runnable task nor a wallet debit behind.
func (s *Service) createTaskOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID      string                 `json:"request_id"`
		Job            platform.AutomationJob `json:"job"`
		Mode           string                 `json:"mode"`
		Units          int64                  `json:"units"`
		ExpectedAmount *int64                 `json:"expected_amount_micro"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !creditOrderID.MatchString(in.RequestID) || in.ExpectedAmount == nil {
		fail(w, 400, "缺少下单确认金额或订单号")
		return
	}
	u := currentUser(r)
	if u.Role != "customer" {
		fail(w, 403, "此下单入口仅供客户本人使用")
		return
	}
	ctx := r.Context()
	a, e := s.store.GetAccount(ctx, in.Job.AccountID)
	if e != nil || a.OwnerUserID != u.ID {
		fail(w, 404, "账号不存在")
		return
	}
	in.Job.ID = ""
	raw, _ := json.Marshal(in)
	hash := digest(string(raw))
	tx, e := s.db.Begin(ctx)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, `SELECT id FROM runner_accounts WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, a.ID, u.ID).Scan(&id); e != nil {
		fail(w, 404, "账号不存在")
		return
	}
	var balance int64
	if e = tx.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id=$1 AND role='customer' AND enabled FOR UPDATE`, u.ID).Scan(&balance); e != nil {
		fail(w, 403, "客户已停用")
		return
	}
	var oldUser, oldHash, oldJob string
	e = tx.QueryRow(ctx, `SELECT user_id,request_hash,job_id FROM portal_task_orders WHERE id=$1`, in.RequestID).Scan(&oldUser, &oldHash, &oldJob)
	if e == nil {
		if oldUser != u.ID || oldHash != hash {
			fail(w, 409, "订单号已用于其他下单内容")
			return
		}
		j, e := s.store.GetJob(ctx, oldJob)
		if e != nil {
			s.dbError(w, e)
			return
		}
		jsonReply(w, 200, j)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_jobs WHERE account_id=$1)`, a.ID).Scan(&exists); e != nil {
		s.dbError(w, e)
		return
	}
	if exists {
		fail(w, 409, "该账号已有任务，请在原任务中调整下单额度")
		return
	}
	j := in.Job
	j.ID = platform.NewID("job")
	if e = automation.NormalizeJob(ctx, s.store, &j); e != nil {
		fail(w, 400, e.Error())
		return
	}
	cfg, e := s.Settings(ctx, false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if cfg.Mode != "commercial" || !cfg.Pricing.Enabled {
		fail(w, 409, "计费配置已变化，请刷新后下单")
		return
	}
	var owned bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3)`, u.ID, a.ID, j.PlanID).Scan(&owned); e != nil {
		s.dbError(w, e)
		return
	}
	amount := int64(0)
	if owned {
		in.Mode = "plan_package"
	} else {
		amount, e = prepayCost(cfg.Pricing, in.Mode, in.Units)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		if in.Mode == "per_km" && float64(in.Units) < j.TargetDistanceM {
			fail(w, 400, "下单里程至少覆盖一次任务距离")
			return
		}
	}
	if amount != *in.ExpectedAmount {
		fail(w, 409, "确认金额已变化，请重新核对")
		return
	}
	if amount > balance {
		fail(w, 400, "余额不足，下单未创建")
		return
	}
	j, e = store.CreateJobTx(ctx, tx, j)
	if e == nil && !owned {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_credits(job_id,user_id,account_id,plan_id,mode,remaining_units,available_micro,escrow_version) VALUES($1,$2,$3,$4,$5,$6,$7,1)`, j.ID, u.ID, a.ID, j.PlanID, in.Mode, in.Units, amount)
	}
	if e == nil && !owned {
		_, e = tx.Exec(ctx, `UPDATE portal_users SET balance_micro=balance_micro-$2 WHERE id=$1`, u.ID, amount)
	}
	if e == nil && !owned {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_credit_orders(id,user_id,job_id,mode,units,amount_micro) VALUES($1,$2,$3,$4,$5,$6)`, "checkout_"+in.RequestID, u.ID, j.ID, in.Mode, in.Units, amount)
	}
	if e == nil && !owned {
		_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference) VALUES($1,$2,$3,'task_prepay',$4)`, platform.NewID("led"), u.ID, -amount, "checkout:"+in.RequestID)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_job_billing(job_id,mode) VALUES($1,$2)`, j.ID, in.Mode)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_orders(id,user_id,job_id,request_hash) VALUES($1,$2,$3,$4)`, in.RequestID, u.ID, j.ID, hash)
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(ctx, "task.order", j.ID)
	jsonReply(w, 201, j)
}
