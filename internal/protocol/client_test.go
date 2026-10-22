package protocol

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsPasswordVerificationRequired(t *testing.T) {
	if !IsPasswordVerificationRequired(&ResponseError{Code: PasswordVerificationRequiredCode, Message: "verification required"}) {
		t.Fatal("10101 should enter the SMS verification continuation")
	}
	if IsPasswordVerificationRequired(&ResponseError{Code: 1001, Message: "other"}) {
		t.Fatal("unrelated response code should not enter SMS verification")
	}
}

func TestIsSessionExpired(t *testing.T) {
	for _, err := range []error{
		&ResponseError{Code: 1503, Message: "token invalid"},
		&ResponseError{Code: 9999, Message: "登录状态已失效，请重新登录"},
		fmt.Errorf("wrapped: %w", &ResponseError{Code: 9999, Message: "token已过期"}),
	} {
		if !IsSessionExpired(err) {
			t.Fatalf("expected session expiry for %v", err)
		}
	}
	if IsSessionExpired(&ResponseError{Code: PasswordVerificationRequiredCode, Message: "verification required"}) {
		t.Fatal("password verification challenge is not a session expiry")
	}
}

func TestValidateSessionPropagatesExpiryFromProfileEnrichment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/run-front/account/queryCommonUserInfo":
			_, _ = w.Write([]byte(`{"code":0,"data":{"userName":"tester"}}`))
		case "/run-front/api/school/details":
			_, _ = w.Write([]byte(`{"code":1503,"message":"登录状态已失效，请重新登录"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL + "/run-front", Environment: 9})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ValidateSession(context.Background(), Device{}, Session{Token: "token", SAToken: "satoken"})
	if !IsSessionExpired(err) {
		t.Fatalf("expected session expiry, got %v", err)
	}
}

func TestCompleteSMSLoginUsesLoginCheckPhoneAsTerminalType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/run-front/auth/loginCheck":
			if payload["loginName"] != "student-login" {
				t.Errorf("loginName=%v", payload["loginName"])
			}
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"phone":"resolved-phone"}}`))
		case "/run-front/account/login_v3":
			if payload["terminalType"] != "resolved-phone" || payload["phone"] != nil {
				t.Errorf("login payload=%v", payload)
			}
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"userId":"u1","token":"token","satoken":"satoken"}}`))
		case "/run-front/account/queryCommonUserInfo", "/run-front/api/school/details":
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":{}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL + "/run-front", Environment: 9})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.CompleteSMSLogin(context.Background(), Device{}, "student-login", "123456", "ticket")
	if err != nil {
		t.Fatal(err)
	}
	if session.Token != "token" || session.SAToken != "satoken" {
		t.Fatalf("session=%+v", session)
	}
}

func TestClientUsesRecovered879EnvelopeAndHeaders(t *testing.T) {
	fixed := time.UnixMilli(1700000000000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/run-front/run/pageSunRunRecord" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		checks := map[string]string{
			"appVersion": AppVersion, "buildVersion": BuildVersion, "appCode": AppCode,
			"platform": Platform, "channel": Channel, "timestamp": "1700000000000",
		}
		for key, want := range checks {
			if got := payload[key]; got != want {
				t.Errorf("%s=%v want=%s", key, got, want)
			}
		}
		if r.Header.Get("app") != "run-front" || r.Header.Get("api") != "run" || r.Header.Get("v") != "pageSunRunRecord" {
			t.Errorf("protocol headers: app=%q api=%q v=%q", r.Header.Get("app"), r.Header.Get("api"), r.Header.Get("v"))
		}
		if r.Header.Get("User-Agent") != "ShanDong/8.7.9 (redmi;Android 14)" {
			t.Errorf("user agent=%q", r.Header.Get("User-Agent"))
		}
		raw, _ := compactJSON(payload)
		wantSign, _ := SignBody(raw, 9)
		if r.Header.Get("sign") != wantSign {
			t.Errorf("sign does not cover exact body")
		}
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write([]byte(`{"code":0,"message":"","data":{"list":[]}}`))
		_ = writer.Close()
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL + "/run-front", WebBaseURL: server.URL, Environment: 9})
	if err != nil {
		t.Fatal(err)
	}
	client.clock = func() time.Time { return fixed }
	data, err := client.Records(context.Background(), Device{ID: "0123456789abcdef", ModelName: "Redmi|23013RK75C", SystemVersion: "14"}, Session{Token: "token", SAToken: "satoken"}, 1, 20, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"list":[]}` {
		t.Fatalf("data=%s", data)
	}
}

func TestBodyRequestOmitsFormProtocolHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"app", "api", "v", "pv", "e", "k"} {
			if r.Header.Get(header) != "" {
				t.Errorf("@Body request unexpectedly has %s=%q", header, r.Header.Get(header))
			}
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"","data":{}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL, Environment: 9})
	_, err := client.post(context.Background(), "run/uploadPassPoint", Device{}, map[string]any{"targetPoints": []any{}}, requestOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientDefaultsToRuntimeProductionEnvironment(t *testing.T) {
	client, err := NewClient(Options{BaseURL: "https://api.huachenjie.com/run-front"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Environment() != 9 {
		t.Fatalf("environment=%d want=9", client.Environment())
	}
}

func TestCommonRunConfigUsesUnsignedGETQuery(t *testing.T) {
	fixed := time.UnixMilli(1700000000000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/run-front/common/getConfig" {
			t.Fatalf("method/path=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("sign") != "" {
			t.Fatalf("GET unexpectedly signed: %q", r.Header.Get("sign"))
		}
		if r.Header.Get("Authorization") != "token" || r.Header.Get("satoken") != "satoken" {
			t.Fatalf("session headers missing")
		}
		checks := map[string]string{
			"appVersion": AppVersion, "buildVersion": BuildVersion, "appCode": AppCode,
			"platform": Platform, "channel": Channel, "timestamp": "1700000000000",
			"deviceId": "device", "modelName": "Redmi|model", "systemVersion": "14",
		}
		for key, want := range checks {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("query %s=%q want=%q", key, got, want)
			}
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"poiInterval":15,"paceInterval":50,"stepInterval":20,"strideStatisticsInterval":200}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL + "/run-front", Environment: 9})
	client.clock = func() time.Time { return fixed }
	config, err := client.CommonRunConfig(context.Background(), Device{ID: "device", ModelName: "Redmi|model", SystemVersion: "14"}, Session{Token: "token", SAToken: "satoken"})
	if err != nil {
		t.Fatal(err)
	}
	if config.POIUploadIntervalS != 15 || config.PaceSegmentDistanceM != 50 || config.StepSegmentIntervalS != 20 || config.StrideSegmentDistanceM != 200 {
		t.Fatalf("config=%+v", config)
	}
}

func TestRunRulesReadsSchoolDemandRuleAndNormalizesPaceDirection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/run-front/run/querySunRunAbstractInfoV2" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["runPlanCode"] != "plan-1" || payload["sportType"] != float64(1) {
			t.Fatalf("payload=%v", payload)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"schoolDemandRule":{"singleMinDistance":2000,"singleMaxDistance":5000,"dayMaxDistance":7000,"weekMaxDistance":25000,"minPace":600,"maxPace":240,"minFrequency":140,"maxFrequency":190,"segmentStrideAvg":135.5,"segmentStrideExceptionNum":2,"swingAmplitudeThreshold":3,"startDate":"2026-09-01","endDate":"2027-01-15","timeFragment":[{"timeType":"0","times":"06:00-08:00","tagType":1},{"timeType":"1","times":"18:30～20:00, 21:00至22:00","tagType":2},{"timeType":"2","times":"20:00-24:00"}]}}}`))
	}))
	defer server.Close()
	client, _ := NewClient(Options{BaseURL: server.URL + "/run-front", Environment: 9})
	rules, err := client.RunRules(context.Background(), Device{}, Session{}, "", "plan-1")
	if err != nil {
		t.Fatal(err)
	}
	if rules.MinDistanceM != 2000 || rules.MaxDistanceM != 5000 || rules.FastestPaceSecPerKM != 240 || rules.SlowestPaceSecPerKM != 600 {
		t.Fatalf("rules=%+v", rules)
	}
	if rules.MinCadenceSPM != 140 || rules.MaxCadenceSPM != 190 || rules.MaxSegmentStrideM != 1.355 {
		t.Fatalf("rules=%+v", rules)
	}
	if rules.DayMaxDistanceM != 7000 || rules.WeekMaxDistanceM != 25000 {
		t.Fatalf("distance quotas=%+v", rules)
	}
	if rules.ActiveStartDate != "2026-09-01" || rules.ActiveEndDate != "2027-01-15" {
		t.Fatalf("active dates=%s..%s", rules.ActiveStartDate, rules.ActiveEndDate)
	}
	if len(rules.TimeWindows) != 4 {
		t.Fatalf("time windows=%+v", rules.TimeWindows)
	}
	if rules.TimeWindows[0].DayType != "every_day" || rules.TimeWindows[0].StartMinute != 360 || rules.TimeWindows[0].EndMinute != 480 {
		t.Fatalf("first time window=%+v", rules.TimeWindows[0])
	}
	if rules.TimeWindows[3].DayType != "weekend" || rules.TimeWindows[3].EndMinute != 1440 {
		t.Fatalf("last time window=%+v", rules.TimeWindows[3])
	}
}
