package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

func TestPrepayCostIntegerRounding(t *testing.T) {
	p := Pricing{PerRunMicro: 1234567, PerKMMicro: 1234567}
	for _, tc := range []struct {
		mode        string
		units, want int64
	}{{"per_run", 3, 3703701}, {"per_km", 100, 123457}, {"per_km", 1100, 1358024}, {"per_km", 1000000, 1234567000}} {
		got, e := prepayCost(p, tc.mode, tc.units)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	for _, tc := range []struct {
		mode  string
		units int64
	}{{"per_run", 0}, {"per_run", 10001}, {"per_km", 99}, {"per_km", 150}, {"per_km", 1000100}, {"balance", 1}} {
		if _, e := prepayCost(p, tc.mode, tc.units); e == nil {
			t.Fatal("invalid purchase accepted", tc)
		}
	}
}

func TestPrepaidTasksDoNotOverspendOrFallBackToWallet(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	_, e := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role,balance_micro) VALUES('credit_admin','a@example.test',''::bytea,'superadmin',0),('credit_owner','c@example.test',''::bytea,'customer',10000000),('credit_other','b@example.test',''::bytea,'customer',10000000)`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := Defaults()
	cfg.Mode = "commercial"
	cfg.FrontendEnabled = true
	cfg.Pricing = Pricing{Enabled: true, PerRunMicro: 1000000, PerKMMicro: 1000000, PlanPackageMicro: 5000000}
	raw, _ := json.Marshal(cfg)
	_, e = s.db.Exec(ctx, `UPDATE portal_settings SET config=$1`, raw)
	if e != nil {
		t.Fatal(e)
	}
	owner := User{ID: "credit_owner", Role: "customer", Enabled: true}
	actor := context.WithValue(platform.WithActor(ctx, platform.Actor{ID: owner.ID, Role: owner.Role}), userKey{}, owner)
	a, e := s.store.CreateAccount(actor, platform.Account{ID: "credit_account", Name: "预付测试", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := db.CreatePlan(ctx, platform.RunningPlan{ID: "credit_plan", SourceAccountID: a.ID, RemoteCode: "rp", Name: "计划", TargetDistanceM: 2000, Rules: activity.RunRules{MinDistanceM: 1000, MaxDistanceM: 5000, DayMaxTimes: 3, WeekMaxTimes: 10}})
	if e != nil {
		t.Fatal(e)
	}
	ar, e := db.CreateArea(ctx, platform.RunningArea{ID: "credit_area", SourceAccountID: a.ID, RemoteCode: "ra", Name: "区域", Center: geo.Point{Lat: 35, Lng: 116}, CandidateCount: 1})
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.CreateJob(ctx, platform.AutomationJob{ID: "credit_job", AccountID: a.ID, PlanID: p.ID, AreaID: ar.ID, TargetDistanceM: 2000, DailyRunLimit: 2, WeeklyRunLimit: 5, ScheduleMode: "manual", ManualTime: "06:30", DayScope: "every_day", Timezone: "Asia/Shanghai"})
	if e != nil {
		t.Fatal(e)
	}
	jc := platform.JobContext{Job: j, Account: a, Plan: p, Area: ar}
	mux := http.NewServeMux()
	s.Mount(mux)
	callAs := func(requestContext context.Context, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(requestContext)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		return callAs(actor, method, path, body, want)
	}
	purchase := func(id, mode string, units, amount int64, want int) {
		t.Helper()
		call("POST", "/api/v1/portal/jobs/"+j.ID+"/prepay", map[string]any{"request_id": id, "mode": mode, "units": units, "expected_amount_micro": amount}, want)
	}
	for _, tc := range []struct {
		id, role string
		want     int
	}{{"credit_other", "customer", 404}, {"credit_admin", "superadmin", 403}} {
		other := context.WithValue(platform.WithActor(ctx, platform.Actor{ID: tc.id, Role: tc.role}), userKey{}, User{ID: tc.id, Role: tc.role, Enabled: true})
		callAs(other, "POST", "/api/v1/portal/jobs/"+j.ID+"/prepay", map[string]any{"request_id": "forbidden-prepay-01", "mode": "per_run", "units": 2, "expected_amount_micro": 2000000}, tc.want)
		if tc.role == "superadmin" {
			info := callAs(other, "GET", "/api/v1/portal/jobs/"+j.ID+"/prepay", nil, 200)
			if bytes.Contains(info, []byte(`"balance_micro"`)) || bytes.Contains(info, []byte(`"can_purchase":true`)) {
				t.Fatal("another user's wallet exposed or spendable", string(info))
			}
		}
	}
	purchase("run-order-fixture-01", "per_run", 2, 2000000, 201)
	purchase("run-order-fixture-01", "per_run", 2, 2000000, 200)
	purchase("run-order-fixture-01", "per_run", 3, 3000000, 409)
	purchase("run-order-wrong-price", "per_run", 1, 1, 409)
	purchase("km-order-wrong-mode", "per_km", 2000, 2000000, 409)
	state := func(wantUnits, wantBalance int64) {
		t.Helper()
		var units, balance int64
		if e := s.db.QueryRow(ctx, `SELECT c.remaining_units,u.balance_micro FROM portal_task_credits c JOIN portal_users u ON u.id=c.user_id WHERE c.job_id=$1`, j.ID).Scan(&units, &balance); e != nil || units != wantUnits || balance != wantBalance {
			t.Fatalf("credit=%d balance=%d want=%d/%d err=%v", units, balance, wantUnits, wantBalance, e)
		}
	}
	state(2, 8000000)
	call("PUT", "/api/v1/portal/jobs/"+j.ID+"/billing", map[string]string{"mode": "per_km"}, 409)
	link := func() string {
		t.Helper()
		var link struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call("POST", "/api/v1/portal/jobs/"+j.ID+"/config-link", map[string]int{"days": 30}, 201), &link)
		u, _ := url.Parse(link.URL)
		return strings.TrimPrefix(u.Fragment, "token=")
	}
	secret := link()
	public := func(method, secret string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1/public/tasks/"+j.ID, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("public status=%d want=%d: %s", w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	public("GET", secret, nil, 200)
	newExecution := func(id string) {
		t.Helper()
		if e := db.CreateExecution(ctx, platform.Execution{ID: id, JobID: j.ID, AccountID: a.ID, ScheduledFor: time.Now(), StartedAt: time.Now(), Status: "succeeded"}); e != nil {
			t.Fatal(e)
		}
	}
	newExecution("credit_exec_retry")
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Reserve(ctx, "credit_exec_retry", jc) != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatal("idempotent reservation failed")
	}
	state(1, 8000000)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Finish(ctx, "credit_exec_retry", false) != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatal("idempotent refund failed")
	}
	state(2, 8000000)
	for _, id := range []string{"credit_exec_1", "credit_exec_2"} {
		newExecution(id)
		if e = s.Reserve(ctx, id, jc); e != nil {
			t.Fatal(e)
		}
		if e = s.Finish(ctx, id, true); e != nil {
			t.Fatal(e)
		}
	}
	state(0, 8000000)
	newExecution("credit_no_fallback")
	if e = s.Reserve(ctx, "credit_no_fallback", jc); e == nil {
		t.Fatal("exhausted credits fell through to wallet")
	}
	state(0, 8000000)
	public("GET", secret, nil, 404)
	_, e = s.db.Exec(ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,enabled}','false')`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Reserve(ctx, "credit_no_fallback", jc); e == nil {
		t.Fatal("disabling pricing bypassed exhausted prepaid credits")
	}
	_, e = s.db.Exec(ctx, `UPDATE portal_settings SET config=jsonb_set(config,'{pricing,enabled}','true')`)
	if e != nil {
		t.Fatal(e)
	}
	purchase("km-order-fixture-01", "per_km", 3000, 3000000, 201)
	state(3000, 5000000)
	public("GET", secret, nil, 404) // mode change requires a newly scoped capability
	secret = link()
	newExecution("credit_km_retry")
	if e = s.Reserve(ctx, "credit_km_retry", jc); e != nil {
		t.Fatal(e)
	}
	state(1000, 5000000)
	var info struct {
		Limits struct {
			Max float64 `json:"max_distance_m"`
		} `json:"limits"`
		Remaining int64 `json:"remaining_units"`
	}
	json.Unmarshal(public("GET", secret, nil, 200), &info)
	if info.Limits.Max != 1000 || info.Remaining != 1000 {
		t.Fatal("live remaining mileage not applied", info)
	}
	input := taskConfigInput{TargetDistanceM: 1500, RouteMode: "auto", ScheduleMode: "manual", ManualTime: "07:00", DayScope: "every_day", DailyRunLimit: 1, WeeklyRunLimit: 4}
	public("PUT", secret, input, 403)
	input.TargetDistanceM = 1000
	public("PUT", secret, input, 200)
	if e = s.Finish(ctx, "credit_km_retry", false); e != nil {
		t.Fatal(e)
	}
	state(3000, 5000000)
	// Two distinct executions cannot both spend 2 km from a 3 km credit.
	newExecution("credit_concurrent_1")
	newExecution("credit_concurrent_2")
	var accepted atomic.Int32
	for _, id := range []string{"credit_concurrent_1", "credit_concurrent_2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if s.Reserve(ctx, id, jc) == nil {
				accepted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("concurrent prepaid overspend", accepted.Load())
	}
	state(1000, 5000000)
	// A caller cannot use another account's budget to fund a new binding.
	otherPlan, e := db.CreatePlan(ctx, platform.RunningPlan{ID: "credit_other_plan", SourceAccountID: a.ID, RemoteCode: "other-rp", Name: "另一个计划", TargetDistanceM: 2000, Rules: p.Rules})
	if e != nil {
		t.Fatal(e)
	}
	changed := j
	changed.PlanID = otherPlan.ID
	if _, e = db.UpdateJob(ctx, changed); e == nil {
		t.Fatal("prepaid identity changed")
	}
	var orders, ledger int
	s.db.QueryRow(ctx, `SELECT count(*) FROM portal_task_credit_orders WHERE job_id=$1`, j.ID).Scan(&orders)
	s.db.QueryRow(ctx, `SELECT count(*) FROM portal_ledger WHERE kind='task_prepay' AND user_id=$1`, owner.ID).Scan(&ledger)
	if orders != 2 || ledger != 2 {
		t.Fatal("idempotent orders/ledger duplicated", orders, ledger)
	}
}
