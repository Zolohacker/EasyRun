package route

import (
	"context"
	"math"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/mapapi"
)

type straightWalker struct{}

func (straightWalker) Walking(_ context.Context, a, b geo.Point) (mapapi.Leg, error) {
	points, _ := geo.Densify([]geo.Point{a, b}, 8)
	return mapapi.Leg{DistanceM: geo.PolylineDistance(points), DurationS: 60, Points: points}, nil
}

type outsideRoadWalker struct{}

func (outsideRoadWalker) Walking(_ context.Context, a, b geo.Point) (mapapi.Leg, error) {
	outsideA := geo.Destination(a, 90, 1200)
	outsideB := geo.Destination(a, 110, 1300)
	points := []geo.Point{a, outsideA, outsideB, b}
	return mapapi.Leg{DistanceM: geo.PolylineDistance(points), DurationS: 60, Points: points}, nil
}

func TestPlanRoute(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 3)
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start:           geo.Point{Lat: 31.2304, Lng: 121.4737},
		TargetDistanceM: 3000,
		CandidateCount:  3,
		Seed:            20261021,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Points) < 100 {
		t.Fatalf("points = %d", len(planned.Points))
	}
	if planned.DistanceM < 3000 || planned.DistanceM > 3050 {
		t.Fatalf("distance = %.2f", planned.DistanceM)
	}
	if planned.Source != "map_api_road_network" {
		t.Fatalf("source = %q", planned.Source)
	}
}

func TestPlanRouteAllowsFinalOverrunAtHighestSelectableDistance(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 3)
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: geo.Point{Lat: 31.2304, Lng: 121.4737}, TargetDistanceM: 5000, MaxDistanceM: 5000,
		CandidateCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.DistanceM <= 5000 || planned.DistanceM > 5050.05 {
		t.Fatalf("distance = %.3f", planned.DistanceM)
	}
}

func TestPlanRouteAdaptsToBoundaryWithoutRequiringAShape(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 1)
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	boundary := []geo.Point{
		{Lat: start.Lat - 0.004, Lng: start.Lng - 0.004},
		{Lat: start.Lat - 0.004, Lng: start.Lng + 0.004},
		{Lat: start.Lat + 0.004, Lng: start.Lng + 0.004},
		{Lat: start.Lat + 0.004, Lng: start.Lng - 0.004},
	}
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start, TargetDistanceM: 3000, CandidateCount: 1, Boundary: boundary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.DistanceM < 3000 || planned.DistanceM > 3050.05 {
		t.Fatalf("distance = %.3f", planned.DistanceM)
	}
	if planned.Quality.OutsideAreaRatio != 0 {
		t.Fatalf("outside ratio = %.4f", planned.Quality.OutsideAreaRatio)
	}
}

func TestRoadRouteStartsOnProviderGeometry(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 2)
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	boundary := []geo.Point{
		{Lat: start.Lat - 0.004, Lng: start.Lng - 0.004},
		{Lat: start.Lat - 0.004, Lng: start.Lng + 0.004},
		{Lat: start.Lat + 0.004, Lng: start.Lng + 0.004},
		{Lat: start.Lat + 0.004, Lng: start.Lng - 0.004},
	}
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start, TargetDistanceM: 1000, CandidateCount: 2, Boundary: boundary, Seed: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Source != "map_api_road_network" {
		t.Fatalf("source=%q", planned.Source)
	}
	if geo.Distance(planned.Points[0], start) < 1 {
		t.Fatalf("route retained raw non-provider start: %+v", planned.Points[0])
	}
	if !pointInPolygon(planned.Points[0], boundary) {
		t.Fatalf("snapped road start is outside boundary: %+v", planned.Points[0])
	}
}

func TestNearestInteriorStartRepairsCuixiFenceCentre(t *testing.T) {
	start := geo.Point{Lat: 34.210652, Lng: 108.954316}
	boundary := []geo.Point{
		{Lat: 34.211651, Lng: 108.952832},
		{Lat: 34.209029, Lng: 108.952885},
		{Lat: 34.209074, Lng: 108.956608},
		{Lat: 34.210266, Lng: 108.956511},
		{Lat: 34.210280, Lng: 108.955407},
		{Lat: 34.210276, Lng: 108.954237},
		{Lat: 34.210650, Lng: 108.954243},
		{Lat: 34.211078, Lng: 108.954248},
		{Lat: 34.211087, Lng: 108.954092},
		{Lat: 34.211096, Lng: 108.953931},
		{Lat: 34.211686, Lng: 108.953958},
		{Lat: 34.211651, Lng: 108.952842},
	}
	if pointInPolygon(start, boundary) {
		t.Fatal("fixture centre unexpectedly starts inside")
	}
	adjusted, err := nearestInteriorStart(start, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !pointInPolygon(adjusted, boundary) {
		t.Fatalf("adjusted start remains outside: %+v", adjusted)
	}
	if distance := geo.Distance(start, adjusted); distance > 15 {
		t.Fatalf("adjusted start moved %.1f m, want a local correction", distance)
	}
}

func TestRoadNetworkSuitableUsesAbsoluteInsideCoverage(t *testing.T) {
	points := make([]geo.Point, 8)
	for index := range points {
		points[index] = geo.Point{Lat: 31.23 + float64(index)*0.0002, Lng: 121.47}
	}
	if !roadNetworkSuitable(roadNetwork{Points: points, TotalCount: 40, InsideCount: 8}, 4) {
		t.Fatal("a rich inside atlas was rejected because other probes detoured outside")
	}
}

func TestPlanRouteBuildsAutomaticFieldGraphWhenProviderHasNoInsideRoads(t *testing.T) {
	planner := NewPlanner(outsideRoadWalker{}, 3)
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	boundary := []geo.Point{
		{Lat: start.Lat - 0.0015, Lng: start.Lng - 0.0020},
		{Lat: start.Lat - 0.0015, Lng: start.Lng + 0.0020},
		{Lat: start.Lat + 0.0015, Lng: start.Lng + 0.0020},
		{Lat: start.Lat + 0.0015, Lng: start.Lng - 0.0020},
	}
	via := []geo.Point{
		geo.Destination(start, 25, 90),
		geo.Destination(start, 205, 100),
	}
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start, Via: via, TargetDistanceM: 1200, CandidateCount: 3,
		Boundary: boundary, Seed: 20261021,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Source != "auto_field_graph_via" {
		t.Fatalf("source = %q", planned.Source)
	}
	if planned.DistanceM < 1200 || planned.DistanceM > 1250.05 {
		t.Fatalf("distance = %.3f", planned.DistanceM)
	}
	if planned.Quality.OutsideAreaRatio != 0 {
		t.Fatalf("outside ratio = %.4f", planned.Quality.OutsideAreaRatio)
	}
	lastIndex := -1
	for viaIndex, target := range via {
		nearestIndex, nearestDistance := -1, math.Inf(1)
		for pointIndex, point := range planned.Points {
			if distance := geo.Distance(point, target); distance < nearestDistance {
				nearestIndex, nearestDistance = pointIndex, distance
			}
		}
		if nearestDistance > 0.1 {
			t.Fatalf("via[%d] nearest distance = %.3f", viaIndex, nearestDistance)
		}
		if nearestIndex <= lastIndex {
			t.Fatalf("via order changed: index=%d previous=%d", nearestIndex, lastIndex)
		}
		lastIndex = nearestIndex
	}
	repeated, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start, Via: via, TargetDistanceM: 1200, CandidateCount: 3,
		Boundary: boundary, Seed: 20261021,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Points) != len(repeated.Points) {
		t.Fatalf("field point counts differ for the same seed: %d != %d", len(planned.Points), len(repeated.Points))
	}
	for index := range planned.Points {
		if geo.Distance(planned.Points[index], repeated.Points[index]) > 0.01 {
			t.Fatalf("field point %d differs for the same seed", index)
		}
	}
}

func TestPlanRouteRejectsTargetBeyondServerMaximum(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 3)
	_, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: geo.Point{Lat: 31.2304, Lng: 121.4737}, TargetDistanceM: 5100, MaxDistanceM: 5000,
		CandidateCount: 3,
	})
	if err == nil {
		t.Fatal("expected target above server maximum to be rejected")
	}
}

func TestPlanRouteVisitsServerClockPointsBeforeDistanceFiller(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 3)
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	via := []geo.Point{
		geo.Destination(start, 20, 180),
		geo.Destination(start, 95, 260),
		geo.Destination(start, 170, 210),
	}
	planned, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start, Via: via, TargetDistanceM: 1800, CandidateCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Source != "map_api_road_network_via" {
		t.Fatalf("source=%q", planned.Source)
	}
	if planned.DistanceM < 1800 || planned.DistanceM > 1850.05 {
		t.Fatalf("distance=%.3f", planned.DistanceM)
	}
	lastIndex := -1
	for viaIndex, target := range via {
		nearestIndex, nearestDistance := -1, math.Inf(1)
		for pointIndex, point := range planned.Points {
			distance := geo.Distance(point, target)
			if distance < nearestDistance {
				nearestIndex, nearestDistance = pointIndex, distance
			}
		}
		if nearestDistance > 0.1 {
			t.Fatalf("via[%d] nearest distance=%.3f", viaIndex, nearestDistance)
		}
		if nearestIndex <= lastIndex {
			t.Fatalf("via order changed: index=%d previous=%d", nearestIndex, lastIndex)
		}
		lastIndex = nearestIndex
	}
}

func TestProviderRoadPointsExcludeRawProbeCoordinates(t *testing.T) {
	origin := geo.Point{Lat: 31.2304, Lng: 121.4737}
	roadA := geo.Destination(origin, 20, 80)
	roadB := geo.Destination(origin, 40, 160)
	probe := geo.Destination(origin, 60, 240)
	points := providerRoadPoints([]geo.Point{origin, roadA, roadB, probe}, 1)
	if len(points) != 2 {
		t.Fatalf("road points = %d, want 2", len(points))
	}
	if geo.Distance(points[0], roadA) > 0.01 || geo.Distance(points[1], roadB) > 0.01 {
		t.Fatal("road atlas retained a raw probe coordinate")
	}
}

func TestPlanRouteSeedIsRepeatable(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 1)
	request := PlanRequest{
		Start: geo.Point{Lat: 31.2304, Lng: 121.4737}, TargetDistanceM: 1800,
		CandidateCount: 1, Seed: 42,
	}
	first, err := planner.PlanRoute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.PlanRoute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Points) != len(second.Points) {
		t.Fatalf("point counts differ: %d != %d", len(first.Points), len(second.Points))
	}
	for index := range first.Points {
		if geo.Distance(first.Points[index], second.Points[index]) > 0.01 {
			t.Fatalf("point %d differs for the same seed", index)
		}
	}
}

func TestPlanRouteRejectsClockPointPathLongerThanAllowedResult(t *testing.T) {
	planner := NewPlanner(straightWalker{}, 1)
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	_, err := planner.PlanRoute(context.Background(), PlanRequest{
		Start: start,
		Via: []geo.Point{
			geo.Destination(start, 0, 400),
			geo.Destination(start, 180, 400),
		},
		TargetDistanceM: 500,
	})
	if err == nil {
		t.Fatal("expected mandatory clock-point path to exceed target headroom")
	}
}

func TestPointInPolygon(t *testing.T) {
	polygon := []geo.Point{{Lat: 31.22, Lng: 121.46}, {Lat: 31.22, Lng: 121.48}, {Lat: 31.24, Lng: 121.48}, {Lat: 31.24, Lng: 121.46}}
	if !pointInPolygon(geo.Point{Lat: 31.23, Lng: 121.47}, polygon) {
		t.Fatal("center should be inside polygon")
	}
	if pointInPolygon(geo.Point{Lat: 31.25, Lng: 121.47}, polygon) {
		t.Fatal("point should be outside polygon")
	}
}
