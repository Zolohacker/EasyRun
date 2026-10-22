package portal

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	bip39 "github.com/cosmos/go-bip39"
)

func TestBIP39ReferenceVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/bip39-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors [][]string
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, v := range vectors {
		entropy, err := hex.DecodeString(v[0])
		if err != nil {
			t.Fatal(err)
		}
		phrase, err := bip39.NewMnemonic(entropy)
		if err != nil || phrase != v[1] {
			t.Fatalf("public reference vector %d mnemonic mismatch", i)
		}
		seed, err := bip39.NewSeedWithErrorChecking(phrase, "TREZOR")
		if err != nil || hex.EncodeToString(seed) != v[2] {
			t.Fatalf("public reference vector %d seed mismatch", i)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		seed, phrase, err := newMnemonicWallet()
		if err != nil || len(strings.Fields(string(phrase))) != 24 || len(seed) != 64 {
			t.Fatal("invalid generated mnemonic wallet")
		}
		restored, err := bip39.NewSeedWithErrorChecking(string(phrase), "")
		if err != nil || !bytes.Equal(seed, restored) || seen[string(phrase)] {
			t.Fatal("generated mnemonic did not restore the exact seed, or entropy was repeated")
		}
		seen[string(phrase)] = true
		clear(seed)
		clear(restored)
		clear(phrase)
	}
	// Independent, widely used public mnemonic/address fixture; never use it for funds.
	seed := bip39.NewSeed(strings.Repeat("abandon ", 11)+"about", "")
	defer clear(seed)
	key, err := deriveKey(seed, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Zero()
	if publicAddress(key) != "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH" {
		t.Fatal("public TRON mnemonic derivation vector mismatch")
	}
}

func TestWalletBootstrapDefaultsAndLegacyConfig(t *testing.T) {
	cfg := Defaults()
	if !cfg.Wallet.Enabled || cfg.Wallet.Network != "mainnet" || cfg.Wallet.NodeProvider != "tokenpocket" || cfg.Wallet.NodeURL != TokenPocketNodeURL || cfg.Wallet.Contract != MainnetUSDT || cfg.Wallet.AutoCollect {
		t.Fatal("new deployment wallet defaults are incorrect")
	}
	w := cfg.Wallet
	w.NodeAPIKey, w.RPCAPIKey, w.RPCURL = "index-only", "stale-rpc-key", "https://stale.invalid/token"
	if err := validateNodeProvider(&w); err != nil || w.RPCAPIKey != "" || w.RPCURL != "" {
		t.Fatal("fixed TokenPocket preset kept a private RPC credential")
	}
	base, key, header, err := nodeEndpoint(w, "/wallet/getnowblock")
	if err != nil || base != TokenPocketNodeURL || key != "" || header != "" {
		t.Fatal("TokenPocket RPC forwarded credentials or used a different endpoint")
	}
	w.NodeURL = "https://independent-indexer.example.test"
	base, key, _, err = nodeEndpoint(w, "/walletsolidity/getnowblock")
	if err != nil || base != w.NodeURL || key != "index-only" {
		t.Fatal("independent solidified indexer routing changed")
	}
	old, err := decodeSettingsConfig([]byte(`{"mode":"commercial","wallet":{"enabled":false,"network":"shasta","node_url":"https://api.shasta.trongrid.io","contract":""}}`))
	if err != nil || old.Wallet.Enabled || old.Wallet.Network != "shasta" || old.Wallet.NodeProvider != "trongrid" {
		t.Fatal("legacy configuration migrated to the new mainnet preset")
	}
}

// Unlike generic test helpers, never include a secret-bearing body in failures.
func backupTest(t *testing.T, client *http.Client, base, password string, want int) walletBackup {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"password": password})
	req, _ := http.NewRequest("POST", base+"/api/v1/portal/wallet/export", bytes.NewReader(raw))
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("backup request did not complete")
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("wallet export status=%d expected=%d (body redacted)", resp.StatusCode, want)
	}
	var out walletBackup
	if want == 200 {
		if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Pragma") != "no-cache" {
			t.Fatal("secret export response is cacheable")
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
			t.Fatal("invalid wallet backup response")
		}
	}
	return out
}

func TestWalletBootstrapExportIntegration(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	server := portalTestServer(s)
	defer server.Close()
	root, customer, anonymous := newClient(), newClient(), newClient()
	const password = "test-password-12345"
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "root@example.test", "password": password, "mode": "commercial", "frontend_enabled": true}, 201)
	public, err := s.Settings(ctx, false)
	if err != nil || !public.Wallet.Enabled || !public.Wallet.SeedConfigured || !public.Wallet.MnemonicConfigured || public.Wallet.Network != "mainnet" || public.Wallet.NodeProvider != "tokenpocket" || public.Wallet.AutoCollect {
		t.Fatal("setup did not atomically provision the default mnemonic wallet")
	}
	var configRaw, secretsRaw []byte
	if err := s.db.QueryRow(ctx, `SELECT config,secrets FROM portal_settings WHERE id=1`).Scan(&configRaw, &secretsRaw); err != nil {
		t.Fatal(err)
	}
	var sealed map[string][]byte
	if err := json.Unmarshal(secretsRaw, &sealed); err != nil {
		t.Fatal(err)
	}
	phrase, err := s.vault.Decrypt(sealed["mnemonic"], "portal-secret:mnemonic")
	if err != nil {
		t.Fatal("encrypted mnemonic did not decrypt")
	}
	defer clear(phrase)
	seedHex, err := s.vault.Decrypt(sealed["seed"], "portal-secret:seed")
	if err != nil {
		t.Fatal("encrypted seed did not decrypt")
	}
	defer clear(seedHex)
	publicRaw := requestTest(t, root, server.URL, "GET", "/api/v1/portal/settings", nil, 200)
	for _, raw := range [][]byte{configRaw, secretsRaw, publicRaw} {
		if bytes.Contains(raw, phrase) || bytes.Contains(raw, seedHex) {
			t.Fatal("wallet secret leaked into settings or plaintext persistence")
		}
	}
	backupTest(t, anonymous, server.URL, password, 401)
	requestTest(t, customer, server.URL, "POST", "/api/v1/portal/auth/register", map[string]string{"email": "customer@example.test", "password": password}, 201)
	backupTest(t, customer, server.URL, password, 403)
	admin := newClient()
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/users", map[string]string{"email": "admin@example.test", "password": password, "role": "admin"}, 201)
	requestTest(t, admin, server.URL, "POST", "/api/v1/portal/auth/login", map[string]string{"email": "admin@example.test", "password": password}, 200)
	backupTest(t, admin, server.URL, password, 403)
	backupTest(t, root, server.URL, "wrong-password", 403)
	exported := backupTest(t, root, server.URL, password, 200)
	if exported.Kind != "bip39" || exported.Mnemonic != string(phrase) || exported.SeedHex != "" || exported.Passphrase != "" || exported.DerivationPath != walletDerivationPath {
		t.Fatal("exported mnemonic or backup metadata was incorrect")
	}
	restored, err := bip39.NewSeedWithErrorChecking(exported.Mnemonic, "")
	if err != nil || hex.EncodeToString(restored) != string(seedHex) {
		t.Fatal("exported mnemonic does not restore the saved seed")
	}
	defer clear(restored)
	var address DepositAddress
	if err := json.Unmarshal(requestTest(t, customer, server.URL, "POST", "/api/v1/portal/wallet/address", map[string]any{}, 201), &address); err != nil {
		t.Fatal(err)
	}
	key, err := deriveKey(restored, address.Index)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Zero()
	if publicAddress(key) != address.Address {
		t.Fatal("restoring the exported phrase changes the allocated customer address")
	}
	exported2 := backupTest(t, root, server.URL, password, 200)
	if exported2.Mnemonic != exported.Mnemonic {
		t.Fatal("export regenerated the wallet")
	}
	backupTest(t, root, server.URL, password, 429)
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/wallet/seed", map[string]string{"password": password}, 409)
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "other@example.test", "password": password, "mode": "personal"}, 409)
	var after []byte
	if err := s.db.QueryRow(ctx, `SELECT secrets FROM portal_settings WHERE id=1`).Scan(&after); err != nil || !bytes.Equal(secretsRaw, after) {
		t.Fatal("repeated initialization or creation overwrote the wallet")
	}
	var audited int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM portal_audit WHERE action='wallet.backup-export' AND target='bip39'`).Scan(&audited); err != nil || audited != 2 {
		t.Fatal("wallet export audit missing")
	}
	// Losing encrypted material must never create another root over existing addresses.
	if _, err := s.db.Exec(ctx, `UPDATE portal_settings SET secrets=secrets-'seed'-'mnemonic' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/wallet/seed", map[string]string{"password": password}, 409)
	cfg, err := s.Settings(ctx, false)
	if err != nil || cfg.Wallet.SeedConfigured || cfg.Wallet.MnemonicConfigured {
		t.Fatal("missing backup was replaced despite existing customer addresses")
	}
}

func TestLegacyWalletBackupIntegration(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	const legacyHex = "4242424242424242424242424242424242424242424242424242424242424242"
	seed, err := s.vault.Encrypt([]byte(legacyHex), "portal-secret:seed")
	if err != nil {
		t.Fatal(err)
	}
	secretsRaw, _ := json.Marshal(map[string][]byte{"seed": seed})
	var rawConfig map[string]any
	initial, _ := json.Marshal(Defaults())
	_ = json.Unmarshal(initial, &rawConfig)
	w := rawConfig["wallet"].(map[string]any)
	w["enabled"], w["network"], w["node_url"], w["contract"] = false, "shasta", "https://api.shasta.trongrid.io", ""
	delete(w, "node_provider")
	configRaw, _ := json.Marshal(rawConfig)
	if _, err := s.db.Exec(ctx, `UPDATE portal_settings SET config=$1,secrets=$2 WHERE id=1`, configRaw, secretsRaw); err != nil {
		t.Fatal(err)
	}
	server := portalTestServer(s)
	defer server.Close()
	root := newClient()
	const password = "test-password-12345"
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "root@example.test", "password": password, "mode": "personal"}, 201)
	cfg, err := s.Settings(ctx, false)
	if err != nil || !cfg.Wallet.SeedConfigured || cfg.Wallet.MnemonicConfigured || cfg.Wallet.Enabled || cfg.Wallet.Network != "shasta" || cfg.Wallet.NodeProvider != "trongrid" {
		t.Fatal("legacy wallet or network was silently replaced during setup")
	}
	exported := backupTest(t, root, server.URL, password, 200)
	if exported.Kind != "legacy_seed" || exported.SeedHex != legacyHex || exported.Mnemonic != "" || exported.Network != "shasta" {
		t.Fatal("legacy wallet was misrepresented as a recoverable BIP39 phrase")
	}
	var after []byte
	if err := s.db.QueryRow(ctx, `SELECT secrets FROM portal_settings WHERE id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var afterSealed map[string][]byte
	if err := json.Unmarshal(after, &afterSealed); err != nil || len(afterSealed) != 1 || !bytes.Equal(afterSealed["seed"], seed) {
		t.Fatal("legacy encrypted seed was rewritten")
	}
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/wallet/seed", map[string]string{"password": password}, 409)
}

func TestManualWalletCreationIsAtomic(t *testing.T) {
	s, _ := testService(t)
	server := portalTestServer(s)
	defer server.Close()
	root := newClient()
	const password = "test-password-12345"
	requestTest(t, root, server.URL, "POST", "/api/v1/portal/setup", map[string]any{"email": "root@example.test", "password": password, "mode": "personal"}, 201)
	// Simulate an older initialized deployment which never created a wallet.
	if _, err := s.db.Exec(context.Background(), `UPDATE portal_settings SET secrets='{}',config=jsonb_set(config,'{wallet,enabled}','false') WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		err    error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			raw, _ := json.Marshal(map[string]string{"password": password})
			req, _ := http.NewRequest("POST", server.URL+"/api/v1/portal/wallet/seed", bytes.NewReader(raw))
			req.Header.Set("Origin", server.URL)
			req.Header.Set("Content-Type", "application/json")
			response, err := root.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			response.Body.Close()
			results <- result{status: response.StatusCode}
		}()
	}
	statuses := map[int]int{}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal("concurrent wallet creation failed")
		}
		statuses[r.status]++
	}
	if statuses[201] != 1 || statuses[409] != 1 {
		t.Fatal("concurrent creation did not preserve a single wallet", statuses)
	}
	cfg, err := s.Settings(context.Background(), false)
	if err != nil || !cfg.Wallet.SeedConfigured || !cfg.Wallet.MnemonicConfigured {
		t.Fatal("manual creation failed to commit both encrypted fields")
	}
}
