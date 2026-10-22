package safety

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/protocol"
)

type fakeChecker struct {
	mu     sync.Mutex
	result protocol.AppUpdate
	err    error
	calls  int
}

func (f *fakeChecker) CheckAppUpdate(context.Context, protocol.Device) (protocol.AppUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.result, f.err
}
func (f *fakeChecker) set(v protocol.AppUpdate, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = v, err
}

func TestForcedUpdateCancelsAndBlocksExecutions(t *testing.T) {
	check := &fakeChecker{result: protocol.AppUpdate{NewVersion: protocol.AppVersion}}
	g, err := New(context.Background(), nil, check, "fixture", protocol.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Snapshot().Allowed {
		t.Fatal("unknown state allowed")
	}
	ctx, finish, err := g.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if _, err := g.Pause("update"); err == nil {
		t.Fatal("maintenance interrupted an active run")
	}
	check.set(protocol.AppUpdate{NewVersion: "8.8.0", HaveUpdate: true, ForceUpdate: true}, nil)
	if err := g.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(ctx), ErrBlocked) {
		t.Fatal("active run not cancelled", context.Cause(ctx))
	}
	if err := g.Check(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal("new run accepted", err)
	}
	check.set(protocol.AppUpdate{}, errors.New("fixture network failure"))
	_ = g.Refresh(context.Background(), true)
	if !g.Snapshot().ForceUpdate || g.Snapshot().Allowed {
		t.Fatal("failed probe cleared forced update")
	}
	finish()
	check.set(protocol.AppUpdate{NewVersion: protocol.AppVersion}, nil)
	if err := g.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	release, err := g.Pause("updating platform")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Check(context.Background()); !errors.Is(err, ErrMaintenance) {
		t.Fatal("maintenance was bypassed", err)
	}
	release()
	release()
	if err := g.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownAndStaleChecksBlockWithoutStampeding(t *testing.T) {
	check := &fakeChecker{err: errors.New("offline")}
	g, _ := New(context.Background(), nil, check, "fixture", protocol.Device{})
	now := time.Now()
	g.clock = func() time.Time { return now }
	if err := g.Check(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Check(context.Background()) }()
	}
	wg.Wait()
	if check.calls != 1 {
		t.Fatal("probe stampede", check.calls)
	}
	check.set(protocol.AppUpdate{NewVersion: protocol.AppVersion}, nil)
	if err := g.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	check.set(protocol.AppUpdate{}, errors.New("offline"))
	if err := g.Check(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal("expired safe state allowed", err)
	}
}
