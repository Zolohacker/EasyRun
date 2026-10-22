package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zolohacker/EasyRun/internal/mapapi"
	"github.com/jackc/pgx/v5"
)

// GetMapLeg reads one unexpired walking leg and updates its database LRU
// metadata atomically. Expired rows behave exactly like a cache miss.
func (s *PostgresStore) GetMapLeg(ctx context.Context, key string, now time.Time) (mapapi.PersistentLeg, bool, error) {
	var record mapapi.PersistentLeg
	var points []byte
	err := s.pool.QueryRow(ctx, `
UPDATE map_route_cache
SET hit_count = hit_count + 1, last_hit_at = $2
WHERE cache_key = $1 AND expires_at > $2
RETURNING distance_m, duration_s, points, expires_at`, key, now).Scan(
		&record.Leg.DistanceM, &record.Leg.DurationS, &points, &record.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mapapi.PersistentLeg{}, false, nil
	}
	if err != nil {
		return mapapi.PersistentLeg{}, false, fmt.Errorf("read map route cache: %w", err)
	}
	if err := json.Unmarshal(points, &record.Leg.Points); err != nil {
		return mapapi.PersistentLeg{}, false, fmt.Errorf("decode cached map route: %w", err)
	}
	if len(record.Leg.Points) < 2 {
		return mapapi.PersistentLeg{}, false, errors.New("cached map route has fewer than two points")
	}
	return record, true, nil
}

// PutMapLeg upserts a walking leg without extending its TTL on later reads.
func (s *PostgresStore) PutMapLeg(ctx context.Context, key string, leg mapapi.Leg, expiresAt time.Time) error {
	points, err := json.Marshal(leg.Points)
	if err != nil {
		return fmt.Errorf("encode map route cache: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO map_route_cache (
    cache_key, distance_m, duration_s, points, expires_at,
    hit_count, last_hit_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,0,now(),now(),now())
ON CONFLICT (cache_key) DO UPDATE SET
    distance_m = EXCLUDED.distance_m,
    duration_s = EXCLUDED.duration_s,
    points = EXCLUDED.points,
    expires_at = EXCLUDED.expires_at,
    updated_at = now()`, key, leg.DistanceM, leg.DurationS, points, expiresAt)
	if err != nil {
		return fmt.Errorf("write map route cache: %w", err)
	}
	return nil
}

// PruneMapLegCache removes expired rows first and then applies an LRU capacity
// limit. It is called at most once per minute by the map client.
func (s *PostgresStore) PruneMapLegCache(ctx context.Context, now time.Time, capacity int) error {
	if capacity < 0 {
		capacity = 0
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin map cache prune: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM map_route_cache WHERE expires_at <= $1`, now); err != nil {
		return fmt.Errorf("delete expired map cache rows: %w", err)
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM map_route_cache
WHERE cache_key IN (
    SELECT cache_key
    FROM map_route_cache
    ORDER BY last_hit_at DESC, updated_at DESC
    OFFSET $1
)`, capacity); err != nil {
		return fmt.Errorf("enforce map cache capacity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit map cache prune: %w", err)
	}
	return nil
}
