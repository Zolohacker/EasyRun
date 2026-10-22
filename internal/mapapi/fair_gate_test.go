package mapapi

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestFairGateRoundRobinsAcrossKeys(t *testing.T) {
	gate := newFairGate([]APIKey{{ID: "provider-1", Key: "secret-1", QPS: 10}}, 1)
	_, firstRelease, _, err := gate.acquire(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease()

	type grant struct {
		name    string
		release func()
	}
	granted := make(chan grant, 4)
	start := func(name, key string, waiting int64) {
		go func() {
			_, release, _, acquireErr := gate.acquire(context.Background(), key)
			if acquireErr != nil {
				granted <- grant{name: "error:" + acquireErr.Error()}
				return
			}
			granted <- grant{name: name, release: release}
		}()
		waitForGateWaiting(t, gate, waiting)
	}

	// A has more work queued first. Once B arrives, round-robin scheduling must
	// interleave the two groups rather than draining all of A before B.
	start("a2", "account-a", 1)
	start("a3", "account-a", 2)
	start("b1", "account-b", 3)
	start("b2", "account-b", 4)
	want := []string{"a2", "b1", "a3", "b2"}
	for index, expected := range want {
		select {
		case item := <-granted:
			if item.name != expected {
				t.Fatalf("grant %d = %q, want %q", index, item.name, expected)
			}
			item.release()
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for grant %d (%s)", index, expected)
		}
	}

	stats := gate.snapshot()
	if stats.Inflight != 0 || stats.Waiting != 0 || stats.FairnessKeys != 0 {
		t.Fatalf("gate did not drain: %+v", stats)
	}
}

func TestFairGateCancellationRemovesQueuedRequest(t *testing.T) {
	gate := newFairGate([]APIKey{{ID: "provider-1", Key: "secret-1", QPS: 1}}, 1)
	_, firstRelease, _, err := gate.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, _, acquireErr := gate.acquire(ctx, "cancelled")
		result <- acquireErr
	}()
	waitForGateWaiting(t, gate, 1)
	cancel()

	select {
	case acquireErr := <-result:
		if acquireErr == nil {
			t.Fatal("cancelled waiter unexpectedly received a token")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter was not removed")
	}
	waitForGateWaiting(t, gate, 0)
}

func TestFairGateDoesNotLimitInflightConcurrency(t *testing.T) {
	gate := newFairGate([]APIKey{{ID: "provider-1", Key: "secret-1", QPS: 10_000}}, 3)
	releases := make([]func(), 0, 3)
	for index := 0; index < 3; index++ {
		_, release, _, err := gate.acquire(context.Background(), fmt.Sprintf("account-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if got := gate.snapshot().Inflight; got != 3 {
		t.Fatalf("inflight=%d, want 3: the QPS gate must not impose a concurrency cap", got)
	}
	for _, release := range releases {
		release()
	}
}

func TestFairGateHonorsGlobalTokenRate(t *testing.T) {
	gate := newFairGate([]APIKey{{ID: "provider-1", Key: "secret-1", QPS: 20}}, 1)
	_, firstRelease, _, err := gate.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease()

	started := time.Now()
	_, secondRelease, waited, err := gate.acquire(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	secondRelease()
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("second token arrived too early: elapsed=%s reported_wait=%s", elapsed, waited)
	}
	if waited < 35*time.Millisecond {
		t.Fatalf("reported wait too short: %s", waited)
	}
}

func TestFairGateUsesIndependentKeyBuckets(t *testing.T) {
	gate := newFairGate([]APIKey{
		{ID: "provider-a", Key: "secret-a", QPS: 1},
		{ID: "provider-b", Key: "secret-b", QPS: 1},
	}, 1)
	firstKey, firstRelease, _, err := gate.acquire(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	secondKey, secondRelease, waited, err := gate.acquire(context.Background(), "account-b")
	if err != nil {
		t.Fatal(err)
	}
	firstRelease()
	secondRelease()
	if firstKey == secondKey {
		t.Fatalf("both initial tokens used the same credential %q", firstKey)
	}
	if waited > 20*time.Millisecond {
		t.Fatalf("second independent bucket waited %s", waited)
	}
	stats := gate.snapshot()
	if stats.QPS != 2 || stats.Burst != 2 || len(stats.Keys) != 2 {
		t.Fatalf("unexpected aggregate stats: %+v", stats)
	}
}

func waitForGateWaiting(t *testing.T, gate *fairGate, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := gate.snapshot().Waiting; got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting=%d, want %d (%s)", gate.snapshot().Waiting, want, fmt.Sprint(gate.snapshot()))
}
