package activity

import "math"

func BuildDistanceSplits(samples []Sample, intervalM float64) []DistanceSplit {
	if len(samples) < 2 || intervalM <= 0 {
		return nil
	}
	lastDistance := samples[len(samples)-1].DistanceM
	var result []DistanceSplit
	start := snapshotAtDistance(samples, 0)
	for boundary := intervalM; boundary < lastDistance+intervalM; boundary += intervalM {
		endDistance := math.Min(boundary, lastDistance)
		end := snapshotAtDistance(samples, endDistance)
		distance := end.distance - start.distance
		duration := end.elapsed - start.elapsed
		steps := end.steps - start.steps
		pace, cadence := 0.0, 0.0
		if distance > 0 {
			pace = duration / distance * 1000
		}
		if duration > 0 {
			cadence = float64(steps) / duration * 60
		}
		result = append(result, DistanceSplit{
			Index:          len(result) + 1,
			StartDistanceM: round(start.distance, 2),
			EndDistanceM:   round(end.distance, 2),
			DistanceM:      round(distance, 2),
			StartElapsedS:  round(start.elapsed, 2),
			EndElapsedS:    round(end.elapsed, 2),
			DurationS:      round(duration, 2),
			PaceSecPerKM:   round(pace, 2),
			Steps:          steps,
			AverageCadence: round(cadence, 2),
		})
		start = end
		if endDistance >= lastDistance {
			break
		}
	}
	return result
}

func BuildTimeSegments(samples []Sample, intervalS float64) []TimeSegment {
	if len(samples) < 2 || intervalS <= 0 {
		return nil
	}
	lastElapsed := samples[len(samples)-1].ElapsedS
	var result []TimeSegment
	start := snapshotAtTime(samples, 0)
	for boundary := intervalS; boundary < lastElapsed+intervalS; boundary += intervalS {
		endTime := math.Min(boundary, lastElapsed)
		end := snapshotAtTime(samples, endTime)
		duration := end.elapsed - start.elapsed
		distance := end.distance - start.distance
		steps := end.steps - start.steps
		cadence := 0.0
		if duration > 0 {
			cadence = float64(steps) / duration * 60
		}
		result = append(result, TimeSegment{
			Index:          len(result) + 1,
			StartElapsedS:  round(start.elapsed, 2),
			EndElapsedS:    round(end.elapsed, 2),
			DurationS:      round(duration, 2),
			StartDistanceM: round(start.distance, 2),
			EndDistanceM:   round(end.distance, 2),
			DistanceM:      round(distance, 2),
			StartSteps:     start.steps,
			EndSteps:       end.steps,
			Steps:          steps,
			CadenceSPM:     round(cadence, 2),
		})
		start = end
		if endTime >= lastElapsed {
			break
		}
	}
	return result
}

type snapshot struct {
	distance float64
	elapsed  float64
	steps    int
}

func snapshotAtDistance(samples []Sample, target float64) snapshot {
	if target <= samples[0].DistanceM {
		return snapshot{distance: samples[0].DistanceM, elapsed: samples[0].ElapsedS, steps: samples[0].Steps}
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].DistanceM >= target {
			return interpolateSnapshot(samples[i-1], samples[i], target, true)
		}
	}
	last := samples[len(samples)-1]
	return snapshot{distance: last.DistanceM, elapsed: last.ElapsedS, steps: last.Steps}
}

func snapshotAtTime(samples []Sample, target float64) snapshot {
	if target <= samples[0].ElapsedS {
		return snapshot{distance: samples[0].DistanceM, elapsed: samples[0].ElapsedS, steps: samples[0].Steps}
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].ElapsedS >= target {
			return interpolateSnapshot(samples[i-1], samples[i], target, false)
		}
	}
	last := samples[len(samples)-1]
	return snapshot{distance: last.DistanceM, elapsed: last.ElapsedS, steps: last.Steps}
}

func interpolateSnapshot(a, b Sample, target float64, byDistance bool) snapshot {
	denominator := b.ElapsedS - a.ElapsedS
	valueA := a.ElapsedS
	if byDistance {
		denominator = b.DistanceM - a.DistanceM
		valueA = a.DistanceM
	}
	fraction := 0.0
	if denominator > 0 {
		fraction = (target - valueA) / denominator
	}
	fraction = math.Max(0, math.Min(1, fraction))
	return snapshot{
		distance: a.DistanceM + (b.DistanceM-a.DistanceM)*fraction,
		elapsed:  a.ElapsedS + (b.ElapsedS-a.ElapsedS)*fraction,
		steps:    int(math.Round(float64(a.Steps) + float64(b.Steps-a.Steps)*fraction)),
	}
}
