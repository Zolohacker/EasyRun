package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	DefaultAppMarketURL = "https://sj.qq.com/appdetail/com.huachenjie.shandong_school"
	appMarketPackage    = "com.huachenjie.shandong_school"
	maxAppMarketPage    = 2 << 20
)

// AppUpdate is the VersionUpgrade model returned by common/update in 8.8.1.
type AppUpdate struct {
	NewVersion        string `json:"newVersion"`
	SupportVersion    string `json:"supportVersion"`
	HaveUpdate        bool   `json:"haveUpdate"`
	ForceUpdate       bool   `json:"forceUpdate"`
	DownloadURL       string `json:"downloadUrl"`
	UpdateTitle       string `json:"updateTitle"`
	UpdateDescription string `json:"updateDescription"`
	VersionSource     string `json:"versionSource,omitempty"`
	MarketVersion     string `json:"marketVersion,omitempty"`
	MarketURL         string `json:"marketUrl,omitempty"`
	MarketError       string `json:"marketError,omitempty"`
}

type appMarketRecord struct {
	PackageName string
	Version     string
}

func (c *Client) CheckAppUpdate(ctx context.Context, device Device) (AppUpdate, error) {
	envelope, err := c.post(ctx, "common/update", device, nil, requestOptions{})
	if err != nil {
		return AppUpdate{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &fields); err != nil || fields == nil {
		return AppUpdate{}, errors.New("客户端检查更新接口返回的数据格式不正确")
	}
	// A real no-update response omits forceUpdate; an available update must
	// explicitly say whether it is mandatory. Null flags are not valid booleans.
	var have, force *bool
	if err := json.Unmarshal(fields["haveUpdate"], &have); err != nil || have == nil {
		return AppUpdate{}, errors.New("客户端检查更新接口缺少 haveUpdate")
	}
	if raw, exists := fields["forceUpdate"]; exists {
		if err := json.Unmarshal(raw, &force); err != nil || force == nil {
			return AppUpdate{}, errors.New("客户端检查更新接口的 forceUpdate 格式不正确")
		}
	} else if *have {
		return AppUpdate{}, errors.New("客户端检查更新接口缺少 forceUpdate")
	}
	var result AppUpdate
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		return AppUpdate{}, errors.New("客户端检查更新接口的版本字段格式不正确")
	}
	result.VersionSource = "client_update_api"
	result.NewVersion = strings.TrimSpace(result.NewVersion)
	if result.NewVersion == "" {
		if result.HaveUpdate || result.ForceUpdate {
			return AppUpdate{}, errors.New("客户端检查更新接口未提供待更新的版本号")
		}
		result.NewVersion = AppVersion // The server explicitly reports no available update.
	}

	result.MarketURL = c.appMarketURL
	market, marketErr := c.checkAppMarketVersion(ctx)
	if marketErr != nil {
		// The signed client API remains authoritative for mandatory updates. The
		// public market page is supplementary and must not make automation fail.
		result.MarketError = "应用市场版本检查失败：" + marketErr.Error()
		return result, nil
	}
	result.VersionSource = "client_update_api+app_market"
	result.MarketVersion = market.Version
	// Keep the client's mandatory target intact so the execution-gate message
	// never attributes a newer market release to the signed client API.
	if comparison, err := compareAppVersions(market.Version, result.NewVersion); err == nil && comparison > 0 && !result.ForceUpdate {
		result.NewVersion = market.Version
	}
	if comparison, err := compareAppVersions(market.Version, AppVersion); err == nil && comparison > 0 {
		wasAdvertisedByClientAPI := result.HaveUpdate
		result.HaveUpdate = true
		if !wasAdvertisedByClientAPI {
			result.UpdateTitle = "应用市场已发布 " + market.Version
			result.UpdateDescription = "客户端更新接口尚未下发升级，但应用市场详情页已出现更高版本；该结果仅标记为可选更新，不改变服务端的强制更新判定。"
		}
	}
	return result, nil
}

func (c *Client) checkAppMarketVersion(ctx context.Context) (appMarketRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.appMarketURL, nil)
	if err != nil {
		return appMarketRecord{}, fmt.Errorf("创建请求：%w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "EasyRun-version-check/1.0")
	response, err := c.http.Do(req)
	if err != nil {
		return appMarketRecord{}, fmt.Errorf("请求详情页：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return appMarketRecord{}, fmt.Errorf("详情页返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAppMarketPage+1))
	if err != nil {
		return appMarketRecord{}, fmt.Errorf("读取详情页：%w", err)
	}
	if len(body) > maxAppMarketPage {
		return appMarketRecord{}, errors.New("详情页超过大小限制")
	}
	return parseAppMarketPage(body, appMarketPackage)
}

func parseAppMarketPage(page []byte, packageName string) (appMarketRecord, error) {
	marker := []byte(`id="__NEXT_DATA__"`)
	markerAt := bytes.Index(page, marker)
	if markerAt < 0 {
		return appMarketRecord{}, errors.New("详情页缺少 __NEXT_DATA__")
	}
	start := bytes.IndexByte(page[markerAt+len(marker):], '>')
	if start < 0 {
		return appMarketRecord{}, errors.New("详情页的 __NEXT_DATA__ 标签不完整")
	}
	start += markerAt + len(marker) + 1
	endOffset := bytes.Index(page[start:], []byte("</script>"))
	if endOffset < 0 {
		return appMarketRecord{}, errors.New("详情页的 __NEXT_DATA__ 内容不完整")
	}
	var document any
	if err := json.Unmarshal(page[start:start+endOffset], &document); err != nil {
		return appMarketRecord{}, fmt.Errorf("解析 __NEXT_DATA__：%w", err)
	}
	record, found := findAppMarketRecord(document, packageName)
	if !found {
		return appMarketRecord{}, fmt.Errorf("详情页未包含应用 %s", packageName)
	}
	version, _, err := parseAppVersion(record.Version)
	if err != nil {
		return appMarketRecord{}, fmt.Errorf("应用市场版本号无效：%w", err)
	}
	record.Version = version
	return record, nil
}

func findAppMarketRecord(value any, packageName string) (appMarketRecord, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if name, _ := typed["pkg_name"].(string); strings.TrimSpace(name) == packageName {
			version, _ := typed["version_name"].(string)
			if strings.TrimSpace(version) != "" {
				return appMarketRecord{PackageName: name, Version: version}, true
			}
		}
		for _, child := range typed {
			if record, ok := findAppMarketRecord(child, packageName); ok {
				return record, true
			}
		}
	case []any:
		for _, child := range typed {
			if record, ok := findAppMarketRecord(child, packageName); ok {
				return record, true
			}
		}
	}
	return appMarketRecord{}, false
}

func compareAppVersions(left, right string) (int, error) {
	_, leftParts, err := parseAppVersion(left)
	if err != nil {
		return 0, err
	}
	_, rightParts, err := parseAppVersion(right)
	if err != nil {
		return 0, err
	}
	length := len(leftParts)
	if len(rightParts) > length {
		length = len(rightParts)
	}
	for index := 0; index < length; index++ {
		var leftPart, rightPart uint64
		if index < len(leftParts) {
			leftPart = leftParts[index]
		}
		if index < len(rightParts) {
			rightPart = rightParts[index]
		}
		if leftPart < rightPart {
			return -1, nil
		}
		if leftPart > rightPart {
			return 1, nil
		}
	}
	return 0, nil
}

func parseAppVersion(value string) (string, []uint64, error) {
	normalized := strings.TrimSpace(value)
	if strings.HasPrefix(normalized, "v") || strings.HasPrefix(normalized, "V") {
		normalized = strings.TrimSpace(normalized[1:])
	}
	parts := strings.Split(normalized, ".")
	if len(parts) == 0 || len(parts) > 8 {
		return "", nil, fmt.Errorf("%q 不是数字版本号", value)
	}
	numbers := make([]uint64, len(parts))
	for index, part := range parts {
		if part == "" {
			return "", nil, fmt.Errorf("%q 不是数字版本号", value)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return "", nil, fmt.Errorf("%q 不是数字版本号", value)
			}
		}
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return "", nil, fmt.Errorf("%q 不是数字版本号", value)
		}
		numbers[index] = number
	}
	return normalized, numbers, nil
}
