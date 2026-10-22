package mapapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

type testPersistentCache struct {
	mu      sync.Mutex
	records map[string]PersistentLeg
}

func (c *testPersistentCache) GetMapLeg(_ context.Context, key string, now time.Time) (PersistentLeg, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[key]
	if !ok || !record.ExpiresAt.After(now) {
		return PersistentLeg{}, false, nil
	}
	record.Leg = cloneLeg(record.Leg)
	return record, true, nil
}

func (c *testPersistentCache) PutMapLeg(_ context.Context, key string, leg Leg, expiresAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[key] = PersistentLeg{Leg: cloneLeg(leg), ExpiresAt: expiresAt}
	return nil
}

func (c *testPersistentCache) PruneMapLegCache(_ context.Context, now time.Time, _ int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, record := range c.records {
		if !record.ExpiresAt.After(now) {
			delete(c.records, key)
		}
	}
	return nil
}

func TestWalkingParsesPolyline(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("key") != "test-key" {
			t.Fatal("missing key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"1","info":"OK","infocode":"10000","route":{"paths":[{"distance":"120","duration":"90","steps":[{"polyline":"121.473700,31.230400;121.474200,31.230400;121.474900,31.230400"}]}]}}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, Path: "/walk", Key: "test-key", Timeout: time.Second, QPS: 100, Burst: 10, CacheTTL: time.Hour, CacheSize: 10})
	leg, err := client.Walking(context.Background(), geo.Point{Lat: 31.2304, Lng: 121.4737}, geo.Point{Lat: 31.2304, Lng: 121.4749})
	if err != nil {
		t.Fatal(err)
	}
	if leg.DistanceM != 120 || leg.DurationS != 90 {
		t.Fatalf("unexpected leg: %+v", leg)
	}
	if len(leg.Points) != 3 {
		t.Fatalf("points = %d", len(leg.Points))
	}
	if _, err := client.Walking(context.Background(), geo.Point{Lat: 31.2304, Lng: 121.4737}, geo.Point{Lat: 31.2304, Lng: 121.4749}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || client.Stats().CacheHits != 1 {
		t.Fatalf("requests=%d stats=%+v", requests.Load(), client.Stats())
	}
}

func TestWalkingRequiresConfiguration(t *testing.T) {
	client := NewClient(Options{Path: "/walk", Timeout: time.Second})
	_, err := client.Walking(context.Background(), geo.Point{}, geo.Point{})
	if err != ErrNotConfigured {
		t.Fatalf("error = %v", err)
	}
}

func TestClientDefaultQPSIsThree(t *testing.T) {
	client := NewClient(Options{Key: "test-key"})
	if got := client.Stats().QPS; got != 3 {
		t.Fatalf("default QPS=%v want=3", got)
	}
}

func TestWalkingRotatesAcrossIndependentlyLimitedKeys(t *testing.T) {
	seen := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.Query().Get("key")
		_, _ = w.Write([]byte(`{"status":"1","route":{"paths":[{"distance":"10","duration":"5","steps":[{"polyline":"121.473700,31.230400;121.473800,31.230400"}]}]}}`))
	}))
	defer server.Close()

	client := NewClient(Options{
		BaseURL: server.URL, Path: "/walk", Timeout: time.Second, Burst: 1,
		Keys: []APIKey{
			{ID: "key-a", Key: "secret-a", QPS: 100},
			{ID: "key-b", Key: "secret-b", QPS: 100},
		},
	})
	origin := geo.Point{Lat: 31.2304, Lng: 121.4737}
	if _, err := client.Walking(context.Background(), origin, geo.Point{Lat: 31.2304, Lng: 121.4738}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Walking(context.Background(), origin, geo.Point{Lat: 31.2304, Lng: 121.4739}); err != nil {
		t.Fatal(err)
	}
	first, second := <-seen, <-seen
	if first == second || (first != "secret-a" && first != "secret-b") || (second != "secret-a" && second != "secret-b") {
		t.Fatalf("unexpected key rotation: %q then %q", first, second)
	}
	stats := client.Stats()
	if stats.KeyCount != 2 || stats.QPS != 200 || len(stats.Keys) != 2 {
		t.Fatalf("unexpected key-pool stats: %+v", stats)
	}
}

func TestUpdateRuntimeReplacesKeyPool(t *testing.T) {
	client := NewClient(Options{BaseURL: "https://example.invalid", Key: "old-secret", QPS: 2})
	if err := client.UpdateRuntime([]APIKey{{ID: "new", Key: "new-secret", QPS: 7}}); err != nil {
		t.Fatal(err)
	}
	stats := client.Stats()
	if !client.Configured() || stats.KeyCount != 1 || stats.QPS != 7 || stats.Keys[0].ID != "new" {
		t.Fatalf("runtime key pool was not replaced: %+v", stats)
	}
	if err := client.UpdateRuntime(nil); err != nil {
		t.Fatal(err)
	}
	if client.Configured() {
		t.Fatal("client remained configured after the key pool was cleared")
	}
}

func TestWalkingPromotesPersistentHitAfterClientRestart(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"status":"1","route":{"paths":[{"distance":"120","duration":"90","steps":[{"polyline":"121.473700,31.230400;121.474900,31.230400"}]}]}}`))
	}))
	defer server.Close()

	persistent := &testPersistentCache{records: make(map[string]PersistentLeg)}
	options := Options{
		BaseURL: server.URL, Path: "/walk", Key: "key", Timeout: time.Second,
		QPS: 100, Burst: 10, CacheTTL: time.Hour, CacheSize: 10,
		PersistentCache: persistent,
	}
	origin := geo.Point{Lat: 31.2304, Lng: 121.4737}
	destination := geo.Point{Lat: 31.2304, Lng: 121.4749}
	firstClient := NewClient(options)
	if _, err := firstClient.Walking(context.Background(), origin, destination); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || firstClient.Stats().PersistentWrites != 1 {
		t.Fatalf("first request=%d stats=%+v", requests.Load(), firstClient.Stats())
	}

	secondClient := NewClient(options)
	if _, err := secondClient.Walking(context.Background(), origin, destination); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || secondClient.Stats().PersistentCacheHits != 1 {
		t.Fatalf("restart request=%d stats=%+v", requests.Load(), secondClient.Stats())
	}
	if _, err := secondClient.Walking(context.Background(), origin, destination); err != nil {
		t.Fatal(err)
	}
	if secondClient.Stats().MemoryCacheHits != 1 {
		t.Fatalf("expected L2 hit to be promoted into L1: %+v", secondClient.Stats())
	}
}

func TestWalkingRetriesServerError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","route":{"paths":[{"distance":"10","duration":"5","steps":[{"polyline":"121.473700,31.230400;121.473800,31.230400"}]}]}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, Path: "/walk", Key: "key", Timeout: time.Second, QPS: 100, Burst: 10, MaxRetries: 1})
	_, err := client.Walking(context.Background(), geo.Point{Lat: 31.2304, Lng: 121.4737}, geo.Point{Lat: 31.2304, Lng: 121.4738})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || client.Stats().Retries != 1 {
		t.Fatalf("requests=%d stats=%+v", requests.Load(), client.Stats())
	}
}
