package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) GetSystemSettings(ctx context.Context) (platform.SystemSettings, error) {
	var value platform.SystemSettings
	var keysJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT map_api_key_cipher, map_qps, map_api_keys, worker_processes, worker_concurrency, updated_at
FROM system_settings WHERE singleton_id=1`).Scan(
		&value.MapAPIKeyCipher, &value.MapQPS, &keysJSON, &value.WorkerProcesses,
		&value.WorkerConcurrency, &value.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.SystemSettings{}, ErrNotFound
	}
	if err != nil {
		return platform.SystemSettings{}, fmt.Errorf("select system settings: %w", err)
	}
	if err := json.Unmarshal(keysJSON, &value.MapAPIKeys); err != nil {
		return platform.SystemSettings{}, fmt.Errorf("decode map API keys: %w", err)
	}
	return value, nil
}

func (s *PostgresStore) UpdateSystemSettings(ctx context.Context, value platform.SystemSettings) (platform.SystemSettings, error) {
	if len(value.MapAPIKeys) > 32 {
		return platform.SystemSettings{}, errors.New("no more than 32 map API keys are allowed")
	}
	seen := make(map[string]struct{}, len(value.MapAPIKeys))
	for _, key := range value.MapAPIKeys {
		if key.ID == "" {
			return platform.SystemSettings{}, errors.New("map API key ID is required")
		}
		if _, exists := seen[key.ID]; exists {
			return platform.SystemSettings{}, errors.New("map API key IDs must be unique")
		}
		seen[key.ID] = struct{}{}
		if len(key.KeyCipher) == 0 {
			return platform.SystemSettings{}, errors.New("map API key ciphertext is required")
		}
		if key.QPS <= 0 || key.QPS > 100 {
			return platform.SystemSettings{}, errors.New("map API key qps must be greater than 0 and no more than 100")
		}
	}
	if value.WorkerProcesses < 1 || value.WorkerProcesses > 16 {
		return platform.SystemSettings{}, errors.New("worker_processes must be between 1 and 16")
	}
	if value.WorkerConcurrency < 1 || value.WorkerConcurrency > 32 {
		return platform.SystemSettings{}, errors.New("worker_concurrency must be between 1 and 32")
	}
	value.MapAPIKeyCipher = nil
	value.MapQPS = 3
	if len(value.MapAPIKeys) > 0 {
		value.MapAPIKeyCipher = value.MapAPIKeys[0].KeyCipher
		value.MapQPS = value.MapAPIKeys[0].QPS
	}
	keysJSON, err := json.Marshal(value.MapAPIKeys)
	if err != nil {
		return platform.SystemSettings{}, fmt.Errorf("encode map API keys: %w", err)
	}
	var returnedKeysJSON []byte
	err = s.pool.QueryRow(ctx, `
INSERT INTO system_settings (
    singleton_id, map_api_key_cipher, map_qps, map_api_keys, worker_processes, worker_concurrency, updated_at
) VALUES (1,$1,$2,$3,$4,$5,now())
ON CONFLICT (singleton_id) DO UPDATE SET
    map_api_key_cipher=EXCLUDED.map_api_key_cipher,
    map_qps=EXCLUDED.map_qps,
    map_api_keys=EXCLUDED.map_api_keys,
    worker_processes=EXCLUDED.worker_processes,
    worker_concurrency=EXCLUDED.worker_concurrency,
    updated_at=now()
RETURNING map_api_key_cipher, map_qps, map_api_keys, worker_processes, worker_concurrency, updated_at`,
		value.MapAPIKeyCipher, value.MapQPS, keysJSON, value.WorkerProcesses, value.WorkerConcurrency,
	).Scan(
		&value.MapAPIKeyCipher, &value.MapQPS, &returnedKeysJSON, &value.WorkerProcesses,
		&value.WorkerConcurrency, &value.UpdatedAt,
	)
	if err != nil {
		return platform.SystemSettings{}, fmt.Errorf("upsert system settings: %w", err)
	}
	if err := json.Unmarshal(returnedKeysJSON, &value.MapAPIKeys); err != nil {
		return platform.SystemSettings{}, fmt.Errorf("decode updated map API keys: %w", err)
	}
	return value, nil
}
