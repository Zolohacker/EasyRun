package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/buildinfo"
	"github.com/Zolohacker/EasyRun/internal/config"
	"github.com/Zolohacker/EasyRun/internal/faceengine"
	"github.com/Zolohacker/EasyRun/internal/httpapi"
	"github.com/Zolohacker/EasyRun/internal/mapapi"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/portal"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/route"
	"github.com/Zolohacker/EasyRun/internal/runner"
	"github.com/Zolohacker/EasyRun/internal/safety"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/selfupdate"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func main() {
	// Candidate metadata probes must not load credentials, initialize native
	// libraries, open the database or start network/background work.
	if len(os.Args) == 2 && os.Args[1] == "--version-json" {
		_ = json.NewEncoder(os.Stdout).Encode(buildinfo.Current())
		return
	}
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	containerMode := len(os.Args) == 2 && os.Args[1] == "--container-supervise"
	if containerMode || len(os.Args) == 3 && os.Args[1] == "--supervise" {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		health, err := selfupdate.HealthURL(addr)
		if err == nil {
			if containerMode {
				var image string
				image, err = os.Executable()
				root := os.Getenv("EASYRUN_RUNTIME_DIR")
				if root == "" {
					root = "/app/runtime"
				}
				if err == nil {
					err = selfupdate.ContainerSupervise(runCtx, image, root, health)
				}
			} else {
				err = selfupdate.Supervise(runCtx, os.Args[2], health)
			}
		}
		if err != nil {
			slog.Error("platform supervisor stopped", "error", err)
			os.Exit(1)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if !cfg.DatabaseConfigured() {
		logger.Error("DATABASE_URL is required")
		os.Exit(2)
	}
	startupCtx, startupCancel := context.WithTimeout(context.Background(), cfg.DatabaseTimeout)
	defer startupCancel()
	runStore, err := store.NewPostgres(startupCtx, store.PostgresOptions{
		URL:      cfg.DatabaseURL,
		MaxConns: cfg.DatabaseMaxConns,
		MinConns: cfg.DatabaseMinConns,
	})
	if err != nil {
		logger.Error("open PostgreSQL store", "error", err)
		os.Exit(2)
	}
	defer runStore.Close()
	keyCtx, keyCancel := context.WithTimeout(runCtx, cfg.DatabaseTimeout)
	master, _, err := securestore.LoadMasterKey(cfg.DataDir, cfg.AccountMasterKey, func() (bool, error) { return runStore.HasEncryptedSecrets(keyCtx) })
	keyCancel()
	if err != nil {
		logger.Error("initialize persisted master key", "error", err)
		os.Exit(2)
	}
	cfg.AccountMasterKey = master
	vault, err := securestore.New(cfg.AccountMasterKey)
	if err != nil {
		logger.Error("initialize account credential vault", "error", err)
		os.Exit(2)
	}
	runtimeSettings, err := runStore.GetSystemSettings(startupCtx)
	if errors.Is(err, store.ErrNotFound) {
		runtimeSettings, err = runStore.UpdateSystemSettings(startupCtx, platform.SystemSettings{
			MapAPIKeys:      []platform.MapAPIKey{},
			WorkerProcesses: cfg.AutomationWorkers, WorkerConcurrency: cfg.AutomationWorkerConcurrency,
		})
	}
	if err != nil {
		logger.Error("load persisted system settings", "error", err)
		os.Exit(2)
	}
	// One-time migration from the former singular key columns. The legacy
	// columns remain populated for rollback compatibility, but all new runtime
	// and settings behavior uses MapAPIKeys.
	if len(runtimeSettings.MapAPIKeys) == 0 && len(runtimeSettings.MapAPIKeyCipher) > 0 {
		legacyPlain, decryptErr := vault.Decrypt(runtimeSettings.MapAPIKeyCipher, platform.SystemSettingsMapKeyAAD)
		if decryptErr != nil {
			logger.Error("decrypt legacy map API key", "error", decryptErr)
			os.Exit(2)
		}
		if len(legacyPlain) > 0 {
			qps := runtimeSettings.MapQPS
			if qps <= 0 || qps > 100 {
				qps = cfg.MapQPS
			}
			runtimeSettings.MapAPIKeys = []platform.MapAPIKey{{
				ID: "mapkey_primary", KeyCipher: runtimeSettings.MapAPIKeyCipher, QPS: qps,
			}}
			runtimeSettings, err = runStore.UpdateSystemSettings(startupCtx, runtimeSettings)
			if err != nil {
				logger.Error("migrate legacy map API key", "error", err)
				os.Exit(2)
			}
		}
	}
	runtimeMapKeys := make([]mapapi.APIKey, 0, len(runtimeSettings.MapAPIKeys))
	for _, storedKey := range runtimeSettings.MapAPIKeys {
		plain, decryptErr := vault.Decrypt(storedKey.KeyCipher, platform.SystemSettingsMapKeyAAD)
		if decryptErr != nil {
			logger.Error("decrypt persisted map API key", "id", storedKey.ID, "error", decryptErr)
			os.Exit(2)
		}
		runtimeMapKeys = append(runtimeMapKeys, mapapi.APIKey{ID: storedKey.ID, Key: string(plain), QPS: storedKey.QPS})
	}
	cfg.AutomationWorkers = runtimeSettings.WorkerProcesses
	cfg.AutomationWorkerConcurrency = runtimeSettings.WorkerConcurrency
	protocolClient, err := protocol.NewClient(protocol.Options{
		BaseURL: cfg.ProtocolBaseURL, WebBaseURL: cfg.ProtocolWebBaseURL,
		Environment: cfg.ProtocolEnvironment, Timeout: cfg.ProtocolTimeout,
	})
	if err != nil {
		logger.Error("initialize target protocol", "error", err)
		os.Exit(2)
	}
	if cfg.FaceEngineConfigured() {
		localFace, faceErr := faceengine.New(faceengine.Options{
			LibraryPath: cfg.FaceEngineLibrary, ResourcePack: cfg.FaceModelPack,
			DetectPixelSize: cfg.FaceDetectPixelSize,
		})
		if faceErr != nil {
			cfg.FaceStatus = faceErr.Error()
			logger.Warn("local face engine is not ready", "error", faceErr)
		} else {
			defer localFace.Close()
			protocolClient.SetFaceAnalyzer(localFace)
			cfg.FaceReady = true
			cfg.FaceStatus = "随附模型与本地推理运行库已就绪"
			logger.Info("local face engine ready", "provider", localFace.Name(), "detect_pixel_size", cfg.FaceDetectPixelSize)
		}
	} else {
		cfg.FaceStatus = "未发现对应系统的随附模型或推理运行库，请检查完整发行包"
		logger.Warn("local face engine is not configured; faceModelType=2 checks will report an initialization error")
	}
	mapClient := mapapi.NewClient(mapapi.Options{
		BaseURL:         cfg.MapBaseURL,
		Path:            cfg.MapPath,
		Keys:            runtimeMapKeys,
		Timeout:         cfg.MapTimeout,
		Burst:           cfg.MapBurst,
		MaxRetries:      cfg.MapMaxRetries,
		CacheTTL:        cfg.MapCacheTTL,
		CacheSize:       cfg.MapCacheSize,
		PersistentCache: runStore,
	})
	planner := route.NewPlanner(mapClient, cfg.MapRouteCandidates)
	scopedStore := store.TenantStore{Store: runStore}
	portalCtx, portalCancel := context.WithTimeout(runCtx, cfg.DatabaseTimeout)
	portalService, err := portal.New(portalCtx, runStore.Pool(), vault, scopedStore)
	portalCancel()
	if err != nil {
		logger.Error("initialize customer portal", "error", err)
		os.Exit(2)
	}
	runService := runner.New(scopedStore, vault, protocolClient, planner)
	var deviceID [16]byte
	if _, err := rand.Read(deviceID[:]); err != nil {
		logger.Error("initialize version checker identity", "error", err)
		os.Exit(2)
	}
	versionCtx, versionCancel := context.WithTimeout(runCtx, cfg.DatabaseTimeout)
	versionGuard, err := safety.New(versionCtx, runStore.Pool(), protocolClient,
		fmt.Sprintf("%s:%d", cfg.ProtocolBaseURL, cfg.ProtocolEnvironment),
		protocol.Device{ID: hex.EncodeToString(deviceID[:]), ModelName: "Redmi|23013RK75C", SystemVersion: "14"})
	versionCancel()
	if err != nil {
		logger.Error("initialize client version guard", "error", err)
		os.Exit(2)
	}
	runService.SetExecutionGuard(versionGuard)
	portalService.ClientVersions = versionGuard
	// Perform the startup probe before opening execution paths, even when a
	// still-fresh persisted result exists. Errors remain visible in the guard.
	_ = versionGuard.Refresh(runCtx, true)
	go versionGuard.Run(runCtx)
	portalService.Faces = runService
	scheduler := automation.New(scopedStore, runService, automation.Options{
		Billing: portalService, Guard: versionGuard,
		Workers: cfg.AutomationWorkers, WorkerConcurrency: cfg.AutomationWorkerConcurrency, PollInterval: cfg.AutomationPoll,
		RunTimeout: cfg.AutomationRunTimeout, Logger: logger,
	})
	updateCtx, updateCancel := context.WithTimeout(runCtx, cfg.DatabaseTimeout)
	platformUpdates, err := selfupdate.New(updateCtx, selfupdate.Options{
		FaceReady: cfg.FaceReady,
		DB:        runStore.Pool(), Guard: portalService,
		Busy:     func() bool { v := scheduler.Stats(); return v.Active > 0 || v.QueueDepth > 0 },
		Shutdown: func() { go func() { time.Sleep(time.Second); stop() }() },
	})
	updateCancel()
	if err != nil {
		logger.Error("initialize platform updater", "error", err)
		os.Exit(2)
	}
	portalService.PlatformUpdates = platformUpdates
	go portalService.Run(runCtx)
	go platformUpdates.Run(runCtx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ready, err := portalService.Initialized(runCtx); err == nil && ready {
				scheduler.Start(runCtx)
				return
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	handler := httpapi.New(httpapi.Dependencies{
		Portal:     portalService,
		Planner:    planner,
		Store:      scopedStore,
		Logger:     logger,
		Config:     cfg,
		MapRuntime: mapClient,
		Automation: scheduler,
		Runner:     runService,
		Vault:      vault,
	})
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("server started", "addr", cfg.HTTPAddr, "map_api_configured", mapClient.Configured(), "map_api_keys", len(runtimeMapKeys))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	<-runCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown", "error", err)
	}
}
