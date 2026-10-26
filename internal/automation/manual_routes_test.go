package automation

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/store"
)

type manualExecutionStore struct {
	guardStore
	jobContext platform.JobContext
	manual     platform.ManualRoute
	missing    bool
}

func (s *manualExecutionStore) GetJobContext(context.Context, string) (platform.JobContext, error) {
	return s.jobContext, nil
}
func (s *manualExecutionStore) GetManualRoute(context.Context, string) (platform.ManualRoute, error) {
	if s.missing {
		return platform.ManualRoute{}, store.ErrNotFound
	}
	return s.manual, nil
}

type manualBilling struct {
	reserved, finished int
	distance           float64
	reject             bool
}

func (b *manualBilling) Reserve(_ context.Context, _ string, j platform.JobContext) error {
	b.reserved++
	b.distance = j.Plan.TargetDistanceM
	if b.reject {
		return errors.New("fixture: exhausted prepaid credit")
	}
	return nil
}
func (b *manualBilling) Finish(_ context.Context, _ string, success bool) error {
	b.finished++
	if success {
		return errors.New("fixture submission must not succeed")
	}
	return nil
}

type manualSubmitter struct {
	calls   int
	request activity.SubmissionRequest
}

func (s *manualSubmitter) Submit(_ context.Context, _ platform.Account, _ platform.RunningPlan, _ platform.RunningArea, request activity.SubmissionRequest) (protocol.SubmissionResult, activity.Run, error) {
	s.calls++
	s.request = request
	// No upstream call: exercise the same failure/refund path after selection.
	return protocol.SubmissionResult{}, activity.Run{}, errors.New("fixture: stop before upstream")
}

func TestManualRouteExecutionChecksBeforeBillingAndUsesSelectedDistance(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		modify               func(*manualExecutionStore, *manualBilling)
		wantReserve, wantRun int
		wantDistance         float64
	}{
		{name: "custom prefix", wantReserve: 1, wantRun: 1, wantDistance: 2000},
		{name: "count plan uses minimum", modify: func(s *manualExecutionStore, _ *manualBilling) {
			s.jobContext.Plan.Rules.PlanType = activity.PlanTypeByTimes
		}, wantReserve: 1, wantRun: 1, wantDistance: 1000},
		{name: "missing route", modify: func(s *manualExecutionStore, _ *manualBilling) { s.missing = true }},
		{name: "route shortened", modify: func(s *manualExecutionStore, _ *manualBilling) {
			s.manual.Points = []geo.Point{s.manual.Points[0], geo.OffsetMeters(s.manual.Points[0], 1500, 0)}
		}},
		{name: "foreign binding", modify: func(s *manualExecutionStore, _ *manualBilling) { s.manual.AccountID = "another-account" }},
		{name: "no available credit", modify: func(_ *manualExecutionStore, b *manualBilling) { b.reject = true }, wantReserve: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := geo.Point{Lat: 35, Lng: 116}
			db := &manualExecutionStore{
				jobContext: platform.JobContext{
					Job:     platform.AutomationJob{ID: "job", AccountID: "account", PlanID: "plan", AreaID: "area", RouteMode: "custom", ManualRouteID: "drawing", TargetDistanceM: 2000},
					Account: platform.Account{ID: "account", Enabled: true},
					Plan:    platform.RunningPlan{ID: "plan", SourceAccountID: "account", RemoteCode: "plan-code", TargetDistanceM: 2000, Rules: activity.RunRules{MinDistanceM: 1000, MaxDistanceM: 5000}},
					Area:    platform.RunningArea{ID: "area", SourceAccountID: "account", RemoteCode: "area-code", Center: start},
				},
				manual: platform.ManualRoute{ID: "drawing", Name: "fixture", AccountID: "account", PlanID: "plan", AreaID: "area", Points: []geo.Point{start, geo.OffsetMeters(start, 3000, 0)}},
			}
			billing, submitter := &manualBilling{}, &manualSubmitter{}
			if tc.modify != nil {
				tc.modify(db, billing)
			}
			scheduler := New(db, submitter, Options{Billing: billing})
			scheduler.execute(context.Background(), workItem{jobID: "job", executionID: "test-execution"}, 1, 1)
			if billing.reserved != tc.wantReserve || submitter.calls != tc.wantRun || billing.finished != tc.wantRun {
				t.Fatalf("reserve=%d submit=%d refund=%d, expected %d/%d/%d", billing.reserved, submitter.calls, billing.finished, tc.wantReserve, tc.wantRun, tc.wantRun)
			}
			if tc.wantRun > 0 {
				got := submitter.request
				if billing.distance != tc.wantDistance || got.TargetDistanceM != tc.wantDistance || got.ManualRoute == nil || got.ManualRoute.Source != "manual" || got.ManualRoute.Points[0] != start || math.Abs(got.ManualRoute.DistanceM-tc.wantDistance) > .01 {
					t.Fatalf("selected distance/prefix lost: billing=%f request=%+v", billing.distance, got)
				}
			}
		})
	}
}
