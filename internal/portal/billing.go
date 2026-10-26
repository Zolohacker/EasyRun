package portal

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

func runPrice(p Pricing, mode string, distanceM float64) (int64, error) {
	if distanceM < 0 || distanceM > 100000 || math.IsNaN(distanceM) || math.IsInf(distanceM, 0) {
		return 0, errors.New("计费距离不正确")
	}
	switch mode {
	case "per_run":
		return p.PerRunMicro, nil
	case "per_km":
		return int64(math.Ceil(distanceM*float64(p.PerKMMicro)/1000 - 1e-9)), nil
	case "plan_package":
		return 0, errors.New("请先购买此账号对应跑步计划的完整套餐")
	default:
		return 0, errors.New("计费方式不正确")
	}
}

// Reserve moves prepaid principal into the in-flight pool before work starts.
// Checkout already debited wallet balance; execution never debits it a second time.
func (s *Service) Reserve(ctx context.Context, executionID string, j platform.JobContext) error {
	ok, e := s.initialized(ctx)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("请先初始化平台")
	}
	var role string
	var enabled bool
	e = s.db.QueryRow(ctx, `SELECT role,enabled FROM portal_users WHERE id=$1`, j.Account.OwnerUserID).Scan(&role, &enabled)
	if e != nil {
		return errors.New("账号尚未绑定平台用户")
	}
	if !enabled {
		return errors.New("账号所属平台用户已停用")
	}
	if role != "customer" {
		return nil
	}
	cfg, e := s.Settings(ctx, false)
	if e != nil {
		return e
	}
	if cfg.Mode != "commercial" {
		return errors.New("客户任务仅在商业模式下执行")
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var lockedUser string
	e = tx.QueryRow(ctx, `SELECT id FROM portal_users WHERE id=$1 AND enabled FOR UPDATE`, j.Account.OwnerUserID).Scan(&lockedUser)
	if e != nil {
		return e
	}
	var old string
	e = tx.QueryRow(ctx, `SELECT state FROM portal_charges WHERE execution_id=$1`, executionID).Scan(&old)
	if e == nil {
		if old == "refunded" {
			return errors.New("此执行已经退款")
		}
		return nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var entitlement bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3)`, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID).Scan(&entitlement)
	if e != nil {
		return e
	}
	if !entitlement {
		handled, err := reserveTaskCredit(ctx, tx, executionID, j)
		if err != nil {
			return err
		}
		if handled {
			return tx.Commit(ctx)
		}
		if cfg.Pricing.Enabled {
			return errors.New("请先下单预扣次数或公里额度，再执行任务")
		}
	}
	if !cfg.Pricing.Enabled {
		return nil
	}
	// A full-plan entitlement is recorded for idempotency, with no extra hold.
	_, e = tx.Exec(ctx, `INSERT INTO portal_charges(execution_id,user_id,amount_micro,state,funding_source) VALUES($1,$2,0,'reserved','package')`, executionID, j.Account.OwnerUserID)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) Finish(ctx context.Context, executionID string, success bool) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var user, state, funding, creditJob, creditMode string
	var amount, creditUnits int64
	// All money mutations use owner -> charge -> credit lock order, including
	// cancellation, execution reservation and success settlement.
	if e = tx.QueryRow(ctx, `SELECT user_id FROM portal_charges WHERE execution_id=$1`, executionID).Scan(&user); errors.Is(e, pgx.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM portal_users WHERE id=$1 FOR UPDATE`, user); e != nil {
		return e
	}
	e = tx.QueryRow(ctx, `SELECT user_id,amount_micro,state,funding_source,credit_job_id,credit_mode,credit_units FROM portal_charges WHERE execution_id=$1 FOR UPDATE`, executionID).Scan(&user, &amount, &state, &funding, &creditJob, &creditMode, &creditUnits)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if state != "reserved" {
		return nil
	}
	next := "settled"
	if funding == "prepaid" {
		if e = settleTaskCredit(ctx, tx, executionID, creditJob, user, creditMode, creditUnits, amount, success); e != nil {
			return e
		}
	}
	if !success {
		next = "refunded"
		if funding == "balance" {
			_, e = tx.Exec(ctx, `UPDATE portal_users SET balance_micro=balance_micro+$2 WHERE id=$1`, user, amount)
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference) VALUES($1,$2,$3,'run_refund',$4)`, platform.NewID("led"), user, amount, "refund:"+executionID)
			if e != nil {
				return e
			}
		}
	}
	_, e = tx.Exec(ctx, `UPDATE portal_charges SET state=$2 WHERE execution_id=$1`, executionID, next)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) billingSummary(w http.ResponseWriter, r *http.Request) {
	u, e := scanUser(s.db.QueryRow(r.Context(), `SELECT `+userFields+` FROM portal_users u WHERE u.id=$1`, currentUser(r).ID))
	if e != nil {
		s.dbError(w, e)
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	rows, e := s.db.Query(r.Context(), `SELECT id,user_id,delta_micro,kind,reference,created_at,funding_amount_micro FROM portal_ledger WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	entries := []LedgerEntry{}
	for rows.Next() {
		var x LedgerEntry
		e = rows.Scan(&x.ID, &x.UserID, &x.DeltaMicro, &x.Kind, &x.Reference, &x.CreatedAt, &x.FundingAmountMicro)
		if e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		entries = append(entries, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		s.dbError(w, e)
		return
	}
	type entitlement struct {
		AccountID string `json:"account_id"`
		PlanID    string `json:"plan_id"`
	}
	packs := []entitlement{}
	rows, e = s.db.Query(r.Context(), `SELECT account_id,plan_id FROM portal_entitlements WHERE user_id=$1`, u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	for rows.Next() {
		var x entitlement
		if e = rows.Scan(&x.AccountID, &x.PlanID); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		packs = append(packs, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		s.dbError(w, e)
		return
	}
	modes := map[string]string{}
	rows, e = s.db.Query(r.Context(), `SELECT b.job_id,b.mode FROM portal_job_billing b JOIN automation_jobs j ON j.id=b.job_id JOIN runner_accounts a ON a.id=j.account_id WHERE a.owner_user_id=$1`, u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	for rows.Next() {
		var id, mode string
		if e = rows.Scan(&id, &mode); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		modes[id] = mode
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		s.dbError(w, e)
		return
	}
	var address string
	_ = s.db.QueryRow(r.Context(), `SELECT address FROM portal_wallet_addresses WHERE user_id=$1`, u.ID).Scan(&address)
	jsonReply(w, 200, map[string]any{"user": u, "pricing": cfg.Pricing, "ledger": entries, "entitlements": packs, "job_modes": modes, "deposit_address": address, "wallet_enabled": cfg.Wallet.Enabled, "network": cfg.Wallet.Network, "contract": cfg.Wallet.Contract})
}
func (s *Service) setJobBilling(w http.ResponseWriter, r *http.Request) {
	j, e := s.store.GetJob(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if e = decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if in.Mode != "per_run" && in.Mode != "per_km" && in.Mode != "plan_package" {
		fail(w, 400, "计费方式不正确")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var locked string
	if e = tx.QueryRow(r.Context(), `SELECT id FROM automation_jobs WHERE id=$1 FOR UPDATE`, j.ID).Scan(&locked); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	var prepaidMode string
	e = tx.QueryRow(r.Context(), `SELECT mode FROM portal_task_credits WHERE job_id=$1`, j.ID).Scan(&prepaidMode)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	if prepaidMode != "" && prepaidMode != in.Mode {
		fail(w, 409, "此任务已有预付方式；额度用完后可在预付页面选择新方式续费")
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_job_billing(job_id,mode)VALUES($1,$2)ON CONFLICT(job_id)DO UPDATE SET mode=EXCLUDED.mode`, j.ID, in.Mode)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) buyPackage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		PlanID    string `json:"plan_id"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	a, e := s.store.GetAccount(r.Context(), in.AccountID)
	if e != nil || a.OwnerUserID != currentUser(r).ID {
		fail(w, 404, "账号不存在")
		return
	}
	p, e := s.store.GetPlan(r.Context(), in.PlanID)
	if e != nil || p.SourceAccountID != a.ID {
		fail(w, 404, "计划不存在")
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if !cfg.Pricing.Enabled || cfg.Mode != "commercial" {
		fail(w, 400, "计费当前未开启")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var balance int64
	e = tx.QueryRow(r.Context(), `SELECT balance_micro FROM portal_users WHERE id=$1 FOR UPDATE`, a.OwnerUserID).Scan(&balance)
	if e != nil {
		s.dbError(w, e)
		return
	}
	tag, e := tx.Exec(r.Context(), `INSERT INTO portal_entitlements(user_id,account_id,plan_id)VALUES($1,$2,$3)ON CONFLICT DO NOTHING`, a.OwnerUserID, a.ID, p.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "此账号已经购买该计划")
		return
	}
	amount := cfg.Pricing.PlanPackageMicro
	if amount > balance {
		fail(w, 400, "余额不足")
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE portal_users SET balance_micro=balance_micro-$2 WHERE id=$1`, a.OwnerUserID, amount)
	if e != nil {
		s.dbError(w, e)
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference)VALUES($1,$2,$3,'plan_package',$4)`, platform.NewID("led"), a.OwnerUserID, -amount, "package:"+a.ID+":"+p.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 201, map[string]bool{"ok": true})
}
func (s *Service) generateCodes(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in struct {
		Count       int   `json:"count"`
		AmountMicro int64 `json:"amount_micro"`
		Days        int   `json:"days"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if in.Count < 1 || in.Count > 100 || in.AmountMicro < 1 || in.AmountMicro > 100_000*MicroUSDT || in.Days < 1 || in.Days > 3650 {
		fail(w, 400, "数量 1–100，金额大于 0 且不超过 100000 USDT，有效期 1–3650 天")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	batch := platform.NewID("batch")
	codes := []string{}
	for i := 0; i < in.Count; i++ {
		data := make([]byte, 20)
		if _, e = rand.Read(data); e != nil {
			s.dbError(w, e)
			return
		}
		code := "ER-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
		_, e = tx.Exec(r.Context(), `INSERT INTO portal_recharge_codes(code_hash,label,amount_micro,batch_id,expires_at)VALUES($1,$2,$3,$4,$5)`, digest(code), code[:11], in.AmountMicro, batch, time.Now().Add(time.Duration(in.Days)*24*time.Hour))
		if e != nil {
			s.dbError(w, e)
			return
		}
		codes = append(codes, code)
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "codes.generate", batch)
	jsonReply(w, 201, map[string]any{"batch_id": batch, "codes": codes, "amount_micro": in.AmountMicro})
}
func (s *Service) listCodes(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rows, e := s.db.Query(r.Context(), `SELECT label,amount_micro,batch_id,expires_at,COALESCE(used_by,''),used_at FROM portal_recharge_codes ORDER BY created_at DESC LIMIT 500`)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var label, batch, by string
		var amount int64
		var expires time.Time
		var used *time.Time
		if e = rows.Scan(&label, &amount, &batch, &expires, &by, &used); e != nil {
			s.dbError(w, e)
			return
		}
		out = append(out, map[string]any{"label": label, "amount_micro": amount, "batch_id": batch, "expires_at": expires, "used_by": by, "used_at": used})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, out)
}
func (s *Service) redeem(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Settings(r.Context(), false)
	if err != nil {
		s.dbError(w, err)
		return
	}
	if !cfg.Pricing.RechargeCodeEnabled {
		fail(w, 403, "充值码充值当前未开启")
		return
	}
	if !s.allow("redeem:"+currentUser(r).ID, 12, 10*time.Minute) {
		fail(w, 429, "兑换尝试过多，请稍后重试")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	hash := digest(strings.ToUpper(strings.TrimSpace(in.Code)))
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var amount int64
	e = tx.QueryRow(r.Context(), `UPDATE portal_recharge_codes SET used_by=$2,used_at=now() WHERE code_hash=$1 AND used_by IS NULL AND expires_at>now() RETURNING amount_micro`, hash, currentUser(r).ID).Scan(&amount)
	if e != nil {
		fail(w, 400, "充值码无效、已兑换或已过期")
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE portal_users SET balance_micro=balance_micro+$2 WHERE id=$1`, currentUser(r).ID, amount)
	if e != nil {
		s.dbError(w, e)
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference)VALUES($1,$2,$3,'code_redeem',$4)`, platform.NewID("led"), currentUser(r).ID, amount, "code:"+hash)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]int64{"credited_micro": amount})
}
