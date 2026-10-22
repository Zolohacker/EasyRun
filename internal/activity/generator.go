package activity

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

const maxSamples = 100_000

// gpsDriftProfile controls the positional error added to the clean route.
// Keep the reported receiver accuracy separate from the actual displacement:
// phones often report a several-metre accuracy radius while consecutive fixes
// remain much closer to the same road centreline.
type gpsDriftProfile struct {
	sigmaM          float64
	maxOffsetM      float64
	accuracyBaseM   float64
	correlationSecs float64
}

var gpsDriftProfiles = map[string]gpsDriftProfile{
	"open":  {sigmaM: 0.65, maxOffsetM: 1.7, accuracyBaseM: 2.4, correlationSecs: 32},
	"mixed": {sigmaM: 1.00, maxOffsetM: 2.5, accuracyBaseM: 3.5, correlationSecs: 30},
	"urban": {sigmaM: 1.45, maxOffsetM: 3.8, accuracyBaseM: 5.2, correlationSecs: 28},
}

type Generator struct{}

func NewGenerator() *Generator { return &Generator{} }

func (g *Generator) Generate(request GenerateRequest) (Run, error) {
	profile, err := NormalizeProfile(request.Profile)
	if err != nil {
		return Run{}, err
	}
	line, err := geo.NewPolyline(request.Route.Points)
	if err != nil {
		return Run{}, fmt.Errorf("route: %w", err)
	}
	if line.LengthM < 100 || line.LengthM > 100_000 {
		return Run{}, errors.New("route geometry must be between 100 and 100000 metres")
	}

	seed := randomSeed()
	if request.Seed != nil {
		seed = *request.Seed
	}
	rng := mathrand.New(mathrand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	start := time.Now().UTC().Add(-time.Duration(line.LengthM/(1000/profile.TargetPaceSecPerKM)) * time.Second)
	if request.StartedAt != nil {
		start = request.StartedAt.UTC()
	}

	samples := generateSamples(line, profile, start, rng)
	if len(samples) < 2 {
		return Run{}, errors.New("generated activity has too few samples")
	}
	summary := summarize(samples)
	run := Run{
		SchemaVersion:   1,
		ID:              fmt.Sprintf("run_%d_%08x", time.Now().UTC().UnixNano(), rng.Uint64()),
		AccountID:       request.AccountID,
		Synthetic:       true,
		GeneratedAt:     time.Now().UTC(),
		StartedAt:       start,
		FinishedAt:      samples[len(samples)-1].RecordedAt,
		Seed:            seed,
		Route:           request.Route,
		Profile:         profile,
		Summary:         summary,
		Samples:         samples,
		KilometreSplits: BuildDistanceSplits(samples, 1000),
		StepSegments:    BuildTimeSegments(samples, 20),
	}
	run.Validation = Validate(run)
	return run, nil
}

func NormalizeProfile(profile Profile) (Profile, error) {
	if profile.TargetPaceSecPerKM == 0 {
		profile.TargetPaceSecPerKM = 360
	}
	if profile.CadenceSPM == 0 {
		profile.CadenceSPM = 168
	}
	if profile.TargetStrideM == 0 {
		profile.TargetStrideM = round(60000/(profile.TargetPaceSecPerKM*profile.CadenceSPM), 3)
	}
	if profile.SampleIntervalS == 0 {
		// The 8.7.9 LocationComponent requests a 3000 ms location interval.
		profile.SampleIntervalS = 3
	}
	if profile.PaceVariationPct == 0 {
		profile.PaceVariationPct = 0.035
	}
	if profile.GPSQuality == "" {
		profile.GPSQuality = "mixed"
	}
	if profile.Terrain == "" {
		profile.Terrain = "flat"
	}
	if profile.StartAltitudeM == 0 {
		profile.StartAltitudeM = 32
	}
	switch {
	case profile.TargetPaceSecPerKM < 150 || profile.TargetPaceSecPerKM > 900:
		return Profile{}, errors.New("target_pace_sec_per_km must be between 150 and 900")
	case profile.CadenceSPM < 100 || profile.CadenceSPM > 230:
		return Profile{}, errors.New("cadence_spm must be between 100 and 230")
	case profile.TargetStrideM < 0.2 || profile.TargetStrideM > 3:
		return Profile{}, errors.New("target_stride_m must be between 0.2 and 3")
	case profile.SampleIntervalS < 0.5 || profile.SampleIntervalS > 10:
		return Profile{}, errors.New("sample_interval_s must be between 0.5 and 10")
	case profile.PaceVariationPct < 0.005 || profile.PaceVariationPct > 0.2:
		return Profile{}, errors.New("pace_variation_pct must be between 0.005 and 0.2")
	case profile.PauseRatePerHour < 0 || profile.PauseRatePerHour > 10:
		return Profile{}, errors.New("pause_rate_per_hour must be between 0 and 10")
	}
	if profile.GPSQuality != "open" && profile.GPSQuality != "mixed" && profile.GPSQuality != "urban" {
		return Profile{}, errors.New("gps_quality must be open, mixed, or urban")
	}
	if profile.Terrain != "flat" && profile.Terrain != "rolling" && profile.Terrain != "hilly" {
		return Profile{}, errors.New("terrain must be flat, rolling, or hilly")
	}
	return profile, nil
}

func generateSamples(line geo.Polyline, profile Profile, start time.Time, rng *mathrand.Rand) []Sample {
	baseSpeed := 1000 / profile.TargetPaceSecPerKM
	drift := gpsDriftProfiles[profile.GPSQuality]
	slopeSigma := map[string]float64{"flat": 0.0015, "rolling": 0.005, "hilly": 0.010}[profile.Terrain]

	cleanStart, heading := line.At(0)
	noiseEast, noiseNorth := rng.NormFloat64()*drift.sigmaM*0.35, rng.NormFloat64()*drift.sigmaM*0.35
	noiseEast, noiseNorth = limitGPSOffset(noiseEast, noiseNorth, drift.maxOffsetM)
	observed := geo.OffsetMeters(cleanStart, noiseEast, noiseNorth)
	altitude := profile.StartAltitudeM
	samples := []Sample{{
		Index:               0,
		RecordedAt:          start,
		ElapsedS:            0,
		Lat:                 round(observed.Lat, 7),
		Lng:                 round(observed.Lng, 7),
		DistanceM:           0,
		GPSDistanceM:        0,
		SpeedMPS:            0,
		PaceSecPerKM:        0,
		CadenceSPM:          0,
		Steps:               0,
		StrideM:             0,
		AltitudeM:           round(altitude, 2),
		HorizontalAccuracyM: round(drift.accuracyBaseM, 2),
		Satellites:          satelliteCount(profile.GPSQuality, drift.accuracyBaseM, rng),
		HeadingDeg:          round(heading, 1),
	}}

	var (
		distance, elapsed, gpsDistance, movingTime float64
		paceState, cadenceState, slopeState        float64
		stepAccumulator                            float64
		previousSpeed                              = baseSpeed * 0.82
		previousObserved                           = observed
		degradedFor                                float64
		pauseFor                                   float64
	)

	for distance < line.LengthM && len(samples) < maxSamples {
		dt := profile.SampleIntervalS * clamp(1+rng.NormFloat64()*0.08, 0.72, 1.28)
		progress := distance / line.LengthM
		warmup := 0.91 + 0.09*smoothstep(clamp(progress/0.08, 0, 1))
		fatigue := 1 - 0.055*smoothstep(clamp((progress-0.68)/0.32, 0, 1))
		turn := line.TurnSeverity(distance, 14)
		turnFactor := 1 - 0.09*turn

		paceRho := math.Exp(-dt / 22)
		paceState = paceRho*paceState + math.Sqrt(1-paceRho*paceRho)*profile.PaceVariationPct*rng.NormFloat64()
		desiredSpeed := baseSpeed * warmup * fatigue * turnFactor * (1 + paceState)

		if pauseFor > 0 {
			pauseFor -= dt
			desiredSpeed = 0
		} else if profile.PauseRatePerHour > 0 && rng.Float64() < profile.PauseRatePerHour*dt/3600 {
			pauseFor = 4 + 14*rng.Float64()
			desiredSpeed = 0
		}
		maxDelta := 0.42 * dt
		speed := clamp(desiredSpeed, previousSpeed-maxDelta, previousSpeed+maxDelta)
		if desiredSpeed == 0 && speed < 0.25 {
			speed = 0
		}
		speed = clamp(speed, 0, 7.5)
		deltaDistance := speed * dt
		if distance+deltaDistance > line.LengthM {
			deltaDistance = line.LengthM - distance
			if speed > 0 {
				dt = deltaDistance / speed
			}
		}
		if dt < 0.01 {
			break
		}
		distance += deltaDistance
		elapsed += dt
		if speed > 0.5 {
			movingTime += dt
		}

		cadenceRho := math.Exp(-dt / 12)
		cadenceState = cadenceRho*cadenceState + math.Sqrt(1-cadenceRho*cadenceRho)*1.8*rng.NormFloat64()
		cadence := 0.0
		stride := 0.0
		if speed > 0.2 {
			cadence = profile.CadenceSPM*(0.88+0.12*speed/baseSpeed) + cadenceState + turn*1.5
			cadence = clamp(cadence, 90, 225)
			stride = speed * 60 / cadence
			stepAccumulator += cadence / 60 * dt
		}

		clean, currentHeading := line.At(distance)
		if degradedFor <= 0 {
			degradeRate := map[string]float64{"open": 1.0 / 1800, "mixed": 1.0 / 700, "urban": 1.0 / 300}[profile.GPSQuality]
			if rng.Float64() < degradeRate*dt {
				degradedFor = 6 + 22*rng.Float64()
			}
		} else {
			degradedFor -= dt
		}
		activeSigma := drift.sigmaM
		maxOffset := drift.maxOffsetM
		accuracyBase := drift.accuracyBaseM
		if degradedFor > 0 {
			// A weak-fix interval still wanders slightly more, but remains inside a
			// narrow road corridor instead of producing large off-road excursions.
			activeSigma *= 1.35
			maxOffset *= 1.35
			accuracyBase *= 1.45
		}
		gpsRho := math.Exp(-dt / drift.correlationSecs)
		noiseEast = gpsRho*noiseEast + math.Sqrt(1-gpsRho*gpsRho)*activeSigma*rng.NormFloat64()
		noiseNorth = gpsRho*noiseNorth + math.Sqrt(1-gpsRho*gpsRho)*activeSigma*rng.NormFloat64()
		noiseEast, noiseNorth = limitGPSOffset(noiseEast, noiseNorth, maxOffset)
		observed = geo.OffsetMeters(clean, noiseEast, noiseNorth)
		gpsDistance += geo.Distance(previousObserved, observed)
		previousObserved = observed
		accuracy := clamp(accuracyBase*(0.88+math.Abs(rng.NormFloat64())*0.16), 1.8, 16)

		slopeRho := math.Exp(-math.Max(deltaDistance, 0.1) / 80)
		slopeState = slopeRho*slopeState + math.Sqrt(1-slopeRho*slopeRho)*slopeSigma*rng.NormFloat64()
		slopeState = clamp(slopeState, -0.045, 0.045)
		altitude += slopeState * deltaDistance

		pace := 0.0
		if speed > 0.1 {
			pace = 1000 / speed
		}
		samples = append(samples, Sample{
			Index:               len(samples),
			RecordedAt:          start.Add(time.Duration(elapsed * float64(time.Second))),
			ElapsedS:            round(elapsed, 3),
			Lat:                 round(observed.Lat, 7),
			Lng:                 round(observed.Lng, 7),
			DistanceM:           round(distance, 3),
			GPSDistanceM:        round(gpsDistance, 3),
			SpeedMPS:            round(speed, 3),
			PaceSecPerKM:        round(pace, 2),
			CadenceSPM:          round(cadence, 2),
			Steps:               int(math.Floor(stepAccumulator)),
			StrideM:             round(stride, 3),
			AltitudeM:           round(altitude, 2),
			HorizontalAccuracyM: round(accuracy, 2),
			Satellites:          satelliteCount(profile.GPSQuality, accuracy, rng),
			HeadingDeg:          round(currentHeading, 1),
			Paused:              speed < 0.5,
		})
		previousSpeed = speed
	}

	_ = movingTime
	return samples
}

func limitGPSOffset(eastM, northM, limitM float64) (float64, float64) {
	if limitM <= 0 {
		return 0, 0
	}
	distance := math.Hypot(eastM, northM)
	if distance <= limitM || distance == 0 {
		return eastM, northM
	}
	scale := limitM / distance
	return eastM * scale, northM * scale
}

func summarize(samples []Sample) Summary {
	first, last := samples[0], samples[len(samples)-1]
	duration := last.ElapsedS - first.ElapsedS
	moving := 0.0
	maxSpeed := 0.0
	elevationGain, elevationLoss := 0.0, 0.0
	for i := 1; i < len(samples); i++ {
		dt := samples[i].ElapsedS - samples[i-1].ElapsedS
		if samples[i].SpeedMPS > 0.5 {
			moving += dt
		}
		maxSpeed = math.Max(maxSpeed, samples[i].SpeedMPS)
		deltaElevation := samples[i].AltitudeM - samples[i-1].AltitudeM
		if deltaElevation > 0.15 {
			elevationGain += deltaElevation
		} else if deltaElevation < -0.15 {
			elevationLoss -= deltaElevation
		}
	}
	distance := last.DistanceM
	steps := last.Steps
	averageSpeed, pace, cadence, stride := 0.0, 0.0, 0.0, 0.0
	if moving > 0 {
		averageSpeed = distance / moving
	}
	if distance > 0 {
		pace = moving / distance * 1000
	}
	if moving > 0 {
		cadence = float64(steps) / moving * 60
	}
	if steps > 0 {
		stride = distance / float64(steps)
	}
	return Summary{
		DistanceM:           round(distance, 2),
		GPSDistanceM:        round(last.GPSDistanceM, 2),
		DurationS:           round(duration, 2),
		MovingDurationS:     round(moving, 2),
		AveragePaceSecPerKM: round(pace, 2),
		AverageSpeedMPS:     round(averageSpeed, 3),
		MaxSpeedMPS:         round(maxSpeed, 3),
		TotalSteps:          steps,
		AverageCadenceSPM:   round(cadence, 2),
		AverageStrideM:      round(stride, 3),
		ElevationGainM:      round(elevationGain, 2),
		ElevationLossM:      round(elevationLoss, 2),
	}
}

func satelliteCount(quality string, accuracy float64, rng *mathrand.Rand) int {
	base := map[string]int{"open": 17, "mixed": 13, "urban": 10}[quality]
	count := base - int(math.Max(0, accuracy-3)/2) + int(math.Round(rng.NormFloat64()*1.2))
	if count < 4 {
		count = 4
	}
	if count > 24 {
		count = 24
	}
	return count
}

func randomSeed() int64 {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return int64(binary.LittleEndian.Uint64(bytes[:]))
	}
	return time.Now().UnixNano()
}

func clamp(v, low, high float64) float64 {
	return math.Max(low, math.Min(high, v))
}

func smoothstep(x float64) float64 { return x * x * (3 - 2*x) }

func round(v float64, digits int) float64 {
	power := math.Pow10(digits)
	return math.Round(v*power) / power
}
