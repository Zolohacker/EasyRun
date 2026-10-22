package activity

import (
	"errors"
	"math"
	mathrand "math/rand/v2"
)

const (
	MiddleSeventyPolicy  = "middle_70_percent"
	ManualDistancePolicy = "manual_distance_100m"
	DistanceStepM        = 100.0
	PlanTypeDefault      = 1
	PlanTypeByTimes      = 2
	PlanTypeByDistance   = 3
)

// AutomationTargetDistance applies the Android client's plan accounting mode
// to an automated run. Count-based plans only need one qualifying run, so the
// platform always uses the smallest selectable single-run distance. Distance-
// based and legacy plans retain the explicitly selected 100 metre slot.
func AutomationTargetDistance(fallbackDistanceM float64, rules RunRules, selectedDistanceM float64) (float64, error) {
	low, high, err := SelectableDistanceRange(fallbackDistanceM, rules)
	if err != nil {
		return 0, err
	}
	if rules.PlanType == PlanTypeByTimes {
		return low, nil
	}
	if selectedDistanceM == 0 {
		return high, nil
	}
	return selectedDistanceM, nil
}

// SampleAutomationTargets is the single entry point used by both job
// validation and execution, preventing a stale or hand-crafted count-plan job
// from running above its minimum qualifying distance.
func SampleAutomationTargets(fallbackDistanceM float64, base Profile, rules RunRules, selectedDistanceM float64, seed int64) (TargetSelection, error) {
	distanceM, err := AutomationTargetDistance(fallbackDistanceM, rules, selectedDistanceM)
	if err != nil {
		return TargetSelection{}, err
	}
	return SampleTargetsForDistance(fallbackDistanceM, base, rules, distanceM, seed)
}

// SelectableDistanceRange returns the server distance interval snapped inward
// to 100 metre choices. The highest returned value is the platform default.
func SelectableDistanceRange(fallbackDistanceM float64, rules RunRules) (float64, float64, error) {
	low, high := fallbackDistanceM, fallbackDistanceM
	if validRange(rules.MinDistanceM, rules.MaxDistanceM) {
		low, high = rules.MinDistanceM, rules.MaxDistanceM
	}
	if low < 300 || high > 100000 || high < low {
		return 0, 0, errors.New("跑步计划没有返回有效的单次距离范围")
	}
	selectableLow := math.Ceil((low-1e-9)/DistanceStepM) * DistanceStepM
	selectableHigh := math.Floor((high+1e-9)/DistanceStepM) * DistanceStepM
	if selectableHigh < selectableLow {
		return 0, 0, errors.New("服务端距离范围内没有可用的 100 米档位")
	}
	return selectableLow, selectableHigh, nil
}

// SampleTargetsForDistance keeps pace/cadence/stride inside their central 70%
// windows while taking distance from an explicit 100 metre server-range slot.
// selectedDistanceM=0 selects the highest available slot.
func SampleTargetsForDistance(fallbackDistanceM float64, base Profile, rules RunRules, selectedDistanceM float64, seed int64) (TargetSelection, error) {
	selection, err := SampleTargets(fallbackDistanceM, base, rules, seed)
	if err != nil {
		return TargetSelection{}, err
	}
	low, high, err := SelectableDistanceRange(fallbackDistanceM, rules)
	if err != nil {
		return TargetSelection{}, err
	}
	if selectedDistanceM == 0 {
		selectedDistanceM = high
	}
	selectedDistanceM = roundTo(selectedDistanceM, 1)
	if selectedDistanceM < low-0.01 || selectedDistanceM > high+0.01 {
		return TargetSelection{}, errors.New("所选距离超出服务端允许范围")
	}
	if math.Abs(selectedDistanceM/DistanceStepM-math.Round(selectedDistanceM/DistanceStepM)) > 1e-6 {
		return TargetSelection{}, errors.New("所选距离必须使用 100 米档位")
	}
	selection.Policy = ManualDistancePolicy
	selection.TargetDistanceM = selectedDistanceM
	selection.Window.MinDistanceM = low
	selection.Window.MaxDistanceM = high
	return selection, nil
}

// SampleTargets selects a mutually consistent distance, pace, cadence and
// stride. Each independent value is sampled inside the central 70% of the
// effective server interval (15% trimmed from both ends). Stride is not an
// independent upstream range: the server returns an upper threshold, while
// the lower/upper feasible interval is derived from pace and cadence using
// speed = stride * cadence / 60.
func SampleTargets(fallbackDistanceM float64, base Profile, rules RunRules, seed int64) (TargetSelection, error) {
	profile, err := NormalizeProfile(base)
	if err != nil {
		return TargetSelection{}, err
	}
	rng := mathrand.New(mathrand.NewPCG(uint64(seed), uint64(seed)^0x517cc1b727220a95))
	selection := TargetSelection{Policy: MiddleSeventyPolicy, TargetDistanceM: fallbackDistanceM, Profile: profile}

	if validRange(rules.MinDistanceM, rules.MaxDistanceM) {
		selection.Window.MinDistanceM, selection.Window.MaxDistanceM = middleSeventy(rules.MinDistanceM, rules.MaxDistanceM)
		selection.TargetDistanceM = roundTo(randomBetween(rng, selection.Window.MinDistanceM, selection.Window.MaxDistanceM), 1)
	}
	if selection.TargetDistanceM < 300 || selection.TargetDistanceM > 100000 {
		return TargetSelection{}, errors.New("跑步计划没有返回有效的单次距离范围")
	}

	paceOK := validRange(rules.FastestPaceSecPerKM, rules.SlowestPaceSecPerKM)
	cadenceOK := validRange(rules.MinCadenceSPM, rules.MaxCadenceSPM)
	if paceOK {
		selection.Window.FastestPaceSecPerKM, selection.Window.SlowestPaceSecPerKM = middleSeventy(rules.FastestPaceSecPerKM, rules.SlowestPaceSecPerKM)
	}
	if cadenceOK {
		selection.Window.MinCadenceSPM, selection.Window.MaxCadenceSPM = middleSeventy(rules.MinCadenceSPM, rules.MaxCadenceSPM)
	}

	if paceOK && cadenceOK {
		strideMin := strideFor(rules.SlowestPaceSecPerKM, rules.MaxCadenceSPM)
		strideMax := strideFor(rules.FastestPaceSecPerKM, rules.MinCadenceSPM)
		if rules.MaxSegmentStrideM > 0 && rules.MaxSegmentStrideM < strideMax {
			strideMax = rules.MaxSegmentStrideM
		}
		if !validRange(strideMin, strideMax) {
			return TargetSelection{}, errors.New("服务端配速、步频与步幅规则没有共同可行区间")
		}
		selection.Window.MinStrideM, selection.Window.MaxStrideM = middleSeventy(strideMin, strideMax)

		matched := false
		for attempt := 0; attempt < 256; attempt++ {
			pace := randomBetween(rng, selection.Window.FastestPaceSecPerKM, selection.Window.SlowestPaceSecPerKM)
			speed := 1000 / pace
			cadenceLow := math.Max(selection.Window.MinCadenceSPM, speed*60/selection.Window.MaxStrideM)
			cadenceHigh := math.Min(selection.Window.MaxCadenceSPM, speed*60/selection.Window.MinStrideM)
			if cadenceLow > cadenceHigh {
				continue
			}
			cadence := randomBetween(rng, cadenceLow, cadenceHigh)
			stride := speed * 60 / cadence
			selection.Profile.TargetPaceSecPerKM = math.Round(pace)
			selection.Profile.CadenceSPM = math.Round(cadence)
			selection.Profile.TargetStrideM = roundTo(stride, 3)
			matched = true
			break
		}
		if !matched {
			return TargetSelection{}, errors.New("在规则中间 70% 内找不到一致的配速、步频和步幅组合")
		}
	} else if paceOK && rules.MaxSegmentStrideM > 0 {
		// Some schools return 0/0 for minFrequency/maxFrequency, which means
		// cadence validation is disabled. In that case do not present 168 as a
		// server rule: sample pace first, build a speed-dependent natural running
		// cadence band, choose stride inside the central 70% of the resulting
		// feasible interval and derive cadence from the exact kinematic identity.
		// This local biomechanics band is not exposed as an upstream school rule.
		pace := math.Round(randomBetween(rng, selection.Window.FastestPaceSecPerKM, selection.Window.SlowestPaceSecPerKM))
		speed := 1000 / pace
		cadenceLow, cadenceHigh := naturalCadenceBand(speed, rules.MaxSegmentStrideM)
		if !validRange(cadenceLow, cadenceHigh) {
			return TargetSelection{}, errors.New("服务端步幅上限与自然步频没有可行组合")
		}
		strideLow := speed * 60 / cadenceHigh
		strideHigh := math.Min(rules.MaxSegmentStrideM, speed*60/cadenceLow)
		if !validRange(strideLow, strideHigh) {
			return TargetSelection{}, errors.New("服务端步幅上限与配速规则没有可行组合")
		}
		selection.Window.MinStrideM, selection.Window.MaxStrideM = middleSeventy(strideLow, strideHigh)
		stride := randomBetween(rng, selection.Window.MinStrideM, selection.Window.MaxStrideM)
		cadence := speed * 60 / stride
		selection.Profile.TargetPaceSecPerKM = pace
		selection.Profile.CadenceSPM = math.Round(cadence)
		selection.Profile.TargetStrideM = roundTo(strideFor(selection.Profile.TargetPaceSecPerKM, selection.Profile.CadenceSPM), 3)
	} else {
		if paceOK {
			selection.Profile.TargetPaceSecPerKM = math.Round(randomBetween(rng, selection.Window.FastestPaceSecPerKM, selection.Window.SlowestPaceSecPerKM))
		}
		if cadenceOK {
			selection.Profile.CadenceSPM = math.Round(randomBetween(rng, selection.Window.MinCadenceSPM, selection.Window.MaxCadenceSPM))
		}
		selection.Profile.TargetStrideM = roundTo(strideFor(selection.Profile.TargetPaceSecPerKM, selection.Profile.CadenceSPM), 3)
		if rules.MaxSegmentStrideM > 0 && selection.Profile.TargetStrideM >= rules.MaxSegmentStrideM {
			return TargetSelection{}, errors.New("默认运动参数超过服务端分段步幅上限")
		}
	}

	return selection, nil
}

// naturalCadenceBand supplies a conservative running envelope only when the
// school has explicitly disabled its frequency range (0/0). The centre rises
// with speed instead of using one fixed cadence. A tight stride ceiling may
// raise the lower bound, up to the generator's validated 230 spm limit.
func naturalCadenceBand(speedMPS, maxStrideM float64) (float64, float64) {
	preferred := clamp(150+18*(speedMPS-1.7), 150, 190)
	low := math.Max(145, preferred-9)
	high := math.Min(205, preferred+9)
	if maxStrideM > 0 {
		low = math.Max(low, speedMPS*60/maxStrideM)
	}
	if low > high {
		high = math.Min(230, low+8)
	}
	if low > 230 || high < low {
		return 0, 0
	}
	return low, high
}

func middleSeventy(low, high float64) (float64, float64) {
	margin := (high - low) * 0.15
	return low + margin, high - margin
}

func validRange(low, high float64) bool {
	return low > 0 && high >= low && !math.IsNaN(low) && !math.IsNaN(high) && !math.IsInf(low, 0) && !math.IsInf(high, 0)
}

func strideFor(paceSecPerKM, cadenceSPM float64) float64 {
	if paceSecPerKM <= 0 || cadenceSPM <= 0 {
		return 0
	}
	return 60000 / (paceSecPerKM * cadenceSPM)
}

func randomBetween(rng *mathrand.Rand, low, high float64) float64 {
	if high <= low {
		return low
	}
	return low + rng.Float64()*(high-low)
}

func roundTo(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}
