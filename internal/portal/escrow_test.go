package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func TestEscrowShareAndQuoteConservation(t *testing.T) {
	for _, tc := range []struct{ amount, units, total, want int64 }{{10, 1, 3, 3}, {10, 3, 3, 10}, {1, 1, 3, 0}, {1000000000000000, 999999, 1000000, 999999000000000}, {0, 5, 10, 0}} {
		if got := escrowShare(tc.amount, tc.units, tc.total); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	amount := int64(17)
	units := int64(31)
	spent := int64(0)
	for units > 0 {
		take := min(units, 3)
		share := escrowShare(amount, take, units)
		spent += share
		amount -= share
		units -= take
	}
	if spent != 17 || amount != 0 {
		t.Fatal("rounding lost principal", spent, amount)
	}
	c := taskCredit{Mode: "per_run", RemainingUnits: 3, AvailableMicro: 10, SettledUnits: 1, ReservedUnits: 1, TotalUnits: 5, Revision: 7}
	q, e := quoteTaskAdjustment(c, Pricing{PerRunMicro: 999999}, "per_run", 2)
	if e != nil || q.DeltaMicro != -10 || q.MinimumUnits != 2 || q.Revision != 7 {
		t.Fatal(q, e)
	}
	for _, target := range []int64{-1, 0, 1, 1000000001} {
		if _, e = quoteTaskAdjustment(c, Pricing{}, "per_run", target); e == nil {
			t.Fatal("invalid total", target)
		}
	}
	if _, e = quoteTaskAdjustment(c, Pricing{}, "per_km", 3000); e == nil {
		t.Fatal("mode switched")
	}
	c = taskCredit{Mode: "per_km", RemainingUnits: 1000, AvailableMicro: 7, SettledUnits: 1013, TotalUnits: 2013, Revision: 1}
	if q, e = quoteTaskAdjustment(c, Pricing{}, "per_km", 1013); e != nil || q.DeltaMicro != -7 {
		t.Fatal("full refund at non-slot minimum", q, e)
	}
	if q, e = quoteTaskAdjustment(c, Pricing{}, "per_km", 1113); e != nil || q.DeltaUnits != -900 {
		t.Fatal("relative 100m adjustment rejected", q, e)
	}
	if _, e = quoteTaskAdjustment(c, Pricing{}, "per_km", 1114); e == nil {
		t.Fatal("arbitrary non-slot accepted")
	}
}

type escrowFixture struct {
	t          *testing.T
	s          *Service
	db         *store.PostgresStore
	ctx, actor context.Context
	mux        *http.ServeMux
	jc         platform.JobContext
	balance    int64
}

func newEscrowFixture(t *testing.T) *escrowFixture {
	t.Helper()
	s, db := testService(t)
	key, err := s.vault.Encrypt([]byte("fixture-map-key"), platform.SystemSettingsMapKeyAAD)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSystemSettings(context.Background(), platform.SystemSettings{MapAPIKeys: []platform.MapAPIKey{{ID: "fixture", KeyCipher: key, QPS: 3}}, WorkerProcesses: 1, WorkerConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	f := &escrowFixture{t: t, s: s, db: db, ctx: ctx, balance: 10 * MicroUSDT, mux: http.NewServeMux()}
	_, e := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role,balance_micro) VALUES('esc_admin','a@escrow.test',''::bytea,'superadmin',0),('esc_owner','b@escrow.test',''::bytea,'customer',$1),('esc_other','c@escrow.test',''::bytea,'customer',$1)`, f.balance)
	if e != nil {
		t.Fatal(e)
	}
	f.actor = context.WithValue(platform.WithActor(ctx, platform.Actor{ID: "esc_owner", Role: "customer"}), userKey{}, User{ID: "esc_owner", Role: "customer", Enabled: true})
	cfg := Defaults()
	cfg.Mode = "commercial"
	cfg.FrontendEnabled = true
	cfg.Pricing = Pricing{Enabled: true, PerRunMicro: MicroUSDT, PerKMMicro: MicroUSDT, PlanPackageMicro: 5 * MicroUSDT}
	raw, _ := json.Marshal(cfg)
	if _, e = s.db.Exec(ctx, `UPDATE portal_settings SET config=$1`, raw); e != nil {
		t.Fatal(e)
	}
	a, e := s.store.CreateAccount(f.actor, platform.Account{ID: "esc_account", Name: "测试账号", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := db.CreatePlan(ctx, platform.RunningPlan{ID: "esc_plan", SourceAccountID: a.ID, RemoteCode: "p", Name: "测试计划", TargetDistanceM: 1000, Rules: activity.RunRules{MinDistanceM: 1000, MaxDistanceM: 5000, DayMaxTimes: 3, WeekMaxTimes: 10}})
	if e != nil {
		t.Fatal(e)
	}
	ar, e := db.CreateArea(ctx, platform.RunningArea{ID: "esc_area", SourceAccountID: a.ID, RemoteCode: "a", Name: "测试区域", Center: geo.Point{Lat: 35, Lng: 116}, CandidateCount: 1})
	if e != nil {
		t.Fatal(e)
	}
	f.jc = platform.JobContext{Account: a, Plan: p, Area: ar, Job: platform.AutomationJob{AccountID: a.ID, PlanID: p.ID, AreaID: ar.ID, TargetDistanceM: 1000, DailyRunLimit: 2, WeeklyRunLimit: 5, RouteMode: "auto", ScheduleMode: "manual", ManualTime: "06:30", DayScope: "every_day", Timezone: "Asia/Shanghai"}}
	s.Mount(f.mux)
	return f
}
func (f *escrowFixture) request(ctx context.Context, method, path string, input any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}
func (f *escrowFixture) call(method, path string, input any, status int) []byte {
	f.t.Helper()
	w := f.request(f.actor, method, path, input)
	if w.Code != status {
		f.t.Fatalf("%s %s status %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w.Body.Bytes()
}
func (f *escrowFixture) order(id, mode string, units, amount int64) map[string]any {
	return map[string]any{"request_id": id, "job": f.jc.Job, "mode": mode, "units": units, "expected_amount_micro": amount}
}
func (f *escrowFixture) checkout(mode string, units, amount int64) {
	f.t.Helper()
	raw := f.call("POST", "/api/v1/portal/task-orders", f.order("escrow-checkout-01", mode, units, amount), 201)
	if e := json.Unmarshal(raw, &f.jc.Job); e != nil {
		f.t.Fatal(e)
	}
}
func (f *escrowFixture) credit() taskCredit {
	f.t.Helper()
	var c taskCredit
	if e := scanTaskCredit(f.s.db.QueryRow(f.ctx, creditSelect+` WHERE c.job_id=$1`, f.jc.Job.ID), &c); e != nil {
		f.t.Fatal(e)
	}
	return c
}
func (f *escrowFixture) wallet(want int64) {
	f.t.Helper()
	var got int64
	if e := f.s.db.QueryRow(f.ctx, `SELECT balance_micro FROM portal_users WHERE id='esc_owner'`).Scan(&got); e != nil || got != want {
		f.t.Fatalf("wallet=%d want=%d: %v", got, want, e)
	}
}
func (f *escrowFixture) quote(mode string, total int64) taskQuote {
	f.t.Helper()
	var q taskQuote
	json.Unmarshal(f.call("POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": mode, "total_units": total}, 200), &q)
	return q
}
func adjustment(id string, q taskQuote) taskAdjustmentInput {
	return taskAdjustmentInput{RequestID: id, Mode: q.Mode, TotalUnits: q.TotalUnits, ExpectedRevision: q.Revision, ExpectedDelta: &q.DeltaMicro}
}
func (f *escrowFixture) adjust(id string, q taskQuote, status int) {
	f.t.Helper()
	f.call("PUT", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay", adjustment(id, q), status)
}
func (f *escrowFixture) execution(id string) {
	f.t.Helper()
	if e := f.db.CreateExecution(f.ctx, platform.Execution{ID: id, JobID: f.jc.Job.ID, AccountID: f.jc.Account.ID, StartedAt: time.Now(), ScheduledFor: time.Now(), Status: "succeeded"}); e != nil {
		f.t.Fatal(e)
	}
}
func (f *escrowFixture) reserve(id string) {
	f.t.Helper()
	f.execution(id)
	if e := f.s.Reserve(f.ctx, id, f.jc); e != nil {
		f.t.Fatal(e)
	}
}
func (f *escrowFixture) finish(id string, success bool) {
	f.t.Helper()
	if e := f.s.Finish(f.ctx, id, success); e != nil {
		f.t.Fatal(e)
	}
}

func TestEscrowCheckoutAdjustAndIrreversibleSettlement(t *testing.T) {
	f := newEscrowFixture(t)
	initial := f.order("escrow-checkout-01", "per_run", 3, 3*MicroUSDT)
	bad := f.order("escrow-bad-amount-01", "per_run", 3, 1)
	f.call("POST", "/api/v1/portal/task-orders", bad, 409)
	bad = f.order("escrow-too-expensive", "per_run", 11, 11*MicroUSDT)
	f.call("POST", "/api/v1/portal/task-orders", bad, 400)
	var count int
	f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM automation_jobs`).Scan(&count)
	if count != 0 {
		t.Fatal("failed checkout left task")
	}
	f.wallet(f.balance)
	raw := f.call("POST", "/api/v1/portal/task-orders", initial, 201)
	json.Unmarshal(raw, &f.jc.Job)
	f.wallet(7 * MicroUSDT)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.request(f.actor, "POST", "/api/v1/portal/task-orders", initial)
			if w.Code != 200 {
				t.Errorf("checkout replay: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	changed := f.order("escrow-checkout-01", "per_run", 4, 4*MicroUSDT)
	f.call("POST", "/api/v1/portal/task-orders", changed, 409)
	f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM automation_jobs`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate task")
	}
	q := f.quote("per_run", 5)
	if q.DeltaMicro != 2*MicroUSDT {
		t.Fatal(q)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := f.request(f.actor, "PUT", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay", adjustment("escrow-increase-01", q))
			if w.Code != 200 {
				t.Errorf("increase replay: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	f.wallet(5 * MicroUSDT)
	stale := f.quote("per_run", 4)
	f.reserve("esc_success")
	f.adjust("escrow-stale-quote", stale, 409)
	q = f.quote("per_run", 4)
	if q.DeltaMicro != -MicroUSDT {
		t.Fatal(q)
	}
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := f.request(f.actor, "PUT", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay", adjustment(fmt.Sprintf("escrow-refund-%02d", i), q))
			results <- w.Code
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, conflict := 0, 0
	for code := range results {
		if code == 200 {
			accepted++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatal("refund status", code)
		}
	}
	if accepted != 1 || conflict != 1 {
		t.Fatal("duplicate refund accepted", accepted, conflict)
	}
	f.wallet(6 * MicroUSDT)
	f.call("POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": "per_run", "total_units": 0}, 400)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := f.s.Finish(f.ctx, "esc_success", true); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	f.finish("esc_success", false)
	f.wallet(6 * MicroUSDT)
	c := f.credit()
	if c.SettledUnits != 1 || c.SettledMicro != MicroUSDT || c.AvailableMicro != 3*MicroUSDT || c.ReservedUnits != 0 {
		t.Fatal(c)
	}
	f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM portal_ledger WHERE kind='task_settle' AND delta_micro=0 AND funding_amount_micro=$1`, MicroUSDT).Scan(&count)
	if count != 1 {
		t.Fatal("settlement repeated", count)
	}
	// Refunds use original remaining principal, never the increased catalogue price.
	if _, e := f.s.db.Exec(f.ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,per_run_micro}','9000000')`); e != nil {
		t.Fatal(e)
	}
	q = f.quote("per_run", 1)
	if q.DeltaMicro != -3*MicroUSDT {
		t.Fatal("price change inflated refund", q)
	}
	f.adjust("escrow-refund-all", q, 200)
	f.adjust("escrow-refund-all", q, 200)
	f.wallet(9 * MicroUSDT)
	f.call("POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": "per_run", "total_units": 0}, 400)
	// Failure restores reserved principal, then the owner can release it to wallet.
	q = f.quote("per_run", 2)
	f.adjust("escrow-rebuy-0001", q, 200)
	f.wallet(0)
	f.reserve("esc_failure")
	f.finish("esc_failure", false)
	f.finish("esc_failure", false)
	q = f.quote("per_run", 1)
	if q.DeltaMicro != -9*MicroUSDT {
		t.Fatal(q)
	}
	f.adjust("escrow-release-failure", q, 200)
	f.wallet(9 * MicroUSDT)
	// Account and plan stay bound to paid credit, even when available units are zero.
	changedJob := f.jc.Job
	changedJob.PlanID = "different-plan"
	if _, e := f.db.UpdateJob(f.ctx, changedJob); e == nil {
		t.Fatal("paid binding changed")
	}
	if e := f.db.DeleteJob(f.ctx, f.jc.Job.ID); e == nil {
		t.Fatal("paid history deleted")
	}
}

func TestEscrowKilometresRoundingFailureAndRefund(t *testing.T) {
	f := newEscrowFixture(t)
	if _, e := f.s.db.Exec(f.ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,per_km_micro}','7')`); e != nil {
		t.Fatal(e)
	}
	f.checkout("per_km", 3100, 22)
	f.reserve("km_success")
	c := f.credit()
	if c.ReservedMicro != 7 || c.AvailableMicro != 15 {
		t.Fatal(c)
	}
	q := f.quote("per_km", 2100)
	if q.DeltaMicro != -7 {
		t.Fatal(q)
	}
	f.adjust("km-reduce-0000001", q, 200)
	f.finish("km_success", true)
	f.reserve("km_failure")
	f.finish("km_failure", false)
	q = f.quote("per_km", 1000)
	if q.DeltaMicro != -8 {
		t.Fatal(q)
	}
	f.adjust("km-reduce-final1", q, 200)
	c = f.credit()
	if c.AvailableMicro != 0 || c.RemainingUnits != 0 || c.SettledMicro != 7 || c.SettledUnits != 1000 {
		t.Fatal(c)
	}
	f.wallet(f.balance - 7)
	// Price changes invalidate an increase quote; disabled pricing still permits refunds.
	q = f.quote("per_km", 1100)
	f.s.db.Exec(f.ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,per_km_micro}','99')`)
	f.adjust("km-stale-price-01", q, 409)
	q = f.quote("per_km", 1100)
	f.adjust("km-increase-0001", q, 200)
	f.s.db.Exec(f.ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,enabled}','false')`)
	q = f.quote("per_km", 1000)
	f.adjust("km-disabled-refund", q, 200)
	f.wallet(f.balance - 7)
}

func TestEscrowPackageCheckoutHasNoDuplicatePayment(t *testing.T) {
	f := newEscrowFixture(t)
	if _, e := f.s.db.Exec(f.ctx, `INSERT INTO portal_entitlements(user_id,account_id,plan_id) VALUES('esc_owner',$1,$2)`, f.jc.Account.ID, f.jc.Plan.ID); e != nil {
		t.Fatal(e)
	}
	f.checkout("plan_package", 0, 0)
	f.wallet(f.balance)
	var count int
	f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM portal_task_credits`).Scan(&count)
	if count != 0 {
		t.Fatal("package checkout bought duplicate credit")
	}
	f.reserve("package_exec")
	f.finish("package_exec", true)
	f.finish("package_exec", false)
	f.wallet(f.balance)
	f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM portal_charges WHERE funding_source='package' AND state='settled' AND amount_micro=0`).Scan(&count)
	if count != 1 {
		t.Fatal("package execution settlement not recorded")
	}
}

func TestEscrowZeroPriceStillLocksConsumedQuantity(t *testing.T) {
	f := newEscrowFixture(t)
	if _, e := f.s.db.Exec(f.ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,per_run_micro}','0')`); e != nil {
		t.Fatal(e)
	}
	f.checkout("per_run", 2, 0)
	f.reserve("zero_exec")
	f.finish("zero_exec", true)
	q := f.quote("per_run", 1)
	if q.DeltaUnits != -1 || q.DeltaMicro != 0 {
		t.Fatal(q)
	}
	f.adjust("zero-price-refund", q, 200)
	f.wallet(f.balance)
	f.call("POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": "per_run", "total_units": 0}, 400)
}

func TestEscrowTenantAndCapabilityBoundaries(t *testing.T) {
	f := newEscrowFixture(t)
	f.checkout("per_run", 2, 2*MicroUSDT)
	for _, tc := range []struct {
		id, role string
		status   int
	}{{"esc_other", "customer", 404}, {"esc_admin", "superadmin", 403}} {
		ctx := context.WithValue(platform.WithActor(f.ctx, platform.Actor{ID: tc.id, Role: tc.role}), userKey{}, User{ID: tc.id, Role: tc.role, Enabled: true})
		w := f.request(ctx, "POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": "per_run", "total_units": 0})
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	// A former account's principal must not be refunded to a new owner.
	if _, e := f.s.db.Exec(f.ctx, `UPDATE runner_accounts SET owner_user_id='esc_other' WHERE id=$1`, f.jc.Account.ID); e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(platform.WithActor(f.ctx, platform.Actor{ID: "esc_other", Role: "customer"}), userKey{}, User{ID: "esc_other", Role: "customer", Enabled: true})
	w := f.request(ctx, "POST", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay/quote", map[string]any{"mode": "per_run", "total_units": 0})
	if w.Code != 409 {
		t.Fatal("transferred principal became refundable", w.Code, w.Body.String())
	}
	f.wallet(8 * MicroUSDT)
}

func TestEscrowReserveRacesFullRefund(t *testing.T) {
	for i := 0; i < 6; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newEscrowFixture(t)
			f.checkout("per_run", 1, MicroUSDT)
			q := f.quote("per_run", 0)
			f.execution("race_exec")
			start := make(chan struct{})
			reserved := make(chan error, 1)
			refunded := make(chan *httptest.ResponseRecorder, 1)
			go func() { <-start; reserved <- f.s.Reserve(f.ctx, "race_exec", f.jc) }()
			go func() {
				<-start
				refunded <- f.request(f.actor, "PUT", "/api/v1/portal/jobs/"+f.jc.Job.ID+"/prepay", adjustment("race-full-refund", q))
			}()
			close(start)
			e, w := <-reserved, <-refunded
			c := f.credit()
			if e == nil {
				if w.Code == 200 || c.ReservedUnits != 1 || c.RemainingUnits != 0 {
					t.Fatal("reserved and refunded simultaneously", w.Code, c)
				}
				f.finish("race_exec", true)
				f.wallet(f.balance - MicroUSDT)
			} else {
				if w.Code != 200 || c.ReservedUnits != 0 || c.RemainingUnits != 0 {
					t.Fatal(e, w.Code, c)
				}
				f.wallet(f.balance)
			}
		})
	}
}

func TestEscrowLegacyMigrationConservesPrincipal(t *testing.T) {
	for _, tc := range []struct{ remaining, settled int64 }{{0, 0}, {0, 1}, {0, 3}, {1, 0}, {1, 1}, {3, 0}} {
		t.Run(fmt.Sprintf("remaining_%d_settled_%d", tc.remaining, tc.settled), func(t *testing.T) {
			remaining := tc.remaining
			f := newEscrowFixture(t)
			j, e := f.db.CreateJob(f.ctx, f.jc.Job)
			if e != nil {
				t.Fatal(e)
			}
			f.jc.Job = j
			if _, e = f.s.db.Exec(f.ctx, `INSERT INTO portal_task_credits(job_id,user_id,account_id,plan_id,mode,remaining_units) VALUES($1,'esc_owner',$2,$3,'per_run',$4)`, j.ID, j.AccountID, j.PlanID, remaining); e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.db.Exec(f.ctx, `INSERT INTO portal_task_credit_orders(id,user_id,job_id,mode,units,amount_micro) VALUES('legacy','esc_owner',$1,'per_run',3,10)`, j.ID); e != nil {
				t.Fatal(e)
			}
			for n := int64(0); n < 3-remaining; n++ {
				id := fmt.Sprintf("legacy_exec_%d", n)
				f.execution(id)
				state := "reserved"
				if n < tc.settled {
					state = "settled"
				}
				if _, e = f.s.db.Exec(f.ctx, `INSERT INTO portal_charges(execution_id,user_id,amount_micro,state,funding_source,credit_job_id,credit_mode,credit_units) VALUES($1,'esc_owner',0,$2,'prepaid',$3,'per_run',1)`, id, state, j.ID); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = f.s.db.Exec(f.ctx, escrowSchema); e != nil {
				t.Fatal(e)
			}
			first := f.credit()
			if first.AvailableMicro+first.ReservedMicro+first.SettledMicro != 10 || first.TotalUnits != 3 {
				t.Fatal("migration lost principal", first)
			}
			if first.RemainingUnits == 0 && first.AvailableMicro != 0 {
				t.Fatal("stranded available principal", first)
			}
			if first.SettledUnits == 0 && first.SettledMicro != 0 {
				t.Fatal("invented settlement", first)
			}
			if _, e = f.s.db.Exec(f.ctx, escrowSchema); e != nil {
				t.Fatal(e)
			}
			if next := f.credit(); first != next {
				t.Fatal("migration not idempotent", first, next)
			}
			f.wallet(f.balance)
		})
	}
}
