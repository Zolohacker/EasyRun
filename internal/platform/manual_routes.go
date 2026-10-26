package platform

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/route"
)

type ManualRoute struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	AccountID       string      `json:"account_id"`
	PlanID          string      `json:"plan_id"`
	AreaID          string      `json:"area_id"`
	Points          []geo.Point `json:"points"`
	DistanceM       float64     `json:"distance_m"`
	CreatedByUserID string      `json:"created_by_user_id,omitempty"`
	SourceJobID     string      `json:"source_job_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

func ValidateManualRoute(m *ManualRoute, plan RunningPlan, area RunningArea) error {
	if m.AccountID == "" || m.PlanID != plan.ID || m.AreaID != area.ID || plan.SourceAccountID != m.AccountID || area.SourceAccountID != m.AccountID || plan.RemoteCode == "" || area.RemoteCode == "" {
		return errors.New("路线必须绑定该账号同步的计划和区域")
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len([]rune(m.Name)) > 80 {
		return errors.New("路线名称需要 1–80 个字符")
	}
	minimum := plan.Rules.MinDistanceM
	if minimum <= 0 {
		minimum = plan.TargetDistanceM
	}
	points, distance, err := route.ValidateManual(m.Points, area.Boundary, minimum)
	if err != nil {
		return err
	}
	m.Points, m.DistanceM = points, distance
	return nil
}

func ValidateJobRoute(job AutomationJob, m ManualRoute, plan RunningPlan, area RunningArea) error {
	if job.AccountID != m.AccountID || job.PlanID != m.PlanID || job.AreaID != m.AreaID || job.ManualRouteID != m.ID {
		return errors.New("手绘路线与任务账号、计划或区域不匹配")
	}
	if err := ValidateManualRoute(&m, plan, area); err != nil {
		return err
	}
	if job.TargetDistanceM > m.DistanceM+1e-6 {
		return fmt.Errorf("任务距离 %.0f 米大于手绘路线 %.1f 米，请降低距离或延长路线", job.TargetDistanceM, m.DistanceM)
	}
	return nil
}
