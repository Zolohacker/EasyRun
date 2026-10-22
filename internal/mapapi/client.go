package mapapi

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"golang.org/x/sync/singleflight"
)

var ErrNotConfigured = errors.New("map API is not configured")

type Leg struct {
	DistanceM float64
	DurationS float64
	Points    []geo.Point
}

type Walker interface {
	Walking(context.Context, geo.Point, geo.Point) (Leg, error)
}

// PersistentLeg is the database representation of a walking leg. ExpiresAt is
// returned with the geometry so an L2 hit cannot accidentally extend the
// original TTL when it is promoted into the in-memory L1 cache.
type PersistentLeg struct {
	Leg       Leg
	ExpiresAt time.Time
}

// PersistentCache is an optional L2 cache. Implementations should treat a
// missing or expired key as a normal miss and keep pruning best-effort so a
// temporary database issue never prevents a route from being planned.
type PersistentCache interface {
	GetMapLeg(context.Context, string, time.Time) (PersistentLeg, bool, error)
	PutMapLeg(context.Context, string, Leg, time.Time) error
	PruneMapLegCache(context.Context, time.Time, int) error
}

// APIKey is one member of the provider credential pool. QPS is enforced by an
// independent token bucket for this key.
type APIKey struct {
	ID  string
	Key string
	QPS float64
}

type Options struct {
	BaseURL string
	Path    string
	Keys    []APIKey
	// Key and QPS are retained as a single-key compatibility input.
	Key             string
	Timeout         time.Duration
	QPS             float64
	Burst           int
	MaxRetries      int
	CacheTTL        time.Duration
	CacheSize       int
	PersistentCache PersistentCache
}

type APIKeyStats struct {
	ID         string  `json:"id"`
	QPS        float64 `json:"qps"`
	Dispatched uint64  `json:"dispatched"`
}

type Stats struct {
	Requests            uint64        `json:"requests"`
	CacheHits           uint64        `json:"cache_hits"`
	CacheMisses         uint64        `json:"cache_misses"`
	Retries             uint64        `json:"retries"`
	RateLimited         uint64        `json:"rate_limited"`
	ProviderErrors      uint64        `json:"provider_errors"`
	Inflight            int64         `json:"inflight"`
	Waiting             int64         `json:"waiting"`
	FairnessKeys        int           `json:"fairness_keys"`
	Dispatched          uint64        `json:"dispatched"`
	QPS                 float64       `json:"qps"`
	Burst               int           `json:"burst"`
	BurstPerKey         int           `json:"burst_per_key"`
	KeyCount            int           `json:"key_count"`
	Keys                []APIKeyStats `json:"keys"`
	CacheEntries        int           `json:"cache_entries"`
	CacheCapacity       int           `json:"cache_capacity"`
	CacheTTLSeconds     float64       `json:"cache_ttl_seconds"`
	MemoryCacheHits     uint64        `json:"memory_cache_hits"`
	PersistentCacheHits uint64        `json:"persistent_cache_hits"`
	PersistentWrites    uint64        `json:"persistent_cache_writes"`
	PersistentErrors    uint64        `json:"persistent_cache_errors"`
	PersistentEnabled   bool          `json:"persistent_cache_enabled"`
}

type StatsProvider interface{ Stats() Stats }

type Client struct {
	configMu         sync.RWMutex
	baseURL          string
	path             string
	http             *http.Client
	gate             *fairGate
	maxRetries       int
	cache            *legCache
	cacheTTL         time.Duration
	cacheSize        int
	persistentCache  PersistentCache
	cacheNamespace   string
	pruneMu          sync.Mutex
	lastPrune        time.Time
	requestGroup     singleflight.Group
	requests         atomic.Uint64
	cacheHits        atomic.Uint64
	memoryCacheHits  atomic.Uint64
	persistentHits   atomic.Uint64
	persistentWrites atomic.Uint64
	persistentErrors atomic.Uint64
	cacheMisses      atomic.Uint64
	retries          atomic.Uint64
	rateLimited      atomic.Uint64
	providerErrors   atomic.Uint64
}

func NewClient(options Options) *Client {
	if options.Timeout <= 0 {
		options.Timeout = 8 * time.Second
	}
	if options.Path == "" {
		options.Path = "/v3/direction/walking"
	}
	if options.QPS <= 0 {
		options.QPS = 3
	}
	if options.Burst <= 0 {
		options.Burst = 2
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	keys := options.Keys
	if len(keys) == 0 && strings.TrimSpace(options.Key) != "" {
		keys = []APIKey{{ID: "primary", Key: options.Key, QPS: options.QPS}}
	}
	keys, _ = validateAPIKeys(keys)
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	path := "/" + strings.TrimLeft(options.Path, "/")
	namespaceHash := sha256.Sum256([]byte(baseURL + "|" + path))
	return &Client{
		baseURL:         baseURL,
		path:            path,
		http:            &http.Client{Timeout: options.Timeout, Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second}},
		gate:            newFairGate(keys, options.Burst),
		maxRetries:      options.MaxRetries,
		cache:           newLegCache(options.CacheSize),
		cacheTTL:        options.CacheTTL,
		cacheSize:       options.CacheSize,
		persistentCache: options.PersistentCache,
		cacheNamespace:  fmt.Sprintf("%x", namespaceHash[:8]),
	}
}

func (c *Client) Stats() Stats {
	gate := c.gate.snapshot()
	keyStats := make([]APIKeyStats, 0, len(gate.Keys))
	for _, key := range gate.Keys {
		keyStats = append(keyStats, APIKeyStats{ID: key.ID, QPS: key.QPS, Dispatched: key.Dispatched})
	}
	return Stats{
		Requests: c.requests.Load(), CacheHits: c.cacheHits.Load(), CacheMisses: c.cacheMisses.Load(),
		Retries: c.retries.Load(), RateLimited: c.rateLimited.Load(), ProviderErrors: c.providerErrors.Load(),
		Inflight: gate.Inflight, Waiting: gate.Waiting, FairnessKeys: gate.FairnessKeys,
		Dispatched: gate.Dispatched, QPS: gate.QPS, Burst: gate.Burst,
		BurstPerKey: gate.BurstPerKey, KeyCount: len(keyStats), Keys: keyStats,
		CacheEntries: c.cache.Len(), CacheCapacity: c.cacheSize, CacheTTLSeconds: c.cacheTTL.Seconds(),
		MemoryCacheHits: c.memoryCacheHits.Load(), PersistentCacheHits: c.persistentHits.Load(),
		PersistentWrites: c.persistentWrites.Load(), PersistentErrors: c.persistentErrors.Load(),
		PersistentEnabled: c.persistentCache != nil,
	}
}

func (c *Client) Configured() bool {
	c.configMu.RLock()
	baseConfigured := c.baseURL != "" && c.baseURL != "APP_URL"
	c.configMu.RUnlock()
	return baseConfigured && c.gate.configured()
}

// UpdateRuntime applies database-backed map credentials and rate limits
// without dropping the in-memory or PostgreSQL route caches.
func (c *Client) UpdateRuntime(keys []APIKey) error {
	normalized, err := validateAPIKeys(keys)
	if err != nil {
		return err
	}
	c.gate.updateKeys(normalized)
	return nil
}

func validateAPIKeys(keys []APIKey) ([]APIKey, error) {
	if len(keys) > 32 {
		return nil, errors.New("no more than 32 map API keys are allowed")
	}
	normalized := make([]APIKey, 0, len(keys))
	ids := make(map[string]struct{}, len(keys))
	secrets := make(map[string]struct{}, len(keys))
	for index, key := range keys {
		key.ID = strings.TrimSpace(key.ID)
		key.Key = strings.TrimSpace(key.Key)
		if key.ID == "" {
			key.ID = fmt.Sprintf("key-%d", index+1)
		}
		if key.Key == "" {
			return nil, errors.New("map API key must not be empty")
		}
		if key.QPS <= 0 || key.QPS > 100 {
			return nil, errors.New("each map API key qps must be greater than 0 and no more than 100")
		}
		if _, exists := ids[key.ID]; exists {
			return nil, errors.New("map API key IDs must be unique")
		}
		if _, exists := secrets[key.Key]; exists {
			return nil, errors.New("map API keys must be unique")
		}
		ids[key.ID] = struct{}{}
		secrets[key.Key] = struct{}{}
		normalized = append(normalized, key)
	}
	return normalized, nil
}

func (c *Client) Walking(ctx context.Context, origin, destination geo.Point) (Leg, error) {
	if !c.Configured() {
		return Leg{}, ErrNotConfigured
	}
	if !origin.Valid() || !destination.Valid() {
		return Leg{}, errors.New("origin or destination is invalid")
	}
	cacheKey := c.cacheNamespace + "|" + formatPoint(origin) + ">" + formatPoint(destination)
	if leg, ok := c.lookupCachedLeg(ctx, cacheKey, time.Now()); ok {
		return leg, nil
	}
	c.cacheMisses.Add(1)
	resultChannel := c.requestGroup.DoChan(cacheKey, func() (any, error) {
		if leg, ok := c.cache.Get(cacheKey, time.Now()); ok {
			c.cacheHits.Add(1)
			c.memoryCacheHits.Add(1)
			return leg, nil
		}
		leg, err := c.walkingWithRetry(ctx, origin, destination)
		if err == nil && c.cacheTTL > 0 && c.cacheSize > 0 {
			expiresAt := time.Now().Add(c.cacheTTL)
			c.cache.Put(cacheKey, leg, expiresAt)
			if c.persistentCache != nil {
				if persistErr := c.persistentCache.PutMapLeg(ctx, cacheKey, leg, expiresAt); persistErr != nil {
					c.persistentErrors.Add(1)
				} else {
					c.persistentWrites.Add(1)
					c.prunePersistentCache(ctx)
				}
			}
		}
		return leg, err
	})
	select {
	case <-ctx.Done():
		return Leg{}, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return Leg{}, result.Err
		}
		return result.Val.(Leg), nil
	}
}

func (c *Client) lookupCachedLeg(ctx context.Context, key string, now time.Time) (Leg, bool) {
	if leg, ok := c.cache.Get(key, now); ok {
		c.cacheHits.Add(1)
		c.memoryCacheHits.Add(1)
		return leg, true
	}
	if c.persistentCache == nil || c.cacheTTL <= 0 || c.cacheSize <= 0 {
		return Leg{}, false
	}
	record, ok, err := c.persistentCache.GetMapLeg(ctx, key, now)
	if err != nil {
		c.persistentErrors.Add(1)
		return Leg{}, false
	}
	if !ok {
		return Leg{}, false
	}
	c.cache.Put(key, record.Leg, record.ExpiresAt)
	c.cacheHits.Add(1)
	c.persistentHits.Add(1)
	return record.Leg, true
}

func (c *Client) prunePersistentCache(ctx context.Context) {
	c.pruneMu.Lock()
	defer c.pruneMu.Unlock()
	if time.Since(c.lastPrune) < time.Minute {
		return
	}
	if err := c.persistentCache.PruneMapLegCache(ctx, time.Now(), c.cacheSize); err != nil {
		c.persistentErrors.Add(1)
		return
	}
	c.lastPrune = time.Now()
}

func (c *Client) walkingWithRetry(ctx context.Context, origin, destination geo.Point) (Leg, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		apiKey, release, waitDuration, err := c.gate.acquire(ctx, fairnessKeyFromContext(ctx))
		if err != nil {
			return Leg{}, err
		}
		if waitDuration > 20*time.Millisecond {
			c.rateLimited.Add(1)
		}
		leg, retryAfter, retryable, err := c.walkingOnce(ctx, apiKey, origin, destination)
		release()
		if err == nil {
			return leg, nil
		}
		lastErr = err
		c.providerErrors.Add(1)
		if !retryable || attempt == c.maxRetries {
			break
		}
		c.retries.Add(1)
		if retryAfter <= 0 {
			retryAfter = time.Duration(250*(1<<attempt)+(attempt+1)*73) * time.Millisecond
		}
		timer := time.NewTimer(retryAfter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Leg{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Leg{}, lastErr
}

func (c *Client) walkingOnce(ctx context.Context, apiKey string, origin, destination geo.Point) (Leg, time.Duration, bool, error) {
	c.configMu.RLock()
	baseURL, endpointPath := c.baseURL, c.path
	c.configMu.RUnlock()
	endpoint, err := url.Parse(baseURL + endpointPath)
	if err != nil {
		return Leg{}, 0, false, fmt.Errorf("parse map API URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("origin", formatPoint(origin))
	query.Set("destination", formatPoint(destination))
	query.Set("key", apiKey)
	query.Set("output", "json")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Leg{}, 0, false, fmt.Errorf("build map request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.requests.Add(1)
	resp, err := c.http.Do(req)
	if err != nil {
		return Leg{}, 0, true, fmt.Errorf("map request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return Leg{}, parseRetryAfter(resp.Header.Get("Retry-After")), retryable, fmt.Errorf("map API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload walkResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return Leg{}, 0, true, fmt.Errorf("decode map response: %w", err)
	}
	if payload.Status != "1" || len(payload.Route.Paths) == 0 {
		message := strings.TrimSpace(payload.Info)
		if message == "" {
			message = "no route returned"
		}
		upper := strings.ToUpper(message + " " + payload.InfoCode)
		retryable := strings.Contains(upper, "LIMIT") || strings.Contains(upper, "BUSY") || strings.Contains(upper, "TIMEOUT") || strings.HasPrefix(payload.InfoCode, "3")
		return Leg{}, 0, retryable, fmt.Errorf("map API error %s: %s", payload.InfoCode, message)
	}
	path := payload.Route.Paths[0]
	points := make([]geo.Point, 0, len(path.Steps)*4+2)
	points = append(points, origin)
	for _, step := range path.Steps {
		for _, raw := range strings.Split(step.Polyline, ";") {
			if point, ok := parsePoint(raw); ok {
				points = append(points, point)
			}
		}
	}
	points = append(points, destination)
	points = geo.Deduplicate(points, 0.15)
	if len(points) < 2 {
		return Leg{}, 0, false, errors.New("map API returned an empty polyline")
	}
	distance, _ := strconv.ParseFloat(path.Distance, 64)
	if distance <= 0 {
		distance = geo.PolylineDistance(points)
	}
	duration, _ := strconv.ParseFloat(path.Duration, 64)
	return Leg{DistanceM: distance, DurationS: duration, Points: points}, 0, false, nil
}

type walkResponse struct {
	Status   string `json:"status"`
	Info     string `json:"info"`
	InfoCode string `json:"infocode"`
	Route    struct {
		Paths []struct {
			Distance string `json:"distance"`
			Duration string `json:"duration"`
			Steps    []struct {
				Polyline string `json:"polyline"`
			} `json:"steps"`
		} `json:"paths"`
	} `json:"route"`
}

type cacheItem struct {
	key       string
	leg       Leg
	expiresAt time.Time
}
type legCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List
}

func newLegCache(capacity int) *legCache {
	return &legCache{capacity: capacity, items: make(map[string]*list.Element), order: list.New()}
}
func (c *legCache) Get(key string, now time.Time) (Leg, bool) {
	if c.capacity <= 0 {
		return Leg{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return Leg{}, false
	}
	item := element.Value.(*cacheItem)
	if !item.expiresAt.After(now) {
		c.order.Remove(element)
		delete(c.items, key)
		return Leg{}, false
	}
	c.order.MoveToFront(element)
	return cloneLeg(item.leg), true
}
func (c *legCache) Put(key string, leg Leg, expiresAt time.Time) {
	if c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		item := element.Value.(*cacheItem)
		item.leg, item.expiresAt = cloneLeg(leg), expiresAt
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(&cacheItem{key: key, leg: cloneLeg(leg), expiresAt: expiresAt})
	c.items[key] = element
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		delete(c.items, oldest.Value.(*cacheItem).key)
		c.order.Remove(oldest)
	}
}
func (c *legCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
func cloneLeg(leg Leg) Leg { leg.Points = append([]geo.Point(nil), leg.Points...); return leg }

func formatPoint(point geo.Point) string {
	return strconv.FormatFloat(point.Lng, 'f', 6, 64) + "," + strconv.FormatFloat(point.Lat, 'f', 6, 64)
}
func parsePoint(raw string) (geo.Point, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) != 2 {
		return geo.Point{}, false
	}
	lng, errLng := strconv.ParseFloat(parts[0], 64)
	lat, errLat := strconv.ParseFloat(parts[1], 64)
	point := geo.Point{Lat: lat, Lng: lng}
	return point, errLng == nil && errLat == nil && point.Valid()
}
func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return time.Until(when)
	}
	return 0
}
