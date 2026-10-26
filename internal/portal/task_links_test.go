package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

func TestTaskScopeRejectsExpansion(t *testing.T) {
	s := taskLinkScope{MaxDistanceM: 3000, DailyRunLimit: 2, WeeklyRunLimit: 5, DailyDistanceLimitM: 6000, WeeklyDistanceLimitM: 15000, DayScope: "workday"}
	j := platform.AutomationJob{TargetDistanceM: 2000, DailyRunLimit: 1, WeeklyRunLimit: 3, DailyDistanceLimitM: 5000, WeeklyDistanceLimitM: 10000, DayScope: "workday", ScheduleMode: "manual"}
	if e := checkTaskScope(j, s); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*platform.AutomationJob){
		func(j *platform.AutomationJob) { j.TargetDistanceM = 3100 }, func(j *platform.AutomationJob) { j.TargetDistanceM = math.NaN() },
		func(j *platform.AutomationJob) { j.DailyRunLimit = 0 }, func(j *platform.AutomationJob) { j.WeeklyRunLimit = 6 },
		func(j *platform.AutomationJob) { j.DailyDistanceLimitM = 0 }, func(j *platform.AutomationJob) { j.WeeklyDistanceLimitM = 16000 },
		func(j *platform.AutomationJob) { j.DayScope = "every_day" }, func(j *platform.AutomationJob) { j.ScheduleMode = "plan_random" },
	} {
		changed := j
		change(&changed)
		if checkTaskScope(changed, s) == nil {
			t.Fatal("expanded order scope accepted", changed)
		}
	}
}

func TestTaskLinksPaidBoundsRotationAndManualRoutes(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	for _, id := range []string{"route_owner", "route_other"} {
		if _, e := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role,balance_micro) VALUES($1,$2,''::bytea,'customer',1000000000)`, id, id+"@example.test"); e != nil {
			t.Fatal(e)
		}
	}
	_, e := s.db.Exec(ctx, `UPDATE portal_settings SET config=jsonb_set(jsonb_set(config,'{mode}','"commercial"'),'{frontend_enabled}','true')`)
	if e != nil {
		t.Fatal(e)
	}
	actor := platform.WithActor(ctx, platform.Actor{ID: "route_owner", Role: "customer"})
	a, e := s.store.CreateAccount(actor, platform.Account{ID: "draw_account", Name: "测试账号", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.store.CreatePlan(actor, platform.RunningPlan{ID: "draw_plan", SourceAccountID: a.ID, RemoteCode: "remote_p", Name: "测试计划", TargetDistanceM: 2000, Rules: activity.RunRules{MinDistanceM: 1000, MaxDistanceM: 5000, DayMaxTimes: 3, WeekMaxTimes: 10}})
	if e != nil {
		t.Fatal(e)
	}
	ar, e := s.store.CreateArea(actor, platform.RunningArea{ID: "draw_area", SourceAccountID: a.ID, RemoteCode: "remote_a", Name: "测试区域", Center: geo.Point{Lat: 35, Lng: 116}, CandidateCount: 1})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.store.CreateJob(actor, platform.AutomationJob{ID: "draw_job", AccountID: a.ID, PlanID: p.ID, AreaID: ar.ID, TargetDistanceM: 2000, DailyRunLimit: 2, WeeklyRunLimit: 5, ScheduleMode: "manual", ManualTime: "06:30", DayScope: "every_day", Timezone: "Asia/Shanghai"})
	if e != nil {
		t.Fatal(e)
	}
	points := []geo.Point{ar.Center, geo.OffsetMeters(ar.Center, 2500, 0)}
	m, e := s.store.CreateManualRoute(actor, platform.ManualRoute{ID: "draw_route", Name: "手绘路线", AccountID: a.ID, PlanID: p.ID, AreaID: ar.ID, Points: points, DistanceM: 999999, CreatedByUserID: "spoofed", SourceJobID: "spoofed"})
	if e != nil {
		t.Fatal(e)
	}
	if m.CreatedByUserID != "route_owner" || m.SourceJobID != "" {
		t.Fatal("client spoofed route provenance", m)
	}
	if math.Abs(m.DistanceM-2500) > 1 {
		t.Fatal("client distance wasn't recomputed")
	}
	other := platform.WithActor(ctx, platform.Actor{ID: "route_other", Role: "customer"})
	if _, e = s.store.GetManualRoute(other, m.ID); e == nil {
		t.Fatal("cross-tenant route readable")
	}
	if got, e := s.store.ListManualRoutes(other); e != nil || len(got) != 0 {
		t.Fatal("cross-tenant route listed", e)
	}
	if e = s.store.DeleteManualRoute(other, m.ID); e == nil {
		t.Fatal("cross-tenant route deletion accepted")
	}
	j.RouteMode = "custom"
	j.ManualRouteID = m.ID
	if e = automation.NormalizeJob(actor, s.store, &j); e != nil {
		t.Fatal(e)
	}
	j, e = s.store.UpdateJob(actor, j)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := s.store.GetJobContext(actor, j.ID)
	if e != nil || stored.Job.RouteMode != "custom" || stored.Job.ManualRouteID != m.ID {
		t.Fatal("route mode/reference lost on reload", stored.Job, e)
	}
	listed, e := s.store.ListJobs(actor)
	if e != nil || len(listed) != 1 || listed[0].RouteMode != "custom" || listed[0].ManualRouteID != m.ID {
		t.Fatal("route mode/reference lost in list", listed, e)
	}
	if e = s.store.DeleteManualRoute(actor, m.ID); e == nil {
		t.Fatal("in-use route deleted")
	}
	short := m
	short.Points = []geo.Point{ar.Center, geo.OffsetMeters(ar.Center, 1500, 0)}
	if _, e = s.store.UpdateManualRoute(actor, short); e == nil {
		t.Fatal("in-use route shortened below job")
	}
	tooLong := j
	tooLong.TargetDistanceM = 2600
	if e = automation.NormalizeJob(actor, s.store, &tooLong); e == nil {
		t.Fatal("job longer than drawing accepted")
	}
	if _, e = s.store.UpdateJob(actor, tooLong); e == nil {
		t.Fatal("store accepted long job")
	}

	mux := http.NewServeMux()
	s.Mount(mux)
	ownerRequest := func(method string, body string, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/portal/jobs/"+j.ID+"/config-link", strings.NewReader(body)).WithContext(actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("link status %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	// A large wallet balance is NOT a prepaid entitlement.
	ownerRequest("POST", `{"days":30}`, 403)
	_, e = s.db.Exec(ctx, `INSERT INTO portal_entitlements(user_id,account_id,plan_id) VALUES($1,$2,$3)`, a.OwnerUserID, a.ID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	var link struct {
		URL string `json:"url"`
	}
	if e = json.Unmarshal(ownerRequest("POST", `{"days":30}`, 201), &link); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(link.URL)
	secret := strings.TrimPrefix(u.Fragment, "token=")
	public := func(method, suffix, secret string, body []byte, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/public/tasks/"+j.ID+suffix, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("public %s status %d want %d: %s", method, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	info := public("GET", "", secret, nil, 200)
	if !bytes.Contains(info, []byte(`"map_api_configured":false`)) {
		t.Fatal("missing map capability in scoped response")
	}
	for _, key := range []string{"password_cipher", "token_cipher", "balance_micro", "login_name"} {
		if bytes.Contains(info, []byte(key)) {
			t.Fatal("secret/private info in response", key)
		}
	}
	public("GET", "", "wrong", nil, 404)
	input := taskConfigInput{TargetDistanceM: 1800, RouteMode: "custom", ManualRouteID: m.ID, ScheduleMode: "manual", ManualTime: "07:20", DayScope: "every_day", DailyRunLimit: 1, WeeklyRunLimit: 4}
	put := func(in taskConfigInput, want int) {
		t.Helper()
		raw, _ := json.Marshal(in)
		public("PUT", "", secret, raw, want)
	}
	put(input, 200)
	changed, e := db.GetJob(ctx, j.ID)
	if e != nil || changed.TargetDistanceM != 1800 || changed.ManualTime != "07:20" || changed.Enabled || changed.RouteMode != "custom" || changed.ManualRouteID != m.ID {
		t.Fatal("scoped update mismatch", changed, e)
	}
	bad := input
	bad.RouteMode = "auto"
	put(bad, 400) // No configured map key: selecting automatic is rejected too.
	bad = input
	bad.TargetDistanceM = 2100
	put(bad, 403)
	bad = input
	bad.DailyRunLimit = 3
	put(bad, 403)
	public("PUT", "", secret, []byte(`{"account_id":"other"}`), 400)
	public("PUT", "", secret, []byte(`{"enabled":true}`), 400)
	bad = input
	bad.RoutePoints = []geo.Point{ar.Center, geo.OffsetMeters(ar.Center, 1200, 0)}
	put(bad, 400)
	input.RoutePoints = points
	put(input, 200)
	ownRoutes, e := s.store.ListManualRoutes(actor)
	if e != nil || len(ownRoutes) != 2 {
		t.Fatal("owner cannot see direct and linked routes", len(ownRoutes), e)
	}
	var linked platform.ManualRoute
	for _, r := range ownRoutes {
		if r.ID != m.ID {
			linked = r
		}
	}
	if linked.SourceJobID != j.ID || linked.CreatedByUserID != a.OwnerUserID {
		t.Fatal("link route lost provenance", linked)
	}
	if _, e = s.store.GetManualRoute(other, linked.ID); e == nil {
		t.Fatal("another customer reads link route")
	}
	if got, e := s.store.ListManualRoutes(other); e != nil || len(got) != 0 {
		t.Fatal("another customer lists link route", got, e)
	}
	if _, e = s.store.UpdateManualRoute(other, linked); e == nil {
		t.Fatal("another customer edits link route")
	}
	if e = s.store.DeleteManualRoute(other, linked.ID); e == nil {
		t.Fatal("another customer deletes link route")
	}
	admin := platform.WithActor(ctx, platform.Actor{ID: "route_admin", Role: "superadmin"})
	if got, e := s.store.ListManualRoutes(admin); e != nil || len(got) != 2 {
		t.Fatal("admin cannot see all routes", len(got), e)
	}
	linked.CreatedByUserID = "spoofed"
	linked.SourceJobID = "spoofed"
	linked, e = s.store.UpdateManualRoute(actor, linked)
	if e != nil || linked.CreatedByUserID != a.OwnerUserID || linked.SourceJobID != j.ID {
		t.Fatal("edit rewrote provenance", linked, e)
	}

	var img bytes.Buffer
	if e = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 64, 64))); e != nil {
		t.Fatal(e)
	}
	public("PUT", "/face", secret, img.Bytes(), 200)
	account, e := db.GetAccount(ctx, a.ID)
	if e != nil || len(account.FaceCustomCipher) == 0 {
		t.Fatal("photo not saved", e)
	}
	plain, e := s.vault.Decrypt(account.FaceCustomCipher, "account:"+a.ID+":face-custom")
	if e != nil || len(plain) == 0 {
		t.Fatal("photo encryption invalid", e)
	}
	ownerRequest("POST", `{"days":30}`, 201)
	public("GET", "", secret, nil, 404)
	public("PUT", "/face", secret, img.Bytes(), 404)
	json.Unmarshal(ownerRequest("GET", "", 200), &link)
	u, _ = url.Parse(link.URL)
	secret = strings.TrimPrefix(u.Fragment, "token=")
	_, e = s.db.Exec(ctx, `DELETE FROM portal_entitlements WHERE account_id=$1`, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	public("GET", "", secret, nil, 404)
	public("PUT", "/face", secret, img.Bytes(), 404)
	_, e = s.db.Exec(ctx, `INSERT INTO portal_entitlements(user_id,account_id,plan_id) VALUES($1,$2,$3)`, a.OwnerUserID, a.ID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(ctx, `UPDATE portal_users SET enabled=false WHERE id=$1`, a.OwnerUserID); e != nil {
		t.Fatal(e)
	}
	public("GET", "", secret, nil, 404)
	public("PUT", "/face", secret, img.Bytes(), 404)
	if _, e = s.db.Exec(ctx, `UPDATE portal_users SET enabled=true WHERE id=$1`, a.OwnerUserID); e != nil {
		t.Fatal(e)
	}
	public("GET", "", secret, nil, 200)
	if _, e = s.db.Exec(ctx, `UPDATE portal_task_links SET expires_at=now()-interval '1 second' WHERE job_id=$1`, j.ID); e != nil {
		t.Fatal(e)
	}
	public("GET", "", secret, nil, 404)
	public("PUT", "/face", secret, img.Bytes(), 404)
	ownerRequest("DELETE", "", 200)
	public("GET", "", secret, nil, 404)
	var balance int64
	s.db.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id=$1`, a.OwnerUserID).Scan(&balance)
	if balance != 1000000000 {
		t.Fatal("public link spent wallet balance")
	}
	var mode string
	if e = s.db.QueryRow(ctx, `SELECT route_mode FROM automation_jobs WHERE id=$1`, j.ID).Scan(&mode); e != nil || mode != "custom" {
		t.Fatal(mode, e)
	}
}
