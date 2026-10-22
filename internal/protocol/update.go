package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// AppUpdate is the VersionUpgrade model returned by common/update in 8.7.9.
type AppUpdate struct {
	NewVersion        string `json:"newVersion"`
	SupportVersion    string `json:"supportVersion"`
	HaveUpdate        bool   `json:"haveUpdate"`
	ForceUpdate       bool   `json:"forceUpdate"`
	DownloadURL       string `json:"downloadUrl"`
	UpdateTitle       string `json:"updateTitle"`
	UpdateDescription string `json:"updateDescription"`
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
	result.NewVersion = strings.TrimSpace(result.NewVersion)
	if result.NewVersion == "" {
		if result.HaveUpdate || result.ForceUpdate {
			return AppUpdate{}, errors.New("客户端检查更新接口未提供待更新的版本号")
		}
		result.NewVersion = AppVersion // The server explicitly reports no available update.
	}
	return result, nil
}
