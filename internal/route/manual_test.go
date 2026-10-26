package route

import (
	"math"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

func TestManualGeometryValidationAndExactPrefix(t *testing.T) {
	start := geo.Point{Lat: 35, Lng: 116}
	points := []geo.Point{start, geo.OffsetMeters(start, 600, 0), geo.OffsetMeters(start, 600, 600), geo.OffsetMeters(start, 0, 600)}
	clean, length, err := ValidateManual(points, nil, 1000)
	if err != nil || length < 1799 || length > 1801 {
		t.Fatalf("length=%f err=%v", length, err)
	}
	for _, target := range []float64{300, 1000, 1500, length} {
		r, e := ManualPrefix("draw", clean, target)
		if e != nil {
			t.Fatal(e)
		}
		if r.Source != "manual" || r.Points[0] != start || math.Abs(r.DistanceM-target) > .02 {
			t.Fatalf("incorrect prefix: %+v", r)
		}
		if target > 1201 && len(r.Points) != 4 {
			t.Fatal("lost user bends")
		}
	}
	if _, err := ManualPrefix("draw", points, length+1); err == nil {
		t.Fatal("short route was extended")
	}
	if _, _, err := ValidateManual(points, nil, length); err == nil {
		t.Fatal("minimum length equality accepted")
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1, 0} {
		if _, err := ManualPrefix("draw", points, v); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	broken := append([]geo.Point(nil), points...)
	broken[1].Lat = math.NaN()
	if _, _, err := ValidateManual(broken, nil, 300); err == nil {
		t.Fatal("NaN hidden by deduplication")
	}
}
func TestManualChecksSegmentOutsideConcaveArea(t *testing.T) {
	s := geo.Point{Lat: 35, Lng: 116}
	pt := func(x, y float64) geo.Point { return geo.OffsetMeters(s, x, y) }
	// A U-shape: both endpoints are inside, but the bridge crosses its gap.
	boundary := []geo.Point{pt(0, 0), pt(1000, 0), pt(1000, 1000), pt(700, 1000), pt(700, 300), pt(300, 300), pt(300, 1000), pt(0, 1000)}
	if _, _, err := ValidateManual([]geo.Point{pt(150, 800), pt(850, 800)}, boundary, 300); err == nil {
		t.Fatal("segment outside fence accepted")
	}
	if _, _, err := ValidateManual([]geo.Point{pt(150, 800), pt(150, 150), pt(850, 150), pt(850, 800)}, boundary, 300); err != nil {
		t.Fatal(err)
	}
}
func TestManualCheckpointValidationNeverChangesGeometry(t *testing.T) {
	a := geo.Point{Lat: 35, Lng: 116}
	b := geo.OffsetMeters(a, 1000, 0)
	points := []geo.Point{a, b}
	if !CoversInOrder(points, []geo.Point{geo.OffsetMeters(a, 250, 0), geo.OffsetMeters(a, 750, 0)}, 10) {
		t.Fatal("on-route waypoints rejected")
	}
	if CoversInOrder(points, []geo.Point{geo.OffsetMeters(a, 750, 0), geo.OffsetMeters(a, 250, 0)}, 10) {
		t.Fatal("out-of-order waypoints accepted")
	}
	if CoversInOrder(points, []geo.Point{geo.OffsetMeters(a, 500, 100)}, 10) {
		t.Fatal("off-route waypoint accepted")
	}
	if len(points) != 2 || points[1] != b {
		t.Fatal("validation modified drawing")
	}
}
