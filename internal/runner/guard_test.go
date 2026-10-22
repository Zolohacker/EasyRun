package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/safety"
)

type mandatoryVersion struct{}

func (mandatoryVersion) CheckAppUpdate(context.Context, protocol.Device) (protocol.AppUpdate, error) {
	return protocol.AppUpdate{HaveUpdate: true, ForceUpdate: true, NewVersion: "99.0.0"}, nil
}
func TestSubmissionRequiresClientVersionBeforeAnyCredentialsOrRequests(t *testing.T) {
	g, err := safety.New(context.Background(), nil, mandatoryVersion{}, "fixture", protocol.Device{})
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil, nil, nil)
	s.SetExecutionGuard(g)
	_, _, err = s.Submit(context.Background(), platform.Account{}, platform.RunningPlan{}, platform.RunningArea{}, activity.SubmissionRequest{})
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("%v", err)
	}
}
