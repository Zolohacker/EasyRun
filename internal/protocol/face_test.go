package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/faceengine"
)

type stubFaceAnalyzer struct {
	result faceengine.Comparison
	err    error
}

func (s stubFaceAnalyzer) Compare(context.Context, []byte, []byte) (faceengine.Comparison, error) {
	return s.result, s.err
}
func (stubFaceAnalyzer) Name() string { return "local-mnn/test" }
func (stubFaceAnalyzer) Close() error { return nil }

type faceRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn faceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestDetectFaceUsesUploadAndCompareAPI(t *testing.T) {
	fixed := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	client, err := NewClient(Options{BaseURL: "https://api.example.test/run-front", Environment: 9})
	if err != nil {
		t.Fatal(err)
	}
	client.clock = func() time.Time { return fixed }
	tokenCalls, uploadCalls, compareCalls := 0, 0, 0
	tokenTypes := make([]string, 0, 2)
	client.http = &http.Client{Transport: faceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := func(status int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		switch request.URL.Host {
		case "api.example.test":
			if request.URL.Path != "/run-front/aliyun/oss/getToken" {
				t.Fatalf("unexpected protocol path %s", request.URL.Path)
			}
			tokenCalls++
			raw, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			tokenTypes = append(tokenTypes, payload["tokenType"].(string))
			return response(http.StatusOK, `{"code":0,"data":{"securityToken":"sts-token","accessKeySecret":"secret","accessKeyId":"key-id","domain":"https://cdn.example.test","directory":"users/u1"}}`)
		case "sd-campus-badge.oss-cn-hangzhou.aliyuncs.com":
			uploadCalls++
			if request.Method != http.MethodPut || !strings.HasPrefix(request.URL.Path, "/users/u1/face/run/") {
				t.Fatalf("unexpected upload %s %s", request.Method, request.URL.Path)
			}
			if !strings.HasPrefix(request.Header.Get("Authorization"), "OSS key-id:") || request.Header.Get("x-oss-security-token") != "sts-token" {
				t.Fatalf("missing OSS authorization headers")
			}
			return response(http.StatusOK, "")
		case "facebody.cn-shanghai.aliyuncs.com":
			compareCalls++
			raw, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			form, parseErr := url.ParseQuery(string(raw))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if form.Get("Action") != "CompareFace" || form.Get("ImageURLB") != "https://bound.example.test/base.jpg" || !strings.HasPrefix(form.Get("ImageURLA"), "https://cdn.example.test/users/u1/face/run/") {
				t.Fatalf("unexpected compare form: %v", form)
			}
			return response(http.StatusOK, `{"RequestId":"req-1","Data":{"Confidence":88.75}}`)
		default:
			t.Fatalf("unexpected host %s", request.URL.Host)
			return nil, nil
		}
	})}

	result, err := client.DetectFace(context.Background(), Device{}, Session{Token: "token", SAToken: "satoken"}, FaceInput{
		Image: []byte{0xff, 0xd8, 0xff, 0xd9}, ContentType: "image/jpeg", Source: "custom",
	}, "https://bound.example.test/base.jpg", 75)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Confidence != 88.75 || result.Threshold != 75 || result.RequestID != "req-1" || result.Source != "custom" {
		t.Fatalf("result=%+v", result)
	}
	if !result.CheckedAt.Equal(fixed) || result.Provider != "app-sts/aliyun-facebody" {
		t.Fatalf("metadata=%+v", result)
	}
	if tokenCalls != 2 || uploadCalls != 1 || compareCalls != 1 {
		t.Fatalf("calls token=%d upload=%d compare=%d", tokenCalls, uploadCalls, compareCalls)
	}
	if len(tokenTypes) != 2 || tokenTypes[0] != "" || tokenTypes[1] != "1" {
		t.Fatalf("token types=%v want=[\"\" \"1\"]", tokenTypes)
	}
}

func TestFaceReplayPlanBuildsLocalModelTriggers(t *testing.T) {
	config := CheckRunConfig{
		RunScene: 20, FaceAuth: true, FaceModelType: 2, SimilarityValue: 75,
		FaceCheckConfigList: []FaceCheckConfig{{LowerLimit: 300, UpperLimit: 500}, {LowerLimit: 900, UpperLimit: 900}},
	}
	input := &FaceInput{Image: []byte{0xff, 0xd8, 0xff, 0xd9}, ContentType: "image/jpeg", Source: "base"}
	plan, err := newFaceReplayPlan(config, input, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Records) != 2 {
		t.Fatalf("records=%d", len(plan.Records))
	}
	if plan.Records[0].RandomDistance < 300 || plan.Records[0].RandomDistance >= 500 || plan.Records[1].RandomDistance != 900 {
		t.Fatalf("unexpected trigger distances: %+v", plan.Records)
	}
	repeated, err := newFaceReplayPlan(config, input, 42)
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Records {
		if plan.Records[index].RandomDistance != repeated.Records[index].RandomDistance {
			t.Fatalf("seeded trigger distance changed at %d", index)
		}
	}
}

func TestPerformFaceCheckLocalModelUploadsWithoutCloudCompare(t *testing.T) {
	fixed := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	client, err := NewClient(Options{BaseURL: "https://api.example.test/run-front", Environment: 9})
	if err != nil {
		t.Fatal(err)
	}
	client.clock = func() time.Time { return fixed }
	client.SetFaceAnalyzer(stubFaceAnalyzer{result: faceengine.Comparison{Cosine: 0.83, Confidence: 92.34}})
	tokenCalls, uploadCalls := 0, 0
	client.http = &http.Client{Transport: faceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := func(status int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		switch request.URL.Host {
		case "api.example.test":
			if request.URL.Path != "/run-front/aliyun/oss/getToken" {
				t.Fatalf("unexpected protocol path %s", request.URL.Path)
			}
			tokenCalls++
			return response(http.StatusOK, `{"code":0,"data":{"securityToken":"sts-token","accessKeySecret":"secret","accessKeyId":"key-id","domain":"https://cdn.example.test","directory":"users/u1"}}`)
		case "sd-campus-badge.oss-cn-hangzhou.aliyuncs.com":
			uploadCalls++
			return response(http.StatusOK, "")
		case "facebody.cn-shanghai.aliyuncs.com":
			t.Fatal("LOCAL model unexpectedly called cloud CompareFace")
			return nil, nil
		default:
			t.Fatalf("unexpected host %s", request.URL.Host)
			return nil, nil
		}
	})}

	plan, err := newFaceReplayPlan(CheckRunConfig{
		RunScene: 20, FaceAuth: true, FaceModelType: 2, SimilarityValue: 75,
		FaceCheckConfigList: []FaceCheckConfig{{LowerLimit: 300, UpperLimit: 301}},
	}, &FaceInput{Image: []byte{0xff, 0xd8, 0xff, 0xd9}, ContentType: "image/jpeg", Source: "base"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	record := &plan.Records[0]
	err = client.performFaceCheck(context.Background(), Device{}, Session{Token: "token", SAToken: "satoken"}, "run-1", activity.Sample{
		DistanceM: 302.4, Lng: 121.4, Lat: 31.2,
	}, plan, record)
	if err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || uploadCalls != 1 {
		t.Fatalf("calls token=%d upload=%d", tokenCalls, uploadCalls)
	}
	if !record.HasChecked || !record.FinishFaceCheck || !record.RunFaceUpload || record.RunFaceImg == "" {
		t.Fatalf("record state=%+v", record)
	}
	if record.Confidence == nil || *record.Confidence != 92.34 {
		t.Fatalf("confidence=%v", record.Confidence)
	}
	if record.FaceRequestID != "" || record.FaceCheckErrorCode != "" {
		t.Fatalf("unexpected cloud/error fields: %+v", record)
	}
	if record.PopTime == nil || *record.PopTime != fixed.UnixMilli() || record.CheckDistance == nil || *record.CheckDistance != 302 {
		t.Fatalf("trigger metadata=%+v", record)
	}
}
