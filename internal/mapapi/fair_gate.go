package mapapi

import (
	"container/list"
	"context"
	"math"
	"strings"
	"sync"
	"time"
)

// fairnessContextKey carries the logical owner of a map request. Requests are
// scheduled round-robin by owner so one account with many route candidates
// cannot monopolize the shared API token bucket.
type fairnessContextKey struct{}

// WithFairnessKey assigns a scheduling group to every map request made with
// ctx. Empty keys deliberately collapse into the shared default group.
func WithFairnessKey(ctx context.Context, key string) context.Context {
	key = normalizeFairnessKey(key)
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fairnessContextKey{}, key)
}

func fairnessKeyFromContext(ctx context.Context) string {
	if ctx != nil {
		if key, ok := ctx.Value(fairnessContextKey{}).(string); ok {
			return normalizeFairnessKey(key)
		}
	}
	return "shared"
}

func normalizeFairnessKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "shared"
	}
	return key
}

type gateWaiterState uint8

const (
	gateWaiterQueued gateWaiterState = iota
	gateWaiterGranted
	gateWaiterReleased
	gateWaiterCancelled
)

type gateWaiter struct {
	key       string
	ctx       context.Context
	ready     chan struct{}
	apiKey    string
	err       error
	state     gateWaiterState
	queue     *gateQueue
	element   *list.Element
	queuedAt  time.Time
	grantTime time.Time
}

type gateQueue struct {
	waiters list.List
}

type fairGateSnapshot struct {
	Inflight     int64
	Waiting      int64
	FairnessKeys int
	Dispatched   uint64
	QPS          float64
	Burst        int
	BurstPerKey  int
	Keys         []fairGateKeySnapshot
}

type fairGateKeySnapshot struct {
	ID         string
	QPS        float64
	Dispatched uint64
}

type gateCredential struct {
	id         string
	key        string
	qps        float64
	tokens     float64
	dispatched uint64
}

// fairGate owns one token bucket per provider credential. It deliberately does
// not cap the number of in-flight HTTP requests: once any credential has a
// token the request is dispatched, even when earlier requests are still
// waiting on the provider.
//
// The rotation contains each non-empty scheduling group exactly once. After a
// request is selected its group moves to the tail, providing round-robin
// service across accounts while preserving FIFO order within each account.
type fairGate struct {
	mu         sync.Mutex
	burst      int
	lastRefill time.Time
	keys       []gateCredential
	keyCursor  int
	inflight   int64
	waiting    int64
	dispatched uint64
	queues     map[string]*gateQueue
	rotation   []string
	timer      *time.Timer
}

func newFairGate(keys []APIKey, burst int) *fairGate {
	if burst <= 0 {
		burst = 1
	}
	now := time.Now()
	gate := &fairGate{
		burst: burst, lastRefill: now,
		queues: make(map[string]*gateQueue),
	}
	gate.replaceKeysLocked(keys)
	return gate
}

// acquire blocks until one configured API key has a token. The selected secret
// is fixed on the waiter so a concurrent settings update cannot change the key
// halfway through request construction. The returned release function only
// maintains the inflight observation metric; releasing never gates another call.
func (g *fairGate) acquire(ctx context.Context, key string) (apiKey string, release func(), waitDuration time.Duration, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", nil, 0, err
	}
	waiter := &gateWaiter{
		key: normalizeFairnessKey(key), ctx: ctx, ready: make(chan struct{}, 1),
		state: gateWaiterQueued, queuedAt: time.Now(),
	}

	g.mu.Lock()
	if len(g.keys) == 0 {
		g.mu.Unlock()
		return "", nil, 0, ErrNotConfigured
	}
	g.enqueueLocked(waiter)
	g.dispatchLocked(time.Now())
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		if waiter.err != nil {
			return "", nil, time.Since(waiter.queuedAt), waiter.err
		}
		if err := ctx.Err(); err != nil {
			g.cancel(waiter)
			return "", nil, time.Since(waiter.queuedAt), err
		}
		var once sync.Once
		return waiter.apiKey, func() { once.Do(func() { g.release(waiter) }) }, waiter.grantTime.Sub(waiter.queuedAt), nil
	case <-ctx.Done():
		g.cancel(waiter)
		return "", nil, time.Since(waiter.queuedAt), ctx.Err()
	}
}

func (g *fairGate) enqueueLocked(waiter *gateWaiter) {
	queue := g.queues[waiter.key]
	if queue == nil {
		queue = &gateQueue{}
		g.queues[waiter.key] = queue
		g.rotation = append(g.rotation, waiter.key)
	}
	waiter.queue = queue
	waiter.element = queue.waiters.PushBack(waiter)
	g.waiting++
}

func (g *fairGate) cancel(waiter *gateWaiter) {
	g.mu.Lock()
	switch waiter.state {
	case gateWaiterQueued:
		if waiter.queue != nil && waiter.element != nil {
			waiter.queue.waiters.Remove(waiter.element)
			waiter.element = nil
			g.waiting--
			if waiter.queue.waiters.Len() == 0 {
				delete(g.queues, waiter.key)
				g.removeRotationKeyLocked(waiter.key)
			}
		}
		waiter.state = gateWaiterCancelled
	case gateWaiterGranted:
		waiter.state = gateWaiterCancelled
		g.inflight--
	}
	g.dispatchLocked(time.Now())
	g.mu.Unlock()
}

func (g *fairGate) release(waiter *gateWaiter) {
	g.mu.Lock()
	if waiter.state == gateWaiterGranted {
		waiter.state = gateWaiterReleased
		g.inflight--
	}
	g.dispatchLocked(time.Now())
	g.mu.Unlock()
}

func (g *fairGate) updateKeys(keys []APIKey) {
	g.mu.Lock()
	now := time.Now()
	g.refillLocked(now)
	g.replaceKeysLocked(keys)
	if len(g.keys) == 0 {
		g.failQueuedLocked(ErrNotConfigured)
	}
	g.dispatchLocked(now)
	g.mu.Unlock()
}

func (g *fairGate) replaceKeysLocked(keys []APIKey) {
	previous := make(map[string]gateCredential, len(g.keys))
	for _, key := range g.keys {
		previous[key.id] = key
	}
	replacement := make([]gateCredential, 0, len(keys))
	for _, key := range keys {
		credential := gateCredential{id: key.ID, key: key.Key, qps: key.QPS, tokens: float64(g.burst)}
		if existing, ok := previous[key.ID]; ok {
			credential.tokens = math.Min(float64(g.burst), existing.tokens)
			credential.dispatched = existing.dispatched
		}
		replacement = append(replacement, credential)
	}
	g.keys = replacement
	if len(g.keys) == 0 || g.keyCursor >= len(g.keys) {
		g.keyCursor = 0
	}
}

func (g *fairGate) failQueuedLocked(err error) {
	for _, queue := range g.queues {
		for element := queue.waiters.Front(); element != nil; element = element.Next() {
			waiter := element.Value.(*gateWaiter)
			waiter.state = gateWaiterCancelled
			waiter.err = err
			waiter.queue = nil
			waiter.element = nil
			waiter.ready <- struct{}{}
		}
	}
	g.waiting = 0
	g.queues = make(map[string]*gateQueue)
	g.rotation = nil
}

func (g *fairGate) configured() bool {
	g.mu.Lock()
	configured := len(g.keys) > 0
	g.mu.Unlock()
	return configured
}

func (g *fairGate) snapshot() fairGateSnapshot {
	g.mu.Lock()
	g.refillLocked(time.Now())
	keys := make([]fairGateKeySnapshot, 0, len(g.keys))
	var qps float64
	for _, key := range g.keys {
		qps += key.qps
		keys = append(keys, fairGateKeySnapshot{ID: key.id, QPS: key.qps, Dispatched: key.dispatched})
	}
	snapshot := fairGateSnapshot{
		Inflight: g.inflight, Waiting: g.waiting, FairnessKeys: len(g.rotation),
		Dispatched: g.dispatched, QPS: qps, Burst: g.burst * len(g.keys), BurstPerKey: g.burst,
		Keys: keys,
	}
	g.mu.Unlock()
	return snapshot
}

func (g *fairGate) dispatchLocked(now time.Time) {
	g.refillLocked(now)
	for len(g.rotation) > 0 {
		keyIndex := g.nextAvailableKeyLocked()
		if keyIndex < 0 {
			break
		}
		waiter := g.popNextLocked()
		if waiter == nil {
			break
		}
		if waiter.ctx.Err() != nil {
			waiter.state = gateWaiterCancelled
			continue
		}
		credential := &g.keys[keyIndex]
		credential.tokens = math.Max(0, credential.tokens-1)
		credential.dispatched++
		g.keyCursor = (keyIndex + 1) % len(g.keys)
		g.inflight++
		g.dispatched++
		waiter.state = gateWaiterGranted
		waiter.grantTime = now
		waiter.apiKey = credential.key
		waiter.ready <- struct{}{}
	}
	g.scheduleWakeLocked(now)
}

func (g *fairGate) nextAvailableKeyLocked() int {
	if len(g.keys) == 0 {
		return -1
	}
	for offset := 0; offset < len(g.keys); offset++ {
		index := (g.keyCursor + offset) % len(g.keys)
		if g.keys[index].tokens+1e-9 >= 1 {
			return index
		}
	}
	return -1
}

func (g *fairGate) popNextLocked() *gateWaiter {
	for len(g.rotation) > 0 {
		key := g.rotation[0]
		g.rotation = g.rotation[1:]
		queue := g.queues[key]
		if queue == nil || queue.waiters.Len() == 0 {
			delete(g.queues, key)
			continue
		}
		element := queue.waiters.Front()
		waiter := element.Value.(*gateWaiter)
		queue.waiters.Remove(element)
		waiter.element = nil
		waiter.queue = nil
		g.waiting--
		if queue.waiters.Len() == 0 {
			delete(g.queues, key)
		} else {
			g.rotation = append(g.rotation, key)
		}
		return waiter
	}
	return nil
}

func (g *fairGate) removeRotationKeyLocked(key string) {
	for index, existing := range g.rotation {
		if existing != key {
			continue
		}
		copy(g.rotation[index:], g.rotation[index+1:])
		g.rotation = g.rotation[:len(g.rotation)-1]
		return
	}
}

func (g *fairGate) refillLocked(now time.Time) {
	if now.Before(g.lastRefill) {
		g.lastRefill = now
		return
	}
	if elapsed := now.Sub(g.lastRefill).Seconds(); elapsed > 0 {
		for index := range g.keys {
			g.keys[index].tokens = math.Min(float64(g.burst), g.keys[index].tokens+elapsed*g.keys[index].qps)
		}
		g.lastRefill = now
	}
}

func (g *fairGate) scheduleWakeLocked(now time.Time) {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if len(g.rotation) == 0 || len(g.keys) == 0 || g.nextAvailableKeyLocked() >= 0 {
		return
	}
	seconds := math.Inf(1)
	for _, key := range g.keys {
		untilToken := (1 - key.tokens) / key.qps
		if untilToken < seconds {
			seconds = untilToken
		}
	}
	delay := time.Duration(math.Ceil(seconds * float64(time.Second)))
	if delay < time.Microsecond {
		delay = time.Microsecond
	}
	g.timer = time.AfterFunc(delay, func() {
		g.mu.Lock()
		g.timer = nil
		g.dispatchLocked(time.Now())
		g.mu.Unlock()
	})
}
