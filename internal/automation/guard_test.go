package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/safety"
	"github.com/Zolohacker/EasyRun/internal/store"
)

type blockedGuard struct{ err error }

func (g blockedGuard) Check(context.Context) error { return g.err }

type guardStore struct {
	store.Store
	job                       platform.AutomationJob
	executions                []platform.Execution
	finishStatus, finishError string
	claimed                   bool
}

func (s *guardStore) DueJobs(context.Context, time.Time, int) ([]platform.AutomationJob, error) {
	return []platform.AutomationJob{s.job}, nil
}
func (s *guardStore) ClaimJob(context.Context, string, time.Time, time.Time) (bool, error) {
	s.claimed = true
	return true, nil
}
func (s *guardStore) CreateExecution(_ context.Context, e platform.Execution) error {
	s.executions = append(s.executions, e)
	return nil
}
func (s *guardStore) GetJobContext(context.Context, string) (platform.JobContext, error) {
	return platform.JobContext{Job: s.job, Account: platform.Account{ID: "account", Enabled: true}}, nil
}
func (s *guardStore) FinishExecution(_ context.Context, _ string, status string, _ *string, _ string, message string, _ time.Time) error {
	s.finishStatus, s.finishError = status, message
	return nil
}
func TestVersionGateBlocksManualTriggerBeforeAccessingAnyJob(t *testing.T) {
	s := New(nil, nil, Options{Guard: blockedGuard{safety.ErrBlocked}})
	if _, err := s.Trigger(context.Background(), "any"); !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("%v", err)
	}
	if len(s.queue) != 0 {
		t.Fatal("blocked task queued")
	}
}
func TestVersionGateSkipsDueAndQueuedExecutionsWithoutBilling(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	db := &guardStore{job: platform.AutomationJob{ID: "job", AccountID: "account", NextRunAt: &now, ScheduleMode: platform.ScheduleModeManual, ManualTime: "06:00", DayScope: platform.DayScopeEveryDay, Timezone: "Asia/Shanghai"}}
	s := New(db, nil, Options{Guard: blockedGuard{safety.ErrBlocked}})
	s.pollOnce(context.Background())
	if !db.claimed || len(s.queue) != 0 || len(db.executions) != 1 || db.executions[0].Status != "skipped" {
		t.Fatalf("due job not skipped: %+v", db)
	}
	if !strings.Contains(db.executions[0].Error, safety.ErrBlocked.Error()) {
		t.Fatal("missing reason")
	}
	s.execute(context.Background(), workItem{jobID: "job", executionID: "queued"}, 1, 1)
	if db.finishStatus != "skipped" || !strings.Contains(db.finishError, safety.ErrBlocked.Error()) {
		t.Fatal("queued execution bypassed guard")
	}
}
func TestUpdateMaintenanceDoesNotClaimOrLoseDueTasks(t *testing.T) {
	s := New(nil, nil, Options{Guard: blockedGuard{safety.ErrMaintenance}})
	s.pollOnce(context.Background()) // A nil store panics if maintenance touches schedules.
}
