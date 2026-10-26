package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/safety"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/selfupdate"
	"github.com/Zolohacker/EasyRun/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db                        *pgxpool.Pool
	vault                     *securestore.Vault
	store                     store.Store
	Faces                     FaceWriter
	ClientVersions            *safety.Guard
	PlatformUpdates           *selfupdate.Service
	http                      *http.Client
	mu                        sync.Mutex
	rates                     map[string]rateEntry
	networkMu                 sync.Mutex
	nextRequest               time.Time
	scanRequested             chan struct{}
	collectRequested          chan struct{}
	backgroundMu              sync.Mutex
	backgroundRunning         bool
	backgroundPaused          bool
	collectionPending         bool
	lastBackgroundMaintenance time.Time
}
type FaceWriter interface {
	SetCustomFace(context.Context, string, string, []byte) (platform.FaceProfile, error)
}
type rateEntry struct {
	Count int
	Until time.Time
}

func New(ctx context.Context, db *pgxpool.Pool, vault *securestore.Vault, database store.Store) (*Service, error) {
	s := &Service{db: db, vault: vault, store: database, http: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, rates: map[string]rateEntry{}, scanRequested: make(chan struct{}, 1), collectRequested: make(chan struct{}, 1)}
	if _, e := db.Exec(ctx, schema); e != nil {
		return nil, fmt.Errorf("migrate portal: %w", e)
	}
	if _, e := db.Exec(ctx, escrowSchema); e != nil {
		return nil, fmt.Errorf("migrate task escrow: %w", e)
	}
	data, _ := json.Marshal(Defaults())
	_, err := db.Exec(ctx, `INSERT INTO portal_settings(id,config) VALUES(1,$1) ON CONFLICT DO NOTHING`, data)
	return s, err
}
func token() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(s string) string { d := sha256.Sum256([]byte(s)); return hex.EncodeToString(d[:]) }

func decodeSettingsConfig(raw []byte) (Settings, error) {
	c := Defaults()
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	// Missing provider predates the selector. Those deployments used TronGrid,
	// including testnet wallets: upgrading must not silently route them to mainnet.
	var fields struct {
		Wallet map[string]json.RawMessage `json:"wallet"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return c, err
	}
	if _, exists := fields.Wallet["node_provider"]; !exists {
		c.Wallet.NodeProvider = "trongrid"
	}
	if _, exists := fields.Wallet["enabled"]; !exists {
		c.Wallet.Enabled = false
	}
	if _, exists := fields.Wallet["network"]; !exists {
		c.Wallet.Network = "shasta"
	}
	if _, exists := fields.Wallet["node_url"]; !exists {
		c.Wallet.NodeURL = "https://api.shasta.trongrid.io"
	}
	if _, exists := fields.Wallet["contract"]; !exists {
		c.Wallet.Contract = ""
	}
	return c, nil
}

func (s *Service) Settings(ctx context.Context, secrets bool) (Settings, error) {
	var raw, encrypted []byte
	c := Defaults()
	e := s.db.QueryRow(ctx, `SELECT config,secrets FROM portal_settings WHERE id=1`).Scan(&raw, &encrypted)
	if e != nil {
		return c, e
	}
	c, e = decodeSettingsConfig(raw)
	if e != nil {
		return c, e
	}
	var sealed map[string][]byte
	if e = json.Unmarshal(encrypted, &sealed); e != nil {
		return c, e
	}
	c.Mail.PasswordConfigured = len(sealed["mail"]) > 0
	c.Wallet.SeedConfigured = len(sealed["seed"]) > 0
	c.Wallet.MnemonicConfigured = len(sealed["mnemonic"]) > 0
	c.Wallet.NodeAPIKeyConfigured = len(sealed["node"]) > 0
	c.Wallet.RPCURLConfigured = len(sealed["rpc_url"]) > 0
	c.Wallet.RPCAPIKeyConfigured = len(sealed["rpc_key"]) > 0
	c.Wallet.FeeeAPIKeyConfigured = len(sealed["feee"]) > 0
	if secrets {
		for key, target := range map[string]*string{"mail": &c.Mail.Password, "seed": &c.Wallet.SeedHex, "node": &c.Wallet.NodeAPIKey, "feee": &c.Wallet.FeeeAPIKey, "rpc_url": &c.Wallet.RPCURL, "rpc_key": &c.Wallet.RPCAPIKey} {
			data, err := s.vault.Decrypt(sealed[key], "portal-secret:"+key)
			if err != nil {
				return c, err
			}
			*target = string(data)
		}
	}
	return c, nil
}
func (s *Service) initialized(ctx context.Context) (bool, error) {
	var ok bool
	e := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_users WHERE role='superadmin')`).Scan(&ok)
	return ok, e
}

// Initialized is used by startup to retain existing task schedules until the
// deployment owner has completed the first-run wizard.
func (s *Service) Initialized(ctx context.Context) (bool, error) { return s.initialized(ctx) }
func (s *Service) audit(ctx context.Context, action, target string) {
	a, _ := platform.ActorFrom(ctx)
	if _, e := s.db.Exec(ctx, `INSERT INTO portal_audit(actor_id,action,target) VALUES($1,$2,$3)`, a.ID, action, target); e != nil {
		slog.Error("write portal audit", "action", action, "error", e)
	}
}
func (s *Service) allow(key string, max int, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.rates) > 10000 {
		for k, v := range s.rates {
			if now.After(v.Until) {
				delete(s.rates, k)
			}
		}
	}
	v := s.rates[key]
	if now.After(v.Until) {
		v = rateEntry{Until: now.Add(window)}
	}
	v.Count++
	s.rates[key] = v
	return v.Count <= max
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonReply(w, status, map[string]any{"error": map[string]string{"code": "portal_error", "message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("请求格式不正确")
	}
	return nil
}
func (s *Service) dbError(w http.ResponseWriter, e error) {
	slog.Error("portal database operation", "error", e)
	fail(w, 500, "操作暂未完成，请稍后重试")
}
func publicPath(path string) bool {
	return path == "/api/v1/portal/status" || path == "/api/v1/portal/setup" || path == "/api/v1/portal/auth/login" || path == "/api/v1/portal/auth/register" || path == "/api/v1/portal/auth/email-code" || strings.HasPrefix(path, "/api/v1/public/face/") || strings.HasPrefix(path, "/api/v1/public/tasks/")
}

const schema = `
CREATE TABLE IF NOT EXISTS portal_settings(id smallint PRIMARY KEY CHECK(id=1),config jsonb NOT NULL,secrets jsonb NOT NULL DEFAULT '{}',next_collect_at timestamptz,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS portal_users(id text PRIMARY KEY,email text NOT NULL UNIQUE,password_hash bytea NOT NULL,role text NOT NULL CHECK(role IN('superadmin','admin','customer')),enabled boolean NOT NULL DEFAULT true,email_verified boolean NOT NULL DEFAULT false,balance_micro bigint NOT NULL DEFAULT 0 CHECK(balance_micro>=0),created_at timestamptz NOT NULL DEFAULT now());
CREATE UNIQUE INDEX IF NOT EXISTS portal_one_superadmin ON portal_users(role) WHERE role='superadmin';
CREATE TABLE IF NOT EXISTS portal_sessions(token_hash text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS portal_session_expiry ON portal_sessions(expires_at);
CREATE TABLE IF NOT EXISTS portal_email_codes(id text PRIMARY KEY,email text NOT NULL,code_hash bytea NOT NULL,attempts integer NOT NULL DEFAULT 0,used boolean NOT NULL DEFAULT false,expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS portal_ledger(id text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id),delta_micro bigint NOT NULL,kind text NOT NULL,reference text NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS portal_ledger_user ON portal_ledger(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS portal_recharge_codes(code_hash text PRIMARY KEY,label text NOT NULL,amount_micro bigint NOT NULL CHECK(amount_micro>0),batch_id text NOT NULL,expires_at timestamptz NOT NULL,used_by text REFERENCES portal_users(id),used_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS portal_entitlements(user_id text NOT NULL REFERENCES portal_users(id),account_id text NOT NULL REFERENCES runner_accounts(id) ON DELETE CASCADE,plan_id text NOT NULL REFERENCES running_plans(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,plan_id));
CREATE TABLE IF NOT EXISTS portal_job_billing(job_id text PRIMARY KEY REFERENCES automation_jobs(id) ON DELETE CASCADE,mode text NOT NULL CHECK(mode IN('per_run','per_km','plan_package')));
CREATE TABLE IF NOT EXISTS portal_charges(execution_id text PRIMARY KEY REFERENCES automation_executions(id),user_id text NOT NULL REFERENCES portal_users(id),amount_micro bigint NOT NULL,state text NOT NULL CHECK(state IN('reserved','settled','refunded')),created_at timestamptz NOT NULL DEFAULT now());
CREATE UNIQUE INDEX IF NOT EXISTS automation_jobs_credit_identity ON automation_jobs(id,account_id,plan_id);
CREATE TABLE IF NOT EXISTS portal_task_credits(job_id text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id),account_id text NOT NULL,plan_id text NOT NULL,mode text NOT NULL CHECK(mode IN('per_run','per_km')),remaining_units bigint NOT NULL CHECK(remaining_units>=0),updated_at timestamptz NOT NULL DEFAULT now(),FOREIGN KEY(job_id,account_id,plan_id) REFERENCES automation_jobs(id,account_id,plan_id) ON DELETE RESTRICT ON UPDATE RESTRICT);
CREATE TABLE IF NOT EXISTS portal_task_credit_orders(id text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id),job_id text NOT NULL,mode text NOT NULL CHECK(mode IN('per_run','per_km')),units bigint NOT NULL CHECK(units>0),amount_micro bigint NOT NULL CHECK(amount_micro>=0),created_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE portal_charges ADD COLUMN IF NOT EXISTS funding_source text NOT NULL DEFAULT 'balance';
ALTER TABLE portal_charges ADD COLUMN IF NOT EXISTS credit_job_id text NOT NULL DEFAULT '';
ALTER TABLE portal_charges ADD COLUMN IF NOT EXISTS credit_mode text NOT NULL DEFAULT '';
ALTER TABLE portal_charges ADD COLUMN IF NOT EXISTS credit_units bigint NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS portal_charges_credit_idx ON portal_charges(credit_job_id,state) WHERE funding_source='prepaid';
CREATE TABLE IF NOT EXISTS portal_face_links(account_id text PRIMARY KEY REFERENCES runner_accounts(id) ON DELETE CASCADE,token_hash text NOT NULL,token_cipher bytea NOT NULL,expires_at timestamptz NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS portal_task_links(job_id text PRIMARY KEY REFERENCES automation_jobs(id) ON DELETE CASCADE,token_hash text NOT NULL,token_cipher bytea NOT NULL,expires_at timestamptz NOT NULL,scope jsonb NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
CREATE SEQUENCE IF NOT EXISTS portal_wallet_index_seq MINVALUE 0 START 0 MAXVALUE 2147483647;
CREATE TABLE IF NOT EXISTS portal_wallet_addresses(user_id text PRIMARY KEY REFERENCES portal_users(id),address text NOT NULL UNIQUE,derivation_index bigint NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now(),scanned_at timestamptz);
ALTER TABLE portal_wallet_addresses ADD COLUMN IF NOT EXISTS full_scanned_at timestamptz;
CREATE TABLE IF NOT EXISTS portal_deposits(tx_id text NOT NULL,event_index integer NOT NULL,user_id text NOT NULL REFERENCES portal_users(id),amount_micro bigint NOT NULL CHECK(amount_micro>0),address text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(tx_id,event_index));
CREATE INDEX IF NOT EXISTS portal_deposits_user_time ON portal_deposits(user_id,created_at);
CREATE TABLE IF NOT EXISTS portal_deposit_scan_requests(user_id text PRIMARY KEY REFERENCES portal_users(id),state text NOT NULL CHECK(state IN('queued','running','succeeded','failed')),requested_at timestamptz NOT NULL,started_at timestamptz,finished_at timestamptz,message text NOT NULL DEFAULT '',credited_micro bigint NOT NULL DEFAULT 0,deposit_count integer NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS portal_sweeps(id text PRIMARY KEY,address text NOT NULL,amount_micro bigint NOT NULL DEFAULT 0,state text NOT NULL,tx_id text NOT NULL DEFAULT '',signed_tx jsonb,message text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE portal_sweeps ADD COLUMN IF NOT EXISTS destination text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS portal_one_active_sweep ON portal_sweeps(address) WHERE state IN('queued','renting','signed','broadcast','needs_review');
CREATE TABLE IF NOT EXISTS portal_audit(id bigserial PRIMARY KEY,actor_id text NOT NULL,action text NOT NULL,target text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
`
