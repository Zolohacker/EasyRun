package portal

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/bcrypt"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func formatCode(n int64) string         { return fmt.Sprintf("%06d", n) }
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !currentUser(r).Admin() {
		fail(w, 403, "此操作需要管理员身份")
		return false
	}
	return true
}
func requireSuper(w http.ResponseWriter, r *http.Request) bool {
	if currentUser(r).Role != "superadmin" {
		fail(w, 403, "此操作需要超级管理员身份")
		return false
	}
	return true
}
func (s *Service) passwordOK(r *http.Request, p string) bool {
	var hash []byte
	e := s.db.QueryRow(r.Context(), `SELECT password_hash FROM portal_users WHERE id=$1`, currentUser(r).ID).Scan(&hash)
	return e == nil && bcrypt.CompareHashAndPassword(hash, []byte(p)) == nil
}
func validateSettings(c *Settings) error {
	if c.Mode != "personal" && c.Mode != "commercial" {
		return errors.New("使用模式不正确")
	}
	if c.Mode == "personal" {
		c.FrontendEnabled = false
		c.RegistrationEnabled = false
		c.Pricing.Enabled = false
	}
	if c.EmailVerification && (c.Mail.Host == "" || c.Mail.From == "") {
		return errors.New("开启邮箱验证前请配置邮件服务器和发件邮箱")
	}
	if c.Mail.Protocol != "smtp" && c.Mail.Protocol != "smtps" && c.Mail.Protocol != "starttls" {
		return errors.New("邮件协议支持 SMTP、SMTPS 和 STARTTLS")
	}
	if c.Mail.Port < 1 || c.Mail.Port > 65535 || strings.ContainsAny(c.Mail.Host, "\r\n") || strings.ContainsAny(c.Mail.Username, "\r\n") {
		return errors.New("邮件服务器参数不正确")
	}
	if c.Mail.From != "" {
		if _, e := normalizedEmail(c.Mail.From); e != nil {
			return e
		}
	}
	for _, v := range []int64{c.Pricing.PerRunMicro, c.Pricing.PerKMMicro, c.Pricing.PlanPackageMicro} {
		if v < 0 || v > 1_000_000*MicroUSDT {
			return errors.New("价格超出允许范围")
		}
	}
	w := &c.Wallet
	if w.Network != "mainnet" && w.Network != "shasta" && w.Network != "nile" {
		return errors.New("请选择 mainnet、shasta 或 nile 网络")
	}
	u, e := url.Parse(w.NodeURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return errors.New("TRON 节点必须使用不含凭据的 HTTPS 地址")
	}
	w.NodeURL = strings.TrimRight(w.NodeURL, "/")
	if w.Network == "mainnet" {
		w.Contract = MainnetUSDT
	}
	if w.Contract != "" && !ValidAddress(w.Contract) {
		return errors.New("TRC20 合约地址不正确")
	}
	if w.CollectionAddress != "" && !ValidAddress(w.CollectionAddress) {
		return errors.New("归集地址不正确")
	}
	if w.Enabled && w.Contract == "" {
		return errors.New("启用充值前请填写对应网络的 USDT 合约地址")
	}
	if w.AutoCollect && (!w.Enabled || w.CollectionAddress == "" || w.Network != "mainnet") {
		return errors.New("自动归集需启用主网充值并配置归集地址；测试网请使用手动归集验证")
	}
	if w.FeeLimitSun < 1_000_000 || w.FeeLimitSun > 100_000_000 || w.MinCollectMicro < 1 || w.EnergyPriceSun < 1 || w.EnergyPriceSun > 10000 {
		return errors.New("归集阈值、手续费或能量单价超出范围")
	}
	if _, e = cron.ParseStandard("CRON_TZ=" + w.Timezone + " " + w.CollectCron); e != nil {
		return errors.New("归集 Cron 或时区不正确")
	}
	if w.SeedHex != "" {
		seed, e := hex.DecodeString(w.SeedHex)
		if e != nil || len(seed) < 32 || len(seed) > 64 {
			return errors.New("HD seed 需要 32–64 字节十六进制数据")
		}
	}
	return nil
}
func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, cfg)
}
func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	var in struct {
		Settings Settings `json:"settings"`
		Password string   `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !s.passwordOK(r, in.Password) {
		fail(w, 403, "请填写当前超级管理员密码")
		return
	}
	if e := validateSettings(&in.Settings); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e := s.saveSettings(r, in.Settings); e != nil {
		fail(w, 400, e.Error())
		return
	}
	s.audit(r.Context(), "settings.update", in.Settings.Mode)
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, cfg)
}
func (s *Service) saveSettings(r *http.Request, c Settings) error {
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	var data, raw []byte
	e = tx.QueryRow(r.Context(), `SELECT config,secrets FROM portal_settings WHERE id=1 FOR UPDATE`).Scan(&raw, &data)
	if e != nil {
		return e
	}
	var old Settings
	if e = json.Unmarshal(raw, &old); e != nil {
		return e
	}
	var sealed map[string][]byte
	if e = json.Unmarshal(data, &sealed); e != nil {
		return e
	}
	var allocated bool
	if e = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM portal_wallet_addresses)`).Scan(&allocated); e != nil {
		return e
	}
	if allocated && (old.Wallet.Network != c.Wallet.Network || old.Wallet.Contract != c.Wallet.Contract) {
		return errors.New("已有充值地址，钱包网络与合约固定，请使用独立新部署切换网络")
	}
	for key, p := range map[string]*string{"mail": &c.Mail.Password, "seed": &c.Wallet.SeedHex, "node": &c.Wallet.NodeAPIKey, "feee": &c.Wallet.FeeeAPIKey} {
		if *p != "" {
			if key == "seed" && len(sealed[key]) > 0 {
				return errors.New("HD seed 已配置且禁止覆盖；请保留离线备份")
			}
			sealed[key], e = s.vault.Encrypt([]byte(*p), "portal-secret:"+key)
			if e != nil {
				return e
			}
		}
		*p = ""
	}
	if c.Wallet.Enabled && len(sealed["seed"]) == 0 {
		return errors.New("请先生成并备份 HD seed")
	}
	if c.Wallet.AutoCollect && len(sealed["feee"]) == 0 {
		return errors.New("自动归集需要配置 Feee API key")
	}
	data, e = json.Marshal(sealed)
	if e != nil {
		return e
	}
	raw, e = json.Marshal(c)
	if e != nil {
		return e
	}
	_, e = tx.Exec(r.Context(), `UPDATE portal_settings SET config=$1,secrets=$2,next_collect_at=CASE WHEN config->'wallet'->>'collect_cron' IS DISTINCT FROM $3 OR config->'wallet'->>'timezone' IS DISTINCT FROM $4 THEN NULL ELSE next_collect_at END,updated_at=now() WHERE id=1`, raw, data, c.Wallet.CollectCron, c.Wallet.Timezone)
	if e != nil {
		return e
	}
	return tx.Commit(r.Context())
}
func (s *Service) testMail(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	cfg, e := s.Settings(r.Context(), true)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = SendMail(r.Context(), cfg.Mail, currentUser(r).Email, "EasyRun 邮件服务测试", "邮件服务已连接成功。此邮件用于测试 SMTP 配置。"); e != nil {
		fail(w, 502, "邮件服务测试失败："+e.Error())
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rows, e := s.db.Query(r.Context(), `SELECT `+userFields+` FROM portal_users u ORDER BY u.created_at DESC LIMIT 1000`)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			s.dbError(w, e)
			return
		}
		out = append(out, u)
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, out)
}
func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
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
	if in.Role != "admin" && in.Role != "customer" {
		fail(w, 400, "新增角色支持管理员和客户")
		return
	}
	id := platform.NewID("usr")
	_, e = s.db.Exec(r.Context(), `INSERT INTO portal_users(id,email,password_hash,role,email_verified)VALUES($1,$2,$3,$4,true)`, id, email, hash, in.Role)
	if e != nil {
		fail(w, 409, "该邮箱已注册")
		return
	}
	s.audit(r.Context(), "user.create", id)
	jsonReply(w, 201, map[string]string{"id": id})
}
func (s *Service) editUser(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	tag, e := tx.Exec(r.Context(), `UPDATE portal_users SET enabled=$2 WHERE id=$1 AND role<>'superadmin'`, r.PathValue("id"), in.Enabled)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 400, "超级管理员保持启用，或该用户不存在")
		return
	}
	if !in.Enabled {
		_, e = tx.Exec(r.Context(), `DELETE FROM portal_sessions WHERE user_id=$1`, r.PathValue("id"))
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "user.enabled", r.PathValue("id"))
	jsonReply(w, 200, map[string]bool{"ok": true})
}
