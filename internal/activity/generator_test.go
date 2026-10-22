package activity

import (
	"math"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/route"
)

func TestGeneratorProducesConsistentDeterministicRun(t *testing.T) {
	routeValue := testLoop(5000)
	seed := int64(42)
	start := time.Date(2026, 10, 21, 6, 30, 0, 0, time.UTC)
	request := GenerateRequest{
		Route: routeValue,
		Profile: Profile{
			TargetPaceSecPerKM: 360,
			CadenceSPM:         168,
			SampleIntervalS:    2,
			PaceVariationPct:   0.035,
			GPSQuality:         "mixed",
			Terrain:            "rolling",
		},
		StartedAt: &start,
		Seed:      &seed,
	}
	generator := NewGenerator()
	runA, err := generator.Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	runB, err := generator.Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	if !runA.Validation.Valid {
		t.Fatalf("validation: %+v", runA.Validation.Findings)
	}
	if len(runA.Samples) < 700 || len(runA.Samples) > 1200 {
		t.Fatalf("sample count = %d", len(runA.Samples))
	}
	if runA.Summary.TotalSteps < 3500 || runA.Summary.TotalSteps > 6000 {
		t.Fatalf("steps = %d", runA.Summary.TotalSteps)
	}
	if len(runA.KilometreSplits) != 5 {
		t.Fatalf("splits = %d", len(runA.KilometreSplits))
	}
	if runA.Summary.DistanceM != runB.Summary.DistanceM || runA.Summary.DurationS != runB.Summary.DurationS || runA.Summary.TotalSteps != runB.Summary.TotalSteps {
		t.Fatal("same seed and start should reproduce summary")
	}
	if math.Abs(runA.Summary.AveragePaceSecPerKM-360) > 35 {
		t.Fatalf("average pace = %.2f", runA.Summary.AveragePaceSecPerKM)
	}
}

func TestProfileValidation(t *testing.T) {
	_, err := NewGenerator().Generate(GenerateRequest{Route: testLoop(1000), Profile: Profile{TargetPaceSecPerKM: 20}})
	if err == nil {
		t.Fatal("expected pace validation error")
	}
}

func TestGeneratedGPSDriftStaysCloseToPlannedRoute(t *testing.T) {
	routeValue := testLoop(5000)
	line, err := geo.NewPolyline(routeValue.Points)
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(20261022)
	startedAt := time.Date(2026, 10, 22, 6, 30, 0, 0, time.UTC)
	for _, quality := range []string{"open", "mixed", "urban"} {
		run, generateErr := NewGenerator().Generate(GenerateRequest{
			Route: routeValue,
			Profile: Profile{
				TargetPaceSecPerKM: 360,
				CadenceSPM:         168,
				SampleIntervalS:    3,
				PaceVariationPct:   0.035,
				GPSQuality:         quality,
				Terrain:            "flat",
			},
			StartedAt: &startedAt,
			Seed:      &seed,
		})
		if generateErr != nil {
			t.Fatalf("%s: %v", quality, generateErr)
		}
		// Weak-fix periods can increase the normal cap by 35%. Coordinate
		// rounding contributes only centimetres, hence the small tolerance.
		maximum := gpsDriftProfiles[quality].maxOffsetM*1.35 + 0.05
		for _, sample := range run.Samples {
			clean, _ := line.At(sample.DistanceM)
			deviation := geo.Distance(clean, geo.Point{Lat: sample.Lat, Lng: sample.Lng})
			if deviation > maximum {
				t.Fatalf("%s sample %d deviates %.3fm; maximum %.3fm", quality, sample.Index, deviation, maximum)
			}
		}
	}
}

func TestLimitGPSOffsetPreservesDirection(t *testing.T) {
	east, north := limitGPSOffset(6, 8, 2.5)
	if math.Abs(math.Hypot(east, north)-2.5) > 1e-9 {
		t.Fatalf("limited magnitude = %.6f", math.Hypot(east, north))
	}
	if math.Abs(east/north-0.75) > 1e-9 {
		t.Fatalf("direction changed: east=%.6f north=%.6f", east, north)
	}
}

func testLoop(distanceM float64) route.Route {
	start := geo.Point{Lat: 31.2304, Lng: 121.4737}
	side := distanceM / 4
	a := geo.Destination(start, 90, side)
	b := geo.Destination(a, 0, side)
	c := geo.Destination(b, 270, side)
	points, _ := geo.Densify([]geo.Point{start, a, b, c, start}, 5)
	return route.Route{ID: "test", Source: "test", TargetDistanceM: distanceM, DistanceM: geo.PolylineDistance(points), Points: points}
}
