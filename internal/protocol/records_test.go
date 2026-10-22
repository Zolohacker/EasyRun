package protocol

import (
	"encoding/json"
	"strconv"
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
