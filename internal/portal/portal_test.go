package portal

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestHDWalletAndAddress(t *testing.T) {
	key := secp.PrivKeyFromBytes(append(make([]byte, 31), 1))
	defer key.Zero()
	if got := publicAddress(key); got != "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC" {
		t.Fatalf("private key 1 address = %s", got)
	}
	for _, a := range []string{MainnetUSDT, publicAddress(key)} {
		if !ValidAddress(a) {
			t.Fatal("invalid address", a)
		}
	}
	if ValidAddress("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6a") {
		t.Fatal("checksum ignored")
	}
	seed := bytes.Repeat([]byte{42}, 64)
	a, e := deriveKey(seed, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Zero()
	b, e := deriveKey(seed, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Zero()
	c, e := deriveKey(seed, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Zero()
	if publicAddress(a) != publicAddress(b) || publicAddress(a) == publicAddress(c) {
		t.Fatal("non-deterministic or repeated HD derivation")
	}
	if _, e = deriveKey(seed, 1<<31); e == nil {
		t.Fatal("hardened index accepted")
	}
}
func TestRunPrice(t *testing.T) {
	p := Pricing{PerRunMicro: 123, PerKMMicro: 333333}
	for _, tt := range []struct {
		mode     string
		distance float64
		want     int64
	}{{"per_run", 3000, 123}, {"per_km", 3000, 999999}, {"per_km", 1100, 366667}, {"per_km", 100, 33334}} {
		got, e := runPrice(p, tt.mode, tt.distance)
		if e != nil || got != tt.want {
			t.Fatalf("%+v: got %d %v", tt, got, e)
		}
	}
	for _, n := range []float64{-1, math.NaN(), math.Inf(1), 100001} {
		if _, e := runPrice(p, "per_run", n); e == nil {
			t.Fatal("invalid billing distance accepted")
		}
	}
}
func TestOriginAndLinkToken(t *testing.T) {
	r := httptest.NewRequest("POST", "https://run.example/api/test", nil)
	r.Header.Set("Origin", "https://evil.example")
	if originOK(r) {
		t.Fatal("cross-origin write accepted")
	}
	r.Header.Set("Origin", "https://run.example")
	if !originOK(r) {
		t.Fatal("same-origin rejected")
	}
	secret, e := token()
	if e != nil {
		t.Fatal(e)
	}
	if !checkFaceToken(digest(secret), secret) || checkFaceToken(digest(secret), strings.Repeat("x", 43)) {
		t.Fatal("face token check failed")
	}
}
func TestSettingsValidation(t *testing.T) {
	cfg := Defaults()
	if e := validateSettings(&cfg); e != nil {
		t.Fatal(e)
	}
	cfg.Wallet.NodeURL = "http://node.example"
	if validateSettings(&cfg) == nil {
		t.Fatal("insecure node accepted")
	}
	cfg = Defaults()
	cfg.Mode = "commercial"
	cfg.Wallet.Network = "mainnet"
	cfg.Wallet.Contract = "wrong"
	if e := validateSettings(&cfg); e != nil || cfg.Wallet.Contract != MainnetUSDT {
		t.Fatal("mainnet USDT contract is not pinned", e)
	}
	cfg.Wallet.AutoCollect = true
	if validateSettings(&cfg) == nil {
		t.Fatal("unconfigured automatic collection accepted")
	}
}

func TestRechargeCodeShopURLValidation(t *testing.T) {
	for _, value := range []string{"", "https://cards.example.test/easyrun?ref=portal", "http://cards.example.test/products", "  https://cards.example.test/  "} {
		cfg := Defaults()
		cfg.Pricing.RechargeCodeShopURL = value
		if err := validateSettings(&cfg); err != nil || cfg.Pricing.RechargeCodeShopURL != strings.TrimSpace(value) {
			t.Fatalf("valid shop URL %q rejected or not normalized: %v", value, err)
		}
	}
	for _, value := range []string{"javascript:alert(1)", "data:text/html,test", "//cards.example.test", "https://", "https://user:password@cards.example.test", "https://cards.example.test/\nheader", strings.Repeat("x", 2049)} {
		cfg := Defaults()
		cfg.Pricing.RechargeCodeShopURL = value
		if validateSettings(&cfg) == nil {
			t.Fatalf("invalid shop URL %q accepted", value)
		}
	}
}

func TestMergeSettingsSections(t *testing.T) {
	old := Defaults()
	old.Mode = "commercial"
	old.Mail.Host = "smtp.current.example.test"
	old.Pricing = Pricing{Enabled: true, PerRunMicro: MicroUSDT, RechargeCodeShopURL: "https://cards.example.test"}
	old.Wallet.CollectionAddress = MainnetUSDT
	stale := Defaults()
	stale.Mail.Host = "smtp.new.example.test"
	stale.Wallet.NodeURL = "https://node.new.example.test"
	stale.Pricing.PerKMMicro = 2 * MicroUSDT
	for _, section := range []string{"mail", "pricing", "wallet", "collection"} {
		merged, err := mergeSettings(old, stale, section)
		if err != nil {
			t.Fatalf("%s: %v", section, err)
		}
		if merged.Mode != old.Mode {
			t.Fatalf("%s overwrote usage mode", section)
		}
		if section != "mail" && merged.Mail != old.Mail {
			t.Fatalf("%s overwrote mail settings", section)
		}
		if section != "pricing" && merged.Pricing != old.Pricing {
			t.Fatalf("%s overwrote pricing", section)
		}
		if section != "collection" && merged.Wallet.CollectionAddress != old.Wallet.CollectionAddress {
			t.Fatalf("%s overwrote collection destination", section)
		}
		if section != "wallet" && merged.Wallet.NodeURL != old.Wallet.NodeURL {
			t.Fatalf("%s overwrote wallet node", section)
		}
	}
	if _, err := mergeSettings(old, stale, "users"); err == nil {
		t.Fatal("unknown settings section accepted")
	}
	merged, err := mergeSettings(old, stale, "pricing")
	if err != nil || merged.Pricing.RechargeCodeEnabled != old.Pricing.RechargeCodeEnabled {
		t.Fatal("pricing page overwrote recharge method switch", err)
	}
	merged, err = mergeSettings(old, stale, "recharge")
	if err != nil || merged.Pricing.RechargeCodeEnabled != stale.Pricing.RechargeCodeEnabled || merged.Pricing.PerRunMicro != old.Pricing.PerRunMicro || merged.Pricing.RechargeCodeShopURL != old.Pricing.RechargeCodeShopURL || merged.Wallet.CollectionAddress != old.Wallet.CollectionAddress {
		t.Fatal("recharge page overwrote pricing or collection settings", err)
	}
}

func TestLegacyRechargeMethodDefault(t *testing.T) {
	cfg := Defaults()
	if err := json.Unmarshal([]byte(`{"mode":"commercial","pricing":{"per_run_micro":100000}}`), &cfg); err != nil || !cfg.Pricing.RechargeCodeEnabled || cfg.Wallet.Enabled {
		t.Fatal("legacy code redemption must remain available without enabling chain deposits", err)
	}
}
func TestConfirmedTransferLogValidation(t *testing.T) {
	raw, _ := addressBytes(MainnetUSDT)
	topic, _ := abiAddress(MainnetUSDT)
	l := tronLog{Address: hex.EncodeToString(raw[1:]), Topics: []string{transferTopic, strings.Repeat("0", 64), topic}, Data: strings.Repeat("0", 58) + "0f4240"}
	amount, ok := receivedAmount(l, MainnetUSDT, MainnetUSDT)
	if !ok || amount != MicroUSDT {
		t.Fatalf("amount=%d ok=%v", amount, ok)
	}
	l.Topics[0] = strings.Repeat("0", 64)
	if _, ok = receivedAmount(l, MainnetUSDT, MainnetUSDT); ok {
		t.Fatal("non-Transfer event accepted")
	}
}
func wireBytes(field int, value []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(field<<3|2))
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}
func wireInt(field int, value uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(field<<3)), value)
}
func sampleTransferRaw(w WalletConfig, owner, destination string, amount int64) []byte {
	o, _ := addressBytes(owner)
	c, _ := addressBytes(w.Contract)
	abi, _ := transferABI(destination, amount)
	call, _ := hex.DecodeString("a9059cbb" + abi)
	v := append(wireBytes(1, o), wireBytes(2, c)...)
	v = append(v, wireBytes(4, call)...)
	any := append(wireBytes(1, []byte("type.googleapis.com/protocol.TriggerSmartContract")), wireBytes(2, v)...)
	contract := append(wireInt(1, 31), wireBytes(2, any)...)
	raw := append(wireBytes(1, []byte{0, 1}), wireBytes(4, make([]byte, 8))...)
	raw = append(raw, wireInt(8, uint64(time.Now().Add(time.Minute).UnixMilli()))...)
	raw = append(raw, wireBytes(11, contract)...)
	return append(raw, wireInt(18, uint64(w.FeeLimitSun))...)
}
func TestUnsignedTransactionFirewall(t *testing.T) {
	w := Defaults().Wallet
	w.Contract = MainnetUSDT
	raw := sampleTransferRaw(w, MainnetUSDT, MainnetUSDT, MicroUSDT)
	if e := validateTransferRaw(raw, w, MainnetUSDT, MainnetUSDT, MicroUSDT, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := validateTransferRaw(raw, w, MainnetUSDT, MainnetUSDT, MicroUSDT+1, time.Now()); e == nil {
		t.Fatal("wrong amount accepted for signing")
	}
	if e := validateTransferRaw(append(raw, wireBytes(11, []byte{})...), w, MainnetUSDT, MainnetUSDT, MicroUSDT, time.Now()); e == nil {
		t.Fatal("multiple contracts accepted")
	}
	if e := validateTransferRaw(append(raw, wireBytes(10, []byte("memo"))...), w, MainnetUSDT, MainnetUSDT, MicroUSDT, time.Now()); e == nil {
		t.Fatal("unexpected field accepted")
	}
}
