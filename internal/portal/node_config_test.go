package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeProviderValidation(t *testing.T) {
	for _, tc := range []struct{ provider, network, endpoint, header string }{
		{"", "shasta", "", ""},
		{"tokenpocket", "mainnet", "", ""},
		{"trongrid", "mainnet", "", ""},
		{"nownodes", "mainnet", "https://trx.nownodes.io/", "api-key"},
		{"quicknode", "mainnet", "https://example.tron-mainnet.quiknode.pro/fixture-token/", ""},
		{"quicknode", "nile", "https://example.tron-nile.quiknode.pro/fixture-token", ""},
		{"getblock", "mainnet", "https://go.getblock.io/fixture-token", ""},
		{"getblock", "nile", "https://go.getblock.io/fixture-token/", ""},
		{"custom", "shasta", "https://rpc.example.test/private-path", "Authorization"},
	} {
		cfg := Defaults()
		cfg.Wallet.Network, cfg.Wallet.NodeProvider, cfg.Wallet.RPCURL, cfg.Wallet.RPCAPIHeader = tc.network, tc.provider, tc.endpoint, tc.header
		if err := validateSettings(&cfg); err != nil {
			t.Errorf("%s/%s rejected: %v", tc.provider, tc.network, err)
		}
		if cfg.Wallet.RPCURL != strings.TrimRight(tc.endpoint, "/") {
			t.Error("endpoint trailing slash not normalized")
		}
	}
	for _, tc := range []struct{ provider, network, endpoint, header string }{
		{"unknown", "mainnet", "", ""},
		{"tokenpocket", "shasta", "", ""},
		{"tokenpocket", "nile", "", ""},
		{"nownodes", "nile", "https://trx.nownodes.io", ""},
		{"quicknode", "shasta", "https://example.quiknode.pro/token", ""},
		{"getblock", "shasta", "https://go.getblock.io/token", ""},
		{"nownodes", "mainnet", "https://evil.example.test", ""},
		{"quicknode", "mainnet", "https://example.quiknode.pro.evil.test/token", ""},
		{"quicknode", "mainnet", "https://example.quiknode.pro", ""},
		{"getblock", "nile", "https://go.getblock.io", ""},
		{"custom", "mainnet", "http://rpc.example.test", ""},
		{"custom", "mainnet", "https://user:password@rpc.example.test", ""},
		{"custom", "mainnet", "https://rpc.example.test?key=secret", ""},
		{"custom", "mainnet", "https://rpc.example.test#key=secret", ""},
		{"custom", "mainnet", "https://rpc.example.test", "Cookie"},
	} {
		cfg := Defaults()
		cfg.Wallet.Network, cfg.Wallet.NodeProvider, cfg.Wallet.RPCURL, cfg.Wallet.RPCAPIHeader = tc.network, tc.provider, tc.endpoint, tc.header
		if validateSettings(&cfg) == nil {
			t.Errorf("invalid %s/%s configuration accepted", tc.provider, tc.network)
		}
	}
	cfg := Defaults()
	cfg.Wallet.NodeAPIKey = "key\r\nInjected: true"
	if validateSettings(&cfg) == nil {
		t.Fatal("header injection accepted")
	}
}

func TestNodeEndpointCredentialIsolation(t *testing.T) {
	w := WalletConfig{NodeProvider: "custom", NodeURL: "https://indexer.example.test", NodeAPIKey: "indexer-key", RPCURL: "https://rpc.example.test/token/", RPCAPIKey: "rpc-key", RPCAPIHeader: "api-key"}
	for _, tc := range []struct{ path, base, key, header string }{
		{"/wallet/getnowblock", "https://rpc.example.test/token", "rpc-key", "api-key"},
		{"/wallet/triggerconstantcontract", "https://rpc.example.test/token", "rpc-key", "api-key"},
		{"/wallet/broadcasttransaction", "https://rpc.example.test/token", "rpc-key", "api-key"},
		{"/walletsolidity/gettransactioninfobyid", "https://indexer.example.test", "indexer-key", "TRON-PRO-API-KEY"},
	} {
		base, key, header, err := nodeEndpoint(w, tc.path)
		if err != nil || base != tc.base || key != tc.key || header != tc.header {
			t.Fatalf("wrong route or credentials for %s: %v", tc.path, err)
		}
	}
	w.NodeProvider = "trongrid"
	base, key, header, err := nodeEndpoint(w, "/wallet/getnowblock")
	if err != nil || base != w.NodeURL || key != w.NodeAPIKey || header != "TRON-PRO-API-KEY" {
		t.Fatal("legacy TronGrid mode changed")
	}
	if _, _, _, err := nodeEndpoint(w, "/unexpected"); err == nil {
		t.Fatal("unexpected endpoint path accepted")
	}
}

func TestNodeProbeReadOnlyAndResponseValidation(t *testing.T) {
	var invalid atomic.Bool
	var rpcCalls, indexCalls atomic.Int32
	block := map[string]any{"blockID": strings.Repeat("a", 64), "block_header": map[string]any{"raw_data": map[string]int64{"number": 123456}}}
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcCalls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/private-token/wallet/getnowblock" || r.Header.Get("api-key") != "rpc-only" || r.Header.Get("TRON-PRO-API-KEY") != "" {
			t.Error("probe mutated chain or leaked indexer credentials")
		}
		if invalid.Load() {
			jsonReply(w, 200, map[string]string{"Error": "fixture"})
			return
		}
		jsonReply(w, 200, block)
	}))
	defer rpc.Close()
	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		indexCalls.Add(1)
		if r.Header.Get("TRON-PRO-API-KEY") != "index-only" || r.Header.Get("api-key") != "" {
			t.Error("RPC credentials leaked to indexer")
		}
		switch r.URL.Path {
		case "/walletsolidity/getnowblock":
			if r.Method != "POST" {
				t.Error("wrong solidity query method")
			}
			jsonReply(w, 200, block)
		case "/v1/accounts/T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb/transactions/trc20":
			if r.Method != "GET" || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("only_confirmed") != "true" {
				t.Error("wrong read-only history probe")
			}
			if invalid.Load() {
				jsonReply(w, 200, map[string]any{"success": true}) // Missing list is not success.
			} else {
				jsonReply(w, 200, map[string]any{"success": true, "data": []any{}})
			}
		default:
			t.Error("probe called a non-read-only endpoint", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer indexer.Close()
	s := &Service{http: rpc.Client()}
	w := WalletConfig{Network: "mainnet", NodeProvider: "custom", NodeURL: indexer.URL, NodeAPIKey: "index-only", RPCURL: rpc.URL + "/private-token", RPCAPIKey: "rpc-only", RPCAPIHeader: "api-key"}
	got := s.probeNodes(context.Background(), w)
	if !got.Passed || len(got.Checks) != 3 || rpcCalls.Load() != 1 || indexCalls.Load() != 2 || got.Checks[0].BlockHeight != 123456 {
		t.Fatal("probe did not validate all three read-only services", got)
	}
	invalid.Store(true)
	got = s.probeNodes(context.Background(), w)
	if got.Passed || got.Checks[0].Passed || got.Checks[2].Passed {
		t.Fatal("HTTP 200 with invalid body treated as a successful probe", got)
	}
}

func TestWalletNodeSettingsIntegration(t *testing.T) {
	s, _ := testService(t)
	r := httptest.NewRequest("PUT", "http://easyrun.example.test/settings", nil)
	cfg := Defaults()
	cfg.Wallet.Enabled = false // This test intentionally saves node config before setup.
	cfg.Wallet.NodeProvider = "custom"
	cfg.Wallet.NodeAPIKey = "fixture-indexer-key"
	cfg.Wallet.RPCURL = "https://rpc.example.test/private-endpoint-token"
	cfg.Wallet.RPCAPIKey = "fixture-rpc-key"
	cfg.Wallet.RPCAPIHeader = "api-key"
	if err := s.saveSettings(r, cfg, "wallet"); err != nil {
		t.Fatal(err)
	}
	read := func(secrets bool) Settings {
		t.Helper()
		got, err := s.Settings(context.Background(), secrets)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	public := read(false)
	if public.Wallet.RPCURL != "" || public.Wallet.RPCAPIKey != "" || !public.Wallet.RPCURLConfigured || !public.Wallet.RPCAPIKeyConfigured || public.Wallet.RPCURLHost != "rpc.example.test" {
		t.Fatal("endpoint secret returned in settings or flags are incorrect")
	}
	var raw []byte
	if err := s.db.QueryRow(context.Background(), `SELECT config FROM portal_settings WHERE id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cfg.Wallet.RPCURL, cfg.Wallet.RPCAPIKey, cfg.Wallet.NodeAPIKey, "private-endpoint-token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret stored in plaintext config")
		}
	}
	private := read(true)
	if private.Wallet.RPCURL != cfg.Wallet.RPCURL || private.Wallet.RPCAPIKey != cfg.Wallet.RPCAPIKey || private.Wallet.NodeAPIKey != cfg.Wallet.NodeAPIKey {
		t.Fatal("encrypted secrets did not round-trip")
	}
	// Blank secrets on an ordinary save retain the same endpoint and keys.
	if err := s.saveSettings(r, public, "wallet"); err != nil {
		t.Fatal(err)
	}
	if read(true).Wallet.RPCAPIKey != cfg.Wallet.RPCAPIKey {
		t.Fatal("ordinary save removed a saved credential")
	}
	// A new endpoint never inherits a previous service's credentials.
	public.Wallet.RPCURL = "https://another.example.test/new-token"
	public.Wallet.NodeURL = "https://another-indexer.example.test"
	if err := s.saveSettings(r, public, "wallet"); err != nil {
		t.Fatal(err)
	}
	private = read(true)
	if private.Wallet.RPCAPIKey != "" || private.Wallet.NodeAPIKey != "" || private.Wallet.RPCURLHost != "another.example.test" {
		t.Fatal("old key leaked to a new endpoint")
	}
	public = read(false)
	public.Wallet.NodeProvider, public.Wallet.Network = "getblock", "nile"
	if err := s.saveSettings(r, public, "wallet"); err != nil {
		t.Fatal(err)
	}
	private = read(true)
	if private.Wallet.RPCURL != "" || private.Wallet.RPCAPIKey != "" || private.Wallet.RPCURLHost != "" || private.Wallet.RPCURLConfigured {
		t.Fatal("changing provider retained the old private endpoint")
	}
	public = read(false)
	public.Wallet.Network, public.Wallet.Contract = "mainnet", MainnetUSDT
	public.Wallet.Enabled, public.Wallet.SeedHex = true, strings.Repeat("42", 32)
	if err := s.saveSettings(r, public, "wallet"); err == nil || !strings.Contains(err.Error(), "RPC") {
		t.Fatal("enabled third-party service without an endpoint was accepted", err)
	}
	public.Wallet.Network, public.Wallet.NodeProvider, public.Wallet.RPCURL = "mainnet", "nownodes", "https://trx.nownodes.io"
	if err := s.saveSettings(r, public, "wallet"); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatal("enabled NOWNodes without its own key was accepted", err)
	}
	if read(false).Wallet.SeedConfigured {
		t.Fatal("failed configuration save partially committed the seed")
	}
	// Probe endpoint is authenticated, superadmin-only and rate-limited.
	var calls atomic.Int32
	stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			jsonReply(w, 200, map[string]any{"success": true, "data": []any{}})
			return
		}
		jsonReply(w, 200, map[string]any{"blockID": strings.Repeat("a", 64), "block_header": map[string]any{"raw_data": map[string]int64{"number": 456}}})
	}))
	defer stub.Close()
	s.http = stub.Client()
	public = read(false)
	public.Wallet.NodeProvider, public.Wallet.NodeURL = "trongrid", stub.URL
	if err := s.saveSettings(r, public, "wallet"); err != nil {
		t.Fatal(err)
	}
	server := portalTestServer(s)
	defer server.Close()
	admin, customer, anonymous := newClient(), newClient(), newClient()
	requestTest(t, anonymous, server.URL, "POST", "/api/v1/portal/wallet/probe", nil, 428)
	requestTest(t, admin, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "admin@example.test", "password": "test-password-12345", "mode": "commercial", "frontend_enabled": true}, 201)
	if err := s.saveSettings(r, public, "wallet"); err != nil {
		t.Fatal(err)
	}
	requestTest(t, customer, server.URL, "POST", "/api/v1/portal/auth/register", map[string]any{"email": "customer@example.test", "password": "test-password-12345"}, 201)
	requestTest(t, anonymous, server.URL, "POST", "/api/v1/portal/wallet/probe", nil, 401)
	requestTest(t, customer, server.URL, "POST", "/api/v1/portal/wallet/probe", nil, 403)
	for i := 0; i < 3; i++ {
		var probe nodeProbeResult
		if err := json.Unmarshal(requestTest(t, admin, server.URL, "POST", "/api/v1/portal/wallet/probe", nil, 200), &probe); err != nil || !probe.Passed {
			t.Fatal("saved node probe failed", err, probe)
		}
	}
	requestTest(t, admin, server.URL, "POST", "/api/v1/portal/wallet/probe", nil, 429)
	if calls.Load() != 9 {
		t.Fatal("denied requests performed network calls", calls.Load())
	}
}

func TestWalletRejectsInvalidDeposits(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `INSERT INTO portal_users(id,email,password_hash,role)VALUES('depositor','deposit@example.test',''::bytea,'customer')`); err != nil {
		t.Fatal(err)
	}
	validLog := func(amount int64) tronLog {
		return tronLog{Address: "a614f803b6fd780986a42c78ec9c7f77e6ded13c", Topics: []string{transferTopic, strings.Repeat("0", 64), strings.Repeat("0", 24) + "a614f803b6fd780986a42c78ec9c7f77e6ded13c"}, Data: fmt.Sprintf("%064x", amount)}
	}
	// Every invalid receipt has a plausible Transfer event; history alone is
	// never sufficient to credit money. One real transaction has two events.
	receipts := map[string]tronReceipt{}
	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("%064x", i)
		receipt := tronReceipt{ID: id, BlockNumber: 123, Logs: []tronLog{validLog(MicroUSDT)}}
		receipt.Receipt.Result = "SUCCESS"
		switch i {
		case 1:
			receipt.Receipt.Result = "REVERT"
		case 2:
			receipt.BlockNumber = 0
		case 3:
			receipt.ID = strings.Repeat("f", 64)
		case 4:
			receipt.Logs[0].Address = strings.Repeat("1", 40)
		case 5:
			receipt.Logs[0].Topics[2] = strings.Repeat("0", 64)
		case 6:
			receipt.Result = "FAILED"
		case 7:
			receipt.Logs = append(receipt.Logs, validLog(2*MicroUSDT))
		}
		receipts[id] = receipt
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			var input struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			jsonReply(w, 200, receipts[input.Value])
			return
		}
		items := []map[string]string{}
		for i := 1; i <= 7; i++ {
			items = append(items, map[string]string{"transaction_id": fmt.Sprintf("%064x", i)})
		}
		items = append(items, items[len(items)-1]) // Duplicate indexer row.
		jsonReply(w, 200, map[string]any{"success": true, "data": items})
	}))
	defer mock.Close()
	w := Defaults().Wallet
	w.NodeURL, w.Contract = mock.URL, MainnetUSDT
	// Deposit discovery and final receipts must stay on the indexer, not RPC.
	w.NodeProvider, w.RPCURL = "custom", "https://unused-rpc.invalid"
	a := DepositAddress{UserID: "depositor", Address: MainnetUSDT, CreatedAt: time.Now().Add(-time.Hour)}
	for i := 0; i < 2; i++ {
		if err := s.scanAddress(ctx, w, a, a.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	var balance int64
	var deposits, entries int
	if err := s.db.QueryRow(ctx, `SELECT balance_micro,(SELECT count(*) FROM portal_deposits),(SELECT count(*) FROM portal_ledger) FROM portal_users WHERE id='depositor'`).Scan(&balance, &deposits, &entries); err != nil {
		t.Fatal(err)
	}
	if balance != 3*MicroUSDT || deposits != 2 || entries != 2 {
		t.Fatalf("invalid or duplicate deposits credited: balance=%d deposits=%d entries=%d", balance, deposits, entries)
	}
}
