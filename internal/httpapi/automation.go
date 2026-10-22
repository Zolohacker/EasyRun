package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	automationpkg "github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

func (a *api) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListJobs(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) createJob(w http.ResponseWriter, r *http.Request) {
	var job platform.AutomationJob
	if err := decodeJSON(w, r, &job, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	job.ID = platform.NewID("job")
	if err := a.normalizeJob(r, &job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		return
	}
	created, err := a.store.CreateJob(r.Context(), job)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *api) getJob(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *api) updateJob(w http.ResponseWriter, r *http.Request) {
	var job platform.AutomationJob
	if err := decodeJSON(w, r, &job, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	job.ID = r.PathValue("id")
	if err := a.normalizeJob(r, &job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		return
	}
	updated, err := a.store.UpdateJob(r.Context(), job)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteJob(r.Context(), r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) runJobNow(w http.ResponseWriter, r *http.Request) {
	if a.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation_unavailable", "automation scheduler is not running")
		return
	}
	executionID, err := a.automation.Trigger(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"execution_id": executionID, "status": "queued"})
}

func (a *api) listExecutions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.store.ListExecutions(r.Context(), limit)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) normalizeJob(r *http.Request, job *platform.AutomationJob) error {
	job.ScheduleMode = strings.TrimSpace(job.ScheduleMode)
	job.ManualTime = strings.TrimSpace(job.ManualTime)
	job.DayScope = strings.TrimSpace(job.DayScope)
	job.CronExpr = strings.TrimSpace(job.CronExpr)
	job.Timezone = strings.TrimSpace(job.Timezone)
	if job.AccountID == "" || job.PlanID == "" || job.AreaID == "" {
		return errors.New("account_id, plan_id and area_id are required")
	}
	if job.Timezone == "" {
		job.Timezone = "Asia/Shanghai"
	}
	if _, err := a.store.GetAccount(r.Context(), job.AccountID); err != nil {
		return errors.New("account_id does not exist")
	}
	jobs, err := a.store.ListJobs(r.Context())
	if err != nil {
		return fmt.Errorf("读取现有自动任务: %w", err)
	}
	for _, existing := range jobs {
		if existing.AccountID == job.AccountID && existing.ID != job.ID {
			return errors.New("该账号已经有自动任务，请直接编辑现有任务")
		}
	}
	plan, err := a.store.GetPlan(r.Context(), job.PlanID)
	if err != nil {
		return errors.New("plan_id does not exist")
	}
	if plan.SourceAccountID != job.AccountID || plan.RemoteCode == "" {
		return errors.New("plan_id must be a plan synchronized from this account")
	}
	if job.DailyRunLimit <= 0 {
		job.DailyRunLimit = 1
	}
	if plan.Rules.DayMaxTimes > 0 && job.DailyRunLimit > plan.Rules.DayMaxTimes {
		return fmt.Errorf("daily_run_limit 必须在 1–%d 次之间", plan.Rules.DayMaxTimes)
	}
	if plan.Rules.DayMaxTimes <= 0 && job.DailyRunLimit != 1 {
		return errors.New("服务器尚未返回每日次数范围，请先同步账号计划")
	}
	weekMin, weekMax := plan.Rules.WeekMinTimes, plan.Rules.WeekMaxTimes
	if weekMax > 0 {
		if weekMin <= 0 {
			weekMin = 1
		}
		if job.WeeklyRunLimit <= 0 {
			job.WeeklyRunLimit = weekMin
		}
		if job.WeeklyRunLimit < weekMin || job.WeeklyRunLimit > weekMax {
			return fmt.Errorf("weekly_run_limit 必须在 %d–%d 次之间", weekMin, weekMax)
		}
	} else if weekMin > 0 {
		if job.WeeklyRunLimit <= 0 {
			job.WeeklyRunLimit = weekMin
		}
		if job.WeeklyRunLimit != weekMin {
			return fmt.Errorf("服务器只返回每周最低 %d 次，当前任务固定使用该次数", weekMin)
		}
	} else {
		job.WeeklyRunLimit = 0
	}
	selection, err := activity.SampleAutomationTargets(plan.TargetDistanceM, plan.Profile, plan.Rules, job.TargetDistanceM, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("target_distance_m: %w", err)
	}
	job.TargetDistanceM = selection.TargetDistanceM
	if plan.Rules.DayMaxDistanceM > 0 {
		if job.DailyDistanceLimitM <= 0 {
			job.DailyDistanceLimitM = plan.Rules.DayMaxDistanceM
		}
		if job.DailyDistanceLimitM > plan.Rules.DayMaxDistanceM+0.5 {
			return fmt.Errorf("daily_distance_limit_m 不得超过服务器每日 %.2f km 的限制", plan.Rules.DayMaxDistanceM/1000)
		}
		if job.DailyDistanceLimitM+0.5 < job.TargetDistanceM {
			return errors.New("每日公里数限额必须不小于单次任务距离")
		}
	} else {
		job.DailyDistanceLimitM = 0
	}
	if plan.Rules.WeekMaxDistanceM > 0 {
		if job.WeeklyDistanceLimitM <= 0 {
			job.WeeklyDistanceLimitM = plan.Rules.WeekMaxDistanceM
		}
		if job.WeeklyDistanceLimitM > plan.Rules.WeekMaxDistanceM+0.5 {
			return fmt.Errorf("weekly_distance_limit_m 不得超过服务器每周 %.2f km 的限制", plan.Rules.WeekMaxDistanceM/1000)
		}
		if job.WeeklyDistanceLimitM+0.5 < job.TargetDistanceM {
			return errors.New("每周公里数限额必须不小于单次任务距离")
		}
	} else {
		job.WeeklyDistanceLimitM = 0
	}
	area, err := a.store.GetArea(r.Context(), job.AreaID)
	if err != nil {
		return errors.New("area_id does not exist")
	}
	if area.SourceAccountID != job.AccountID || area.RemoteCode == "" {
		return errors.New("area_id must be an area synchronized from this account")
	}
	if job.ScheduleMode == "" {
		if job.CronExpr != "" {
			job.ScheduleMode = platform.ScheduleModeCron
		} else {
			job.ScheduleMode = platform.ScheduleModeManual
		}
	}
	switch job.ScheduleMode {
	case platform.ScheduleModeCron:
		if job.CronExpr == "" {
			return errors.New("cron_expr is required for cron schedule mode")
		}
		job.ManualTime = ""
		job.DayScope = ""
	case platform.ScheduleModeManual:
		if job.ManualTime == "" {
			job.ManualTime = "06:30"
		}
		parsedTime, parseErr := time.Parse("15:04", job.ManualTime)
		if parseErr != nil {
			return errors.New("manual_time must use HH:mm")
		}
		job.ManualTime = parsedTime.Format("15:04")
		if job.DayScope == "" {
			job.DayScope = platform.DayScopeEveryDay
		}
		expression, cronErr := automationpkg.ManualCron(job.ManualTime, job.DayScope)
		if cronErr != nil {
			return cronErr
		}
		job.CronExpr = expression
	case platform.ScheduleModePlanRandom:
		if len(plan.Rules.TimeWindows) == 0 {
			return errors.New("该跑步计划没有同步到规定时间范围，请先重新同步账号计划")
		}
		job.ManualTime = ""
		job.DayScope = ""
		job.CronExpr = ""
	default:
		return errors.New("schedule_mode must be manual, plan_random, or cron")
	}
	if !job.Enabled {
		if _, timezoneErr := time.LoadLocation(job.Timezone); timezoneErr != nil {
			return fmt.Errorf("load timezone: %w", timezoneErr)
		}
		if job.ScheduleMode != platform.ScheduleModePlanRandom {
			if _, scheduleErr := automationpkg.NextScheduledRun(*job, plan, time.Now().UTC()); scheduleErr != nil {
				return scheduleErr
			}
		}
		job.NextRunAt = nil
		return nil
	}
	next, err := automationpkg.NextScheduledRun(*job, plan, time.Now().UTC())
	if err != nil {
		return err
	}
	job.NextRunAt = &next
	return nil
}
