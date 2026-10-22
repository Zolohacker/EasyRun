package activity

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

type RemoteRecordMetrics struct {
	DistanceM          float64 `json:"distance_m"`
	DurationS          float64 `json:"duration_s"`
	ReportedPace       float64 `json:"reported_pace_sec_per_km"`
	DerivedPace        float64 `json:"derived_pace_sec_per_km"`
	TotalSteps         int     `json:"total_steps"`
	ReportedCadence    float64 `json:"reported_cadence_spm"`
	DerivedCadence     float64 `json:"derived_cadence_spm"`
	DerivedStrideM     float64 `json:"derived_stride_m"`
	TrackDistanceM     float64 `json:"track_distance_m"`
	PointCount         int     `json:"point_count"`
	StepSegmentCount   int     `json:"step_segment_count"`
	PaceSegmentCount   int     `json:"pace_segment_count"`
	MedianSamplePeriod float64 `json:"median_sample_period_s"`
}

type RemoteRecordCheck struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type RemoteRecordInspection struct {
	Engine     string              `json:"engine"`
	Valid      bool                `json:"valid"`
	Score      int                 `json:"score"`
	RuleSource string              `json:"rule_source,omitempty"`
	Metrics    RemoteRecordMetrics `json:"metrics"`
	Checks     []RemoteRecordCheck `json:"checks"`
	Findings   []Finding           `json:"findings"`
}

// InspectRemoteRecord applies the same distance/time, pace, cadence, stride,
// segment and GPS consistency principles used by Validate to a record returned
// by run/runRecordDetail. Remote details are aggregate/segment based rather
// than full generated Sample values, so every check reports whether it was
// applied, skipped, warned or failed.
func InspectRemoteRecord(raw json.RawMessage, rules RunRules, ruleSource string) RemoteRecordInspection {
	result := RemoteRecordInspection{
		Engine: "activity-preflight/v1", Valid: true, Score: 100,
		RuleSource: ruleSource, Checks: make([]RemoteRecordCheck, 0, 12), Findings: []Finding{},
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		result.Valid = false
		result.Score = 0
		result.Checks = append(result.Checks, RemoteRecordCheck{Code: "record_json", Label: "记录结构", Status: "error", Message: "记录详情不是有效 JSON"})
		result.Findings = append(result.Findings, Finding{Level: "error", Code: "record_json", Message: err.Error()})
		return result
	}
	record := unwrapRemoteObject(value)
	if record == nil {
		result.Valid = false
		result.Score = 0
		result.Checks = append(result.Checks, RemoteRecordCheck{Code: "record_shape", Label: "记录结构", Status: "error", Message: "记录详情缺少数据对象"})
		result.Findings = append(result.Findings, Finding{Level: "error", Code: "record_shape", Message: "记录详情缺少数据对象"})
		return result
	}

	distance := firstPositiveNumber(record, "validDistance", "distance", "runDistance")
	duration := firstPositiveNumber(record, "duration", "runDuration", "time")
	reportedPace := firstPositiveNumber(record, "pace", "averagePace", "avgPace")
	steps := int(math.Round(firstPositiveNumber(record, "totalStep", "totalSteps", "stepCount", "steps")))
	reportedCadence := firstPositiveNumber(record, "frequency", "cadence", "averageCadence", "avgCadence")
	derivedPace, derivedCadence, derivedStride := 0.0, 0.0, 0.0
	if distance > 0 && duration > 0 {
		derivedPace = duration / distance * 1000
	}
	if duration > 0 && steps > 0 {
		derivedCadence = float64(steps) / duration * 60
	}
	if distance > 0 && steps > 0 {
		derivedStride = distance / float64(steps)
	}
	stepList := remoteNumberSlice(record["stepList"])
	paceList := remoteNumberSlice(record["paceList"])
	points := remotePoints(record["pois"])
	if len(points) == 0 {
		for _, key := range []string{"pointList", "trackList", "locationList", "routePoints"} {
			points = remotePoints(record[key])
			if len(points) > 0 {
				break
			}
		}
	}
	trackDistance, medianPeriod, invalidPoints, orderErrors := remoteTrackStats(points)
	result.Metrics = RemoteRecordMetrics{
		DistanceM: distance, DurationS: duration, ReportedPace: reportedPace, DerivedPace: derivedPace,
		TotalSteps: steps, ReportedCadence: reportedCadence, DerivedCadence: derivedCadence,
		DerivedStrideM: derivedStride, TrackDistanceM: trackDistance, PointCount: len(points),
		StepSegmentCount: len(stepList), PaceSegmentCount: len(paceList), MedianSamplePeriod: medianPeriod,
	}

	add := func(code, label, status, message string) {
		result.Checks = append(result.Checks, RemoteRecordCheck{Code: code, Label: label, Status: status, Message: message})
		if status == "error" || status == "warning" {
			level := status
			result.Findings = append(result.Findings, Finding{Level: level, Code: code, Message: message})
		}
		if status == "error" {
			result.Valid = false
		}
	}

	if distance <= 0 || duration <= 0 || steps <= 0 {
		add("core_fields", "核心字段", "error", "距离、时长或总步数缺失")
	} else {
		add("core_fields", "核心字段", "pass", fmt.Sprintf("距离 %.0fm · 时长 %.0fs · %d 步", distance, duration, steps))
	}
	if reportedPace > 0 && derivedPace > 0 {
		delta := math.Abs(reportedPace - derivedPace)
		if delta > math.Max(5, derivedPace*0.02) {
			add("pace_consistency", "配速一致性", "error", fmt.Sprintf("上报配速与距离/时长推导值相差 %.1f 秒/公里", delta))
		} else {
			add("pace_consistency", "配速一致性", "pass", fmt.Sprintf("上报 %.0f 秒/公里，推导 %.1f 秒/公里", reportedPace, derivedPace))
		}
	} else {
		add("pace_consistency", "配速一致性", "skipped", "缺少可用于推导的配速数据")
	}
	if reportedCadence > 0 && derivedCadence > 0 {
		delta := math.Abs(reportedCadence - derivedCadence)
		if delta > math.Max(3, derivedCadence*0.025) {
			add("cadence_consistency", "步频一致性", "error", fmt.Sprintf("上报步频与总步数/时长推导值相差 %.1f spm", delta))
		} else {
			add("cadence_consistency", "步频一致性", "pass", fmt.Sprintf("上报 %.0f spm，推导 %.1f spm", reportedCadence, derivedCadence))
		}
	} else {
		add("cadence_consistency", "步频一致性", "skipped", "服务端未返回可比对的步频")
	}
	if derivedStride > 0 {
		if rules.MaxSegmentStrideM > 0 && derivedStride > rules.MaxSegmentStrideM+0.001 {
			add("stride_limit", "步幅阈值", "error", fmt.Sprintf("推导步幅 %.3fm 超过计划阈值 %.3fm", derivedStride, rules.MaxSegmentStrideM))
		} else if rules.MaxSegmentStrideM > 0 {
			add("stride_limit", "步幅阈值", "pass", fmt.Sprintf("推导步幅 %.3fm，计划阈值 %.3fm", derivedStride, rules.MaxSegmentStrideM))
		} else {
			add("stride_limit", "步幅关系", "pass", fmt.Sprintf("距离/步数推导步幅 %.3fm", derivedStride))
		}
	} else {
		add("stride_limit", "步幅关系", "skipped", "缺少距离或步数")
	}

	if validRange(rules.MinDistanceM, rules.MaxDistanceM) && distance > 0 {
		if distance < rules.MinDistanceM-0.01 || distance > rules.MaxDistanceM+50.01 {
			add("distance_rule", "计划距离", "error", fmt.Sprintf("记录 %.0fm，计划允许 %.0f–%.0fm（上界含 +50m）", distance, rules.MinDistanceM, rules.MaxDistanceM))
		} else {
			add("distance_rule", "计划距离", "pass", fmt.Sprintf("记录 %.0fm 位于 %.0f–%.0fm 允许区间", distance, rules.MinDistanceM, rules.MaxDistanceM+50))
		}
	} else {
		add("distance_rule", "计划距离", "skipped", "账号计划未返回距离区间")
	}
	paceForRule := reportedPace
	if paceForRule <= 0 {
		paceForRule = derivedPace
	}
	if validRange(rules.FastestPaceSecPerKM, rules.SlowestPaceSecPerKM) && paceForRule > 0 {
		if paceForRule < rules.FastestPaceSecPerKM || paceForRule > rules.SlowestPaceSecPerKM {
			add("pace_rule", "计划配速", "error", fmt.Sprintf("记录 %.0f 秒/公里，计划允许 %.0f–%.0f", paceForRule, rules.FastestPaceSecPerKM, rules.SlowestPaceSecPerKM))
		} else {
			add("pace_rule", "计划配速", "pass", fmt.Sprintf("记录 %.0f 秒/公里位于计划区间", paceForRule))
		}
	} else {
		add("pace_rule", "计划配速", "skipped", "账号计划未返回配速区间")
	}
	cadenceForRule := reportedCadence
	if cadenceForRule <= 0 {
		cadenceForRule = derivedCadence
	}
	if validRange(rules.MinCadenceSPM, rules.MaxCadenceSPM) && cadenceForRule > 0 {
		if cadenceForRule < rules.MinCadenceSPM || cadenceForRule > rules.MaxCadenceSPM {
			add("cadence_rule", "计划步频", "error", fmt.Sprintf("记录 %.0f spm，计划允许 %.0f–%.0f", cadenceForRule, rules.MinCadenceSPM, rules.MaxCadenceSPM))
		} else {
			add("cadence_rule", "计划步频", "pass", fmt.Sprintf("记录 %.0f spm 位于计划区间", cadenceForRule))
		}
	} else {
		add("cadence_rule", "计划步频", "skipped", "服务端未启用步频范围校验")
	}

	if len(stepList) > 0 && steps > 0 {
		sum := 0.0
		for _, segment := range stepList {
			sum += segment
		}
		delta := math.Abs(sum - float64(steps))
		if delta > math.Max(3, float64(steps)*0.01) {
			add("step_segments", "步数分段", "error", fmt.Sprintf("分段合计 %.0f 与总步数 %d 相差 %.0f", sum, steps, delta))
		} else {
			add("step_segments", "步数分段", "pass", fmt.Sprintf("%d 段合计 %.0f 步，与总步数一致", len(stepList), sum))
		}
	} else {
		add("step_segments", "步数分段", "skipped", "记录未返回步数分段")
	}
	if len(paceList) > 0 && paceForRule > 0 {
		average := 0.0
		for _, segment := range paceList {
			average += segment
		}
		average /= float64(len(paceList))
		delta := math.Abs(average - paceForRule)
		if delta > math.Max(12, paceForRule*0.05) {
			add("pace_segments", "配速分段", "warning", fmt.Sprintf("分段均值与总配速相差 %.1f 秒/公里", delta))
		} else {
			add("pace_segments", "配速分段", "pass", fmt.Sprintf("%d 段均值 %.1f 秒/公里", len(paceList), average))
		}
	} else {
		add("pace_segments", "配速分段", "skipped", "记录未返回配速分段")
	}

	if len(points) < 2 {
		add("track_points", "轨迹点", "error", "有效定位点少于 2 个")
	} else if invalidPoints > 0 || orderErrors > 0 {
		add("track_points", "轨迹点", "error", fmt.Sprintf("%d 个坐标无效，%d 处索引或时间逆序", invalidPoints, orderErrors))
	} else {
		add("track_points", "轨迹点", "pass", fmt.Sprintf("%d 个定位点，索引与时间单调递增", len(points)))
	}
	if medianPeriod > 0 {
		expected := rules.LocationSampleIntervalS
		if expected <= 0 {
			expected = 3
		}
		if math.Abs(medianPeriod-expected) > 1.25 {
			add("sample_period", "采样周期", "warning", fmt.Sprintf("中位采样间隔 %.2fs，客户端规则 %.2fs", medianPeriod, expected))
		} else {
			add("sample_period", "采样周期", "pass", fmt.Sprintf("中位采样间隔 %.2fs，符合 %.2fs 节奏", medianPeriod, expected))
		}
	} else {
		add("sample_period", "采样周期", "skipped", "轨迹未提供可比对的采样时间")
	}
	if trackDistance > 0 && distance > 0 {
		ratio := trackDistance / distance
		if ratio < 0.55 || ratio > 1.55 {
			add("track_distance", "轨迹距离", "error", fmt.Sprintf("轨迹几何距离 %.0fm / 上报距离 %.0fm，比值 %.3f", trackDistance, distance, ratio))
		} else if ratio < 0.82 || ratio > 1.22 {
			add("track_distance", "轨迹距离", "warning", fmt.Sprintf("轨迹几何距离 %.0fm / 上报距离 %.0fm，比值 %.3f", trackDistance, distance, ratio))
		} else {
			add("track_distance", "轨迹距离", "pass", fmt.Sprintf("轨迹几何距离 %.0fm / 上报距离 %.0fm，比值 %.3f", trackDistance, distance, ratio))
		}
	} else {
		add("track_distance", "轨迹距离", "skipped", "缺少有效轨迹或距离")
	}
	if warning, ok := remoteBool(record, "warn"); ok && warning {
		add("server_status", "服务端状态", "warning", "服务端记录已标记 warn")
	} else if status, ok := remoteNumber(record, "sunRunRecordStatus", "status"); ok {
		message, warning := describeRemoteRunStatus(int(math.Round(status)), record)
		if warning {
			add("server_status", "服务端状态", "warning", message)
		} else {
			add("server_status", "服务端状态", "pass", message)
		}
	} else {
		add("server_status", "服务端状态", "pass", "服务端记录未标记异常")
	}

	applicable, pointsEarned := 0, 0.0
	for _, check := range result.Checks {
		switch check.Status {
		case "pass":
			applicable++
			pointsEarned++
		case "warning":
			applicable++
			pointsEarned += 0.5
		case "error":
			applicable++
		}
	}
	if applicable > 0 {
		result.Score = int(math.Round(pointsEarned / float64(applicable) * 100))
	}
	return result
}

// describeRemoteRunStatus mirrors the status text used by the Android client.
// In particular, 3 and 4 are workflow states rather than opaque error codes:
// 3 is invalid but appealable, while 4 is invalid with an appeal in progress.
func describeRemoteRunStatus(status int, record map[string]any) (string, bool) {
	message := ""
	warning := false
	switch status {
	case 0:
		message, warning = "服务端正在处理记录，结果尚未最终确定", true
	case 1:
		message = "服务端已判定计入成绩"
	case 2:
		message, warning = "服务端判定本次跑步无效，且不可申诉", true
	case 3:
		message, warning = "服务端判定本次跑步无效，可查看原因并发起申诉", true
	case 4:
		message, warning = "服务端判定本次跑步无效，申诉处理中", true
	case 5:
		message, warning = "服务端判定本次跑步无效，且不可申诉", true
	case 6:
		message = "服务端已判定计入成绩"
	case 10:
		message, warning = "服务端正在进行无感识别，结果尚未最终确定", true
	default:
		message, warning = fmt.Sprintf("服务端返回未识别的记录状态（代码 %d）", status), true
	}
	if reasons := remoteInvalidReasons(record); len(reasons) > 0 {
		message += "；原因：" + strings.Join(reasons, "；")
	}
	return message, warning
}

func remoteInvalidReasons(record map[string]any) []string {
	reasons := make([]string, 0, 4)
	seen := make(map[string]struct{})
	appendReason := func(value any) {
		text, ok := value.(string)
		text = strings.TrimSpace(text)
		if !ok || text == "" {
			return
		}
		if _, exists := seen[text]; exists {
			return
		}
		seen[text] = struct{}{}
		reasons = append(reasons, text)
	}
	if values, ok := record["invalidReasons"].([]any); ok {
		for _, value := range values {
			if reason, ok := value.(map[string]any); ok {
				appendReason(reason["invalidDetail"])
			}
		}
	}
	if values, ok := record["statusLogRecords"].([]any); ok {
		for _, value := range values {
			log, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if remarks, ok := log["remark"].([]any); ok {
				for _, remark := range remarks {
					appendReason(remark)
				}
			}
		}
	}
	return reasons
}

type remotePoint struct {
	Point       geo.Point
	Index       float64
	HasIndex    bool
	CollectedMS float64
	RunTimeS    float64
	Valid       bool
}

func unwrapRemoteObject(value any) map[string]any {
	for depth := 0; depth < 5; depth++ {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		if _, hasPOI := object["pois"]; hasPOI {
			return object
		}
		if _, hasCode := object["runRecordCode"]; hasCode {
			return object
		}
		next, exists := object["data"]
		if !exists {
			return object
		}
		value = next
	}
	return nil
}

func firstPositiveNumber(object map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number, ok := toRemoteNumber(object[key]); ok && number > 0 {
			return number
		}
	}
	return 0
}

func remoteNumber(object map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if number, ok := toRemoteNumber(object[key]); ok {
			return number, true
		}
	}
	return 0, false
}

func toRemoteNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return value, err == nil
	default:
		return 0, false
	}
}

func remoteBool(object map[string]any, key string) (bool, bool) {
	value, exists := object[key]
	if !exists {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(typed)
		return parsed, err == nil
	default:
		return false, false
	}
}

func remoteNumberSlice(value any) []float64 {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]float64, 0, len(values))
	for _, item := range values {
		if number, valid := toRemoteNumber(item); valid {
			result = append(result, number)
		}
	}
	return result
}

func remotePoints(value any) []remotePoint {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]remotePoint, 0, len(values))
	for _, item := range values {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		lat, latOK := remoteNumber(object, "lat", "latitude", "y")
		lng, lngOK := remoteNumber(object, "lng", "lon", "longitude", "x")
		index, indexOK := remoteNumber(object, "index")
		collected, collectedOK := remoteNumber(object, "collectTime", "collectedAt", "timestamp")
		runTime, _ := remoteNumber(object, "runTime", "elapsed", "elapsedTime")
		if collectedOK && collected > 0 && collected < 1e12 {
			collected *= 1000
		}
		point := geo.Point{Lat: lat, Lng: lng}
		result = append(result, remotePoint{Point: point, Index: index, HasIndex: indexOK, CollectedMS: collected, RunTimeS: runTime, Valid: latOK && lngOK && point.Valid()})
	}
	return result
}

func remoteTrackStats(points []remotePoint) (distance, medianPeriod float64, invalidPoints, orderErrors int) {
	intervals := make([]float64, 0, len(points)-1)
	for index, point := range points {
		if !point.Valid {
			invalidPoints++
		}
		if index == 0 {
			continue
		}
		previous := points[index-1]
		if point.Valid && previous.Valid {
			distance += geo.Distance(previous.Point, point.Point)
		}
		if point.HasIndex && previous.HasIndex && point.Index <= previous.Index {
			orderErrors++
		}
		if point.CollectedMS > 0 && previous.CollectedMS > 0 {
			delta := (point.CollectedMS - previous.CollectedMS) / 1000
			if delta <= 0 {
				orderErrors++
			} else if delta <= 30 {
				intervals = append(intervals, delta)
			}
		} else if point.RunTimeS > 0 && previous.RunTimeS >= 0 {
			delta := point.RunTimeS - previous.RunTimeS
			if delta <= 0 {
				orderErrors++
			} else if delta <= 30 {
				intervals = append(intervals, delta)
			}
		}
	}
	if len(intervals) > 0 {
		sort.Float64s(intervals)
		middle := len(intervals) / 2
		medianPeriod = intervals[middle]
		if len(intervals)%2 == 0 {
			medianPeriod = (intervals[middle-1] + intervals[middle]) / 2
		}
	}
	return distance, medianPeriod, invalidPoints, orderErrors
}
