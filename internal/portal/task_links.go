package portal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
	"github.com/jackc/pgx/v5"
)

// A capability grants configuration only, never payment, account switching or
// task execution. Its ceiling is snapshotted when issued, not supplied by guests.
type taskLinkScope struct {
	OwnerID              string  `json:"owner_id"`
	AccountID            string  `json:"account_id"`
	PlanID               string  `json:"plan_id"`
	AreaID               string  `json:"area_id"`
	MaxDistanceM         float64 `json:"max_distance_m"`
	DailyRunLimit        int     `json:"daily_run_limit"`
	WeeklyRunLimit       int     `json:"weekly_run_limit"`
	DailyDistanceLimitM  float64 `json:"daily_distance_limit_m"`
	WeeklyDistanceLimitM float64 `json:"weekly_distance_limit_m"`
	DayScope             string  `json:"day_scope"`
	Kind                 string  `json:"kind"`
	RemainingUnits       int64   `json:"remaining_units,omitempty"`
}

func newTaskScope(j platform.JobContext, kind string) taskLinkScope {
	return taskLinkScope{OwnerID: j.Account.OwnerUserID, AccountID: j.Account.ID, PlanID: j.Plan.ID, AreaID: j.Area.ID, MaxDistanceM: j.Job.TargetDistanceM, DailyRunLimit: j.Job.DailyRunLimit, WeeklyRunLimit: j.Job.WeeklyRunLimit, DailyDistanceLimitM: j.Job.DailyDistanceLimitM, WeeklyDistanceLimitM: j.Job.WeeklyDistanceLimitM, DayScope: j.Job.DayScope, Kind: kind}
}

func (s *Service) paidTaskScope(ctx context.Context, tx pgx.Tx, j platform.JobContext) (string, error) {
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM portal_users WHERE id=$1 AND enabled FOR SHARE`, j.Account.OwnerUserID).Scan(&role); err != nil {
		return "", errors.New("任务所属用户已停用")
	}
	if role != "customer" {
		return "personal", nil
	}
	cfg, err := s.Settings(ctx, false)
	if err != nil {
		return "", err
	}
	if cfg.Mode != "commercial" || !cfg.FrontendEnabled {
		return "", errors.New("客户入口当前未启用")
	}
	var found string
	err = tx.QueryRow(ctx, `SELECT plan_id FROM portal_entitlements WHERE user_id=$1 AND account_id=$2 AND plan_id=$3 FOR SHARE`, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		var mode string
		err = tx.QueryRow(ctx, `SELECT mode FROM portal_task_credits WHERE job_id=$1 AND user_id=$2 AND account_id=$3 AND plan_id=$4 AND remaining_units>0 FOR SHARE`, j.Job.ID, j.Account.OwnerUserID, j.Account.ID, j.Plan.ID).Scan(&mode)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("请先为此任务预付次数/里程，或购买对应完整计划套餐；账户余额不属于已付费额度")
		}
		return mode, err
	}
	if err != nil {
		return "", err
	}
	return "plan_package", nil
}

func (s *Service) getTaskLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetJob(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	var cipher, raw []byte
	var expires time.Time
	e := s.db.QueryRow(r.Context(), `SELECT token_cipher,expires_at,scope FROM portal_task_links WHERE job_id=$1 AND expires_at>now()`, r.PathValue("id")).Scan(&cipher, &expires, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		jsonReply(w, 200, map[string]any{"url": nil})
		return
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	plain, e := s.vault.Decrypt(cipher, "task-link:"+r.PathValue("id"))
	if e != nil {
		s.dbError(w, e)
		return
	}
	var scope taskLinkScope
	if e = json.Unmarshal(raw, &scope); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]any{"url": "/task-config/" + r.PathValue("id") + "#token=" + string(plain), "expires_at": expires, "max_distance_m": scope.MaxDistanceM})
}
func (s *Service) rotateTaskLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetJob(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	var in struct {
		Days int `json:"days"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if in.Days == 0 {
		in.Days = 30
	}
	if in.Days < 1 || in.Days > 365 {
		fail(w, 400, "链接有效期为 1–365 天")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var locked string
	if e = tx.QueryRow(r.Context(), `SELECT id FROM automation_jobs WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&locked); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	j, e := s.store.GetJobContext(r.Context(), locked)
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	kind, e := s.paidTaskScope(r.Context(), tx, j)
	if e != nil {
		fail(w, 403, e.Error())
		return
	}
	scope := newTaskScope(j, kind)
	// Resolve legacy distance=0 without granting a zero or arbitrary ceiling.
	if scope.MaxDistanceM <= 0 {
		scope.MaxDistanceM, e = activity.AutomationTargetDistance(j.Plan.TargetDistanceM, j.Plan.Rules, 0)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	if e = s.applyCreditScope(r.Context(), tx, j, &scope); e != nil {
		fail(w, 403, e.Error())
		return
	}
	secret, e := token()
	if e != nil {
		s.dbError(w, e)
		return
	}
	cipher, e := s.vault.Encrypt([]byte(secret), "task-link:"+locked)
	if e != nil {
		s.dbError(w, e)
		return
	}
	raw, e := json.Marshal(scope)
	if e != nil {
		s.dbError(w, e)
		return
	}
	expires := time.Now().Add(time.Duration(in.Days) * 24 * time.Hour)
	_, e = tx.Exec(r.Context(), `INSERT INTO portal_task_links(job_id,token_hash,token_cipher,expires_at,scope) VALUES($1,$2,$3,$4,$5) ON CONFLICT(job_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,token_cipher=EXCLUDED.token_cipher,expires_at=EXCLUDED.expires_at,scope=EXCLUDED.scope,updated_at=now()`, locked, digest(secret), cipher, expires, raw)
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "task-link.rotate", locked)
	jsonReply(w, 201, map[string]any{"url": "/task-config/" + locked + "#token=" + secret, "expires_at": expires, "max_distance_m": scope.MaxDistanceM})
}
func (s *Service) revokeTaskLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetJob(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if _, e := s.db.Exec(r.Context(), `DELETE FROM portal_task_links WHERE job_id=$1`, r.PathValue("id")); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "task-link.revoke", r.PathValue("id"))
	jsonReply(w, 200, map[string]bool{"ok": true})
}

// All public reads and writes check the current entitlement and hold locks until
// commit. Rotation/revocation or ownership/plan changes cannot race a guest write.
func (s *Service) openTaskLink(r *http.Request) (pgx.Tx, platform.JobContext, taskLinkScope, error) {
	ctx := r.Context()
	var j platform.JobContext
	var scope taskLinkScope
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return nil, j, scope, e
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(ctx)
		}
	}()
	var id, hash string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT id FROM automation_jobs WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&id)
	if e != nil {
		return nil, j, scope, errors.New("配置链接无效或已过期")
	}
	e = tx.QueryRow(ctx, `SELECT l.token_hash,l.scope FROM portal_task_links l JOIN automation_jobs j ON j.id=l.job_id JOIN runner_accounts a ON a.id=j.account_id WHERE l.job_id=$1 AND l.expires_at>now() FOR SHARE OF l,a`, id).Scan(&hash, &raw)
	if e != nil || !checkFaceToken(hash, faceToken(r)) {
		return nil, j, scope, errors.New("配置链接无效或已过期")
	}
	if e = json.Unmarshal(raw, &scope); e != nil {
		return nil, j, scope, e
	}
	j, e = s.store.GetJobContext(ctx, id)
	if e != nil {
		return nil, j, scope, e
	}
	if j.Account.OwnerUserID != scope.OwnerID || j.Account.ID != scope.AccountID || j.Plan.ID != scope.PlanID || j.Area.ID != scope.AreaID {
		return nil, j, scope, errors.New("任务绑定已变更，请重新生成配置链接")
	}
	kind, e := s.paidTaskScope(ctx, tx, j)
	if e != nil {
		return nil, j, scope, e
	}
	if kind != scope.Kind {
		return nil, j, scope, errors.New("任务计费身份已变更，请重新生成配置链接")
	}
	if e = s.applyCreditScope(ctx, tx, j, &scope); e != nil {
		return nil, j, scope, e
	}
	ok = true
	return tx, j, scope, nil
}

func (s *Service) publicTaskInfo(w http.ResponseWriter, r *http.Request) {
	tx, j, scope, e := s.openTaskLink(r)
	if e != nil {
		fail(w, 404, e.Error())
		return
	}
	defer tx.Rollback(r.Context())
	items, e := s.store.ListManualRoutes(platform.WithActor(r.Context(), platform.Actor{ID: scope.OwnerID, Role: "customer"}))
	if e != nil {
		s.dbError(w, e)
		return
	}
	routes := []platform.ManualRoute{}
	for _, m := range items {
		if m.AccountID == scope.AccountID && m.PlanID == scope.PlanID && m.AreaID == scope.AreaID {
			routes = append(routes, m)
		}
	}
	mapConfigured, e := automation.MapAPIConfigured(r.Context(), s.store)
	if e != nil {
		s.dbError(w, e)
		return
	}
	// No login name, credentials, balance or billing mutations in this response.
	jsonReply(w, 200, map[string]any{"map_api_configured": mapConfigured, "job": j.Job, "plan": j.Plan, "area": j.Area, "routes": routes, "account_name": j.Account.Name, "face_configured": len(j.Account.FaceCustomCipher) > 0, "limits": map[string]any{"max_distance_m": scope.MaxDistanceM, "daily_run_limit": scope.DailyRunLimit, "weekly_run_limit": scope.WeeklyRunLimit, "daily_distance_limit_m": scope.DailyDistanceLimitM, "weekly_distance_limit_m": scope.WeeklyDistanceLimitM, "day_scope": scope.DayScope}, "scope_kind": scope.Kind, "remaining_units": scope.RemainingUnits})
}

type taskConfigInput struct {
	TargetDistanceM      float64     `json:"target_distance_m"`
	RouteMode            string      `json:"route_mode"`
	ManualRouteID        string      `json:"manual_route_id"`
	RoutePoints          []geo.Point `json:"route_points,omitempty"`
	ScheduleMode         string      `json:"schedule_mode"`
	ManualTime           string      `json:"manual_time"`
	DayScope             string      `json:"day_scope"`
	DailyRunLimit        int         `json:"daily_run_limit"`
	WeeklyRunLimit       int         `json:"weekly_run_limit"`
	DailyDistanceLimitM  float64     `json:"daily_distance_limit_m"`
	WeeklyDistanceLimitM float64     `json:"weekly_distance_limit_m"`
}

func withinCap(value, cap float64) bool { return cap <= 0 || (value > 0 && value <= cap+1e-6) }
func checkTaskScope(j platform.AutomationJob, s taskLinkScope) error {
	if math.IsNaN(j.TargetDistanceM) || math.IsInf(j.TargetDistanceM, 0) || j.TargetDistanceM <= 0 || j.TargetDistanceM > s.MaxDistanceM+1e-6 {
		return errors.New("距离超出此配置链接的已下单范围")
	}
	if !withinCap(float64(j.DailyRunLimit), float64(s.DailyRunLimit)) || !withinCap(float64(j.WeeklyRunLimit), float64(s.WeeklyRunLimit)) || !withinCap(j.DailyDistanceLimitM, s.DailyDistanceLimitM) || !withinCap(j.WeeklyDistanceLimitM, s.WeeklyDistanceLimitM) {
		return errors.New("次数或公里数限额超出此配置链接的已下单范围")
	}
	if s.DayScope != "" && s.DayScope != "every_day" && (j.ScheduleMode != "manual" || j.DayScope != s.DayScope) {
		return errors.New("执行日超出下单范围，请联系下单者修改")
	}
	return nil
}
func (s *Service) publicTaskSave(w http.ResponseWriter, r *http.Request) {
	var in taskConfigInput
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	tx, j, scope, e := s.openTaskLink(r)
	if e != nil {
		fail(w, 404, e.Error())
		return
	}
	defer tx.Rollback(r.Context())
	job := j.Job
	job.TargetDistanceM = in.TargetDistanceM
	job.RouteMode = in.RouteMode
	job.ManualRouteID = in.ManualRouteID
	job.ScheduleMode = in.ScheduleMode
	job.ManualTime = in.ManualTime
	job.DayScope = in.DayScope
	job.CronExpr = ""
	job.DailyRunLimit = in.DailyRunLimit
	job.WeeklyRunLimit = in.WeeklyRunLimit
	job.DailyDistanceLimitM = in.DailyDistanceLimitM
	job.WeeklyDistanceLimitM = in.WeeklyDistanceLimitM
	if job.ScheduleMode != "manual" && job.ScheduleMode != "plan_random" {
		fail(w, 400, "请选择固定时间或计划时段随机")
		return
	}
	if e = checkTaskScope(job, scope); e != nil {
		fail(w, 403, e.Error())
		return
	}
	var drawing *platform.ManualRoute
	if len(in.RoutePoints) > 0 {
		if job.RouteMode != "custom" {
			fail(w, 400, "绘制路线需要选择自定义模式")
			return
		}
		m := platform.ManualRoute{ID: platform.NewID("route"), AccountID: scope.AccountID, PlanID: scope.PlanID, AreaID: scope.AreaID, Name: "独立配置路线 · " + time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02 15:04"), Points: in.RoutePoints, CreatedByUserID: scope.OwnerID, SourceJobID: j.Job.ID}
		job.ManualRouteID = m.ID
		if e = platform.ValidateManualRoute(&m, j.Plan, j.Area); e == nil {
			e = platform.ValidateJobRoute(job, m, j.Plan, j.Area)
		}
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		drawing = &m

	}
	ownerCtx := platform.WithActor(r.Context(), platform.Actor{ID: scope.OwnerID, Role: "customer"})
	database := s.store
	if drawing != nil {
		database = pendingDrawingStore{Store: s.store, drawing: *drawing}
	}
	if e = automation.NormalizeJob(ownerCtx, database, &job); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e = checkTaskScope(job, scope); e != nil {
		fail(w, 403, e.Error())
		return
	}
	if drawing != nil {
		m, err := store.InsertManualRouteTx(r.Context(), tx, *drawing)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		job.RouteMode = "custom"
		job.ManualRouteID = m.ID
		if e = platform.ValidateJobRoute(job, m, j.Plan, j.Area); e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	// Enable/disable, identity, timezone and billing are preserved from storage.
	job, e = store.UpdateJobTx(r.Context(), tx, job)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "task.public-configure", job.ID)
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) publicTaskFace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	data, e := io.ReadAll(r.Body)
	if e != nil {
		fail(w, 400, "图片超过 6 MB")
		return
	}
	tx, j, _, e := s.openTaskLink(r)
	if e != nil {
		fail(w, 404, e.Error())
		return
	}
	defer tx.Rollback(r.Context())
	if !s.allow("task-face:"+j.Job.ID, 15, 10*time.Minute) {
		fail(w, 429, "上传过于频繁")
		return
	}
	var running bool
	if e = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM automation_executions WHERE account_id=$1 AND status='running')`, j.Account.ID).Scan(&running); e != nil {
		s.dbError(w, e)
		return
	}
	if running {
		fail(w, 409, "账号任务正在执行，请完成后再修改照片")
		return
	}
	data, e = cleanPortrait(data)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	cipher, e := s.vault.Encrypt(data, "account:"+j.Account.ID+":face-custom")
	if e != nil {
		s.dbError(w, e)
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE runner_accounts SET face_custom_cipher=$2,face_custom_mime='image/jpeg',updated_at=now() WHERE id=$1`, j.Account.ID, cipher)
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "task.public-face", j.Job.ID)
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func retiredFaceLink(w http.ResponseWriter, r *http.Request) {
	fail(w, http.StatusGone, "人脸链接已升级，请在任务列表中重新生成独立配置链接")
}

// Validate an uncommitted drawing through the normal custom-route path without
// disguising it as automatic or skipping capability/geometry checks.
type pendingDrawingStore struct {
	store.Store
	drawing platform.ManualRoute
}

func (s pendingDrawingStore) GetManualRoute(ctx context.Context, id string) (platform.ManualRoute, error) {
	if id == s.drawing.ID {
		return s.drawing, nil
	}
	return s.Store.GetManualRoute(ctx, id)
}
