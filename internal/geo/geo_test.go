package geo

import (
	"math"
	"testing"
)

func TestDistanceAndDestination(t *testing.T) {
	start := Point{Lat: 31.2304, Lng: 121.4737}
	end := Destination(start, 90, 1000)
	distance := Distance(start, end)
	if math.Abs(distance-1000) > 0.5 {
		t.Fatalf("distance = %.3f, want 1000", distance)
	}
	bearing := Bearing(start, end)
	if math.Abs(bearing-90) > 0.2 {
		t.Fatalf("bearing = %.3f, want 90", bearing)
	}
}

func TestPolylineAtAndDensify(t *testing.T) {
	a := Point{Lat: 31.2304, Lng: 121.4737}
	b := Destination(a, 0, 120)
	line, err := NewPolyline([]Point{a, b})
	if err != nil {
		t.Fatal(err)
	}
	middle, _ := line.At(60)
	if got := Distance(a, middle); math.Abs(got-60) > 0.5 {
		t.Fatalf("middle distance = %.2f", got)
	}
	points, err := Densify([]Point{a, b}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) < 24 || len(points) > 26 {
		t.Fatalf("densified points = %d", len(points))
	}
}
