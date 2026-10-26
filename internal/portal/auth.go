package portal

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const cookieName = "easyrun_session"

func normalizedEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, e := mail.ParseAddress(s)
	if e != nil || a.Address != s || len(s) > 254 {
		return "", errors.New("请填写有效邮箱地址")
	}
	return s, nil
}
func passwordHash(p string) ([]byte, error) {
	if len(p) < 12 || len(p) > 72 {
		return nil, errors.New("密码需为 12–72 字节")
	}
	return bcrypt.GenerateFromPassword([]byte(p), 12)
}
func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, e := url.Parse(origin)
	return e == nil && strings.EqualFold(u.Host, r.Host) && (u.Scheme == "http" || u.Scheme == "https")
}
func remoteIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return host
}
func currentUser(r *http.Request) User { u, _ := r.Context().Value(userKey{}).(User); return u }

type userKey struct{}

func (s *Service) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && !originOK(r) {
			fail(w, 403, "请求来源校验失败")
			return
		}
		if publicPath(r.URL.Path) {
			if !s.allow("public:"+remoteIP(r), 90, time.Minute) {
				fail(w, 429, "请求过于频繁")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		ok, e := s.initialized(r.Context())
		if e != nil {
			s.dbError(w, e)
			return
		}
		if !ok {
			fail(w, 428, "请先完成首次初始化")
			return
		}
		cookie, e := r.Cookie(cookieName)
		if e != nil || len(cookie.Value) > 200 {
			fail(w, 401, "请登录平台")
			return
		}
		u, e := scanUser(s.db.QueryRow(r.Context(), `SELECT `+userFields+` FROM portal_users u JOIN portal_sessions s ON s.user_id=u.id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.enabled`, digest(cookie.Value)))
		if e != nil {
			fail(w, 401, "平台登录已过期，请重新登录")
			return
		}
		cfg, e := s.Settings(r.Context(), false)
		if e != nil {
			s.dbError(w, e)
			return
		}
		if !u.Admin() && (!cfg.FrontendEnabled || cfg.Mode != "commercial") {
			fail(w, 403, "客户前台当前未启用")
			return
		}
		path := r.URL.Path
		if !u.Admin() && (strings.HasPrefix(path, "/api/v1/system/") || path == "/openapi.json" || (r.Method != "GET" && (path == "/api/v1/plans" || path == "/api/v1/areas" || ((strings.HasPrefix(path, "/api/v1/plans/") || strings.HasPrefix(path, "/api/v1/areas/")) && (r.Method == "PUT" || r.Method == "DELETE"))))) {
			fail(w, 403, "此操作需要管理员身份")
			return
		}
		ctx := platform.WithActor(r.Context(), platform.Actor{ID: u.ID, Role: u.Role})
		ctx = context.WithValue(ctx, userKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const userFields = `u.id,u.email,u.role,u.enabled,u.email_verified,u.balance_micro,u.created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	e := row.Scan(&u.ID, &u.Email, &u.Role, &u.Enabled, &u.EmailVerified, &u.BalanceMicro, &u.CreatedAt)
	return u, e
}
func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/status", s.status)
	mux.HandleFunc("POST /api/v1/portal/setup", s.setup)
	mux.HandleFunc("POST /api/v1/portal/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/portal/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/portal/auth/email-code", s.emailCode)
	mux.HandleFunc("POST /api/v1/portal/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie(cookieName); e == nil {
			_, _ = s.db.Exec(r.Context(), `DELETE FROM portal_sessions WHERE token_hash=$1`, digest(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
		jsonReply(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/portal/me", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, currentUser(r)) })
	mux.HandleFunc("GET /api/v1/portal/settings", s.getSettings)
	mux.HandleFunc("PUT /api/v1/portal/settings", s.putSettings)
	mux.HandleFunc("POST /api/v1/portal/mail/test", s.testMail)
	mux.HandleFunc("GET /api/v1/portal/users", s.listUsers)
	mux.HandleFunc("POST /api/v1/portal/users", s.createUser)
	mux.HandleFunc("PUT /api/v1/portal/users/{id}", s.editUser)
	mux.HandleFunc("GET /api/v1/portal/billing", s.billingSummary)
	mux.HandleFunc("POST /api/v1/portal/codes", s.generateCodes)
	mux.HandleFunc("GET /api/v1/portal/codes", s.listCodes)
	mux.HandleFunc("POST /api/v1/portal/redeem", s.redeem)
	mux.HandleFunc("PUT /api/v1/portal/jobs/{id}/billing", s.setJobBilling)
	mux.HandleFunc("POST /api/v1/portal/packages", s.buyPackage)
	mux.HandleFunc("GET /api/v1/portal/accounts/{id}/face-link", retiredFaceLink)
	mux.HandleFunc("POST /api/v1/portal/accounts/{id}/face-link", retiredFaceLink)
	mux.HandleFunc("DELETE /api/v1/portal/accounts/{id}/face-link", retiredFaceLink)
	mux.HandleFunc("GET /api/v1/public/face/{id}", retiredFaceLink)
	mux.HandleFunc("PUT /api/v1/public/face/{id}", retiredFaceLink)
	mux.HandleFunc("GET /api/v1/portal/jobs/{id}/prepay", s.getTaskCredit)
	mux.HandleFunc("POST /api/v1/portal/task-orders", s.createTaskOrder)
	mux.HandleFunc("POST /api/v1/portal/jobs/{id}/prepay", s.purchaseTaskCredit)
	mux.HandleFunc("POST /api/v1/portal/jobs/{id}/prepay/quote", s.adjustTaskCredit)
	mux.HandleFunc("PUT /api/v1/portal/jobs/{id}/prepay", s.adjustTaskCredit)
	mux.HandleFunc("GET /api/v1/portal/jobs/{id}/config-link", s.getTaskLink)
	mux.HandleFunc("POST /api/v1/portal/jobs/{id}/config-link", s.rotateTaskLink)
	mux.HandleFunc("DELETE /api/v1/portal/jobs/{id}/config-link", s.revokeTaskLink)
	mux.HandleFunc("GET /api/v1/public/tasks/{id}", s.publicTaskInfo)
	mux.HandleFunc("PUT /api/v1/public/tasks/{id}", s.publicTaskSave)
	mux.HandleFunc("PUT /api/v1/public/tasks/{id}/face", s.publicTaskFace)
	mux.HandleFunc("POST /api/v1/portal/wallet/address", s.depositAddress)
	mux.HandleFunc("POST /api/v1/portal/wallet/seed", s.generateSeed)
	mux.HandleFunc("POST /api/v1/portal/wallet/export", s.exportWalletBackup)
	mux.HandleFunc("POST /api/v1/portal/wallet/scan", s.scanNow)
	mux.HandleFunc("GET /api/v1/portal/wallet/scan", s.scanStatus)
	mux.HandleFunc("POST /api/v1/portal/wallet/probe", s.probeWalletNodes)
	mux.HandleFunc("GET /api/v1/portal/wallet/sweeps", s.listSweeps)
	mux.HandleFunc("POST /api/v1/portal/wallet/collect", s.collectNow)
	mux.HandleFunc("GET /api/v1/portal/system/versions", s.getVersions)
	mux.HandleFunc("POST /api/v1/portal/system/versions/client/check", s.checkClientVersion)
	mux.HandleFunc("POST /api/v1/portal/system/versions/platform/check", s.checkPlatformVersion)
	mux.HandleFunc("PUT /api/v1/portal/system/versions/settings", s.putUpdateSettings)
	mux.HandleFunc("POST /api/v1/portal/system/versions/platform/install", s.installPlatformUpdate)
	mux.HandleFunc("POST /api/v1/portal/system/versions/platform/rollback", s.rollbackPlatformUpdate)
}
func secureCookie(r *http.Request) bool { return r.TLS != nil || os.Getenv("COOKIE_SECURE") == "true" }
func (s *Service) session(w http.ResponseWriter, r *http.Request, id string) error {
	t, e := token()
	if e != nil {
		return e
	}
	_, e = s.db.Exec(r.Context(), `INSERT INTO portal_sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '7 days')`, digest(t), id)
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", MaxAge: 604800, Expires: time.Now().Add(7 * 24 * time.Hour), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r)})
	return nil
}
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	ok, e := s.initialized(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]any{"setup_required": !ok, "mode": cfg.Mode, "frontend_enabled": cfg.FrontendEnabled, "registration_enabled": cfg.RegistrationEnabled, "email_verification": cfg.EmailVerification})
}
func (s *Service) setup(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.initialized(r.Context())
	if err != nil {
		s.dbError(w, err)
		return
	}
	if initialized {
		fail(w, 409, "平台已经初始化")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Mode     string `json:"mode"`
		Frontend bool   `json:"frontend_enabled"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	email, e := normalizedEmail(in.Email)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	hash, e := passwordHash(in.Password)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if in.Mode != "personal" && in.Mode != "commercial" {
		fail(w, 400, "请选择个人使用或商业使用")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	_, e = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(42831001)`)
	if e != nil {
		s.dbError(w, e)
		return
	}
	var exists bool
	e = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM portal_users)`).Scan(&exists)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if exists {
		fail(w, 409, "平台已经初始化")
		return
	}
	id := platform.NewID("usr")
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_users(id,email,password_hash,role,email_verified) VALUES($1,$2,$3,'superadmin',true)`, id, email, hash)
	if e != nil {
		s.dbError(w, e)
		return
	}
	var configRaw, secretsRaw []byte
	if e = tx.QueryRow(r.Context(), `SELECT config,secrets FROM portal_settings WHERE id=1 FOR UPDATE`).Scan(&configRaw, &secretsRaw); e != nil {
		s.dbError(w, e)
		return
	}
	cfg, e := decodeSettingsConfig(configRaw)
	if e != nil {
		s.dbError(w, e)
		return
	}
	sealed := map[string][]byte{}
	if e = json.Unmarshal(secretsRaw, &sealed); e != nil {
		s.dbError(w, e)
		return
	}
	if len(sealed["seed"]) == 0 {
		cfg.Wallet = Defaults().Wallet
		delete(sealed, "node")
		delete(sealed, "rpc_url")
		delete(sealed, "rpc_key")
		if e = s.sealNewMnemonicWallet(sealed); e != nil {
			s.dbError(w, e)
			return
		}
	}
	cfg.Mode = in.Mode
	cfg.FrontendEnabled = in.Mode == "commercial" && in.Frontend
	cfg.RegistrationEnabled = cfg.FrontendEnabled
	raw, _ := jsonMarshal(cfg)
	secretsRaw, e = jsonMarshal(sealed)
	if e != nil {
		s.dbError(w, e)
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE portal_settings SET config=$1,secrets=$2,updated_at=now() WHERE id=1`, raw, secretsRaw)
	if e == nil {
		_, e = tx.Exec(r.Context(), `UPDATE runner_accounts SET owner_user_id=$1 WHERE owner_user_id=''`, id)
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	if e = s.session(w, r, id); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(platform.WithActor(r.Context(), platform.Actor{ID: id, Role: "superadmin"}), "setup", in.Mode)
	jsonReply(w, 201, map[string]bool{"ok": true})
}
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	if !s.allow("login:"+remoteIP(r), 8, 5*time.Minute) {
		fail(w, 429, "登录尝试过多，请稍后重试")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	email, _ := normalizedEmail(in.Email)
	var id, role string
	var enabled bool
	var hash []byte
	e := s.db.QueryRow(r.Context(), `SELECT id,password_hash,enabled,role FROM portal_users WHERE email=$1`, email).Scan(&id, &hash, &enabled, &role)
	if e != nil {
		hash = []byte("$2a$12$z3hrx6puRQVpzwsSxGkK5O/1tKK3u7T7xR8eyTx3Tj3x2.MvaEx5G")
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) == nil
	if e != nil || !valid || !enabled {
		fail(w, 401, "邮箱或密码错误，或账号已停用")
		return
	}
	if role == "customer" {
		cfg, err := s.Settings(r.Context(), false)
		if err != nil {
			s.dbError(w, err)
			return
		}
		if !cfg.FrontendEnabled || cfg.Mode != "commercial" {
			fail(w, 403, "客户前台当前未启用")
			return
		}
	}
	if e = s.session(w, r, id); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) emailCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	email, e := normalizedEmail(in.Email)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	cfg, e := s.Settings(r.Context(), true)
	if e != nil {
		s.dbError(w, e)
		return
	}
	ok, _ := s.initialized(r.Context())
	if !ok || !cfg.FrontendEnabled || !cfg.RegistrationEnabled || !cfg.EmailVerification {
		fail(w, 403, "邮箱验证码注册当前未开启")
		return
	}
	if !s.allow("mail:"+email, 3, time.Hour) || !s.allow("mail-ip:"+remoteIP(r), 10, time.Hour) {
		fail(w, 429, "验证码发送过于频繁")
		return
	}
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		s.dbError(w, e)
		return
	}
	code := formatCode(n.Int64())
	hash, e := bcrypt.GenerateFromPassword([]byte(code), 10)
	if e != nil {
		s.dbError(w, e)
		return
	}
	id, e := token()
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = SendMail(r.Context(), cfg.Mail, email, "EasyRun 注册验证码", "您的验证码为："+code+"\n有效期为 10 分钟。若非本人操作，请忽略此邮件。"); e != nil {
		fail(w, 502, "邮件发送失败，请联系管理员检查邮件服务器")
		return
	}
	_, e = s.db.Exec(r.Context(), `INSERT INTO portal_email_codes(id,email,code_hash,expires_at) VALUES($1,$2,$3,now()+interval '10 minutes')`, id, email, hash)
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]string{"challenge_id": id})
}
func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		Code        string `json:"code"`
		ChallengeID string `json:"challenge_id"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !s.allow("register:"+remoteIP(r), 8, time.Hour) {
		fail(w, 429, "注册请求过于频繁")
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	ok, _ := s.initialized(r.Context())
	if !ok || cfg.Mode != "commercial" || !cfg.FrontendEnabled || !cfg.RegistrationEnabled {
		fail(w, 403, "自助注册当前未开启")
		return
	}
	email, e := normalizedEmail(in.Email)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	hash, e := passwordHash(in.Password)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if cfg.EmailVerification {
		var codeHash []byte
		var attempts int
		e = tx.QueryRow(r.Context(), `SELECT code_hash,attempts FROM portal_email_codes WHERE id=$1 AND email=$2 AND NOT used AND expires_at>now() FOR UPDATE`, in.ChallengeID, email).Scan(&codeHash, &attempts)
		if e != nil || attempts >= 5 {
			fail(w, 400, "验证码已失效")
			return
		}
		_, e = tx.Exec(r.Context(), `UPDATE portal_email_codes SET attempts=attempts+1 WHERE id=$1`, in.ChallengeID)
		if e != nil {
			s.dbError(w, e)
			return
		}
		if bcrypt.CompareHashAndPassword(codeHash, []byte(in.Code)) != nil {
			_ = tx.Commit(r.Context())
			fail(w, 400, "验证码错误")
			return
		}
		_, e = tx.Exec(r.Context(), `UPDATE portal_email_codes SET used=true WHERE id=$1`, in.ChallengeID)
		if e != nil {
			s.dbError(w, e)
			return
		}
	}
	id := platform.NewID("usr")
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_users(id,email,password_hash,role,email_verified) VALUES($1,$2,$3,'customer',$4)`, id, email, hash, cfg.EmailVerification)
	if e != nil {
		fail(w, 409, "邮箱已注册或注册暂未完成")
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	if e = s.session(w, r, id); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 201, map[string]bool{"ok": true})
}
