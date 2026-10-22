package runner

import (
	"testing"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/protocol"
)

func TestSelectTargetWaypointsHandlesNoPointModes(t *testing.T) {
	start := geo.Point{Lat: 31.2, Lng: 121.4}
	points := []protocol.TargetPoint{{Code: 1, Lat: 31.201, Lng: 121.401, Type: 2}}
	if got := selectTargetWaypoints(start, protocol.ClockModeNoPoint, points); len(got) != 0 {
		t.Fatalf("no-point mode returned %v", got)
	}
	if got := selectTargetWaypoints(start, protocol.ClockModeOrder, nil); len(got) != 0 {
		t.Fatalf("empty target response returned %v", got)
	}
}

func TestSelectTargetWaypointsPreservesOrderMode(t *testing.T) {
	start := geo.Point{Lat: 31.2, Lng: 121.4}
	points := []protocol.TargetPoint{
		{Code: 10, Lat: 31.201, Lng: 121.401, Type: 2, Sort: 1},
		{Code: 20, Lat: 31.202, Lng: 121.402, Type: 2, Sort: 2},
	}
	got := selectTargetWaypoints(start, protocol.ClockModeOrder, points)
	if len(got) != 2 || got[0].Lat != points[0].Lat || got[1].Lat != points[1].Lat {
		t.Fatalf("ordered waypoints=%v", got)
	}
}

func TestSelectTargetWaypointsFreeModeUsesRequiredAndOneOptional(t *testing.T) {
	start := geo.Point{Lat: 31.2, Lng: 121.4}
	points := []protocol.TargetPoint{
		{Code: 1, Lat: 31.2010, Lng: 121.4000, Type: 2},
		{Code: 2, Lat: 31.2020, Lng: 121.4000, Type: 2},
		{Code: 3, Lat: 31.2011, Lng: 121.4000, Type: 1},
		{Code: 4, Lat: 31.2300, Lng: 121.4300, Type: 1},
	}
	got := selectTargetWaypoints(start, protocol.ClockModeFree, points)
	if len(got) != 3 {
		t.Fatalf("free waypoints=%v", got)
	}
	farOptional := geo.Point{Lat: points[3].Lat, Lng: points[3].Lng}
	for _, point := range got {
		if geo.Distance(point, farOptional) < 0.1 {
			t.Fatalf("selected farther optional point: %v", got)
		}
	}
}

func TestSelectTargetWaypointsRandomModeUsesServerSequence(t *testing.T) {
	start := geo.Point{Lat: 31.2, Lng: 121.4}
	points := []protocol.TargetPoint{
		{Code: 1, Lat: 31.201, Lng: 121.401},
		{Code: 2, Lat: 31.202, Lng: 121.402},
		{Code: 3, Lat: 31.203, Lng: 121.403},
		{Code: 4, Lat: 31.204, Lng: 121.404},
	}
	got := selectTargetWaypoints(start, protocol.ClockModeRandom, points)
	if len(got) != 3 || got[0].Lat != points[0].Lat || got[2].Lat != points[2].Lat {
		t.Fatalf("random waypoints=%v", got)
	}
}
