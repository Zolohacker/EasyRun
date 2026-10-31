package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RunQuotaUsage is the accepted-record count used immediately before starting
// an automated run. Boundaries are calculated in the job's local timezone and
// weeks start on Monday, matching the client-facing weekly rule semantics.
type RunQuotaUsage struct {
	DayCount      int
	WeekCount     int
	DayDistanceM  float64
	WeekDistanceM float64
	DayStart      time.Time
	WeekStart     time.Time
}

// Records returns the target application's original page object. Keeping the
// response as JSON preserves fields that vary by school and plan version.
func (c *Client) Records(ctx context.Context, device Device, session Session, pageNum, pageSize int, semesterCode, runPlanCode string) (json.RawMessage, error) {
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	envelope, err := c.post(ctx, "run/pageSunRunRecord", device, map[string]any{
		"pageNum":      strconv.Itoa(pageNum),
		"pageSize":     strconv.Itoa(pageSize),
		"semesterCode": semesterCode,
		"runPlanCode":  runPlanCode,
	}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	return cloneRaw(envelope.Data), nil
}

func (c *Client) RecordDetail(ctx context.Context, device Device, session Session, runRecordCode, semesterCode string) (json.RawMessage, error) {
	envelope, err := c.post(ctx, "run/runRecordDetail", device, map[string]any{
		"runRecordCode": runRecordCode,
		"semesterCode":  semesterCode,
	}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	return cloneRaw(envelope.Data), nil
}

func (c *Client) RunSummary(ctx context.Context, device Device, session Session, timeType, sportType int) (json.RawMessage, error) {
	envelope, err := c.post(ctx, "run/queryRunRecordCount", device, map[string]any{
		"timeType":  strconv.Itoa(timeType),
		"sportType": strconv.Itoa(sportType),
	}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	return cloneRaw(envelope.Data), nil
}

// RunQuotaUsage reads the account's recent records for one plan and counts
// only records the service has accepted into the plan progress. Pagination is
// intentionally kept at 20 because the upstream endpoint rejects larger pages
// for some schools.
func (c *Client) RunQuotaUsage(ctx context.Context, device Device, session Session, semesterCode, runPlanCode, timezone string, now time.Time) (RunQuotaUsage, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return RunQuotaUsage{}, fmt.Errorf("加载任务时区: %w", err)
	}
	if now.IsZero() {
		now = c.clock()
	}
	localNow := now.In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	weekdayOffset := (int(dayStart.Weekday()) + 6) % 7
	weekStart := dayStart.AddDate(0, 0, -weekdayOffset)
	usage := RunQuotaUsage{DayStart: dayStart, WeekStart: weekStart}

	for page := 1; page <= 20; page++ {
		raw, requestErr := c.Records(ctx, device, session, page, 20, semesterCode, runPlanCode)
		if requestErr != nil {
			return RunQuotaUsage{}, fmt.Errorf("读取第 %d 页跑步记录: %w", page, requestErr)
		}
		items, hasNext, parseErr := parseRunQuotaPage(raw)
		if parseErr != nil {
			return RunQuotaUsage{}, parseErr
		}
		oldest := time.Time{}
		for _, item := range items {
			startedAt, ok := quotaRecordTime(item, location)
			if !ok {
				continue
			}
			if oldest.IsZero() || startedAt.Before(oldest) {
				oldest = startedAt
			}
			if !recordCountsTowardQuota(item) || startedAt.Before(weekStart) {
				continue
			}
			distanceM := quotaRecordDistanceM(item)
			usage.WeekCount++
			usage.WeekDistanceM += distanceM
			if !startedAt.Before(dayStart) {
				usage.DayCount++
				usage.DayDistanceM += distanceM
			}
		}
		if !hasNext || len(items) < 20 || (!oldest.IsZero() && oldest.Before(weekStart)) {
			break
		}
	}
	return usage, nil
}

func parseRunQuotaPage(raw json.RawMessage) ([]map[string]any, bool, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false, fmt.Errorf("解析跑步记录分页: %w", err)
	}
	items, err := unwrapArray(raw)
	if err != nil {
		if isEmptyRunQuotaPage(root) {
			return []map[string]any{}, false, nil
		}
		return nil, false, fmt.Errorf("解析跑步记录分页: %w", err)
	}
	hasNext, found := recursiveBool(root, "hasNext")
	if !found {
		hasNext = len(items) == 20
	}
	return items, hasNext, nil
}

// New accounts can have a null/empty page or pagination metadata without a
// list. Only recognize empty record pages here: unrelated responses and pages
// advertising more records must still fail, and other list APIs remain strict.
func isEmptyRunQuotaPage(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	case map[string]any:
		empty := len(typed) == 0
		for key, child := range typed {
			switch key {
			case "list", "records", "items", "data":
				if !isEmptyRunQuotaPage(child) {
					return false
				}
				empty = true
			case "total":
				total, ok := numericValue(child)
				if !ok || total != 0 {
					return false
				}
				empty = true
			case "hasNext":
				hasNext, ok := child.(bool)
				if text, isString := child.(string); isString {
					var err error
					hasNext, err = strconv.ParseBool(text)
					ok = err == nil
				}
				if !ok || hasNext {
					return false
				}
				empty = true
			case "pageNum", "pageSize", "refreshTime":
				// Pagination metadata alone does not establish an empty page.
				switch child.(type) {
				case nil, float64, string:
				default:
					return false
				}
			default:
				return false
			}
		}
		return empty
	default:
		return false
	}
}

func recursiveBool(value any, key string) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if candidate, ok := typed[key]; ok {
			switch boolean := candidate.(type) {
			case bool:
				return boolean, true
			case string:
				parsed, err := strconv.ParseBool(boolean)
				return parsed, err == nil
			}
		}
		for _, child := range typed {
			if result, ok := recursiveBool(child, key); ok {
				return result, true
			}
		}
	case []any:
		for _, child := range typed {
			if result, ok := recursiveBool(child, key); ok {
				return result, true
			}
		}
	}
	return false, false
}

func recordCountsTowardQuota(record map[string]any) bool {
	if value, exists := record["sunRunRecordStatus"]; exists {
		status, ok := numericValue(value)
		return ok && (status == 1 || status == 6 || status == 10)
	}
	if value, exists := record["status"]; exists {
		status, ok := numericValue(value)
		return ok && (status == 1 || status == 6 || status == 10)
	}
	return false
}

// quotaRecordDistanceM follows SportRecord.getValidDistance(): campus runs use
// validDistance, while sportType=2 uses the raw distance. Older server versions
// sometimes omit validDistance, so retain distance/runDistance as fallbacks.
func quotaRecordDistanceM(record map[string]any) float64 {
	sportType, _ := numericValue(record["sportType"])
	keys := []string{"validDistance", "distance", "runDistance"}
	if sportType == 2 {
		keys = []string{"distance", "validDistance", "runDistance"}
	}
	for _, key := range keys {
		value, exists := record[key]
		if !exists {
			continue
		}
		if distance, ok := numericValue(value); ok && distance > 0 {
			return distance
		}
	}
	return 0
}

func quotaRecordTime(record map[string]any, location *time.Location) (time.Time, bool) {
	for _, key := range []string{"startTime", "runStartTime", "createTime", "runTime"} {
		value, exists := record[key]
		if !exists {
			continue
		}
		if number, ok := numericValue(value); ok && number > 0 {
			milliseconds := int64(number)
			if milliseconds < 1_000_000_000_000 {
				milliseconds *= 1000
			}
			return time.UnixMilli(milliseconds).In(location), true
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
			if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(text), location); err == nil {
				return parsed.In(location), true
			}
		}
	}
	return time.Time{}, false
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), raw...)
}
