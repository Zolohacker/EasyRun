package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/activity"
)

type unfinishedRunTestStep struct {
	path     string
	response string
	status   int
}

func TestSubmitRunReconcilesUnfinishedRunBeforeStart(t *testing.T) {
	const oldCode = "9007199254740993"
	const queryPath = "/run-front/run/queryUnFinishRun"
	const endPath = "/run-front/run/abnormalFinishSunRun"
	active := unfinishedRunTestStep{path: queryPath, response: `{"code":0,"data":{"runRecordCode":"` + oldCode + `","sportType":1,"resume":true}}`}
	empty := unfinishedRunTestStep{path: queryPath, response: `{"code":0,"data":null}`}
	ended := unfinishedRunTestStep{path: endPath, response: `{"code":0,"data":{}}`}
	expiredQuery := unfinishedRunTestStep{path: queryPath, response: `{"code":1503,"message":"token invalid"}`}

	for _, tc := range []struct {
		name           string
		steps          []unfinishedRunTestStep
		wantStart      bool
		wantError      string
		wantExpired    bool
		cancelAfterEnd bool
	}{
		{name: "no unfinished run", steps: []unfinishedRunTestStep{empty}, wantStart: true},
		{name: "end and verify before start", steps: []unfinishedRunTestStep{active, ended, empty}, wantStart: true},
		{name: "initial query failed", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":5001,"message":"query failed"}`}}, wantError: "查询未完成跑步"},
		{name: "initial query expired", steps: []unfinishedRunTestStep{expiredQuery}, wantError: "查询未完成跑步", wantExpired: true},
		{name: "absent nullable data", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0}`}}, wantStart: true},
		{name: "array instead of record", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":[]}`}}, wantError: "解析未完成跑步响应"},
		{name: "missing record code", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":{"sportType":1}}`}}, wantError: "缺少记录编号"},
		{name: "blank record code", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":{"runRecordCode":"  ","sportType":1}}`}}, wantError: "缺少记录编号"},
		{name: "numeric record code", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":{"runRecordCode":9007199254740993,"sportType":1}}`}}, wantError: "解析未完成跑步响应"},
		{name: "different sport", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":{"runRecordCode":"other-sport","sportType":2}}`}}, wantError: "运动类型为 2"},
		{name: "missing sport", steps: []unfinishedRunTestStep{{path: queryPath, response: `{"code":0,"data":{"runRecordCode":"missing-sport"}}`}}, wantError: "运动类型为 0"},
		{name: "end failed", steps: []unfinishedRunTestStep{active, {path: endPath, response: `{"code":5001,"message":"end failed"}`}}, wantError: "结束未完成跑步 " + oldCode + " 失败"},
		{name: "end HTTP failed", steps: []unfinishedRunTestStep{active, {path: endPath, response: `{"message":"unavailable"}`, status: http.StatusServiceUnavailable}}, wantError: "结束未完成跑步 " + oldCode + " 失败"},
		{name: "end expired", steps: []unfinishedRunTestStep{active, {path: endPath, response: `{"code":1503,"message":"token invalid"}`}}, wantError: "结束未完成跑步", wantExpired: true},
		{name: "verification failed", steps: []unfinishedRunTestStep{active, ended, {path: queryPath, response: `{"code":5001,"message":"query failed"}`}}, wantError: "复查失败"},
		{name: "verification expired", steps: []unfinishedRunTestStep{active, ended, expiredQuery}, wantError: "复查失败", wantExpired: true},
		{name: "malformed verification", steps: []unfinishedRunTestStep{active, ended, {path: queryPath, response: `{"code":0,"data":{}}`}}, wantError: "复查失败"},
		{name: "same record remains", steps: []unfinishedRunTestStep{active, ended, active}, wantError: "结束后仍存在未完成跑步 " + oldCode},
		{name: "new record appears without ending it", steps: []unfinishedRunTestStep{active, ended, {path: queryPath, response: `{"code":0,"data":{"runRecordCode":"another-run","sportType":1}}`}}, wantError: "结束后仍存在未完成跑步 another-run"},
		{name: "cancel during end", steps: []unfinishedRunTestStep{active, ended}, wantError: "本次起跑已停止", cancelAfterEnd: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			steps := []unfinishedRunTestStep{
				{path: "/run-front/common/getConfig", response: `{"code":0,"data":{}}`},
				{path: "/run-front/run/plan/selectList", response: `{"code":0,"data":[{"runPlanCode":"plan-1"}]}`},
				{path: "/run-front/school/querySchoolFences", response: `{"code":0,"data":[{"fenceCode":"fence-1","lat":31.2,"lng":121.4}]}`},
				{path: "/run-front/school/filterUsableFence", response: `{"code":0,"data":{}}`},
			}
			steps = append(steps, tc.steps...)
			if tc.wantStart {
				// Stop at the mocked start response: no trajectory replay or real
				// run is needed to verify the complete pre-start request order.
				steps = append(steps,
					unfinishedRunTestStep{path: "/run-front/run/checkSunRunConfig", response: `{"code":0,"data":{}}`},
					unfinishedRunTestStep{path: "/run-front/run/startSunRun_v2", response: `{"code":0,"data":{}}`},
				)
			}
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := int(requestCount.Add(1)) - 1
				if index >= len(steps) {
					t.Errorf("unexpected extra request: %s", r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				step := steps[index]
				if r.URL.Path != step.path {
					t.Errorf("request %d: path=%s want=%s", index, r.URL.Path, step.path)
					http.Error(w, "incorrect request order", http.StatusInternalServerError)
					return
				}
				if r.URL.Path == queryPath || r.URL.Path == endPath {
					if r.Method != http.MethodPost || r.Header.Get("Authorization") != "test-token" || r.Header.Get("satoken") != "test-satoken" {
						t.Error("unfinished run requests must use POST and the current account session")
					}
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode request: %v", err)
						http.Error(w, "invalid payload", http.StatusBadRequest)
						return
					}
					if r.URL.Path == queryPath && string(payload["sportType"]) != `"1"` {
						t.Errorf("query sportType=%s", payload["sportType"])
					}
					if r.URL.Path == endPath {
						if string(payload["runRecordCode"]) != `"`+oldCode+`"` || string(payload["abnormalCodes"]) != `[7001]` {
							t.Errorf("end payload: record=%s codes=%s", payload["runRecordCode"], payload["abnormalCodes"])
						}
						if len(payload["timestamp"]) == 0 || len(payload["runImgRecord"]) == 0 {
							t.Error("missing end request timestamp or runImgRecord")
						}
						for _, field := range []string{"distance", "duration", "stepCount", "pois", "stepList", "paceList", "faceCheckRecordList"} {
							if _, exists := payload[field]; exists {
								t.Errorf("unexpected metric on abandoned run: %s", field)
							}
						}
						if tc.cancelAfterEnd {
							cancel()
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if step.status != 0 {
					w.WriteHeader(step.status)
				}
				_, _ = fmt.Fprint(w, step.response)
			}))
			defer server.Close()
			client, err := NewClient(Options{BaseURL: server.URL + "/run-front", Environment: 9})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = client.SubmitRun(ctx, Device{}, Session{Token: "test-token", SAToken: "test-satoken", SchoolCode: "school-1"}, "plan-1", "semester-1", "fence-1", 1000, 1, nil,
				func(context.Context, RunStartContext) (activity.Run, error) {
					t.Error("builder must not run before a valid start response")
					return activity.Run{}, errors.New("unexpected builder call")
				})
			wantError := tc.wantError
			if tc.wantStart {
				wantError = "起跑响应没有返回 runRecordCode"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("error=%v want containing %q", err, wantError)
			}
			if IsSessionExpired(err) != tc.wantExpired {
				t.Errorf("session expiry lost or misclassified: %v", err)
			}
			if tc.cancelAfterEnd && !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation lost: %v", err)
			}
			if count := int(requestCount.Load()); count != len(steps) {
				t.Errorf("requests=%d want=%d", count, len(steps))
			}
		})
	}
}
