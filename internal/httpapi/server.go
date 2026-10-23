package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/buildinfo"
	"github.com/Zolohacker/EasyRun/internal/config"
	"github.com/Zolohacker/EasyRun/internal/mapapi"
	"github.com/Zolohacker/EasyRun/internal/portal"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/route"
	"github.com/Zolohacker/EasyRun/internal/runner"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/store"
)

//go:embed web/dist
var webAssets embed.FS

type RoutePlanner interface {
	PlanRoute(context.Context, route.PlanRequest) (route.Route, error)
}

type MapRuntime interface {
	mapapi.StatsProvider
	Configured() bool
	UpdateRuntime([]mapapi.APIKey) error
}

type Dependencies struct {
	Portal     *portal.Service
	Planner    RoutePlanner
	Store      store.Store
	Logger     *slog.Logger
	Config     config.Config
	MapRuntime MapRuntime
	Automation *automation.Scheduler
	Runner     *runner.Service
	Vault      *securestore.Vault
}

type api struct {
	planner    RoutePlanner
	store      store.Store
	generator  *activity.Generator
	logger     *slog.Logger
	config     config.Config
	mapRuntime MapRuntime
	automation *automation.Scheduler
	runner     *runner.Service
	vault      *securestore.Vault
}

func New(dependencies Dependencies) http.Handler {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.Default()
	}
	a := &api{
		planner:    dependencies.Planner,
		store:      dependencies.Store,
		generator:  activity.NewGenerator(),
		logger:     logger,
		config:     dependencies.Config,
		mapRuntime: dependencies.MapRuntime,
		automation: dependencies.Automation,
		runner:     dependencies.Runner,
		vault:      dependencies.Vault,
	}
	mux := http.NewServeMux()
	if dependencies.Portal != nil {
		dependencies.Portal.Mount(mux)
	}
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/v1/config", a.getConfig)
	mux.HandleFunc("GET /api/v1/system/settings", a.getSystemSettings)
	mux.HandleFunc("PUT /api/v1/system/settings", a.updateSystemSettings)
	mux.HandleFunc("GET /api/v1/map/metrics", a.mapMetrics)
	mux.HandleFunc("POST /api/v1/routes/plan", a.planRoute)
	mux.HandleFunc("POST /api/v1/runs/simulate", a.simulateRun)
	mux.HandleFunc("GET /api/v1/runs", a.listRuns)
	mux.HandleFunc("GET /api/v1/runs/{id}", a.getRun)
	mux.HandleFunc("GET /api/v1/plans", a.listPlans)
	mux.HandleFunc("POST /api/v1/plans", a.createPlan)
	mux.HandleFunc("GET /api/v1/plans/{id}", a.getPlan)
	mux.HandleFunc("POST /api/v1/plans/{id}/sample", a.samplePlan)
	mux.HandleFunc("PUT /api/v1/plans/{id}", a.updatePlan)
	mux.HandleFunc("DELETE /api/v1/plans/{id}", a.deletePlan)
	mux.HandleFunc("GET /api/v1/areas", a.listAreas)
	mux.HandleFunc("POST /api/v1/areas", a.createArea)
	mux.HandleFunc("GET /api/v1/areas/{id}", a.getArea)
	mux.HandleFunc("PUT /api/v1/areas/{id}", a.updateArea)
	mux.HandleFunc("DELETE /api/v1/areas/{id}", a.deleteArea)
	mux.HandleFunc("GET /api/v1/accounts", a.listAccounts)
	mux.HandleFunc("POST /api/v1/accounts", a.createAccount)
	mux.HandleFunc("POST /api/v1/accounts/onboard/password", a.onboardAccountPassword)
	mux.HandleFunc("POST /api/v1/accounts/onboard/sms/request", a.requestOnboardSMS)
	mux.HandleFunc("POST /api/v1/accounts/onboard/sms/verify", a.verifyOnboardSMS)
	mux.HandleFunc("GET /api/v1/accounts/{id}", a.getAccount)
	mux.HandleFunc("PUT /api/v1/accounts/{id}", a.updateAccount)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", a.deleteAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/login", a.loginAccount)
	mux.HandleFunc("POST /api/v1/accounts/{id}/sms/request", a.requestAccountSMS)
	mux.HandleFunc("POST /api/v1/accounts/{id}/sms/verify", a.verifyAccountSMS)
	mux.HandleFunc("POST /api/v1/accounts/{id}/sync", a.syncAccountResources)
	mux.HandleFunc("GET /api/v1/accounts/{id}/face", a.accountFaceProfile)
	mux.HandleFunc("POST /api/v1/accounts/{id}/face/sync", a.syncAccountFace)
	mux.HandleFunc("POST /api/v1/accounts/{id}/face/check", a.checkAccountFace)
	mux.HandleFunc("GET /api/v1/accounts/{id}/face/image", a.accountFaceImage)
	mux.HandleFunc("PUT /api/v1/accounts/{id}/face/image", a.setAccountFaceImage)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}/face/image", a.clearAccountFaceImage)
	mux.HandleFunc("GET /api/v1/accounts/{id}/records", a.accountRecords)
	mux.HandleFunc("GET /api/v1/accounts/{id}/records/summary", a.accountRunSummary)
	mux.HandleFunc("GET /api/v1/accounts/{id}/records/{record_id}/inspection", a.accountRecordInspection)
	mux.HandleFunc("GET /api/v1/accounts/{id}/records/{record_id}", a.accountRecordDetail)
	mux.HandleFunc("GET /api/v1/jobs", a.listJobs)
	mux.HandleFunc("POST /api/v1/jobs", a.createJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}", a.getJob)
	mux.HandleFunc("PUT /api/v1/jobs/{id}", a.updateJob)
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", a.deleteJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/run", a.runJobNow)
	mux.HandleFunc("GET /api/v1/executions", a.listExecutions)
	mux.HandleFunc("GET /openapi.json", a.openapi)
	content, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(content))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" || r.URL.Path == "/app" || r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/face-upload/") {
			raw, err := fs.ReadFile(content, "index.html")
			if err != nil {
				http.Error(w, "application unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(raw)
			return
		}
		files.ServeHTTP(w, r)
	}))
	var handler http.Handler = mux
	if dependencies.Portal != nil {
		handler = dependencies.Portal.Wrap(handler)
	}
	return a.middleware(handler)
}

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	databaseOK := a.store.Ping(ctx) == nil
	status := http.StatusOK
	state := "ok"
	if !databaseOK {
		status, state = http.StatusServiceUnavailable, "degraded"
	}
	writeJSON(w, status, map[string]any{
		"build":              buildinfo.Current(),
		"boot_id":            os.Getenv("EASYRUN_BOOT_ID"),
		"face_runtime_ready": a.config.FaceReady,
		"status":             state,
		"database":           databaseOK,
		"map_api_configured": a.mapConfigured(),
		"time":               time.Now().UTC(),
	})
}

func (a *api) getConfig(w http.ResponseWriter, _ *http.Request) {
	automationStats := any(map[string]any{"workers": 0, "worker_processes": 0, "worker_concurrency": 0, "active": 0, "queue_depth": 0})
	if a.automation != nil {
		automationStats = a.automation.Stats()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"face_runtime":        map[string]any{"ready": a.config.FaceReady, "status": a.config.FaceStatus, "model": "Megatron"},
		"map_api_configured":  a.mapConfigured(),
		"database_configured": a.config.DatabaseConfigured(),
		"protocol_configured": a.config.ProtocolConfigured(),
		"client_protocol": map[string]any{
			"app_version": protocol.AppVersion, "build_version": protocol.BuildVersion,
			"app_code": protocol.AppCode, "environment": a.config.ProtocolEnvironment,
			"environment_name": protocolEnvironmentName(a.config.ProtocolEnvironment),
			"base_url":         a.config.ProtocolBaseURL,
		},
		"route_candidates": a.config.MapRouteCandidates,
		"map_rate_limit":   a.currentMapRateLimit(),
		"automation":       automationStats,
		"limits": map[string]any{
			"minimum_route_m": 300,
			"maximum_route_m": 100000,
			"maximum_runs":    200,
		},
	})
}

func protocolEnvironmentName(environment int) string {
	return map[int]string{1: "Dev", 4: "Test", 7: "Pre", 9: "Pro", 10: "Custom", 11: "Sd"}[environment]
}

func (a *api) mapMetrics(w http.ResponseWriter, _ *http.Request) {
	if a.mapRuntime == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, a.mapRuntime.Stats())
}

func (a *api) mapConfigured() bool {
	if a.mapRuntime != nil {
		return a.mapRuntime.Configured()
	}
	return a.config.MapConfigured()
}

func (a *api) currentMapRateLimit() map[string]any {
	if a.mapRuntime != nil {
		stats := a.mapRuntime.Stats()
		return map[string]any{
			"qps": stats.QPS, "burst": stats.Burst, "burst_per_key": stats.BurstPerKey,
			"key_count": stats.KeyCount,
		}
	}
	keyCount := 0
	if a.config.MapConfigured() {
		keyCount = 1
	}
	return map[string]any{
		"qps": a.config.MapQPS, "burst": a.config.MapBurst * keyCount,
		"burst_per_key": a.config.MapBurst, "key_count": keyCount,
	}
}

func (a *api) planRoute(w http.ResponseWriter, r *http.Request) {
	var request route.PlanRequest
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	request.FairnessKey = "interactive-route-test"
	planned, err := a.planner.PlanRoute(ctx, request)
	if err != nil {
		status := http.StatusBadGateway
		code := "route_planning_failed"
		if errors.Is(err, mapapi.ErrNotConfigured) {
			status = http.StatusServiceUnavailable
			code = "map_api_not_configured"
		} else if strings.Contains(err.Error(), "must be") || strings.Contains(err.Error(), "invalid") {
			status = http.StatusBadRequest
			code = "invalid_route_request"
		}
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, planned)
}

func (a *api) simulateRun(w http.ResponseWriter, r *http.Request) {
	var request activity.GenerateRequest
	if err := decodeJSON(w, r, &request, 12<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.AccountID != "" {
		if _, err := a.store.GetAccount(r.Context(), request.AccountID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_account", "account_id does not exist")
			return
		}
	}
	run, err := a.generator.Generate(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "generation_failed", err.Error())
		return
	}
	if request.Persist {
		if err := a.store.Save(r.Context(), run); err != nil {
			writeError(w, http.StatusInternalServerError, "persistence_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, run)
}

func (a *api) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.store.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "run does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (a *api) openapi(w http.ResponseWriter, _ *http.Request) {
	payload, err := webAssets.ReadFile("web/dist/openapi.json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "openapi_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (a *api) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "method", r.Method, "path", r.URL.Path, "error", fmt.Sprint(recovered))
				writeError(w, http.StatusInternalServerError, "internal_error", "request failed")
			}
			a.logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
