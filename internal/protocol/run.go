package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
)

type RemotePlan struct {
	RunPlanCode    string  `json:"runPlanCode"`
	SemesterCode   string  `json:"semesterCode"`
	Name           string  `json:"name"`
	TargetDistance float64 `json:"targetDistance"`
}

type RemoteRunRules struct {
	PlanType                int
	TotalDistanceM          float64
	TotalTimes              int
	DayMaxTimes             int
	DayMaxDistanceM         float64
	WeekMinTimes            int
	WeekMaxTimes            int
	WeekMaxDistanceM        float64
	CompletionRate          float64
	DoneDistanceM           float64
	DoneTargetTimes         int
	MorningTimes            int
	MinDistanceM            float64
	MaxDistanceM            float64
	FastestPaceSecPerKM     float64
	SlowestPaceSecPerKM     float64
	MinCadenceSPM           float64
	MaxCadenceSPM           float64
	MaxSegmentStrideM       float64
	SegmentStrideExceptions int
	SwingAmplitudeThreshold int
	ActiveStartDate         string
	ActiveEndDate           string
	TimeWindows             []activity.RunTimeWindow
}

var runTimeRangePattern = regexp.MustCompile(`(?i)([01]?\d|2[0-4])[:：]([0-5]\d)(?::[0-5]\d)?\s*(?:-|—|–|~|～|至|到)\s*([01]?\d|2[0-4])[:：]([0-5]\d)(?::[0-5]\d)?`)

// CommonRunConfig is the account-independent running cadence returned by
// GET /common/getConfig. The Android client keeps the same local defaults and
// replaces them with this response at app startup.
type CommonRunConfig struct {
	RunFilterAccuracyM     float64 `json:"runFilterAccuracy"`
	POIUploadIntervalS     int     `json:"poiInterval"`
	PaceSegmentDistanceM   int64   `json:"paceInterval"`
	StepSegmentIntervalS   int     `json:"stepInterval"`
	PaceAbnormalTimeS      int     `json:"paceAbnormalTime"`
	AMAPGPSLevel           int     `json:"amapGPSLevel"`
	StrideSegmentDistanceM int     `json:"strideStatisticsInterval"`
	GPSAbnormalMaxTimes    int     `json:"gpsAbnormalMaxTimes"`
	GPSFilterAccuracyM     int     `json:"gpsFilterAccuracy"`
}

func DefaultCommonRunConfig() CommonRunConfig {
	return CommonRunConfig{
		RunFilterAccuracyM: 50, POIUploadIntervalS: 15,
		PaceSegmentDistanceM: 50, StepSegmentIntervalS: 20,
		PaceAbnormalTimeS: 120, AMAPGPSLevel: 3,
		StrideSegmentDistanceM: 100, GPSAbnormalMaxTimes: 4,
		GPSFilterAccuracyM: 150,
	}
}

func (v CommonRunConfig) withDefaults() CommonRunConfig {
	defaults := DefaultCommonRunConfig()
	if v.RunFilterAccuracyM <= 0 {
		v.RunFilterAccuracyM = defaults.RunFilterAccuracyM
	}
	if v.POIUploadIntervalS <= 0 {
		v.POIUploadIntervalS = defaults.POIUploadIntervalS
	}
	if v.PaceSegmentDistanceM <= 0 {
		v.PaceSegmentDistanceM = defaults.PaceSegmentDistanceM
	}
	if v.StepSegmentIntervalS <= 0 {
		v.StepSegmentIntervalS = defaults.StepSegmentIntervalS
	}
	if v.PaceAbnormalTimeS <= 0 {
		v.PaceAbnormalTimeS = defaults.PaceAbnormalTimeS
	}
	if v.AMAPGPSLevel <= 0 {
		v.AMAPGPSLevel = defaults.AMAPGPSLevel
	}
	if v.StrideSegmentDistanceM <= 0 {
		v.StrideSegmentDistanceM = defaults.StrideSegmentDistanceM
	}
	if v.GPSAbnormalMaxTimes <= 0 {
		v.GPSAbnormalMaxTimes = defaults.GPSAbnormalMaxTimes
	}
	if v.GPSFilterAccuracyM <= 0 {
		v.GPSFilterAccuracyM = defaults.GPSFilterAccuracyM
	}
	return v
}

type FencePoint struct {
	Code string  `json:"code,omitempty"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

type Fence struct {
	FenceCode     string       `json:"fenceCode"`
	SchoolCode    string       `json:"schoolCode"`
	SubSchoolCode string       `json:"subSchoolCode"`
	SchoolName    string       `json:"schoolName"`
	FenceName     string       `json:"fenceName"`
	Lat           float64      `json:"lat"`
	Lng           float64      `json:"lng"`
	FenceList     []FencePoint `json:"fenceList"`
}

type TargetPoint struct {
	Code       int64   `json:"code"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	ClockTime  int64   `json:"clockTime"`
	PassStatus bool    `json:"passStatus"`
	PointName  string  `json:"-"`
	SimpleName string  `json:"-"`
	Type       int     `json:"-"`
	Sort       int     `json:"-"`
	NFCEnable  bool    `json:"-"`
}

const (
	ClockModeFree    = 1
	ClockModeRandom  = 2
	ClockModeOrder   = 3
	ClockModeNoPoint = 10
)

// RunStartContext is available only after startSunRun_v2 succeeds. Keeping the
// trajectory builder behind this boundary prevents a route from being planned
// without the server-selected clock points.
type RunStartContext struct {
	RunRecordCode string
	Start         geo.Point
	ClockMode     int
	TargetPoints  []TargetPoint
}

type RunBuilder func(context.Context, RunStartContext) (activity.Run, error)

type SubmissionResult struct {
	RunRecordCode string          `json:"run_record_code"`
	Message       string          `json:"message"`
	ServerData    json.RawMessage `json:"server_data,omitempty"`
	SubmittedAt   time.Time       `json:"submitted_at"`
}

func (c *Client) CommonRunConfig(ctx context.Context, device Device, session Session) (CommonRunConfig, error) {
	envelope, err := c.get(ctx, "common/getConfig", device, nil, session)
	if err != nil {
		return CommonRunConfig{}, err
	}
	config, err := decodeData[CommonRunConfig](envelope)
	if err != nil {
		return CommonRunConfig{}, fmt.Errorf("解析运行公共配置: %w", err)
	}
	return config.withDefaults(), nil
}

func (c *Client) Plans(ctx context.Context, device Device, session Session, semesterCode string) ([]RemotePlan, error) {
	envelope, err := c.post(ctx, "run/plan/selectList", device, map[string]any{"semesterCode": semesterCode}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	items, err := unwrapArray(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("解析跑步计划: %w", err)
	}
	result := make([]RemotePlan, 0, len(items))
	for _, item := range items {
		plan := RemotePlan{
			RunPlanCode:    stringValue(item, "runPlanCode", "planCode", "code"),
			SemesterCode:   stringValue(item, "semesterCode"),
			Name:           stringValue(item, "runPlanName", "planName", "name"),
			TargetDistance: recursiveNumber(item, "targetDistance", "onceDistance", "singleDistance", "singleMinDistance", "minDistance"),
		}
		if plan.RunPlanCode != "" {
			result = append(result, plan)
		}
	}
	return result, nil
}

// RunRules reads the effective per-plan school rules. The plan list itself only
// contains identity/status fields in 8.7.9; distance, pace, cadence and stride
// limits come from querySunRunAbstractInfoV2.schoolDemandRule.
func (c *Client) RunRules(ctx context.Context, device Device, session Session, semesterCode, runPlanCode string) (RemoteRunRules, error) {
	envelope, err := c.post(ctx, "run/querySunRunAbstractInfoV2", device, map[string]any{
		"semesterCode": semesterCode,
		"runPlanCode":  runPlanCode,
		"sportType":    1,
	}, requestOptions{form: true, session: session})
	if err != nil {
		return RemoteRunRules{}, err
	}
	return parseRemoteRunRules(envelope.Data)
}

func parseRemoteRunRules(raw json.RawMessage) (RemoteRunRules, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return RemoteRunRules{}, fmt.Errorf("解析阳光跑规则: %w", err)
	}
	rule, ok := findObject(root, "schoolDemandRule")
	if !ok {
		return RemoteRunRules{}, errors.New("阳光跑详情未返回 schoolDemandRule")
	}
	minDistance := recursiveNumber(rule, "singleMinDistance")
	maxDistance := recursiveNumber(rule, "singleMaxDistance")
	// The app treats a zero singleMaxDistance as 10 km.
	if minDistance > 0 && maxDistance == 0 {
		maxDistance = 10000
	}
	if minDistance <= 0 || maxDistance < minDistance {
		return RemoteRunRules{}, fmt.Errorf("阳光跑规则返回无效距离范围 %.0f-%.0f 米", minDistance, maxDistance)
	}
	rawMinPace := recursiveNumber(rule, "minPace")
	rawMaxPace := recursiveNumber(rule, "maxPace")
	fastestPace, slowestPace := orderedPositive(rawMaxPace, rawMinPace)
	minCadence, maxCadence := orderedPositive(recursiveNumber(rule, "minFrequency"), recursiveNumber(rule, "maxFrequency"))
	strideCM := recursiveNumber(rule, "segmentStrideAvg")
	strideM := strideCM / 100
	// Float.MAX_VALUE is the local disabled sentinel.
	if strideM <= 0 || strideM > 10 {
		strideM = 0
	}
	done, _ := findObject(root, "studentDoneRuleInfo")
	return RemoteRunRules{
		PlanType:       int(math.Round(recursiveNumber(root, "planType"))),
		TotalDistanceM: recursiveNumber(rule, "totalDistance"),
		TotalTimes:     int(math.Round(recursiveNumber(rule, "totalTimes"))),
		DayMaxTimes:    int(math.Round(recursiveNumber(rule, "dayMaxTimes"))),
		// SchoolRule stores distance limits in metres. The Android formatter
		// divides these values by 1000 before rendering the "km" suffix.
		DayMaxDistanceM:  recursiveNumber(rule, "dayMaxDistance"),
		WeekMinTimes:     int(math.Round(recursiveNumber(rule, "weekMinTimes"))),
		WeekMaxTimes:     int(math.Round(recursiveNumber(rule, "weekMaxTimes"))),
		WeekMaxDistanceM: recursiveNumber(rule, "weekMaxDistance"),
		CompletionRate:   recursiveNumber(done, "completionRate"),
		DoneDistanceM:    recursiveNumber(done, "doneDistance"),
		DoneTargetTimes:  int(math.Round(recursiveNumber(done, "doneTargetTimes"))),
		MorningTimes:     int(math.Round(recursiveNumber(done, "morningTimes"))),
		MinDistanceM:     minDistance, MaxDistanceM: maxDistance,
		FastestPaceSecPerKM: fastestPace, SlowestPaceSecPerKM: slowestPace,
		MinCadenceSPM: minCadence, MaxCadenceSPM: maxCadence,
		MaxSegmentStrideM:       strideM,
		SegmentStrideExceptions: int(math.Round(recursiveNumber(rule, "segmentStrideExceptionNum"))),
		SwingAmplitudeThreshold: int(math.Round(recursiveNumber(rule, "swingAmplitudeThreshold"))),
		ActiveStartDate:         stringValue(rule, "startDate"),
		ActiveEndDate:           stringValue(rule, "endDate"),
		TimeWindows:             parseRunTimeWindows(rule["timeFragment"]),
	}, nil
}

func parseRunTimeWindows(value any) []activity.RunTimeWindow {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	windows := make([]activity.RunTimeWindow, 0, len(items))
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rawType := strings.TrimSpace(stringValue(fields, "timeType"))
		dayType := normalizeRunTimeType(rawType)
		if dayType == "" {
			continue
		}
		times := strings.TrimSpace(stringValue(fields, "times"))
		for _, match := range runTimeRangePattern.FindAllStringSubmatch(times, -1) {
			startHour, _ := strconv.Atoi(match[1])
			startMinute, _ := strconv.Atoi(match[2])
			endHour, _ := strconv.Atoi(match[3])
			endMinute, _ := strconv.Atoi(match[4])
			if startHour >= 24 || endHour > 24 || (endHour == 24 && endMinute != 0) {
				continue
			}
			windows = append(windows, activity.RunTimeWindow{
				DayType: dayType, RawTimeType: rawType, Times: times,
				StartMinute: startHour*60 + startMinute,
				EndMinute:   endHour*60 + endMinute,
				TagType:     int(math.Round(recursiveNumber(fields, "tagType"))),
			})
		}
	}
	return windows
}

func normalizeRunTimeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "every_day", "everyday", "daily":
		return "every_day"
	case "1", "workday", "weekday":
		return "workday"
	case "2", "weekend":
		return "weekend"
	default:
		return ""
	}
}

func (c *Client) Fences(ctx context.Context, device Device, session Session, schoolCode string) ([]Fence, error) {
	envelope, err := c.post(ctx, "school/querySchoolFences", device, map[string]any{"schoolCode": schoolCode}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	items, err := unwrapArray(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("解析学校围栏: %w", err)
	}
	result := make([]Fence, 0, len(items))
	for _, item := range items {
		fence := Fence{
			FenceCode:     stringValue(item, "fenceCode", "code"),
			SchoolCode:    stringValue(item, "schoolCode"),
			SubSchoolCode: stringValue(item, "subSchoolCode"),
			SchoolName:    stringValue(item, "schoolName"),
			FenceName:     stringValue(item, "fenceName", "name", "subSchoolName"),
			Lat:           floatValue(item, "lat", "latitude"),
			Lng:           floatValue(item, "lng", "longitude"),
		}
		if list, ok := item["fenceList"].([]any); ok {
			for _, rawPoint := range list {
				pointMap, ok := rawPoint.(map[string]any)
				if !ok {
					continue
				}
				point := FencePoint{Code: stringValue(pointMap, "code"), Lat: floatValue(pointMap, "lat", "latitude"), Lng: floatValue(pointMap, "lng", "longitude")}
				if (geo.Point{Lat: point.Lat, Lng: point.Lng}).Valid() {
					fence.FenceList = append(fence.FenceList, point)
				}
			}
		}
		if fence.Lat == 0 && len(fence.FenceList) > 0 {
			fence.Lat = fence.FenceList[0].Lat
			fence.Lng = fence.FenceList[0].Lng
		}
		if fence.FenceCode != "" {
			result = append(result, fence)
		}
	}
	return result, nil
}

// SubmitRun speaks the 8.7.9 protocol directly. It uses the official sequence
// (plan/fence/preflight/start, incremental evidence uploads, finish) and does
// not invoke the legacy panel or a Python helper.
func (c *Client) SubmitRun(ctx context.Context, device Device, session Session, selectedPlanCode, selectedSemesterCode, selectedFenceCode string, selectedDistanceM float64, seed int64, faceInput *FaceInput, buildRun RunBuilder) (SubmissionResult, activity.Run, error) {
	if session.Token == "" || session.SAToken == "" {
		return SubmissionResult{}, activity.Run{}, errors.New("账号会话尚未登录")
	}
	if buildRun == nil {
		return SubmissionResult{}, activity.Run{}, errors.New("起跑后轨迹生成器未配置")
	}
	if math.IsNaN(selectedDistanceM) || math.IsInf(selectedDistanceM, 0) || selectedDistanceM < 300 || selectedDistanceM > 100_000 {
		return SubmissionResult{}, activity.Run{}, errors.New("所选跑步距离必须在 300 至 100000 米之间")
	}
	commonConfig, err := c.CommonRunConfig(ctx, device, session)
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("读取客户端采样/上传配置: %w", err)
	}

	if selectedPlanCode == "" || selectedFenceCode == "" {
		return SubmissionResult{}, activity.Run{}, errors.New("账号尚未选择目标应用中的跑步计划和围栏区域")
	}
	plans, err := c.Plans(ctx, device, session, selectedSemesterCode)
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("获取跑步计划: %w", err)
	}
	if len(plans) == 0 {
		return SubmissionResult{}, activity.Run{}, errors.New("当前账号没有可用的阳光跑计划")
	}
	var plan RemotePlan
	for _, candidate := range plans {
		if candidate.RunPlanCode == selectedPlanCode {
			plan = candidate
			break
		}
	}
	if plan.RunPlanCode == "" {
		return SubmissionResult{}, activity.Run{}, errors.New("已选跑步计划已不在账号可用列表中，请重新同步")
	}
	fences, err := c.Fences(ctx, device, session, session.SchoolCode)
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("获取学校围栏: %w", err)
	}
	if len(fences) == 0 {
		return SubmissionResult{}, activity.Run{}, errors.New("当前学校没有返回可用围栏")
	}
	var fence Fence
	for _, candidate := range fences {
		if candidate.FenceCode == selectedFenceCode {
			fence = candidate
			break
		}
	}
	if fence.FenceCode == "" {
		return SubmissionResult{}, activity.Run{}, errors.New("已选围栏区域已不在账号可用列表中，请重新同步")
	}
	if !(geo.Point{Lat: fence.Lat, Lng: fence.Lng}).Valid() {
		return SubmissionResult{}, activity.Run{}, errors.New("选中围栏缺少有效起点坐标")
	}

	if _, err := c.post(ctx, "school/filterUsableFence", device, map[string]any{"fenceCodeList": []string{fence.FenceCode}}, requestOptions{session: session}); err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("围栏可用性检查: %w", err)
	}
	if err := c.finishUnfinishedRunBeforeStart(ctx, device, session); err != nil {
		return SubmissionResult{}, activity.Run{}, err
	}

	targetDistance := strconv.Itoa(int(math.Round(selectedDistanceM)))
	preflightValues := map[string]any{
		"schoolCode": session.SchoolCode, "subSchoolCode": fence.SubSchoolCode,
		"targetDistance": targetDistance, "activityCode": "", "runPlanCode": plan.RunPlanCode,
		"fenceCode": fence.FenceCode, "sportType": "1",
	}
	preflightEnvelope, err := c.post(ctx, "run/checkSunRunConfig", device, preflightValues, requestOptions{form: true, session: session})
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("起跑配置检查: %w", err)
	}
	preflight, err := decodeData[CheckRunConfig](preflightEnvelope)
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("解析起跑人脸配置: %w", err)
	}
	facePlan, err := newFaceReplayPlan(preflight, faceInput, seed)
	if err != nil {
		return SubmissionResult{}, activity.Run{}, err
	}

	startEnvelope, err := c.post(ctx, "run/startSunRun_v2", device, map[string]any{
		"runPlanCode": plan.RunPlanCode, "schoolCode": session.SchoolCode, "fenceCode": fence.FenceCode,
		"activityCode": "", "lng": formatCoordinate(fence.Lng), "lat": formatCoordinate(fence.Lat),
		"sportType": "1", "targetDistance": targetDistance, "useCreditSword": "false",
	}, requestOptions{form: true, session: session})
	if err != nil {
		return SubmissionResult{}, activity.Run{}, fmt.Errorf("申请起跑: %w", err)
	}
	runRecordCode := findStringInJSON(startEnvelope.Data, "runRecordCode")
	if runRecordCode == "" {
		return SubmissionResult{}, activity.Run{}, errors.New("起跑响应没有返回 runRecordCode")
	}
	abortStartedRun := func(cause error) error {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		defer cancel()
		if cleanupErr := c.abortSunRun(cleanupContext, device, session, runRecordCode, abnormalMissingRunData); cleanupErr != nil {
			return fmt.Errorf("%w；清理已起跑会话失败: %v", cause, cleanupErr)
		}
		return cause
	}
	clockMode, _ := strconv.Atoi(findStringInJSON(startEnvelope.Data, "clockMode"))
	targetPoints := parseTargetPoints(startEnvelope.Data)
	if clockMode == ClockModeNoPoint {
		targetPoints = nil
	}
	run, err := buildRun(ctx, RunStartContext{
		RunRecordCode: runRecordCode,
		Start:         geo.Point{Lat: fence.Lat, Lng: fence.Lng},
		ClockMode:     clockMode,
		TargetPoints:  append([]TargetPoint(nil), targetPoints...),
	})
	if err != nil {
		return SubmissionResult{}, activity.Run{}, abortStartedRun(fmt.Errorf("起跑后规划并生成轨迹: %w", err))
	}
	if !run.Validation.Valid || len(run.Samples) < 2 {
		return SubmissionResult{}, run, abortStartedRun(errors.New("起跑后生成的跑步数据未通过一致性检查"))
	}
	distanceDeltaM := run.Summary.DistanceM - selectedDistanceM
	if distanceDeltaM < -0.5 || distanceDeltaM > 50.05 {
		return SubmissionResult{}, run, abortStartedRun(fmt.Errorf("实际距离与所选距离偏差 %.1f 米，要求范围为 +0 至 +50 米", distanceDeltaM))
	}
	// The app starts collecting only after startSunRun_v2 succeeds. Rebase the
	// newly generated relative timeline here and replay it at wall-clock speed.
	alignRunTimeline(&run, c.clock().UTC())
	replay, err := c.replayRun(ctx, device, session, runRecordCode, &run, commonConfig, clockMode, targetPoints, facePlan)
	if err != nil {
		return SubmissionResult{}, run, err
	}
	passedPoints := replay.TargetPoints

	timestamp := strconv.FormatInt(c.clock().UnixMilli(), 10)
	finishValues := buildFinishRunValues(runRecordCode, run, commonConfig, passedPoints)
	finishValues["runImgRecord"] = RunImageRecord(runRecordCode, AppCode, AppVersion, timestamp, c.environment)
	finishValues["timestamp"] = timestamp
	if len(replay.PendingPOIs) > 0 {
		finishValues["pois"] = replay.PendingPOIs
	}
	if len(replay.PendingSteps) > 0 {
		finishValues["stepList"] = replay.PendingSteps
	}
	if len(replay.PendingPaces) > 0 {
		finishValues["paceList"] = replay.PendingPaces
	}
	if facePlan != nil {
		finishValues["faceCheckRecordList"] = replay.FaceRecords
	}
	finishEnvelope, err := c.post(ctx, "run/finishSunRun_v2", device, finishValues, requestOptions{session: session})
	if err != nil {
		return SubmissionResult{}, run, fmt.Errorf("提交跑步结算: %w", err)
	}
	message := strings.TrimSpace(finishEnvelope.Message)
	if message == "" {
		message = "提交成功"
	}
	return SubmissionResult{RunRecordCode: runRecordCode, Message: message, ServerData: cloneRaw(finishEnvelope.Data), SubmittedAt: c.clock().UTC()}, run, nil
}

func (c *Client) abortSunRun(ctx context.Context, device Device, session Session, runRecordCode string, abnormalCode int) error {
	timestamp := strconv.FormatInt(c.clock().UnixMilli(), 10)
	values := map[string]any{
		"runRecordCode": runRecordCode,
		"abnormalCodes": []int{abnormalCode},
		"runImgRecord":  RunImageRecord(runRecordCode, AppCode, AppVersion, timestamp, c.environment),
		"timestamp":     timestamp,
	}
	_, err := c.post(ctx, "run/abnormalFinishSunRun", device, values, requestOptions{session: session})
	return err
}

func unwrapArray(raw json.RawMessage) ([]map[string]any, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	for {
		switch typed := value.(type) {
		case []any:
			result := make([]map[string]any, 0, len(typed))
			for _, item := range typed {
				if object, ok := item.(map[string]any); ok {
					result = append(result, object)
				}
			}
			return result, nil
		case map[string]any:
			advanced := false
			for _, key := range []string{"list", "records", "items", "data"} {
				if nested, ok := typed[key]; ok {
					value, advanced = nested, true
					break
				}
			}
			if !advanced {
				return nil, errors.New("响应不含列表字段")
			}
		default:
			return nil, errors.New("响应列表格式未知")
		}
	}
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := values[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case json.Number:
			return value.String()
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	return ""
}

func floatValue(values map[string]any, keys ...string) float64 {
	text := stringValue(values, keys...)
	value, _ := strconv.ParseFloat(text, 64)
	return value
}

func recursiveNumber(value any, keys ...string) float64 {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if candidate, ok := typed[key]; ok {
				switch number := candidate.(type) {
				case float64:
					return number
				case string:
					if parsed, err := strconv.ParseFloat(number, 64); err == nil {
						return parsed
					}
				}
			}
		}
		for _, child := range typed {
			if result := recursiveNumber(child, keys...); result != 0 {
				return result
			}
		}
	case []any:
		for _, child := range typed {
			if result := recursiveNumber(child, keys...); result != 0 {
				return result
			}
		}
	}
	return 0
}

func findObject(value any, key string) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if candidate, ok := typed[key].(map[string]any); ok {
			return candidate, true
		}
		for _, child := range typed {
			if result, ok := findObject(child, key); ok {
				return result, true
			}
		}
	case []any:
		for _, child := range typed {
			if result, ok := findObject(child, key); ok {
				return result, true
			}
		}
	}
	return nil, false
}

func orderedPositive(first, second float64) (float64, float64) {
	if first <= 0 || second <= 0 {
		return 0, 0
	}
	return math.Min(first, second), math.Max(first, second)
}

func findStringInJSON(raw json.RawMessage, key string) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(node any) string {
		switch typed := node.(type) {
		case map[string]any:
			if value, ok := typed[key]; ok {
				if text := fmt.Sprint(value); text != "" && text != "<nil>" {
					return text
				}
			}
			for _, child := range typed {
				if result := walk(child); result != "" {
					return result
				}
			}
		case []any:
			for _, child := range typed {
				if result := walk(child); result != "" {
					return result
				}
			}
		}
		return ""
	}
	return walk(value)
}

func parseTargetPoints(raw json.RawMessage) []TargetPoint {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	var rawPoints []any
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if list, ok := typed["targetPoints"].([]any); ok && rawPoints == nil {
				rawPoints = list
				return
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	result := make([]TargetPoint, 0, len(rawPoints))
	for _, item := range rawPoints {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		code, hasCode := int64Field(object, "code")
		point := TargetPoint{
			Code: code, Lat: floatValue(object, "lat"), Lng: floatValue(object, "lng"),
			PointName: stringValue(object, "pointName"), SimpleName: stringValue(object, "simpleName"),
			Type: int(numberValue(object, "type")), Sort: int(numberValue(object, "sort")),
			NFCEnable: boolValue(object, "nfcEnable"),
		}
		if hasCode && (geo.Point{Lat: point.Lat, Lng: point.Lng}).Valid() {
			result = append(result, point)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Sort == result[j].Sort {
			return false
		}
		return result[i].Sort < result[j].Sort
	})
	return result
}

func int64Field(values map[string]any, key string) (int64, bool) {
	value, ok := values[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberValue(values map[string]any, key string) float64 {
	return floatValue(values, key)
}

func boolValue(values map[string]any, key string) bool {
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	case json.Number:
		parsed, _ := value.Int64()
		return parsed != 0
	case float64:
		return value != 0
	default:
		return false
	}
}

const paceUploadCheckIntervalS = 15

type timedPayload struct {
	ReadyElapsedS float64
	Payload       map[string]any
}

type replayResult struct {
	PendingPOIs  []map[string]any
	PendingPaces []map[string]any
	PendingSteps []map[string]any
	FaceRecords  []FaceCheckRecord
	TargetPoints []TargetPoint
}

type faceReplayPlan struct {
	Config  CheckRunConfig
	Input   FaceInput
	Records []FaceCheckRecord
}

func newFaceReplayPlan(config CheckRunConfig, input *FaceInput, seed int64) (*faceReplayPlan, error) {
	// The Android finish path only attaches faceCheckRecordList for sunshine
	// runs (runScene 20) with faceAuth enabled.
	if !config.FaceAuth || (config.RunScene != 0 && config.RunScene != 20) {
		return nil, nil
	}
	if input == nil || len(input.Image) == 0 {
		return nil, errors.New("该跑步计划要求人脸核验；请先在账号人脸设置中同步底图或上传人脸")
	}
	baseURL := strings.TrimSpace(config.FaceImg)
	if baseURL == "" {
		baseURL = strings.TrimSpace(input.BaseURL)
	}
	if config.FaceModelType != 2 && baseURL == "" {
		return nil, errors.New("该跑步计划要求人脸核验，但账号没有可用于比对的绑定底图")
	}
	inputCopy := *input
	inputCopy.BaseURL = baseURL
	if config.SimilarityValue <= 0 {
		config.SimilarityValue = 50
	}
	random := rand.New(rand.NewSource(seed ^ int64(len(config.FaceCheckConfigList)+1)*0x5deece66d))
	records := make([]FaceCheckRecord, 0, len(config.FaceCheckConfigList))
	for index, check := range config.FaceCheckConfigList {
		lower, upper := check.LowerLimit, check.UpperLimit
		if lower < 0 {
			lower = 0
		}
		distance := lower
		// java.util.Random.nextInt(upper-lower)+lower uses an exclusive
		// upper bound; retain that exact range behavior.
		if upper > lower {
			distance = random.Intn(upper-lower) + lower
		}
		records = append(records, FaceCheckRecord{CheckIndex: index + 1, RandomDistance: distance})
	}
	return &faceReplayPlan{Config: config, Input: inputCopy, Records: records}, nil
}

func alignRunTimeline(run *activity.Run, startedAt time.Time) {
	run.StartedAt = startedAt
	for index := range run.Samples {
		run.Samples[index].RecordedAt = startedAt.Add(time.Duration(run.Samples[index].ElapsedS * float64(time.Second)))
	}
	run.FinishedAt = run.Samples[len(run.Samples)-1].RecordedAt
}

func (c *Client) replayRun(ctx context.Context, device Device, session Session, runRecordCode string, run *activity.Run, config CommonRunConfig, clockMode int, targetPoints []TargetPoint, facePlan *faceReplayPlan) (replayResult, error) {
	paceEvents, paceTail := buildAppPaceSegments(run.Samples, config.PaceSegmentDistanceM)
	stepEvents, stepTail := buildAppStepSegments(run.Samples, config.StepSegmentIntervalS)
	strideEvents := buildAppStrideSegments(run.Samples, config.StrideSegmentDistanceM)

	pointState := append([]TargetPoint(nil), targetPoints...)
	for index := range pointState {
		pointState[index].PassStatus = false
		pointState[index].ClockTime = 0
	}
	pendingPOIs := make([]map[string]any, 0, 8)
	pendingPaces := make([]map[string]any, 0, 4)
	pendingSteps := make([]map[string]any, 0, 4)
	paceCursor, stepCursor, strideCursor := 0, 0, 0
	nextPOIUpload := float64(config.POIUploadIntervalS)
	nextPaceCheck := float64(paceUploadCheckIntervalS)
	var previousSample activity.Sample
	var previousDispatch time.Time

	for sampleIndex := range run.Samples {
		sample := run.Samples[sampleIndex]
		deadline := sample.RecordedAt
		if sampleIndex > 0 {
			deadline = pacedReplayDeadline(deadline, previousDispatch, sample.ElapsedS-previousSample.ElapsedS)
		}
		if err := waitUntil(ctx, deadline, c.clock); err != nil {
			return replayResult{}, err
		}
		// Use the actual dispatch instant as the anti-catch-up anchor. If a request
		// or the process stalls, later samples are shifted forward instead of being
		// drained immediately in a burst to catch the original wall-clock line.
		collectedAt := c.clock().UTC()
		sample.RecordedAt = collectedAt
		run.Samples[sampleIndex].RecordedAt = collectedAt
		createdAt := c.clock().UTC()
		dispatchedAt := createdAt
		previousDispatch = dispatchedAt
		previousSample = sample
		pendingPOIs = append(pendingPOIs, buildAppPOI(sample, createdAt))

		if facePlan != nil {
			for recordIndex := range facePlan.Records {
				record := &facePlan.Records[recordIndex]
				if record.HasChecked || int(math.Round(sample.DistanceM)) < record.RandomDistance {
					continue
				}
				if err := c.performFaceCheck(ctx, device, session, runRecordCode, sample, facePlan, record); err != nil {
					return replayResult{}, err
				}
				// Face recognition pauses the app's run timer. Move the replay
				// dispatch anchor to the end of recognition so later samples do
				// not catch up in a burst.
				previousDispatch = c.clock()
			}
		}

		for paceCursor < len(paceEvents) && paceEvents[paceCursor].ReadyElapsedS <= sample.ElapsedS+0.001 {
			pendingPaces = append(pendingPaces, paceEvents[paceCursor].Payload)
			paceCursor++
		}
		newStep := false
		for stepCursor < len(stepEvents) && stepEvents[stepCursor].ReadyElapsedS <= sample.ElapsedS+0.001 {
			pendingSteps = append(pendingSteps, stepEvents[stepCursor].Payload)
			stepCursor++
			newStep = true
		}
		for strideCursor < len(strideEvents) && strideEvents[strideCursor].ReadyElapsedS <= sample.ElapsedS+0.001 {
			_, uploadErr := c.post(ctx, "run/uploadStrideRecord", device, map[string]any{
				"runRecordCode":  runRecordCode,
				"strideInterval": config.StrideSegmentDistanceM,
				"strideList":     []map[string]any{strideEvents[strideCursor].Payload},
			}, requestOptions{session: session})
			if IsSessionExpired(uploadErr) {
				return replayResult{}, uploadErr
			}
			strideCursor++
		}

		pointChanged := markReachedTargetPoints(clockMode, pointState, sample)
		if pointChanged {
			_, uploadErr := c.post(ctx, "run/uploadPassPoint", device, map[string]any{
				"runRecordCode": runRecordCode, "targetPoints": pointState,
			}, requestOptions{session: session})
			if IsSessionExpired(uploadErr) {
				return replayResult{}, uploadErr
			}
		}

		if sample.ElapsedS+0.001 >= nextPOIUpload {
			// ApiDataComponent.c0 requires more than three pending valid POIs.
			if len(pendingPOIs) > 3 {
				if _, uploadErr := c.post(ctx, "run/uploadRunRecord", device, map[string]any{
					"runRecordCode": runRecordCode, "pois": pendingPOIs,
				}, requestOptions{session: session}); uploadErr == nil {
					pendingPOIs = pendingPOIs[:0]
				} else if IsSessionExpired(uploadErr) {
					return replayResult{}, uploadErr
				}
			}
			for nextPOIUpload <= sample.ElapsedS+0.001 {
				nextPOIUpload += float64(config.POIUploadIntervalS)
			}
		}

		if sample.ElapsedS+0.001 >= nextPaceCheck {
			// l0 checks every 15 s but only sends once three completed distance
			// segments are waiting.
			if len(pendingPaces) >= 3 {
				if _, uploadErr := c.post(ctx, "run/uploadPaceRecord", device, map[string]any{
					"runRecordCode": runRecordCode,
					"paceInterval":  config.PaceSegmentDistanceM,
					"paceList":      pendingPaces,
				}, requestOptions{session: session}); uploadErr == nil {
					pendingPaces = pendingPaces[:0]
				} else if IsSessionExpired(uploadErr) {
					return replayResult{}, uploadErr
				}
			}
			for nextPaceCheck <= sample.ElapsedS+0.001 {
				nextPaceCheck += paceUploadCheckIntervalS
			}
		}

		// Step upload has no separate wall-clock gate. The service asks after a
		// new step section is stored, and ApiDataComponent requires >=3 rows.
		if newStep && len(pendingSteps) >= 3 {
			if _, uploadErr := c.post(ctx, "run/uploadStepsRecord", device, map[string]any{
				"runRecordCode": runRecordCode,
				"stepInterval":  config.StepSegmentIntervalS,
				"stepList":      pendingSteps,
			}, requestOptions{session: session}); uploadErr == nil {
				pendingSteps = pendingSteps[:0]
			} else if IsSessionExpired(uploadErr) {
				return replayResult{}, uploadErr
			}
		}
	}
	if len(run.Samples) > 0 {
		run.FinishedAt = run.Samples[len(run.Samples)-1].RecordedAt
	}

	// FinishSunRun adds only local rows that have not already been uploaded,
	// plus the current incomplete pace/step tails.
	if paceTail != nil {
		pendingPaces = append(pendingPaces, paceTail)
	}
	if stepTail != nil {
		pendingSteps = append(pendingSteps, stepTail)
	}
	result := replayResult{
		PendingPOIs: pendingPOIs, PendingPaces: pendingPaces, PendingSteps: pendingSteps,
		TargetPoints: pointState,
	}
	if facePlan != nil {
		result.FaceRecords = append([]FaceCheckRecord(nil), facePlan.Records...)
	}
	return result, nil
}

func markReachedTargetPoints(clockMode int, points []TargetPoint, sample activity.Sample) bool {
	if clockMode == ClockModeNoPoint || len(points) == 0 {
		return false
	}
	sequential := clockMode == ClockModeOrder || clockMode == ClockModeRandom
	for index := range points {
		if points[index].PassStatus {
			continue
		}
		distance := geo.Distance(
			geo.Point{Lat: points[index].Lat, Lng: points[index].Lng},
			geo.Point{Lat: sample.Lat, Lng: sample.Lng},
		)
		if distance <= 20 {
			points[index].PassStatus = true
			points[index].ClockTime = sample.RecordedAt.UnixMilli()
			return true
		}
		if sequential {
			// Ordered and random modes expose one active point at a time.
			return false
		}
	}
	return false
}

func (c *Client) performFaceCheck(ctx context.Context, device Device, session Session, runRecordCode string, sample activity.Sample, plan *faceReplayPlan, record *FaceCheckRecord) error {
	popTime := c.clock().UnixMilli()
	checkDistance := int64(math.Round(sample.DistanceM))
	checkLng, checkLat := sample.Lng, sample.Lat
	record.PopTime, record.CheckDistance = &popTime, &checkDistance
	record.CheckLng, record.CheckLat = &checkLng, &checkLat

	if plan.Config.FaceModelType == 2 {
		if c.face == nil {
			record.HasChecked = true
			record.FaceCheckErrorCode = "LOCAL_ENGINE_UNAVAILABLE"
			return fmt.Errorf("第 %d 次人脸核验的本地模型尚未初始化", record.CheckIndex)
		}
		reference := plan.Input.ReferenceImage
		if len(reference) == 0 {
			reference = plan.Input.Image
		}
		comparison, compareErr := c.face.Compare(ctx, reference, plan.Input.Image)
		if compareErr != nil {
			record.HasChecked = true
			record.FaceCheckErrorCode = "LOCAL_COMPARE_FAILED"
			return fmt.Errorf("第 %d 次本地人脸比对失败: %w", record.CheckIndex, compareErr)
		}
		confidence := comparison.Confidence
		record.Confidence = &confidence
		record.HasChecked = true
		if confidence < float64(plan.Config.SimilarityValue) {
			record.FaceCheckErrorCode = "SIMILARITY_LOW"
			return fmt.Errorf("第 %d 次人脸相似度 %.2f 低于要求 %d", record.CheckIndex, confidence, plan.Config.SimilarityValue)
		}
		runFaceURL, uploadErr := c.UploadRunFace(ctx, device, session, plan.Input.Image, plan.Input.ContentType)
		if uploadErr != nil {
			record.FaceCheckErrorCode = "UPLOAD_FAILED"
			return fmt.Errorf("第 %d 次人脸核验上传失败: %w", record.CheckIndex, uploadErr)
		}
		record.RunFaceImg = runFaceURL
		record.RunFaceUpload = runFaceURL != ""
		record.FinishFaceCheck = true
		return nil
	}

	runFaceURL, uploadErr := c.UploadRunFace(ctx, device, session, plan.Input.Image, plan.Input.ContentType)
	if uploadErr != nil {
		record.HasChecked = true
		record.FaceCheckErrorCode = "UPLOAD_FAILED"
		return fmt.Errorf("第 %d 次人脸核验上传失败: %w", record.CheckIndex, uploadErr)
	}
	record.RunFaceImg = runFaceURL
	record.RunFaceUpload = runFaceURL != ""

	comparison, compareErr := c.CompareFace(ctx, device, session, runFaceURL, plan.Input.BaseURL)
	if compareErr != nil {
		record.HasChecked = true
		record.FaceCheckErrorCode = "COMPARE_FAILED"
		return fmt.Errorf("第 %d 次人脸比对失败: %w", record.CheckIndex, compareErr)
	}
	confidence := comparison.Confidence
	record.Confidence = &confidence
	record.FaceRequestID = comparison.RequestID
	record.HasChecked = true
	if confidence < float64(plan.Config.SimilarityValue) {
		record.FaceCheckErrorCode = "SIMILARITY_LOW"
		return fmt.Errorf("第 %d 次人脸相似度 %.2f 低于要求 %d", record.CheckIndex, confidence, plan.Config.SimilarityValue)
	}
	record.FinishFaceCheck = true
	// The current protocol exposes a separate body/liveness switch. The
	// account image fallback has no camera motion stream, so only the face
	// match fields are populated; nullable body fields remain absent just as
	// they do when the app receives no body result.
	_ = runRecordCode
	return nil
}

func pacedReplayDeadline(nominal, previousDispatch time.Time, sampleGapS float64) time.Time {
	if previousDispatch.IsZero() || sampleGapS <= 0 {
		return nominal
	}
	minimum := previousDispatch.Add(time.Duration(sampleGapS * float64(time.Second)))
	if minimum.After(nominal) {
		return minimum
	}
	return nominal
}

func waitUntil(ctx context.Context, target time.Time, clock func() time.Time) error {
	for {
		delay := target.Sub(clock())
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func buildAppPOI(sample activity.Sample, createdAt time.Time) map[string]any {
	// RunLatLng marks speed/isValid/runCode as serialize=false. Keep the exact
	// public wire fields rather than adding simulator-only properties.
	return map[string]any{
		"index": sample.Index + 1,
		"lat":   sample.Lat, "lng": sample.Lng,
		"accuracy":    sample.HorizontalAccuracyM,
		"collectTime": sample.RecordedAt.UnixMilli(),
		"createTime":  createdAt.UnixMilli(),
		"runTime":     int(math.Round(sample.ElapsedS)),
		"satellites":  sample.Satellites,
		"state":       1, "offFenceDisM": 0,
	}
}

func buildAppPaceSegments(samples []activity.Sample, intervalM int64) ([]timedPayload, map[string]any) {
	if len(samples) < 2 || intervalM <= 0 {
		return nil, nil
	}
	startIndex := 0
	recordIndex := 1
	result := make([]timedPayload, 0, int(samples[len(samples)-1].DistanceM/float64(intervalM)))
	for index := 1; index < len(samples); index++ {
		if sampleDistance(samples[index])-sampleDistance(samples[startIndex]) <= intervalM {
			continue
		}
		endIndex := index - 1
		if endIndex > startIndex {
			result = append(result, timedPayload{
				ReadyElapsedS: samples[index].ElapsedS,
				Payload:       pacePayload(recordIndex, samples[startIndex], samples[endIndex]),
			})
			recordIndex++
		}
		startIndex = endIndex
	}
	tail := pacePayload(recordIndex, samples[startIndex], samples[len(samples)-1])
	if payloadIsEmpty(tail, "distance", "time", "stepCount") {
		tail = nil
	}
	return result, tail
}

func pacePayload(index int, start, end activity.Sample) map[string]any {
	startDistance, endDistance := sampleDistance(start), sampleDistance(end)
	startTime, endTime := sampleElapsed(start), sampleElapsed(end)
	return map[string]any{
		"index":     index,
		"startTime": startTime, "endTime": endTime, "time": endTime - startTime,
		"startDistance": startDistance, "endDistance": endDistance, "distance": endDistance - startDistance,
		"startStepCount": start.Steps, "endStepCount": end.Steps, "stepCount": end.Steps - start.Steps,
	}
}

func buildAppStepSegments(samples []activity.Sample, intervalS int) ([]timedPayload, map[string]any) {
	if len(samples) < 2 || intervalS <= 0 {
		return nil, nil
	}
	segments := activity.BuildTimeSegments(samples, float64(intervalS))
	result := make([]timedPayload, 0, len(segments))
	var tail map[string]any
	for _, segment := range segments {
		payload := map[string]any{
			"index":     segment.Index,
			"startTime": int64(math.Round(segment.StartElapsedS)),
			"endTime":   int64(math.Round(segment.EndElapsedS)),
			"time":      int64(math.Round(segment.DurationS)),
			"startStep": segment.StartSteps, "endStep": segment.EndSteps, "step": segment.Steps,
		}
		if segment.DurationS+0.001 < float64(intervalS) {
			tail = payload
			continue
		}
		result = append(result, timedPayload{ReadyElapsedS: segment.EndElapsedS, Payload: payload})
	}
	return result, tail
}

func buildAppStrideSegments(samples []activity.Sample, intervalM int) []timedPayload {
	if len(samples) < 2 || intervalM <= 0 {
		return nil
	}
	baseline := -1
	for index, sample := range samples {
		if sampleDistance(sample) > 0 {
			baseline = index
			break
		}
	}
	if baseline < 0 {
		return nil
	}
	result := make([]timedPayload, 0, int(samples[len(samples)-1].DistanceM/float64(intervalM)))
	segmentIndex := 0
	for index := baseline + 1; index < len(samples); index++ {
		distance := sampleDistance(samples[index]) - sampleDistance(samples[baseline])
		if distance < int64(intervalM) {
			continue
		}
		steps := samples[index].Steps - samples[baseline].Steps
		if steps > 0 {
			rawStrideCM := float64(distance) * 100 / float64(steps)
			strideCM := math.Floor(rawStrideCM*10) / 10
			result = append(result, timedPayload{ReadyElapsedS: samples[index].ElapsedS, Payload: map[string]any{
				"distance": distance, "index": segmentIndex,
				"time":   sampleElapsed(samples[index]) - sampleElapsed(samples[baseline]),
				"stride": strideCM, "stepCount": steps,
			}})
		}
		segmentIndex++
		baseline = index
	}
	return result
}

func sampleDistance(sample activity.Sample) int64 { return int64(math.Round(sample.DistanceM)) }
func sampleElapsed(sample activity.Sample) int64  { return int64(math.Round(sample.ElapsedS)) }

func payloadIsEmpty(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case int:
			if value != 0 {
				return false
			}
		case int64:
			if value != 0 {
				return false
			}
		}
	}
	return true
}

func pauseCount(samples []activity.Sample) int {
	count, paused := 0, false
	for _, sample := range samples {
		if sample.Paused && !paused {
			count++
		}
		paused = sample.Paused
	}
	return count
}

// buildFinishRunValues mirrors DataComponent.t1(true, 0). The native method
// uses the active run timer for duration and reports paused time separately;
// the API wrapper adds the pending evidence lists, runImgRecord and timestamp.
func buildFinishRunValues(runRecordCode string, run activity.Run, config CommonRunConfig, targetPoints []TargetPoint) map[string]any {
	activeDurationS := run.Summary.MovingDurationS
	if activeDurationS <= 0 {
		activeDurationS = run.Summary.DurationS
	}
	pauseSeconds := math.Max(0, run.Summary.DurationS-activeDurationS)
	values := map[string]any{
		"runRecordCode": runRecordCode,
		"duration":      strconv.FormatInt(int64(math.Round(activeDurationS)), 10),
		"distance":      strconv.FormatInt(int64(math.Round(run.Summary.DistanceM)), 10),
		"totalStep":     strconv.Itoa(max(0, run.Summary.TotalSteps)),
		"stepInterval":  config.StepSegmentIntervalS,
		"paceInterval":  config.PaceSegmentDistanceM,
		"alignType":     config.AMAPGPSLevel,
		"pauseCount":    pauseCount(run.Samples),
		"pauseTimes":    int64(math.Round(pauseSeconds)),
		"sportType":     1,
		"status":        1,
	}
	if len(targetPoints) > 0 {
		values["targetPoints"] = targetPoints
	}
	return values
}

func formatCoordinate(value float64) string {
	return strconv.FormatFloat(value, 'f', 7, 64)
}
