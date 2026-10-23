package portal

import (
	"net/http"
	"time"
)

func (s *Service) getVersions(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.ClientVersions == nil || s.PlatformUpdates == nil {
		fail(w, 503, "版本管理服务尚未就绪")
		return
	}
	jsonReply(w, 200, map[string]any{"client": s.ClientVersions.Snapshot(), "platform": s.PlatformUpdates.Snapshot(r.Context())})
}

func (s *Service) checkClientVersion(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.ClientVersions == nil {
		fail(w, 503, "版本管理服务尚未就绪")
		return
	}
	if !s.allow("client-version-check", 1, time.Minute) {
		fail(w, 429, "每分钟可手动检查一次客户端版本")
		return
	}
	// Always return the snapshot, including the last error and retained mandatory
	// update state; a failed HTTP probe must not look like 'no update'.
	_ = s.ClientVersions.Refresh(r.Context(), true)
	jsonReply(w, 200, s.ClientVersions.Snapshot())
}

func (s *Service) checkPlatformVersion(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.PlatformUpdates == nil {
		fail(w, 503, "版本管理服务尚未就绪")
		return
	}
	if !s.allow("platform-version-check", 1, time.Minute) {
		fail(w, 429, "每分钟可手动检查一次平台版本")
		return
	}
	_ = s.PlatformUpdates.Check(r.Context(), true)
	jsonReply(w, 200, s.PlatformUpdates.Snapshot(r.Context()))
}

func (s *Service) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	if !s.allow("platform-update-settings:"+currentUser(r).ID, 5, time.Minute) {
		fail(w, 429, "操作过于频繁，请稍后再试")
		return
	}
	if s.PlatformUpdates == nil {
		fail(w, 503, "版本管理服务尚未就绪")
		return
	}
	var in struct {
		AutoInstall bool   `json:"auto_install"`
		Password    string `json:"password"`
	}
	if decode(w, r, &in) != nil {
		fail(w, 400, "请求格式不正确")
		return
	}
	if !s.passwordOK(r, in.Password) {
		fail(w, 403, "超级管理员密码校验失败")
		return
	}
	if err := s.PlatformUpdates.SaveSettings(r.Context(), in.AutoInstall); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r.Context(), "platform.update.settings", "platform")
	jsonReply(w, 200, s.PlatformUpdates.Snapshot(r.Context()))
}

func (s *Service) installPlatformUpdate(w http.ResponseWriter, r *http.Request) {
	s.applyPlatformUpdate(w, r, false)
}
func (s *Service) rollbackPlatformUpdate(w http.ResponseWriter, r *http.Request) {
	s.applyPlatformUpdate(w, r, true)
}
func (s *Service) applyPlatformUpdate(w http.ResponseWriter, r *http.Request, rollback bool) {
	if !requireSuper(w, r) {
		return
	}
	if s.PlatformUpdates == nil {
		fail(w, 503, "版本管理服务尚未就绪")
		return
	}
	if !s.allow("platform-update:"+currentUser(r).ID, 3, 5*time.Minute) {
		fail(w, 429, "操作过于频繁，请稍后再试")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if decode(w, r, &in) != nil {
		fail(w, 400, "请求格式不正确")
		return
	}
	if !s.passwordOK(r, in.Password) {
		fail(w, 403, "超级管理员密码校验失败")
		return
	}
	if err := s.PlatformUpdates.Start(r.Context(), rollback); err != nil {
		fail(w, 409, err.Error())
		return
	}
	action := "platform.update.install"
	if rollback {
		action = "platform.update.rollback"
	}
	s.audit(r.Context(), action, "platform")
	jsonReply(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
