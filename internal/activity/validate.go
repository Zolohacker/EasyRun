package activity

import (
	"fmt"
	"math"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

func Validate(run Run) Validation {
	var findings []Finding
	errorf := func(code, format string, args ...any) {
		findings = append(findings, Finding{Level: "error", Code: code, Message: fmt.Sprintf(format, args...)})
	}
	warnf := func(code, format string, args ...any) {
		findings = append(findings, Finding{Level: "warning", Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if len(run.Samples) < 2 {
		errorf("sample_count", "at least two samples are required")
		return Validation{Valid: false, Findings: findings}
	}

	var rawGPSDistance float64
	spikes, accelerationSpikes := 0, 0
	for i, sample := range run.Samples {
		if sample.Index != i {
			errorf("sample_index", "sample %d has index %d", i, sample.Index)
		}
		if !(geo.Point{Lat: sample.Lat, Lng: sample.Lng}).Valid() {
			errorf("coordinate", "sample %d has an invalid coordinate", i)
		}
		if i == 0 {
			continue
		}
		previous := run.Samples[i-1]
		dt := sample.ElapsedS - previous.ElapsedS
		dd := sample.DistanceM - previous.DistanceM
		if dt <= 0 || !sample.RecordedAt.After(previous.RecordedAt) {
			errorf("timestamp_order", "sample %d is not later than sample %d", i, i-1)
		}
		if dd < -0.001 {
			errorf("distance_order", "sample %d decreases cumulative distance", i)
		}
		if sample.Steps < previous.Steps {
			errorf("step_order", "sample %d decreases cumulative steps", i)
		}
		if dt > 0 {
			derivedSpeed := dd / dt
			if math.Abs(derivedSpeed-sample.SpeedMPS) > 0.08 {
				errorf("speed_distance_time", "sample %d speed differs from distance/time by %.3f m/s", i, math.Abs(derivedSpeed-sample.SpeedMPS))
			}
			if math.Abs(sample.SpeedMPS-previous.SpeedMPS)/dt > 2.5 {
				accelerationSpikes++
			}
		}
		if sample.SpeedMPS > 8 {
			spikes++
		}
		if sample.SpeedMPS > 0.2 && sample.CadenceSPM > 0 {
			relatedSpeed := sample.StrideM * sample.CadenceSPM / 60
			if math.Abs(relatedSpeed-sample.SpeedMPS) > 0.08 {
				errorf("stride_cadence_speed", "sample %d has inconsistent stride/cadence/speed", i)
			}
		}
		rawGPSDistance += geo.Distance(
			geo.Point{Lat: previous.Lat, Lng: previous.Lng},
			geo.Point{Lat: sample.Lat, Lng: sample.Lng},
		)
	}
	if spikes > 0 {
		warnf("speed_spikes", "%d samples exceed 8 m/s", spikes)
	}
	if accelerationSpikes > 0 {
		warnf("acceleration_spikes", "%d samples exceed 2.5 m/s²", accelerationSpikes)
	}

	last := run.Samples[len(run.Samples)-1]
	if math.Abs(run.Summary.DistanceM-last.DistanceM) > 0.02 {
		errorf("summary_distance", "summary distance differs from the final sample")
	}
	if run.Summary.TotalSteps != last.Steps {
		errorf("summary_steps", "summary steps differ from the final sample")
	}
	if math.Abs(run.Summary.DurationS-last.ElapsedS) > 0.02 {
		errorf("summary_duration", "summary duration differs from the final sample")
	}
	if run.Route.TargetDistanceM > 0 {
		delta := run.Summary.DistanceM - run.Route.TargetDistanceM
		if delta < -0.5 || delta > 50.05 {
			errorf("target_distance_error", "actual distance differs from selected distance by %.2f m; expected +0 to +50 m", delta)
		}
	}
	if last.DistanceM > 0 {
		ratio := rawGPSDistance / last.DistanceM
		if ratio < 0.82 || ratio > 1.22 {
			warnf("gps_geometry_ratio", "raw GPS geometry / fused distance ratio is %.3f", ratio)
		}
	}

	valid := true
	for _, finding := range findings {
		if finding.Level == "error" {
			valid = false
			break
		}
	}
	return Validation{Valid: valid, Findings: findings}
}
