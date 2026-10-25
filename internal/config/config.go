package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr                    string
	DataDir                     string
	DatabaseURL                 string
	DatabaseMaxConns            int32
	DatabaseMinConns            int32
	DatabaseTimeout             time.Duration
	MapBaseURL                  string
	MapPath                     string
	MapTimeout                  time.Duration
	MapQPS                      float64
	MapBurst                    int
	MapMaxRetries               int
	MapCacheTTL                 time.Duration
	MapCacheSize                int
	MapRouteCandidates          int
	AutomationWorkers           int
	AutomationWorkerConcurrency int
	AutomationPoll              time.Duration
	AutomationRunTimeout        time.Duration
	AccountMasterKey            string
	ProtocolBaseURL             string
	ProtocolWebBaseURL          string
	ProtocolTimeout             time.Duration
	ProtocolEnvironment         int
	FaceEngineLibrary           string
	FaceModelPack               string
	FaceDetectPixelSize         int
	FaceReady                   bool
	FaceStatus                  string
}

func Load() (Config, error) {
	timeout, err := time.ParseDuration(env("MAP_API_TIMEOUT", "8s"))
	if err != nil || timeout <= 0 {
		return Config{}, fmt.Errorf("MAP_API_TIMEOUT must be a positive duration")
	}
	candidates, err := strconv.Atoi(env("MAP_ROUTE_CANDIDATES", "4"))
	if err != nil || candidates < 1 || candidates > 8 {
		return Config{}, fmt.Errorf("MAP_ROUTE_CANDIDATES must be between 1 and 8")
	}
	mapBurst, err := intEnv("MAP_API_BURST", 2, 1, 20)
	if err != nil {
		return Config{}, err
	}
	mapRetries, err := intEnv("MAP_API_MAX_RETRIES", 2, 0, 5)
	if err != nil {
		return Config{}, err
	}
	mapCacheSize, err := intEnv("MAP_API_CACHE_SIZE", 5000, 0, 100000)
	if err != nil {
		return Config{}, err
	}
	mapQPS, err := strconv.ParseFloat(env("MAP_API_QPS", "3"), 64)
	if err != nil || mapQPS <= 0 || mapQPS > 100 {
		return Config{}, fmt.Errorf("MAP_API_QPS must be greater than 0 and no more than 100")
	}
	mapCacheTTL, err := time.ParseDuration(env("MAP_API_CACHE_TTL", "24h"))
	if err != nil || mapCacheTTL < 0 {
		return Config{}, fmt.Errorf("MAP_API_CACHE_TTL must be a non-negative duration")
	}
	databaseMaxConns, err := intEnv("DATABASE_MAX_CONNS", 12, 1, 100)
	if err != nil {
		return Config{}, err
	}
	databaseMinConns, err := intEnv("DATABASE_MIN_CONNS", 2, 0, databaseMaxConns)
	if err != nil {
		return Config{}, err
	}
	databaseTimeout, err := time.ParseDuration(env("DATABASE_CONNECT_TIMEOUT", "5s"))
	if err != nil || databaseTimeout <= 0 {
		return Config{}, fmt.Errorf("DATABASE_CONNECT_TIMEOUT must be a positive duration")
	}
	automationWorkers, err := intEnv("AUTOMATION_WORKERS", 2, 1, 16)
	if err != nil {
		return Config{}, err
	}
	automationWorkerConcurrency, err := intEnv("AUTOMATION_WORKER_CONCURRENCY", 1, 1, 32)
	if err != nil {
		return Config{}, err
	}
	automationPoll, err := time.ParseDuration(env("AUTOMATION_POLL_INTERVAL", "10s"))
	if err != nil || automationPoll < time.Second {
		return Config{}, fmt.Errorf("AUTOMATION_POLL_INTERVAL must be at least 1s")
	}
	automationRunTimeout, err := time.ParseDuration(env("AUTOMATION_RUN_TIMEOUT", "90m"))
	if err != nil || automationRunTimeout < 10*time.Second {
		return Config{}, fmt.Errorf("AUTOMATION_RUN_TIMEOUT must be at least 10s")
	}
	protocolTimeout, err := time.ParseDuration(env("PROTOCOL_TIMEOUT", "20s"))
	if err != nil || protocolTimeout < time.Second {
		return Config{}, fmt.Errorf("PROTOCOL_TIMEOUT must be at least 1s")
	}
	protocolEnvironment, err := intEnv("PROTOCOL_ENVIRONMENT", 9, 1, 11)
	if err != nil {
		return Config{}, err
	}
	faceDetectPixelSize, err := intEnv("FACE_DETECT_PIXEL_SIZE", 320, 160, 640)
	if err != nil {
		return Config{}, err
	}
	if faceDetectPixelSize != 160 && faceDetectPixelSize != 320 && faceDetectPixelSize != 640 {
		return Config{}, fmt.Errorf("FACE_DETECT_PIXEL_SIZE must be 160, 320, or 640")
	}

	cfg := Config{
		HTTPAddr:                    env("HTTP_ADDR", ":8080"),
		DataDir:                     env("EASYRUN_DATA_DIR", "data"),
		DatabaseURL:                 strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DatabaseMaxConns:            int32(databaseMaxConns),
		DatabaseMinConns:            int32(databaseMinConns),
		DatabaseTimeout:             databaseTimeout,
		MapBaseURL:                  strings.TrimRight(env("MAP_API_BASE_URL", "https://restapi.amap.com"), "/"),
		MapPath:                     env("MAP_API_PATH", "/v3/direction/walking"),
		MapTimeout:                  timeout,
		MapQPS:                      mapQPS,
		MapBurst:                    mapBurst,
		MapMaxRetries:               mapRetries,
		MapCacheTTL:                 mapCacheTTL,
		MapCacheSize:                mapCacheSize,
		MapRouteCandidates:          candidates,
		AutomationWorkers:           automationWorkers,
		AutomationWorkerConcurrency: automationWorkerConcurrency,
		AutomationPoll:              automationPoll,
		AutomationRunTimeout:        automationRunTimeout,
		AccountMasterKey:            strings.TrimSpace(os.Getenv("ACCOUNT_MASTER_KEY")),
		ProtocolBaseURL:             strings.TrimRight(env("PROTOCOL_BASE_URL", "https://api.huachenjie.com/run-front"), "/"),
		ProtocolWebBaseURL:          strings.TrimRight(env("PROTOCOL_WEB_BASE_URL", "https://mh5.huachenjie.com"), "/"),
		ProtocolTimeout:             protocolTimeout,
		ProtocolEnvironment:         protocolEnvironment,
		FaceEngineLibrary:           strings.TrimSpace(os.Getenv("FACE_ENGINE_LIBRARY_PATH")),
		FaceModelPack:               strings.TrimSpace(os.Getenv("FACE_MODEL_PACK_PATH")),
		FaceDetectPixelSize:         faceDetectPixelSize,
	}
	cfg.discoverFaceAssets()
	return cfg, nil
}

func (c Config) MapConfigured() bool {
	// Keys live exclusively in PostgreSQL system settings, not the environment.
	return false
}

func (c *Config) discoverFaceAssets() {
	roots := []string{"models"}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(exe), "models"), filepath.Join(filepath.Dir(exe), "..", "models"))
	}
	for _, root := range roots {
		if c.FaceModelPack == "" {
			p := filepath.Join(root, "Megatron")
			if regularFile(p) {
				c.FaceModelPack = p
			}
		}
		if c.FaceEngineLibrary == "" {
			name := "libInspireFace.so"
			if runtime.GOOS == "windows" {
				name = "libInspireFace.dll"
			}
			p := filepath.Join(root, runtime.GOOS+"-"+runtime.GOARCH, name)
			if regularFile(p) {
				c.FaceEngineLibrary = p
			}
		}
	}
}
func regularFile(p string) bool { v, e := os.Stat(p); return e == nil && v.Mode().IsRegular() }

func (c Config) DatabaseConfigured() bool {
	return c.DatabaseURL != "" && c.DatabaseURL != "POSTGRES_DSN"
}

func (c Config) ProtocolConfigured() bool {
	return c.ProtocolBaseURL != "" && c.AccountMasterKey != ""
}

func (c Config) FaceEngineConfigured() bool {
	return c.FaceEngineLibrary != "" && c.FaceModelPack != ""
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}
