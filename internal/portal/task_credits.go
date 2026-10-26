package portal

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

type taskCredit struct {
	userID, accountID, planID string
	JobID                     string `json:"job_id"`
	Mode                      string `json:"mode"`
	RemainingUnits            int64  `json:"remaining_units"`
	ReservedUnits             int64  `json:"reserved_units"`
	SettledUnits              int64  `json:"settled_units"`
	TotalUnits                int64  `json:"total_units"`
	AvailableMicro            int64  `json:"available_micro"`
	ReservedMicro             int64  `json:"reserved_micro"`
	SettledMicro              int64  `json:"settled_micro"`
	Revision                  int64  `json:"revision"`
}

func prepayCost(pricing Pricing, mode string, units int64) (int64, error) {
	var price int64
	switch mode {
	case "per_run":
		if units < 1 || units > 10000 {
			return 0, errors.New("预付次数应为 1–10000 次")
		}
		price = pricing.PerRunMicro
	case "per_km":
		if units < 100 || units > 1000000 || units%100 != 0 {
			return 0, errors.New("预付里程应为 0.1–1000 km，使用 100 m 档位")
		}
		price = pricing.PerKMMicro
	default:
		return 0, errors.New("预付模式只支持按次或按公里")
	}
	if price < 0 || price > 100000*MicroUSDT {
		return 0, errors.New("预付单价不正确")
	}
	if mode == "per_run" {
		return price * units, nil
	}
	return (price*units + 999) / 1000, nil // integer micro-USDT, round up once per order
}

var creditOrderID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

func (s *Service) getTaskCredit(w http.ResponseWriter, r *http.Request) {
	j, e := s.store.GetJobContext(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	credit := taskCredit{JobID: j.Job.ID, Mode: "per_run"}
	e = scanTaskCredit(s.db.QueryRow(r.Context(), creditSelect+` WHERE c.job_id=$1`, j.Job.ID), &credit)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	if credit.Revision > 0 && (credit.userID != j.Account.OwnerUserID || credit.accountID != j.Account.ID || credit.planID != j.Plan.ID) {
		fail(w, 409, "预扣额度与当前账号归属不一致，请联系管理员核对")
		return
	}
	credit.TotalUnits = credit.RemainingUnits + credit.ReservedUnits + credit.SettledUnits
	var role string
	var balance int64
	var owned bool
	if e = s.db.QueryRow(r.Context(), `SELECT role,balance_micro FROM portal_users WHERE id=$1`, j.Account.OwnerUserID).Scan(&role, &balance); e != nil {
		s.dbError(w, e)
		return
	}
	if e = s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3)`, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID).Scan(&owned); e != nil {
		s.dbError(w, e)
		return
	}
	canPurchase := currentUser(r).ID == j.Account.OwnerUserID && role == "customer" && cfg.Mode == "commercial" && cfg.Pricing.Enabled && !owned
	result := map[string]any{"credit": credit, "can_purchase": canPurchase, "package_owned": owned, "personal": role != "customer", "pricing_enabled": cfg.Pricing.Enabled, "per_run_micro": cfg.Pricing.PerRunMicro, "per_km_micro": cfg.Pricing.PerKMMicro}
	result["can_adjust"] = currentUser(r).ID == j.Account.OwnerUserID && role == "customer" && (credit.Revision > 0 || canPurchase)
	if currentUser(r).ID == j.Account.OwnerUserID {
		result["balance_micro"] = balance
	}
	jsonReply(w, 200, result)
}

func (s *Service) purchaseTaskCredit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID      string `json:"request_id"`
		Mode           string `json:"mode"`
		Units          int64  `json:"units"`
		ExpectedAmount *int64 `json:"expected_amount_micro"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !creditOrderID.MatchString(in.RequestID) || in.ExpectedAmount == nil {
		fail(w, 400, "缺少有效的幂等订单号或确认金额")
		return
	}
	ctx := r.Context()
	j, e := s.store.GetJobContext(ctx, r.PathValue("id"))
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if currentUser(r).ID != j.Account.OwnerUserID || currentUser(r).Role != "customer" {
		fail(w, 403, "预付需由账号所属客户确认，不允许代扣其他用户余额")
		return
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, `SELECT j.id FROM automation_jobs j JOIN runner_accounts a ON a.id=j.account_id WHERE j.id=$1 AND j.account_id=$2 AND j.plan_id=$3 AND a.owner_user_id=$4 FOR UPDATE OF j FOR SHARE OF a`, j.Job.ID, j.Account.ID, j.Plan.ID, j.Account.OwnerUserID).Scan(&id); e != nil {
		fail(w, 409, "任务绑定已变更，请刷新后重试")
		return
	}
	var balance int64
	if e = tx.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id=$1 AND role='customer' AND enabled FOR UPDATE`, j.Account.OwnerUserID).Scan(&balance); e != nil {
		fail(w, 403, "用户已停用")
		return
	}
	var oldUser, oldJob, oldMode string
	var oldUnits, oldAmount int64
	e = tx.QueryRow(ctx, `SELECT user_id,job_id,mode,units,amount_micro FROM portal_task_credit_orders WHERE id=$1`, in.RequestID).Scan(&oldUser, &oldJob, &oldMode, &oldUnits, &oldAmount)
	if e == nil {
		if oldUser != j.Account.OwnerUserID || oldJob != j.Job.ID || oldMode != in.Mode || oldUnits != in.Units || oldAmount != *in.ExpectedAmount {
			fail(w, 409, "订单号已用于其他预付请求")
			return
		}
		jsonReply(w, 200, map[string]any{"ok": true, "amount_micro": oldAmount, "replayed": true})
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	cfg, e := s.Settings(ctx, false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if !cfg.Pricing.Enabled || cfg.Mode != "commercial" {
		fail(w, 403, "当前未开启商业计费")
		return
	}
	var owned bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3)`, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID).Scan(&owned); e != nil {
		s.dbError(w, e)
		return
	}
	if owned {
		fail(w, 409, "已有完整计划套餐，无需重复预付")
		return
	}
	amount, e := prepayCost(cfg.Pricing, in.Mode, in.Units)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if amount != *in.ExpectedAmount {
		fail(w, 409, "价格已变化，请刷新并重新确认金额")
		return
	}
	if amount > balance {
		fail(w, 400, "余额不足，请先充值")
		return
	}
	var mode, creditUser, creditAccount, creditPlan string
	var remaining int64
	e = tx.QueryRow(ctx, `SELECT mode,remaining_units,user_id,account_id,plan_id FROM portal_task_credits WHERE job_id=$1 FOR UPDATE`, j.Job.ID).Scan(&mode, &remaining, &creditUser, &creditAccount, &creditPlan)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	if mode != "" && (creditUser != j.Account.OwnerUserID || creditAccount != j.Account.ID || creditPlan != j.Plan.ID) {
		fail(w, 409, "预扣额度与当前账号归属不一致，请联系管理员核对")
		return
	}
	if mode != "" && mode != in.Mode {
		var reserved bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_charges WHERE credit_job_id=$1 AND state='reserved' AND funding_source='prepaid')`, j.Job.ID).Scan(&reserved); e != nil {
			s.dbError(w, e)
			return
		}
		if remaining > 0 || reserved {
			fail(w, 409, "现有预付额度或执行预留尚未用完，请继续使用相同计费方式")
			return
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO portal_task_credits(job_id,user_id,account_id,plan_id,mode,remaining_units,available_micro,escrow_version) VALUES($1,$2,$3,$4,$5,$6,$7,1) ON CONFLICT(job_id) DO UPDATE SET mode=EXCLUDED.mode,remaining_units=portal_task_credits.remaining_units+EXCLUDED.remaining_units,available_micro=portal_task_credits.available_micro+EXCLUDED.available_micro,settled_units=CASE WHEN portal_task_credits.mode=EXCLUDED.mode THEN portal_task_credits.settled_units ELSE 0 END,settled_micro=CASE WHEN portal_task_credits.mode=EXCLUDED.mode THEN portal_task_credits.settled_micro ELSE 0 END,revision=portal_task_credits.revision+1,updated_at=now()`, j.Job.ID, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID, in.Mode, in.Units, amount)
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE portal_users SET balance_micro=balance_micro-$2 WHERE id=$1`, j.Account.OwnerUserID, amount)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_task_credit_orders(id,user_id,job_id,mode,units,amount_micro) VALUES($1,$2,$3,$4,$5,$6)`, in.RequestID, j.Account.OwnerUserID, j.Job.ID, in.Mode, in.Units, amount)
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference) VALUES($1,$2,$3,'task_prepay',$4)`, platform.NewID("led"), j.Account.OwnerUserID, -amount, "prepay:"+in.RequestID)
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
	s.audit(ctx, "task.prepay", j.Job.ID)
	jsonReply(w, 201, map[string]any{"ok": true, "amount_micro": amount, "replayed": false})
}

// Returns handled=true even when exhausted: a prepaid task must never fall
// through to wallet charging. The caller already holds the owner's wallet lock.
func reserveTaskCredit(ctx context.Context, tx pgx.Tx, executionID string, j platform.JobContext) (bool, error) {
	var mode, user, account, plan string
	var remaining, available int64
	e := tx.QueryRow(ctx, `SELECT mode,remaining_units,user_id,account_id,plan_id,available_micro FROM portal_task_credits WHERE job_id=$1 FOR UPDATE`, j.Job.ID).Scan(&mode, &remaining, &user, &account, &plan, &available)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return true, e
	}
	if user != j.Account.OwnerUserID || account != j.Account.ID || plan != j.Plan.ID {
		return true, errors.New("预付额度与任务绑定不一致")
	}
	units := int64(1)
	if mode == "per_km" {
		if math.IsNaN(j.Plan.TargetDistanceM) || math.IsInf(j.Plan.TargetDistanceM, 0) || j.Plan.TargetDistanceM < 300 || j.Plan.TargetDistanceM > 100000 {
			return true, errors.New("预付任务距离不正确")
		}
		units = int64(math.Ceil(j.Plan.TargetDistanceM - 1e-6))
	}
	if remaining < units {
		return true, errors.New("本任务预付额度不足，请先续费；不会自动扣除账户余额")
	}
	amount := escrowShare(available, units, remaining)
	_, e = tx.Exec(ctx, `UPDATE portal_task_credits SET remaining_units=remaining_units-$2,available_micro=available_micro-$3,revision=revision+1,updated_at=now() WHERE job_id=$1`, j.Job.ID, units, amount)
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO portal_charges(execution_id,user_id,amount_micro,state,funding_source,credit_job_id,credit_mode,credit_units) VALUES($1,$2,$6,'reserved','prepaid',$3,$4,$5)`, executionID, j.Account.OwnerUserID, j.Job.ID, mode, units, amount)
	}
	return true, e
}

func (s *Service) applyCreditScope(ctx context.Context, tx pgx.Tx, j platform.JobContext, scope *taskLinkScope) error {
	if scope.Kind != "per_run" && scope.Kind != "per_km" {
		return nil
	}
	var remaining int64
	if e := tx.QueryRow(ctx, `SELECT remaining_units FROM portal_task_credits WHERE job_id=$1 AND user_id=$2 AND account_id=$3 AND plan_id=$4 AND mode=$5 AND remaining_units>0 FOR SHARE`, j.Job.ID, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID, scope.Kind).Scan(&remaining); e != nil {
		return errors.New("任务预付额度已用完或暂被执行占用，请联系下单者")
	}
	if scope.Kind == "per_km" {
		minimum, _, e := activity.SelectableDistanceRange(j.Plan.TargetDistanceM, j.Plan.Rules)
		if e != nil {
			return e
		}
		scope.MaxDistanceM = math.Min(scope.MaxDistanceM, math.Floor(float64(remaining)/100)*100)
		if scope.MaxDistanceM < minimum {
			return fmt.Errorf("剩余预付里程不足计划最低距离，请联系下单者续费")
		}
	}
	scope.RemainingUnits = remaining
	return nil
}
