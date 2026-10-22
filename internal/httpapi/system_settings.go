package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/mapapi"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

type mapAPIKeyRequest struct {
	ID     string  `json:"id"`
	APIKey string  `json:"api_key"`
	QPS    float64 `json:"qps"`
}

type systemSettingsRequest struct {
	MapAPIKeys        []mapAPIKeyRequest `json:"map_api_keys"`
	WorkerProcesses   int                `json:"worker_processes"`
	WorkerConcurrency int                `json:"worker_concurrency"`
}

type mapAPIKeyResponse struct {
	ID     string  `json:"id"`
	Masked string  `json:"masked"`
	QPS    float64 `json:"qps"`
}

type systemSettingsResponse struct {
	MapAPIKeyConfigured bool                `json:"map_api_key_configured"`
	MapAPIKeys          []mapAPIKeyResponse `json:"map_api_keys"`
	TotalMapQPS         float64             `json:"total_map_qps"`
	WorkerProcesses     int                 `json:"worker_processes"`
	WorkerConcurrency   int                 `json:"worker_concurrency"`
	TotalConcurrency    int                 `json:"total_concurrency"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

func (a *api) getSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.store.GetSystemSettings(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	response, err := a.systemSettingsResponse(settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_decryption_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *api) updateSystemSettings(w http.ResponseWriter, r *http.Request) {
	var request systemSettingsRequest
	if err := decodeJSON(w, r, &request, 128<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(request.MapAPIKeys) > 32 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "高德地图 API Key 最多配置 32 个")
		return
	}
	if request.WorkerProcesses < 1 || request.WorkerProcesses > 16 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "Worker 进程数必须在 1 到 16 之间")
		return
	}
	if request.WorkerConcurrency < 1 || request.WorkerConcurrency > 32 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "每个 Worker 的并发数必须在 1 到 32 之间")
		return
	}
	if a.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "系统配置加密服务未初始化")
		return
	}
	current, err := a.store.GetSystemSettings(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	existing := make(map[string]platform.MapAPIKey, len(current.MapAPIKeys))
	for _, key := range current.MapAPIKeys {
		existing[key.ID] = key
	}

	persisted := make([]platform.MapAPIKey, 0, len(request.MapAPIKeys))
	runtimeKeys := make([]mapapi.APIKey, 0, len(request.MapAPIKeys))
	seenIDs := make(map[string]struct{}, len(request.MapAPIKeys))
	seenSecrets := make(map[string]struct{}, len(request.MapAPIKeys))
	for _, candidate := range request.MapAPIKeys {
		id := strings.TrimSpace(candidate.ID)
		secret := strings.TrimSpace(candidate.APIKey)
		if candidate.QPS <= 0 || candidate.QPS > 100 {
			writeError(w, http.StatusBadRequest, "invalid_settings", "每个高德地图 API Key 的 QPS 必须大于 0 且不超过 100")
			return
		}
		var ciphertext []byte
		if id == "" {
			if secret == "" {
				writeError(w, http.StatusBadRequest, "invalid_settings", "新增高德地图 API Key 时必须输入密钥")
				return
			}
			id = platform.NewID("mapkey")
		} else {
			stored, ok := existing[id]
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid_settings", "高德地图 API Key 标识不存在，请刷新后重试")
				return
			}
			ciphertext = stored.KeyCipher
			if secret == "" {
				plain, decryptErr := a.vault.Decrypt(ciphertext, platform.SystemSettingsMapKeyAAD)
				if decryptErr != nil {
					writeError(w, http.StatusInternalServerError, "settings_decryption_failed", decryptErr.Error())
					return
				}
				secret = string(plain)
			}
		}
		if len(secret) < 8 || len(secret) > 256 {
			writeError(w, http.StatusBadRequest, "invalid_settings", "高德地图 API Key 长度必须在 8 到 256 个字符之间")
			return
		}
		if _, duplicate := seenIDs[id]; duplicate {
			writeError(w, http.StatusBadRequest, "invalid_settings", "高德地图 API Key 标识重复")
			return
		}
		if _, duplicate := seenSecrets[secret]; duplicate {
			writeError(w, http.StatusBadRequest, "invalid_settings", "不能重复添加同一个高德地图 API Key")
			return
		}
		if strings.TrimSpace(candidate.APIKey) != "" || len(ciphertext) == 0 {
			ciphertext, err = a.vault.Encrypt([]byte(secret), platform.SystemSettingsMapKeyAAD)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "settings_encryption_failed", err.Error())
				return
			}
		}
		seenIDs[id] = struct{}{}
		seenSecrets[secret] = struct{}{}
		persisted = append(persisted, platform.MapAPIKey{ID: id, KeyCipher: ciphertext, QPS: candidate.QPS})
		runtimeKeys = append(runtimeKeys, mapapi.APIKey{ID: id, Key: secret, QPS: candidate.QPS})
	}

	updated, err := a.store.UpdateSystemSettings(r.Context(), platform.SystemSettings{
		MapAPIKeys: persisted, WorkerProcesses: request.WorkerProcesses,
		WorkerConcurrency: request.WorkerConcurrency,
	})
	if err != nil {
		a.storeError(w, err)
		return
	}
	if a.mapRuntime != nil {
		if err := a.mapRuntime.UpdateRuntime(runtimeKeys); err != nil {
			writeError(w, http.StatusInternalServerError, "map_reconfigure_failed", err.Error())
			return
		}
	}
	if a.automation != nil {
		if err := a.automation.Reconfigure(updated.WorkerProcesses, updated.WorkerConcurrency); err != nil {
			writeError(w, http.StatusInternalServerError, "automation_reconfigure_failed", err.Error())
			return
		}
	}
	response, err := a.systemSettingsResponse(updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_decryption_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *api) systemSettingsResponse(settings platform.SystemSettings) (systemSettingsResponse, error) {
	if a.vault == nil {
		return systemSettingsResponse{}, errors.New("系统配置加密服务未初始化")
	}
	keys := make([]mapAPIKeyResponse, 0, len(settings.MapAPIKeys))
	var totalQPS float64
	for _, stored := range settings.MapAPIKeys {
		plain, err := a.vault.Decrypt(stored.KeyCipher, platform.SystemSettingsMapKeyAAD)
		if err != nil {
			return systemSettingsResponse{}, err
		}
		keys = append(keys, mapAPIKeyResponse{ID: stored.ID, Masked: maskSecret(string(plain)), QPS: stored.QPS})
		totalQPS += stored.QPS
	}
	return systemSettingsResponse{
		MapAPIKeyConfigured: len(keys) > 0, MapAPIKeys: keys, TotalMapQPS: totalQPS,
		WorkerProcesses: settings.WorkerProcesses, WorkerConcurrency: settings.WorkerConcurrency,
		TotalConcurrency: settings.WorkerProcesses * settings.WorkerConcurrency, UpdatedAt: settings.UpdatedAt,
	}, nil
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	visible := 4
	if len(runes) <= visible {
		return strings.Repeat("•", len(runes))
	}
	return strings.Repeat("•", 8) + string(runes[len(runes)-visible:])
}
