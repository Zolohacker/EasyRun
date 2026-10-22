package portal

import (
	"context"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/safety"
)

func TestPlatformUpdateNeverInterruptsWalletWorkOrLosesQueuedCollection(t *testing.T) {
	g, err := safety.New(context.Background(), nil, nil, "fixture", protocol.Device{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{ClientVersions: g, collectRequested: make(chan struct{}, 1)}
	_, finish, ok := s.beginBackground()
	if !ok {
		t.Fatal("background did not begin")
	}
	if _, err := s.Pause("fixture"); err == nil {
		t.Fatal("update interrupted active wallet work")
	}
	finish()
	if err := s.queueCollection(); err != nil {
		t.Fatal(err)
	}
	<-s.collectRequested // Even after the wake-up was consumed, the request remains pending.
	if _, err := s.Pause("fixture"); err == nil {
		t.Fatal("lost pending collection in scheduling race")
	}
	collect, finish, ok := s.beginBackground()
	if !ok || !collect {
		t.Fatal("collection request lost")
	}
	finish()
	release, err := s.Pause("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.beginBackground(); ok {
		t.Fatal("wallet work began during update")
	}
	if err := s.queueCollection(); err == nil {
		t.Fatal("accepted new collection during update")
	}
	release()
	release()
	_, finish, ok = s.beginBackground()
	if !ok {
		t.Fatal("background not resumed")
	}
	finish()
}
