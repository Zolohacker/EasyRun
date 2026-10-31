package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRunQuotaPageAndAcceptedStatuses(t *testing.T) {
	raw := json.RawMessage(`{"data":{"list":[{"status":1},{"sunRunRecordStatus":"6"},{"sunRunRecordStatus":10},{"sunRunRecordStatus":3,"status":1}],"hasNext":"true"}}`)
	items, hasNext, err := parseRunQuotaPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || !hasNext {
		t.Fatalf("items=%d hasNext=%v", len(items), hasNext)
	}
	if !recordCountsTowardQuota(items[0]) || !recordCountsTowardQuota(items[1]) || !recordCountsTowardQuota(items[2]) || recordCountsTowardQuota(items[3]) {
		t.Fatalf("unexpected accepted-status mapping: %+v", items)
	}
}

func TestParseRunQuotaPageWithoutRecords(t *testing.T) {
	for name, raw := range map[string]string{
		"empty array":             `[]`,
		"empty list":              `{"list":[]}`,
		"empty object":            `{}`,
		"null page":               `null`,
		"null list":               `{"list":null}`,
		"null records":            `{"records":null}`,
		"null items":              `{"items":null}`,
		"empty nested page":       `{"data":{}}`,
		"null nested page":        `{"data":null}`,
		"zero total":              `{"total":0}`,
		"last page":               `{"hasNext":false}`,
		"empty page metadata":     `{"pageNum":1,"pageSize":20,"total":0,"hasNext":false,"refreshTime":1700000000000}`,
		"string page metadata":    `{"pageNum":"1","pageSize":"20","total":"0","hasNext":"false"}`,
		"nested empty metadata":   `{"data":{"total":0,"hasNext":false}}`,
		"null list with metadata": `{"list":null,"pageNum":1,"pageSize":20,"total":0,"hasNext":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			items, hasNext, err := parseRunQuotaPage(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 0 || hasNext {
				t.Fatalf("items=%v hasNext=%v; want an empty final page", items, hasNext)
			}
		})
	}
}

func TestParseRunQuotaPageKeepsUnexpectedResponsesAsErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"missing JSON":                 ``,
		"malformed JSON":               `{`,
		"unexpected string":            `""`,
		"business error":               `{"message":"request failed"}`,
		"unknown list field":           `{"rows":[{"status":1}]}`,
		"nonzero total without list":   `{"total":1,"hasNext":false}`,
		"next page without list":       `{"total":0,"hasNext":true}`,
		"invalid total":                `{"total":"unknown","hasNext":false}`,
		"invalid hasNext":              `{"total":0,"hasNext":"unknown"}`,
		"no empty indicator":           `{"pageNum":1,"pageSize":20}`,
		"unexpected field":             `{"total":0,"error":"request failed"}`,
		"invalid list":                 `{"list":"","total":0}`,
		"null list with nonzero total": `{"list":null,"total":1}`,
		"nested business error":        `{"data":{"message":"request failed"}}`,
		"nested empty with more data":  `{"data":{},"hasNext":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseRunQuotaPage(json.RawMessage(raw)); err == nil {
				t.Fatalf("unexpected response treated as an empty page: %s", raw)
			}
		})
	}
	if _, err := unwrapArray(json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty-page compatibility must not relax plan and fence list parsing")
	}
}

func TestRunQuotaUsageWithoutRecords(t *testing.T) {
	for name, body := range map[string]string{
		"empty page":     `{"code":0,"data":{}}`,
		"empty metadata": `{"code":0,"data":{"total":0,"hasNext":false,"pageNum":1,"pageSize":20}}`,
		"null page":      `{"code":0,"data":null}`,
		"omitted data":   `{"code":0}`,
		"null list":      `{"code":0,"data":{"list":null}}`,
		"empty list":     `{"code":0,"data":{"list":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/run/pageSunRunRecord" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				for key, want := range map[string]string{"pageNum": "1", "pageSize": "20", "semesterCode": "semester", "runPlanCode": "plan"} {
					if payload[key] != want {
						t.Errorf("%s=%q want=%q", key, payload[key], want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client, err := NewClient(Options{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			usage, err := client.RunQuotaUsage(context.Background(), Device{}, Session{}, "semester", "plan", "Asia/Shanghai", time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if usage.DayCount != 0 || usage.WeekCount != 0 || usage.DayDistanceM != 0 || usage.WeekDistanceM != 0 {
				t.Fatalf("expected zero usage for a new account: %+v", usage)
			}
			if usage.DayStart.Format(time.RFC3339) != "2026-10-31T00:00:00+08:00" || usage.WeekStart.Format(time.RFC3339) != "2026-10-26T00:00:00+08:00" {
				t.Fatalf("unexpected quota boundaries: %+v", usage)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests=%d; an empty page must stop pagination", requests.Load())
			}
		})
	}
}

func TestRunQuotaUsageEmptyLastPagePreservesCounts(t *testing.T) {
	records := make([]map[string]any, 20)
	for i := range records {
		records[i] = map[string]any{"sunRunRecordStatus": 1, "startTime": "2026-10-31 06:30:00", "validDistance": 1000}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": records}})
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := client.RunQuotaUsage(context.Background(), Device{}, Session{}, "semester", "plan", "Asia/Shanghai", time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if usage.DayCount != 20 || usage.WeekCount != 20 || usage.DayDistanceM != 20000 || usage.WeekDistanceM != 20000 || requests.Load() != 2 {
		t.Fatalf("usage=%+v requests=%d; an empty last page must preserve earlier records", usage, requests.Load())
	}
}

func TestRunQuotaUsageEmptyDataDoesNotHideUpstreamErrors(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":1503,"message":"session expired","data":{}}`))
			}))
			defer server.Close()
			client, err := NewClient(Options{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.RunQuotaUsage(context.Background(), Device{}, Session{}, "semester", "plan", "Asia/Shanghai", time.Now())
			if err == nil {
				t.Fatal("upstream error was treated as zero usage")
			}
			if status == http.StatusOK && !IsSessionExpired(err) {
				t.Fatalf("session expiry was lost: %v", err)
			}
		})
	}
}

func TestQuotaRecordDistanceMatchesSportRecordSemantics(t *testing.T) {
	if got := quotaRecordDistanceM(map[string]any{"sportType": 1.0, "validDistance": "3025", "distance": 3100.0}); got != 3025 {
		t.Fatalf("campus distance=%v", got)
	}
	if got := quotaRecordDistanceM(map[string]any{"sportType": 2.0, "validDistance": 3025.0, "distance": 3100.0}); got != 3100 {
		t.Fatalf("free-run distance=%v", got)
	}
	if got := quotaRecordDistanceM(map[string]any{"distance": json.Number("2600")}); got != 2600 {
		t.Fatalf("fallback distance=%v", got)
	}
}

func TestQuotaRecordTimeUsesLocalTimezone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 22, 6, 30, 0, 0, location)
	for _, record := range []map[string]any{
		{"startTime": float64(want.UnixMilli())},
		{"runStartTime": want.Format("2006-01-02 15:04:05")},
		{"createTime": json.Number(strconv.FormatInt(want.Unix(), 10))},
	} {
		got, ok := quotaRecordTime(record, location)
		if !ok || !got.Equal(want) {
			t.Fatalf("record=%v got=%v ok=%v want=%v", record, got, ok, want)
		}
	}
}
