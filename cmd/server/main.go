package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/config"
	"github.com/Zolohacker/EasyRun/internal/faceengine"
	"github.com/Zolohacker/EasyRun/internal/httpapi"
	"github.com/Zolohacker/EasyRun/internal/mapapi"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/route"
	"github.com/Zolohacker/EasyRun/internal/runner"
	"github.com/Zolohacker/EasyRun/internal/securestore"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func main() {
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	vault, err := securestore.New(cfg.AccountMasterKey)
	if err != nil {
		logger.Error("initialize account credential vault", "error", err)
		os.Exit(2)
	}
	runtimeSettings, err := runStore.GetSystemSettings(startupCtx)
	if errors.Is(err, store.ErrNotFound) {
		var initialKeys []platform.MapAPIKey
		if cfg.MapAPIKey != "" {
			mapKeyCipher, encryptErr := vault.Encrypt([]byte(cfg.MapAPIKey), platform.SystemSettingsMapKeyAAD)
			if encryptErr != nil {
				logger.Error("encrypt initial map API key", "error", encryptErr)
				os.Exit(2)
			}
			initialKeys = []platform.MapAPIKey{{ID: "mapkey_primary", KeyCipher: mapKeyCipher, QPS: cfg.MapQPS}}
		}
		runtimeSettings, err = runStore.UpdateSystemSettings(startupCtx, platform.SystemSettings{
			MapAPIKeys:      initialKeys,
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
			logger.Error("initialize local face engine", "error", faceErr)
			os.Exit(2)
		}
		defer localFace.Close()
		protocolClient.SetFaceAnalyzer(localFace)
		logger.Info("local face engine ready", "provider", localFace.Name(), "detect_pixel_size", cfg.FaceDetectPixelSize)
	} else {
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
	runService := runner.New(runStore, vault, protocolClient, planner)
	scheduler := automation.New(runStore, runService, automation.Options{
		Workers: cfg.AutomationWorkers, WorkerConcurrency: cfg.AutomationWorkerConcurrency, PollInterval: cfg.AutomationPoll,
		RunTimeout: cfg.AutomationRunTimeout, Logger: logger,
	})
	scheduler.Start(runCtx)

	handler := httpapi.New(httpapi.Dependencies{
		Planner:    planner,
		Store:      runStore,
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
