package portal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func testService(t *testing.T) (*Service, *store.PostgresStore) {
	t.Helper()
	dsn := os.Getenv("PORTAL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PORTAL_TEST_DATABASE_URL; test creates and removes its own isolated schema")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "testportal_" + strings.TrimPrefix(platform.NewID("test"), "test_")
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	database, e := store.NewPostgres(ctx, store.PostgresOptions{URL: u.String(), MaxConns: 12})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		database.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	})
	vault, e := securestore.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(ctx, database.Pool(), vault, store.TenantStore{Store: database})
	if e != nil {
		t.Fatal(e)
	}
	return s, database
}

type testFaces struct{ calls atomic.Int32 }

func (f *testFaces) SetCustomFace(context.Context, string, string, []byte) (platform.FaceProfile, error) {
	f.calls.Add(1)
	return platform.FaceProfile{}, nil
}
func portalTestServer(s *Service) *httptest.Server {
	mux := http.NewServeMux()
	s.Mount(mux)
	mux.HandleFunc("GET /api/v1/test/accounts", func(w http.ResponseWriter, r *http.Request) {
		items, e := s.store.ListAccounts(r.Context())
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		jsonReply(w, 200, items)
	})
	mux.HandleFunc("GET /api/v1/test/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, e := s.store.GetAccount(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, 404, "not found")
			return
		}
		jsonReply(w, 200, a)
	})
	return httptest.NewServer(s.Wrap(mux))
}
func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 20 * time.Second}
}
func requestTest(t *testing.T, c *http.Client, base, method, path string, input any, want int) []byte {
	t.Helper()
	var raw []byte
	if input != nil {
		raw, _ = json.Marshal(input)
	}
	r, e := http.NewRequest(method, base+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", base)
	r.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, want, body)
	}
	return body
}
func userOf(t *testing.T, c *http.Client, base string) User {
	var u User
	if e := json.Unmarshal(requestTest(t, c, base, "GET", "/api/v1/portal/me", nil, 200), &u); e != nil {
		t.Fatal(e)
	}
	return u
}

func TestPortalIntegration(t *testing.T) {
	t.Setenv("PORTAL_SETUP_TOKEN", "")
	s, db := testService(t)
	ctx := context.Background()
	old, e := db.CreateAccount(ctx, platform.Account{ID: "old_account", LoginName: "old", Name: "旧数据", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	server := portalTestServer(s)
	defer server.Close()
	admin := newClient()
	guest := newClient()
	requestTest(t, guest, server.URL, "GET", "/api/v1/test/accounts", nil, 428)
	requestTest(t, admin, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "admin@example.com", "password": "test-password-12345", "mode": "commercial", "frontend_enabled": true}, 201)
	root := userOf(t, admin, server.URL)
	if root.Role != "superadmin" {
		t.Fatal(root)
	}
	old, e = db.GetAccount(ctx, old.ID)
	if e != nil || old.OwnerUserID != root.ID {
		t.Fatal("legacy account was not assigned to superadmin", e)
	}
	requestTest(t, guest, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "other@example.com", "password": "test-password-12345", "mode": "personal"}, 409)
	requestTest(t, guest, server.URL, "GET", "/api/v1/test/accounts", nil, 401)
	requestTest(t, guest, server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "a@example.com", "password": "test-password-12345"}, 201)
	a := userOf(t, guest, server.URL)
	bClient := newClient()
	requestTest(t, bClient, server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "b@example.com", "password": "test-password-12345"}, 201)
	b := userOf(t, bClient, server.URL)
	aCtx := platform.WithActor(ctx, platform.Actor{ID: a.ID, Role: "customer"})
	bCtx := platform.WithActor(ctx, platform.Actor{ID: b.ID, Role: "customer"})
	account, e := s.store.CreateAccount(aCtx, platform.Account{ID: "account_a", LoginName: "a", Name: "客户 A", Enabled: true, OwnerUserID: b.ID})
	if e != nil {
		t.Fatal(e)
	}
	if account.OwnerUserID != a.ID {
		t.Fatal("client-selected owner was accepted")
	}
	_, e = s.store.CreateAccount(bCtx, platform.Account{ID: "account_b", LoginName: "b", Name: "客户 B", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	var own []platform.Account
	json.Unmarshal(requestTest(t, guest, server.URL, "GET", "/api/v1/test/accounts", nil, 200), &own)
	if len(own) != 1 || own[0].ID != account.ID {
		t.Fatalf("cross-tenant list %+v", own)
	}
	requestTest(t, bClient, server.URL, "GET", "/api/v1/test/accounts/"+account.ID, nil, 404)
	requestTest(t, guest, server.URL, "GET", "/api/v1/portal/settings", nil, 403)
	bad, _ := http.NewRequest("POST", server.URL+"/api/v1/portal/auth/login", bytes.NewBufferString(`{}`))
	bad.Header.Set("Origin", "https://evil.example")
	resp, e := guest.Do(bad)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-origin write accepted")
	}
	linkPath := "/api/v1/portal/accounts/" + account.ID + "/face-link"
	requestTest(t, bClient, server.URL, "POST", linkPath, map[string]int{"days": 30}, 404)
	var link struct {
		URL string `json:"url"`
	}
	json.Unmarshal(requestTest(t, guest, server.URL, "POST", linkPath, map[string]int{"days": 30}, 201), &link)
	linkURL, _ := url.Parse(link.URL)
	secret := strings.TrimPrefix(linkURL.Fragment, "token=")
	publicPath := "/api/v1/public/face/" + account.ID
	getPublic := func(secret string, want int) {
		t.Helper()
		r, _ := http.NewRequest("GET", server.URL+publicPath, nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		resp, e := newClient().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("public token status=%d want=%d", resp.StatusCode, want)
		}
	}
	getPublic(secret, 200)
	getPublic("wrong", 404)
	faces := &testFaces{}
	s.Faces = faces
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	r, _ := http.NewRequest("PUT", server.URL+publicPath, &imageBytes)
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Origin", server.URL)
	r.Header.Set("Content-Type", "image/png")
	resp, e = newClient().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || faces.calls.Load() != 1 {
		t.Fatal("public portrait upload failed", resp.StatusCode)
	}
	requestTest(t, guest, server.URL, "POST", linkPath, map[string]int{"days": 30}, 201)
	getPublic(secret, 404)
	var batch struct {
		Codes []string `json:"codes"`
	}
	json.Unmarshal(requestTest(t, admin, server.URL, "POST", "/api/v1/portal/codes", map[string]any{"count": 1, "amount_micro": 5 * MicroUSDT, "days": 1}, 201), &batch)
	if len(batch.Codes) != 1 {
		t.Fatal("missing recharge code")
	}
	var wg sync.WaitGroup
	var redeemed atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]string{"code": batch.Codes[0]})
			r, _ := http.NewRequest("POST", server.URL+"/api/v1/portal/redeem", bytes.NewReader(raw))
			r.Header.Set("Origin", server.URL)
			resp, e := guest.Do(r)
			if e == nil {
				if resp.StatusCode == 200 {
					redeemed.Add(1)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if redeemed.Load() != 1 {
		t.Fatal("code redeemed more than once", redeemed.Load())
	}
	if got := userOf(t, guest, server.URL).BalanceMicro; got != 5*MicroUSDT {
		t.Fatal("wrong balance", got)
	}
	plan := platform.RunningPlan{ID: "plan_a", SourceAccountID: account.ID, RemoteCode: "remote_plan", Name: "测试计划", TargetDistanceM: 3000}
	plan, e = s.store.CreatePlan(aCtx, plan)
	if e != nil {
		t.Fatal(e)
	}
	area, e := s.store.CreateArea(aCtx, platform.RunningArea{ID: "area_a", SourceAccountID: account.ID, Name: "测试区域", CandidateCount: 4})
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.store.CreateJob(aCtx, platform.AutomationJob{ID: "job_a", AccountID: account.ID, PlanID: plan.ID, AreaID: area.ID, Timezone: "Asia/Shanghai", ScheduleMode: "manual", DayScope: "every_day"})
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := s.Settings(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Pricing = Pricing{Enabled: true, PerRunMicro: MicroUSDT, PerKMMicro: MicroUSDT, PlanPackageMicro: 2 * MicroUSDT}
	raw, _ := json.Marshal(cfg)
	if _, e = db.Pool().Exec(ctx, `UPDATE portal_settings SET config=$1 WHERE id=1`, raw); e != nil {
		t.Fatal(e)
	}
	execution := platform.Execution{ID: "exec_a", JobID: job.ID, AccountID: account.ID, StartedAt: time.Now(), ScheduledFor: time.Now(), Status: "running"}
	if e = db.CreateExecution(ctx, execution); e != nil {
		t.Fatal(e)
	}
	jc := platform.JobContext{Job: job, Account: account, Plan: plan, Area: area}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Reserve(ctx, execution.ID, jc); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if got := userOf(t, guest, server.URL).BalanceMicro; got != 4*MicroUSDT {
		t.Fatal("duplicate charge", got)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Finish(ctx, execution.ID, false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if got := userOf(t, guest, server.URL).BalanceMicro; got != 5*MicroUSDT {
		t.Fatal("duplicate refund", got)
	}
	requestTest(t, guest, server.URL, "POST", "/api/v1/portal/packages", map[string]string{"account_id": account.ID, "plan_id": plan.ID}, 201)
	requestTest(t, guest, server.URL, "POST", "/api/v1/portal/packages", map[string]string{"account_id": account.ID, "plan_id": plan.ID}, 409)
	if got := userOf(t, guest, server.URL).BalanceMicro; got != 3*MicroUSDT {
		t.Fatal("duplicate package payment", got)
	}
	address := DepositAddress{UserID: a.ID, Address: MainnetUSDT}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.creditDeposit(ctx, address, strings.Repeat("a", 64), 0, MicroUSDT); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if got := userOf(t, guest, server.URL).BalanceMicro; got != 4*MicroUSDT {
		t.Fatal("duplicate deposit credit", got)
	}
	if s.claimCollectionTime(ctx, cfg.Wallet) {
		t.Fatal("first schedule initialization triggered an immediate collection")
	}
	if _, e = db.Pool().Exec(ctx, `UPDATE portal_settings SET next_collect_at=now()-interval '1 minute' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if !s.claimCollectionTime(ctx, cfg.Wallet) || s.claimCollectionTime(ctx, cfg.Wallet) {
		t.Fatal("collection schedule was not claimed exactly once")
	}
	requestTest(t, admin, server.URL, "PUT", "/api/v1/portal/users/"+a.ID, map[string]bool{"enabled": false}, 200)
	requestTest(t, guest, server.URL, "GET", "/api/v1/test/accounts", nil, 401)
}

func TestWalletHTTPIntegration(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	key := strings.Repeat("b", 64)
	seen := atomic.Int32{}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if strings.Contains(r.URL.Path, "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC") {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			jsonReply(w, 200, map[string]any{"id": key, "blockNumber": 123, "receipt": map[string]string{"result": "SUCCESS"}, "log": []any{map[string]any{"address": "a614f803b6fd780986a42c78ec9c7f77e6ded13c", "topics": []string{transferTopic, strings.Repeat("0", 64), "000000000000000000000000a614f803b6fd780986a42c78ec9c7f77e6ded13c"}, "data": fmt.Sprintf("%064x", MicroUSDT)}}})
			return
		}
		if r.URL.Query().Get("only_confirmed") != "true" || r.URL.Query().Get("contract_address") != MainnetUSDT {
			t.Error("unconfirmed or unexpected token query")
		}
		jsonReply(w, 200, map[string]any{"success": true, "data": []any{map[string]string{"transaction_id": key}}, "meta": map[string]string{}})
	}))
	defer mock.Close()
	cfg := Defaults().Wallet
	cfg.NodeURL = mock.URL
	cfg.Contract = MainnetUSDT
	_, e := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role)VALUES('u','test@example.com',''::bytea,'customer')`)
	if e != nil {
		t.Fatal(e)
	}
	a := DepositAddress{UserID: "u", Address: MainnetUSDT, CreatedAt: time.Now().Add(-time.Hour)}
	for i := 0; i < 2; i++ {
		if e = s.scanAddress(ctx, cfg, a, a.CreatedAt); e != nil {
			t.Fatal(e)
		}
	}
	var balance int64
	if e = s.db.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id='u'`).Scan(&balance); e != nil {
		t.Fatal(e)
	}
	if balance != MicroUSDT || seen.Load() != 4 {
		t.Fatal("mock confirmed deposit not idempotent", balance, seen.Load())
	}
	_, e = s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role)VALUES('bad','bad@example.test',''::bytea,'customer'); INSERT INTO portal_wallet_addresses(user_id,address,derivation_index)VALUES('bad','TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC',0),('u','TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',1);`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.scanDeposits(ctx, cfg); e == nil {
		t.Fatal("missing address failure")
	}
	var goodScanned, badScanned bool
	if e = s.db.QueryRow(ctx, `SELECT scanned_at IS NOT NULL FROM portal_wallet_addresses WHERE user_id='u'`).Scan(&goodScanned); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(ctx, `SELECT scanned_at IS NOT NULL FROM portal_wallet_addresses WHERE user_id='bad'`).Scan(&badScanned); e != nil {
		t.Fatal(e)
	}
	if !goodScanned || badScanned {
		t.Fatal("one failed address blocked another customer or advanced a failed cursor")
	}
}

func TestRegistrationEmailVerification(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	// Fresh deployments must work before a map API key has been configured.
	if _, e := db.UpdateSystemSettings(ctx, platform.SystemSettings{WorkerProcesses: 2, WorkerConcurrency: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role)VALUES('root','root@example.test',''::bytea,'superadmin')`); e != nil {
		t.Fatal(e)
	}
	cfg := Defaults()
	cfg.Mode = "commercial"
	cfg.FrontendEnabled = true
	cfg.RegistrationEnabled = true
	cfg.EmailVerification = true
	raw, _ := json.Marshal(cfg)
	if _, e := s.db.Exec(ctx, `UPDATE portal_settings SET config=$1 WHERE id=1`, raw); e != nil {
		t.Fatal(e)
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("123456"), 10)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(ctx, `INSERT INTO portal_email_codes(id,email,code_hash,expires_at)VALUES('challenge','verify@example.test',$1,now()+interval '10 minutes')`, hash); e != nil {
		t.Fatal(e)
	}
	server := portalTestServer(s)
	defer server.Close()
	c := newClient()
	requestTest(t, c, server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "verify@example.test", "password": "test-password-12345", "challenge_id": "challenge", "code": "000000"}, 400)
	requestTest(t, c, server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "verify@example.test", "password": "test-password-12345", "challenge_id": "challenge", "code": "123456"}, 201)
	if u := userOf(t, c, server.URL); !u.EmailVerified {
		t.Fatal("successful OTP registration is unverified")
	}
	requestTest(t, newClient(), server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "verify@example.test", "password": "test-password-12345", "challenge_id": "challenge", "code": "123456"}, 400)
}
