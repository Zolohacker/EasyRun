package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func depositScanClient(t *testing.T, s *Service, base, id, role, address string, index int) *http.Client {
	t.Helper()
	_, err := s.db.Exec(context.Background(), `INSERT INTO portal_users(id,email,password_hash,role)VALUES($1,$2,''::bytea,$3)`, id, id+"@example.test", role)
	if err != nil {
		t.Fatal(err)
	}
	if address != "" {
		_, err = s.db.Exec(context.Background(), `INSERT INTO portal_wallet_addresses(user_id,address,derivation_index)VALUES($1,$2,$3)`, id, address, index)
		if err != nil {
			t.Fatal(err)
		}
	}
	fixtureToken := "fixture-scan-session-" + id
	_, err = s.db.Exec(context.Background(), `INSERT INTO portal_sessions(token_hash,user_id,expires_at)VALUES($1,$2,now()+interval '1 hour')`, digest(fixtureToken), id)
	if err != nil {
		t.Fatal(err)
	}
	client := newClient()
	u, _ := url.Parse(base)
	client.Jar.SetCookies(u, []*http.Cookie{{Name: cookieName, Value: fixtureToken, Path: "/"}})
	return client
}

func depositScanSettings(t *testing.T, s *Service, node string) WalletConfig {
	t.Helper()
	cfg := Defaults()
	cfg.Mode, cfg.FrontendEnabled = "commercial", true
	cfg.Wallet.NodeURL, cfg.Wallet.NodeProvider = node, "trongrid"
	raw, _ := json.Marshal(cfg)
	if _, err := s.db.Exec(context.Background(), `UPDATE portal_settings SET config=$1 WHERE id=1`, raw); err != nil {
		t.Fatal(err)
	}
	return cfg.Wallet
}

func scanStateOf(t *testing.T, c *http.Client, base string) depositScanState {
	t.Helper()
	var status depositScanState
	if err := json.Unmarshal(requestTest(t, c, base, "GET", "/api/v1/portal/wallet/scan", nil, 200), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestManualDepositScanCooldownIsolationAndRestart(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	var calls, otherCalls atomic.Int32
	txID := strings.Repeat("c", 64)
	otherAddress := "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, otherAddress) {
			otherCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			jsonReply(w, 200, map[string]any{"id": txID, "blockNumber": 123, "receipt": map[string]string{"result": "SUCCESS"}, "log": []any{map[string]any{"address": "a614f803b6fd780986a42c78ec9c7f77e6ded13c", "topics": []string{transferTopic, strings.Repeat("0", 64), "000000000000000000000000a614f803b6fd780986a42c78ec9c7f77e6ded13c"}, "data": fmt.Sprintf("%064x", MicroUSDT)}}})
			return
		}
		if !strings.Contains(r.URL.Path, MainnetUSDT) || r.URL.Query().Get("only_confirmed") != "true" {
			t.Error("unexpected address or unconfirmed scan")
		}
		jsonReply(w, 200, map[string]any{"success": true, "data": []any{map[string]string{"transaction_id": txID}}, "meta": map[string]any{}})
	}))
	defer node.Close()
	wallet := depositScanSettings(t, s, node.URL)
	server := portalTestServer(s)
	defer server.Close()
	admin := depositScanClient(t, s, server.URL, "root", "superadmin", "", 0)
	a := depositScanClient(t, s, server.URL, "a", "customer", MainnetUSDT, 1)
	b := depositScanClient(t, s, server.URL, "b", "customer", otherAddress, 2)
	requestTest(t, newClient(), server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 401)
	requestTest(t, admin, server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 409)
	requestTest(t, a, server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]string{"user_id": "b"}, 400)
	if v := scanStateOf(t, a, server.URL); v.State != "idle" || v.RetryAfterSeconds != 0 {
		t.Fatalf("initial status %+v", v)
	}
	if err := s.processDepositScans(ctx, wallet); err != nil || calls.Load() != 0 {
		t.Fatal("unrequested address scan", err, calls.Load())
	}
	var accepted, limited atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := http.NewRequest("POST", server.URL+"/api/v1/portal/wallet/scan", bytes.NewBufferString("{}"))
			r.Header.Set("Origin", server.URL)
			r.Header.Set("Content-Type", "application/json")
			res, err := a.Do(r)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, res.Body)
			switch res.StatusCode {
			case http.StatusAccepted:
				accepted.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
				seconds, err := strconv.Atoi(res.Header.Get("Retry-After"))
				if err != nil || seconds <= 0 || seconds > 180 {
					t.Error("missing or incorrect cooldown header", seconds, err)
				}
			default:
				t.Error("unexpected scan status", res.StatusCode)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || limited.Load() != 11 || calls.Load() != 0 {
		t.Fatal("concurrent submissions bypassed cooldown or invoked chain inline", accepted.Load(), limited.Load(), calls.Load())
	}
	if v := scanStateOf(t, b, server.URL); v.State != "idle" {
		t.Fatal("cross-user status leakage", v)
	}
	// A new process shares the persisted cooldown and pending request.
	restarted, err := New(ctx, db.Pool(), s.vault, s.store)
	if err != nil {
		t.Fatal(err)
	}
	server2 := portalTestServer(restarted)
	defer server2.Close()
	requestTest(t, a, server2.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 429)
	for _, worker := range []*Service{s, restarted} {
		wg.Add(1)
		go func(worker *Service) {
			defer wg.Done()
			if err := worker.processDepositScans(ctx, wallet); err != nil {
				t.Error(err)
			}
		}(worker)
	}
	wg.Wait()
	v := scanStateOf(t, a, server.URL)
	if v.State != "succeeded" || v.CreditedMicro != MicroUSDT || v.DepositCount != 1 || v.RetryAfterSeconds <= 0 || v.RetryAfterSeconds > 180 || calls.Load() != 2 || otherCalls.Load() != 0 {
		t.Fatalf("scoped recovery/result failed: %+v, calls=%d other=%d", v, calls.Load(), otherCalls.Load())
	}
	var balance int64
	if err := s.db.QueryRow(ctx, `SELECT balance_micro FROM portal_users WHERE id='a'`).Scan(&balance); err != nil || balance != MicroUSDT {
		t.Fatal("deposit not credited once", balance, err)
	}
	// A different user has a separate cooldown; their failure does not re-scan A.
	requestTest(t, b, server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 202)
	if err = s.processDepositScans(ctx, wallet); err != nil {
		t.Fatal(err)
	}
	if v := scanStateOf(t, b, server.URL); v.State != "failed" || v.RetryAfterSeconds <= 0 {
		t.Fatalf("failed check did not retain cooldown: %+v", v)
	}
	before := calls.Load()
	if err = restarted.processDepositScans(ctx, wallet); err != nil || calls.Load() != before {
		t.Fatal("finished/failed requests automatically retried", err)
	}
	requestTest(t, b, server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 429)
	// Advance only this isolated fixture's cooldown, never the production clock.
	if _, err = s.db.Exec(ctx, `UPDATE portal_deposit_scan_requests SET requested_at=clock_timestamp()-interval '181 seconds' WHERE user_id='a'`); err != nil {
		t.Fatal(err)
	}
	requestTest(t, a, server.URL, "POST", "/api/v1/portal/wallet/scan", map[string]any{}, 202)
	if err = restarted.processDepositScans(ctx, wallet); err != nil {
		t.Fatal(err)
	}
	v = scanStateOf(t, a, server.URL)
	if v.State != "succeeded" || v.CreditedMicro != 0 || v.DepositCount != 0 {
		t.Fatal("a manual repeat re-credited an old transaction", v)
	}
}

func TestWalletRunDoesNotAutomaticallyScanDeposits(t *testing.T) {
	s, _ := testService(t)
	var calls atomic.Int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		jsonReply(w, 200, map[string]any{"success": true, "data": []any{}, "meta": map[string]any{}})
	}))
	defer node.Close()
	depositScanSettings(t, s, node.URL)
	server := portalTestServer(s)
	defer server.Close()
	depositScanClient(t, s, server.URL, "root", "superadmin", MainnetUSDT, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer cancel()
	s.Run(ctx) // Covers startup and the first real periodic tick with an existing address.
	if calls.Load() != 0 {
		t.Fatal("timer performed an unsolicited deposit scan", calls.Load())
	}
	var scanned bool
	if err := s.db.QueryRow(context.Background(), `SELECT scanned_at IS NOT NULL FROM portal_wallet_addresses WHERE user_id='root'`).Scan(&scanned); err != nil || scanned {
		t.Fatal("timer advanced an address checkpoint", scanned, err)
	}
}
