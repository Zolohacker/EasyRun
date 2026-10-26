package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zolohacker/EasyRun/internal/store"
)

// MapAPIConfigured exposes capability, never the encrypted credentials. Task
// writes read persisted settings afresh rather than trusting browser state.
func MapAPIConfigured(ctx context.Context, database store.Store) (bool, error) {
	settings, err := database.GetSystemSettings(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取地图配置: %w", err)
	}
	for _, key := range settings.MapAPIKeys {
		if len(key.KeyCipher) > 0 {
			return true, nil
		}
	}
	return len(settings.MapAPIKeys) == 0 && len(settings.MapAPIKeyCipher) > 0, nil
}
