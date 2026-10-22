package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
)

func (a *api) loginAccount(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	result, err := a.runner.PasswordLogin(ctx, r.PathValue("id"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	status := http.StatusOK
	if result.VerificationRequired {
		status = http.StatusAccepted
	}
	writeJSON(w, status, result)
}

func (a *api) requestOnboardSMS(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	var request accountRequest
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account := request.Account
	account.ID = ""
	account.Enabled = true
	if err := a.validateAccount(r, &account); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_account", err.Error())
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	result, err := a.runner.BeginOnboardSMS(ctx, account)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (a *api) verifyOnboardSMS(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	var request struct {
		Account  platform.Account `json:"account"`
		Password string           `json:"password,omitempty"`
		Code     string           `json:"code"`
		Ticket   string           `json:"ticket"`
	}
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Account.ID = ""
	request.Account.Enabled = true
	if err := a.validateAccount(r, &request.Account); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_account", err.Error())
		return
	}
	if strings.TrimSpace(request.Code) == "" || strings.TrimSpace(request.Ticket) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "短信验证码和 ticket 均为必填项")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	account, err := a.runner.CompleteOnboardSMS(ctx, request.Account, request.Password, request.Code, request.Ticket)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (a *api) requestAccountSMS(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	challenge, err := a.runner.BeginSMS(ctx, r.PathValue("id"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, challenge)
}

func (a *api) verifyAccountSMS(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	var request struct {
		Code   string `json:"code"`
		Ticket string `json:"ticket"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(request.Code) == "" || strings.TrimSpace(request.Ticket) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "短信验证码和 ticket 均为必填项")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	account, err := a.runner.CompleteSMS(ctx, r.PathValue("id"), request.Code, request.Ticket)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (a *api) syncAccountResources(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	resources, err := a.runner.SyncResources(ctx, r.PathValue("id"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resources)
}

func (a *api) accountFaceProfile(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	profile, err := a.runner.FaceProfile(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *api) syncAccountFace(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	profile, err := a.runner.SyncFaceProfile(ctx, r.PathValue("id"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *api) checkAccountFace(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	var request struct {
		Source string `json:"source"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request, 64<<10); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	result, err := a.runner.DetectFace(ctx, r.PathValue("id"), request.Source)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *api) setAccountFaceImage(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 7<<20)
	if err := r.ParseMultipartForm(7 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_face_image", "人脸图片表单无效或超过 6 MB")
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_face_image", "请选择要设置的人脸图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (6<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_face_image", "读取人脸图片失败")
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" && len(data) > 0 {
		contentType = http.DetectContentType(data)
	}
	profile, err := a.runner.SetCustomFace(r.Context(), r.PathValue("id"), contentType, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_face_image", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *api) clearAccountFaceImage(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	profile, err := a.runner.ClearCustomFace(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (a *api) accountFaceImage(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	data, contentType, source, err := a.runner.FaceImage(r.Context(), r.PathValue("id"), r.URL.Query().Get("source"))
	if err != nil {
		writeError(w, http.StatusNotFound, "face_image_not_found", err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Face-Source", source)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *api) accountRecords(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	data, err := a.runner.Records(ctx, r.PathValue("id"), page, pageSize, r.URL.Query().Get("semester_code"), r.URL.Query().Get("run_plan_code"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (a *api) accountRecordDetail(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	data, err := a.runner.RecordDetail(ctx, r.PathValue("id"), r.PathValue("record_id"), r.URL.Query().Get("semester_code"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (a *api) accountRecordInspection(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	result, err := a.runner.RecordInspection(ctx, r.PathValue("id"), r.PathValue("record_id"), r.URL.Query().Get("semester_code"))
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *api) accountRunSummary(w http.ResponseWriter, r *http.Request) {
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	timeType, _ := strconv.Atoi(r.URL.Query().Get("time_type"))
	sportType, _ := strconv.Atoi(r.URL.Query().Get("sport_type"))
	if timeType == 0 {
		timeType = 3
	}
	if sportType == 0 {
		sportType = 1
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	data, err := a.runner.Summary(ctx, r.PathValue("id"), timeType, sportType)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func contextWithProtocolTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 90*time.Second)
}

func (a *api) protocolError(w http.ResponseWriter, err error) {
	if protocol.IsSessionExpired(err) {
		writeError(w, http.StatusUnauthorized, "session_expired", "登录状态已失效，请重新登录")
		return
	}
	status := http.StatusBadGateway
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "尚未") || strings.Contains(message, "必填") || strings.Contains(message, "需要填写") || strings.Contains(message, "不存在") {
		status = http.StatusBadRequest
	} else if strings.Contains(message, "已存在") || strings.Contains(message, "duplicate") || strings.Contains(message, "violates") {
		status = http.StatusConflict
	}
	writeError(w, status, "protocol_request_failed", err.Error())
}
