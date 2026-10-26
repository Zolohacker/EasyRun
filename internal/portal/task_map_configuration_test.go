package portal

import (
	"testing"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

func TestCheckoutWithoutMapKeyLeavesBalanceUntouchedAndAllowsDrawing(t *testing.T) {
	f := newEscrowFixture(t)
	if _, err := f.db.UpdateSystemSettings(f.ctx, platform.SystemSettings{WorkerProcesses: 1, WorkerConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/api/v1/portal/task-orders", f.order("map-missing-order-01", "per_run", 1, MicroUSDT), 400)
	f.wallet(f.balance)
	jobs, err := f.db.ListJobs(f.ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("rejected order left a job", jobs, err)
	}
	start := f.jc.Area.Center
	route, err := f.s.store.CreateManualRoute(f.actor, platform.ManualRoute{ID: "no-key-route", Name: "fixture", AccountID: f.jc.Account.ID, PlanID: f.jc.Plan.ID, AreaID: f.jc.Area.ID, Points: []geo.Point{start, geo.OffsetMeters(start, 2000, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	f.jc.Job.RouteMode, f.jc.Job.ManualRouteID = "custom", route.ID
	f.checkout("per_run", 1, MicroUSDT)
	f.wallet(f.balance - MicroUSDT)
}
