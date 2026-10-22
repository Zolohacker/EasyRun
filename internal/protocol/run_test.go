package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
)

func TestAppPaceSegmentsUseFiftyMetreStateMachineAndKeepTail(t *testing.T) {
	samples := linearSamples(15, 3, 10, 5)
	events, tail := buildAppPaceSegments(samples, 50)
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	if got := events[0].Payload["distance"]; got != int64(50) {
		t.Fatalf("first distance=%v", got)
	}
	if events[0].ReadyElapsedS != 18 {
		t.Fatalf("first ready=%.1f", events[0].ReadyElapsedS)
	}
	if tail == nil || tail["startDistance"] != int64(100) || tail["endDistance"] != int64(150) {
		t.Fatalf("tail=%v", tail)
	}
}

func TestAppStepSegmentsUploadInGroupsOfThreeAndKeepPartialTail(t *testing.T) {
	samples := linearSamples(65, 1, 2, 3)
	events, tail := buildAppStepSegments(samples, 20)
	if len(events) != 3 {
		t.Fatalf("events=%d", len(events))
	}
	if events[2].ReadyElapsedS != 60 {
		t.Fatalf("third ready=%.1f", events[2].ReadyElapsedS)
	}
	if tail == nil || tail["time"] != int64(5) {
		t.Fatalf("tail=%v", tail)
	}
}

func TestAppStrideUsesLiveDistanceIntervalAndFlooredCentimetres(t *testing.T) {
	samples := linearSamples(24, 3, 10, 5)
	events := buildAppStrideSegments(samples, 200)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	payload := events[0].Payload
	if payload["distance"] != int64(200) || payload["index"] != 0 || payload["stepCount"] != 100 {
		t.Fatalf("payload=%v", payload)
	}
	if math.Abs(payload["stride"].(float64)-200) > 1e-9 {
		t.Fatalf("stride=%v", payload["stride"])
	}
}

func TestAppPOIOmitsLocalOnlyRunLatLngFields(t *testing.T) {
	sample := linearSamples(1, 3, 10, 5)[1]
	poi := buildAppPOI(sample, sample.RecordedAt.Add(9*time.Millisecond))
	for _, forbidden := range []string{"speed", "timestamp", "runTimestamp", "isValid", "bearing"} {
		if _, ok := poi[forbidden]; ok {
			t.Fatalf("unexpected wire field %s", forbidden)
		}
	}
	for _, required := range []string{"index", "collectTime", "createTime", "runTime", "accuracy", "satellites", "state", "offFenceDisM"} {
		if _, ok := poi[required]; !ok {
			t.Fatalf("missing wire field %s", required)
		}
	}
	if poi["collectTime"] != sample.RecordedAt.UnixMilli() || poi["createTime"] != sample.RecordedAt.UnixMilli()+9 {
		t.Fatalf("unexpected timestamps: %v", poi)
	}
}

func TestTargetPointCodeUsesJSONNumberAndParserPreservesMetadata(t *testing.T) {
	points := parseTargetPoints(json.RawMessage(`{"targetPoints":[
		{"code":"9007199254740991","lat":31.2,"lng":121.4,"type":2,"sort":3,"nfcEnable":true},
		{"code":123,"lat":31.21,"lng":121.41,"type":1,"sort":1}
	]}`))
	if len(points) != 2 {
		t.Fatalf("points=%v", points)
	}
	if points[0].Code != 123 || points[0].Sort != 1 || points[0].Type != 1 {
		t.Fatalf("first=%+v", points[0])
	}
	if points[1].Code != 9007199254740991 || points[1].Sort != 3 || !points[1].NFCEnable {
		t.Fatalf("second=%+v", points[1])
	}
	wire, err := json.Marshal(points[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"code":123`) || strings.Contains(string(wire), `"code":"123"`) {
		t.Fatalf("wire=%s", wire)
	}
	for _, localOnly := range []string{"type", "sort", "nfcEnable", "pointName", "simpleName"} {
		if strings.Contains(string(wire), `"`+localOnly+`"`) {
			t.Fatalf("wire contains local field %s: %s", localOnly, wire)
		}
	}
}

func TestParseRemoteRunRulesPreservesPlanAccountingMode(t *testing.T) {
	rules, err := parseRemoteRunRules(json.RawMessage(`{
		"planType": 2,
		"schoolDemandRule": {
			"totalDistance": 80000,
			"totalTimes": 30,
			"dayMaxTimes": 2,
			"dayMaxDistance": 8000,
			"weekMinTimes": 3,
			"weekMaxTimes": 5,
			"weekMaxDistance": 32000,
			"singleMinDistance": 1000,
			"singleMaxDistance": 5000,
			"minPace": 600,
			"maxPace": 180,
			"minFrequency": 0,
			"maxFrequency": 0,
			"segmentStrideAvg": 180
		},
		"studentDoneRuleInfo": {
			"completionRate": 0.4,
			"doneDistance": 12800,
			"doneTargetTimes": 12,
			"morningTimes": 2
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if rules.PlanType != activity.PlanTypeByTimes || rules.TotalTimes != 30 || rules.TotalDistanceM != 80000 {
		t.Fatalf("accounting rules=%+v", rules)
	}
	if rules.MinDistanceM != 1000 || rules.MaxDistanceM != 5000 {
		t.Fatalf("single-run range=%+v", rules)
	}
	if rules.DayMaxTimes != 2 || rules.WeekMinTimes != 3 || rules.WeekMaxTimes != 5 {
		t.Fatalf("frequency limits=%+v", rules)
	}
	if rules.DayMaxDistanceM != 8000 || rules.WeekMaxDistanceM != 32000 {
		t.Fatalf("distance limits=%+v", rules)
	}
	if rules.CompletionRate != 0.4 || rules.DoneDistanceM != 12800 || rules.DoneTargetTimes != 12 || rules.MorningTimes != 2 {
		t.Fatalf("completion progress=%+v", rules)
	}
}

func TestFinishCoreMatchesNativeFieldShape(t *testing.T) {
	run := activity.Run{
		Summary: activity.Summary{DistanceM: 3025.4, DurationS: 1087.2, MovingDurationS: 1080.1, TotalSteps: 5082},
		Samples: []activity.Sample{{Paused: false}, {Paused: true}, {Paused: true}, {Paused: false}},
	}
	config := DefaultCommonRunConfig()
	config.AMAPGPSLevel = 4
	values := buildFinishRunValues("RUN-1", run, config, []TargetPoint{{Code: 123, Lat: 31.2, Lng: 121.4}})
	if values["duration"] != "1080" || values["distance"] != "3025" || values["totalStep"] != "5082" {
		t.Fatalf("string totals=%v", values)
	}
	if values["alignType"] != 4 || values["pauseCount"] != 1 || values["pauseTimes"] != int64(7) {
		t.Fatalf("native scalar fields=%v", values)
	}
	if _, ok := values["runFaceFlag"]; ok {
		t.Fatalf("unexpected non-client field: %v", values)
	}
}

func TestPacedReplayDeadlineNeverCatchesUpInABurst(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	nominal := base.Add(6 * time.Second)

	if got := pacedReplayDeadline(nominal, base.Add(3*time.Second), 3); !got.Equal(nominal) {
		t.Fatalf("on-time deadline=%s, want %s", got, nominal)
	}

	latePreviousDispatch := base.Add(8 * time.Second)
	want := base.Add(11 * time.Second)
	if got := pacedReplayDeadline(nominal, latePreviousDispatch, 3); !got.Equal(want) {
		t.Fatalf("late deadline=%s, want %s", got, want)
	}
}

func TestMarkReachedTargetPointsHonorsSequentialModes(t *testing.T) {
	now := time.UnixMilli(1700000000123).UTC()
	points := []TargetPoint{
		{Code: 1, Lat: 31.2000, Lng: 121.4000},
		{Code: 2, Lat: 31.2010, Lng: 121.4010},
	}
	second := activity.Sample{Lat: points[1].Lat, Lng: points[1].Lng, RecordedAt: now}
	if markReachedTargetPoints(ClockModeOrder, points, second) {
		t.Fatalf("later ordered point passed before first: %+v", points)
	}
	first := activity.Sample{Lat: points[0].Lat, Lng: points[0].Lng, RecordedAt: now}
	if !markReachedTargetPoints(ClockModeOrder, points, first) || !points[0].PassStatus {
		t.Fatalf("first ordered point was not passed: %+v", points)
	}
	if !markReachedTargetPoints(ClockModeOrder, points, second) || !points[1].PassStatus {
		t.Fatalf("second ordered point was not passed after first: %+v", points)
	}
}

func TestMarkReachedTargetPointsFreeModeCanReachAnyPoint(t *testing.T) {
	points := []TargetPoint{
		{Code: 1, Lat: 31.2000, Lng: 121.4000},
		{Code: 2, Lat: 31.2010, Lng: 121.4010},
	}
	sample := activity.Sample{Lat: points[1].Lat, Lng: points[1].Lng, RecordedAt: time.UnixMilli(1700000000123).UTC()}
	if !markReachedTargetPoints(ClockModeFree, points, sample) || points[0].PassStatus || !points[1].PassStatus {
		t.Fatalf("free point state=%+v", points)
	}
}

func linearSamples(count int, intervalS, distancePerSample float64, stepsPerSample int) []activity.Sample {
	start := time.Unix(1700000000, 0).UTC()
	result := make([]activity.Sample, 0, count+1)
	for index := 0; index <= count; index++ {
		elapsed := float64(index) * intervalS
		result = append(result, activity.Sample{
			Index: index, RecordedAt: start.Add(time.Duration(elapsed * float64(time.Second))),
			ElapsedS: elapsed, DistanceM: float64(index) * distancePerSample,
			Steps: index * stepsPerSample, Lat: 31.2, Lng: 121.4,
			HorizontalAccuracyM: 5, Satellites: 12,
		})
	}
	return result
}
