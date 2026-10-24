package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// StopRunUtils.h() uses 7001 when the unfinished record has no matching local
// run data. End that session without inventing distance, duration or steps.
const abnormalMissingRunData = 7001

type unfinishedRun struct {
	RunRecordCode string `json:"runRecordCode"`
	SportType     int    `json:"sportType"`
}

func (c *Client) queryUnfinishedRun(ctx context.Context, device Device, session Session) (*unfinishedRun, error) {
	envelope, err := c.post(ctx, "run/queryUnFinishRun", device, map[string]any{"sportType": "1"}, requestOptions{form: true, session: session})
	if err != nil {
		return nil, err
	}
	// Like the client's nullable model, absent/null data means no record. A
	// non-null but malformed object must not silently permit another start.
	run, err := decodeData[*unfinishedRun](envelope)
	if err != nil {
		return nil, fmt.Errorf("解析未完成跑步响应: %w", err)
	}
	if run != nil && strings.TrimSpace(run.RunRecordCode) == "" {
		return nil, errors.New("未完成跑步响应缺少记录编号")
	}
	return run, nil
}

// The runner holds its per-account lock across this check and the new run.
// End at most the originally observed record; if a different session appears
// during verification, leave it alone and fail this attempt instead of looping.
func (c *Client) finishUnfinishedRunBeforeStart(ctx context.Context, device Device, session Session) error {
	run, err := c.queryUnfinishedRun(ctx, device, session)
	if err != nil {
		return fmt.Errorf("查询未完成跑步: %w", err)
	}
	if run == nil {
		return nil
	}
	if run.SportType != 1 {
		return fmt.Errorf("未完成跑步 %s 的运动类型为 %d，与阳光跑不匹配，本次起跑已停止", run.RunRecordCode, run.SportType)
	}
	if err := c.abortSunRun(ctx, device, session, run.RunRecordCode, abnormalMissingRunData); err != nil {
		return fmt.Errorf("结束未完成跑步 %s 失败，本次起跑已停止: %w", run.RunRecordCode, err)
	}
	remaining, err := c.queryUnfinishedRun(ctx, device, session)
	if err != nil {
		return fmt.Errorf("结束未完成跑步后复查失败，本次起跑已停止: %w", err)
	}
	if remaining != nil {
		return fmt.Errorf("结束后仍存在未完成跑步 %s，本次起跑已停止", remaining.RunRecordCode)
	}
	return nil
}
