package activity

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestInspectRemoteRecordChecksAggregateSegmentsAndRules(t *testing.T) {
	raw := json.RawMessage(`{
		"distance":1608,"validDistance":1608,"duration":566,"pace":352,"totalStep":1360,"frequency":144,
		"stepInterval":60,"stepList":[148,150,146,150,150,148,146,144,140,38],
		"paceInterval":1000,"paceList":[352,351],"sunRunRecordStatus":1,"warn":false,
		"pois":[
			{"index":1,"lat":31.2304,"lng":121.4737,"collectTime":1000000000000,"runTime":0},
			{"index":2,"lat":31.23763,"lng":121.4737,"collectTime":1000000003000,"runTime":3},
			{"index":3,"lat":31.24486,"lng":121.4737,"collectTime":1000000006000,"runTime":6}
		]
	}`)
	inspection := InspectRemoteRecord(raw, RunRules{
		MinDistanceM: 1600, MaxDistanceM: 5000,
		FastestPaceSecPerKM: 210, SlowestPaceSecPerKM: 600,
		MaxSegmentStrideM: 1.8, LocationSampleIntervalS: 3,
	}, "test plan")
	if !inspection.Valid {
		t.Fatalf("inspection should pass: %+v", inspection.Findings)
	}
	if inspection.Metrics.TotalSteps != 1360 || inspection.Metrics.PointCount != 3 {
		t.Fatalf("unexpected metrics: %+v", inspection.Metrics)
	}
	if inspection.Score < 80 {
		t.Fatalf("score too low: %d", inspection.Score)
	}
}

func TestInspectRemoteRecordRejectsInconsistentAggregate(t *testing.T) {
	raw := json.RawMessage(`{
		"distance":1600,"duration":400,"pace":600,"totalStep":400,"frequency":160,
		"stepList":[100,100],"pois":[
			{"index":2,"lat":31.2,"lng":121.4,"collectTime":1000000003000},
			{"index":1,"lat":31.2,"lng":121.401,"collectTime":1000000000000}
		]
	}`)
	inspection := InspectRemoteRecord(raw, RunRules{MinDistanceM: 1600, MaxDistanceM: 5000, FastestPaceSecPerKM: 210, SlowestPaceSecPerKM: 600}, "")
	if inspection.Valid {
		t.Fatal("inconsistent record should fail")
	}
	codes := map[string]bool{}
	for _, finding := range inspection.Findings {
		codes[finding.Code] = true
	}
	for _, code := range []string{"pace_consistency", "cadence_consistency", "step_segments", "track_points"} {
		if !codes[code] {
			t.Fatalf("missing finding %s: %+v", code, inspection.Findings)
		}
	}
}

func TestInspectRemoteRecordExplainsAppealableAndAppealingStatuses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "appealable", status: 3, want: "无效，可查看原因并发起申诉"},
		{name: "appealing", status: 4, want: "无效，申诉处理中"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{
				"distance":2000,"duration":720,"pace":360,"totalStep":1800,"frequency":150,
				"sunRunRecordStatus":` + strconv.Itoa(test.status) + `,
				"invalidReasons":[{"invalidType":63,"invalidDetail":"系统检测到您未进行跑步校验"}],
				"pois":[
					{"index":1,"lat":31.2304,"lng":121.4737,"collectTime":1000000000000},
					{"index":2,"lat":31.2305,"lng":121.4738,"collectTime":1000000003000}
				]
			}`)
			inspection := InspectRemoteRecord(raw, RunRules{}, "")
			var message string
			for _, check := range inspection.Checks {
				if check.Code == "server_status" {
					message = check.Message
					break
				}
			}
			if !strings.Contains(message, test.want) || !strings.Contains(message, "未进行跑步校验") {
				t.Fatalf("unexpected status explanation: %q", message)
			}
		})
	}
}
