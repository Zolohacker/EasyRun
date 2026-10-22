package automation

import (
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

func TestNextRunWithTimezone(t *testing.T) {
	after := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	next, err := NextRun("30 6 * * 1-5", "Asia/Shanghai", after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 21, 22, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next=%s want=%s", next, want)
	}
}

func TestNextRunRejectsInvalidSchedule(t *testing.T) {
	if _, err := NextRun("not a cron", "Asia/Shanghai", time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestManualCronAndNextScheduledRun(t *testing.T) {
	expression, err := ManualCron("06:30", platform.DayScopeWorkday)
	if err != nil {
		t.Fatal(err)
	}
	if expression != "30 6 * * 1-5" {
		t.Fatalf("expression=%q", expression)
	}
	job := platform.AutomationJob{ScheduleMode: platform.ScheduleModeManual, ManualTime: "21:15", DayScope: platform.DayScopeWeekend, Timezone: "Asia/Shanghai"}
	next, err := NextScheduledRun(job, platform.RunningPlan{}, time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	local, _ := time.LoadLocation("Asia/Shanghai")
	got := next.In(local)
	if got.Weekday() != time.Saturday || got.Hour() != 21 || got.Minute() != 15 {
		t.Fatalf("next local=%s", got)
	}
}

func TestNextPlanRandomRunUsesApplicableWeekdayWindow(t *testing.T) {
	rules := activity.RunRules{
		ActiveStartDate: "2026-09-01",
		ActiveEndDate:   "2027-01-15",
		TimeWindows: []activity.RunTimeWindow{
			{DayType: platform.DayScopeWorkday, StartMinute: 6 * 60, EndMinute: 8 * 60},
			{DayType: platform.DayScopeWeekend, StartMinute: 18 * 60, EndMinute: 20 * 60},
		},
	}
	after := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) // Friday 20:00 in Shanghai.
	next, err := NextPlanRandomRun("job-test", "Asia/Shanghai", rules, after)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	local := next.In(location)
	if local.Weekday() != time.Saturday || local.Hour() < 18 || local.Hour() >= 20 {
		t.Fatalf("next local=%s", local)
	}
}

func TestNextPlanRandomRunRespectsActiveStartAndMergesWindows(t *testing.T) {
	rules := activity.RunRules{
		ActiveStartDate: "2026-11-02",
		ActiveEndDate:   "2026-11-05",
		TimeWindows: []activity.RunTimeWindow{
			{DayType: platform.DayScopeEveryDay, StartMinute: 360, EndMinute: 420},
			{DayType: platform.DayScopeEveryDay, StartMinute: 390, EndMinute: 480},
		},
	}
	next, err := NextPlanRandomRun("job-test", "Asia/Shanghai", rules, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	local := next.In(location)
	if local.Format("2006-01-02") != "2026-11-02" || local.Hour() < 6 || local.Hour() >= 8 {
		t.Fatalf("next local=%s", local)
	}
}

func TestNextPlanRandomRunRejectsExpiredPlan(t *testing.T) {
	rules := activity.RunRules{
		ActiveStartDate: "2026-01-01",
		ActiveEndDate:   "2026-06-30",
		TimeWindows:     []activity.RunTimeWindow{{DayType: platform.DayScopeEveryDay, StartMinute: 360, EndMinute: 480}},
	}
	if _, err := NextPlanRandomRun("job-test", "Asia/Shanghai", rules, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected expired plan error")
	}
}

func TestNextScheduledPlanRandomAdvancesAfterConsumedDate(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	consumed := time.Date(2026, 10, 21, 12, 30, 0, 0, location).UTC()
	job := platform.AutomationJob{
		ID: "job-test", ScheduleMode: platform.ScheduleModePlanRandom, Timezone: "Asia/Shanghai",
		TargetDistanceM: 2000, NextRunAt: &consumed,
	}
	plan := platform.RunningPlan{
		Profile: activity.Profile{TargetPaceSecPerKM: 360},
		Rules: activity.RunRules{
			ActiveStartDate: "2026-10-01", ActiveEndDate: "2026-12-31",
			TimeWindows: []activity.RunTimeWindow{{DayType: platform.DayScopeEveryDay, StartMinute: 360, EndMinute: 1320}},
		},
	}
	next, err := NextScheduledRun(job, plan, consumed.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := next.In(location).Format("2006-01-02"); got != "2026-10-22" {
		t.Fatalf("next local date=%s", got)
	}
}

func TestNextScheduledPlanRandomReservesRunDuration(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	job := platform.AutomationJob{ID: "job-test", ScheduleMode: platform.ScheduleModePlanRandom, Timezone: "Asia/Shanghai", TargetDistanceM: 5000}
	plan := platform.RunningPlan{
		Profile: activity.Profile{TargetPaceSecPerKM: 360},
		Rules: activity.RunRules{
			ActiveStartDate: "2026-10-22", ActiveEndDate: "2026-10-22", SlowestPaceSecPerKM: 360,
			TimeWindows: []activity.RunTimeWindow{{DayType: platform.DayScopeEveryDay, StartMinute: 360, EndMinute: 420}},
		},
	}
	next, err := NextScheduledRun(job, plan, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	local := next.In(location)
	reserve := estimatedRunDuration(job, plan)
	windowEnd := time.Date(2026, 10, 22, 7, 0, 0, 0, location)
	if local.Add(reserve).After(windowEnd) {
		t.Fatalf("start=%s reserve=%s ends after %s", local, reserve, windowEnd)
	}
}

func TestEstimatedRunDurationUsesMinimumForCountPlan(t *testing.T) {
	job := platform.AutomationJob{TargetDistanceM: 5000}
	plan := platform.RunningPlan{
		TargetDistanceM: 5000,
		Profile:         activity.Profile{TargetPaceSecPerKM: 360},
		Rules: activity.RunRules{
			PlanType: activity.PlanTypeByTimes, MinDistanceM: 1000, MaxDistanceM: 5000,
			SlowestPaceSecPerKM: 600,
		},
	}
	want := time.Duration((1000.0/1000*600*1.08 + 120) * float64(time.Second))
	if got := estimatedRunDuration(job, plan); got != want {
		t.Fatalf("duration=%s want=%s", got, want)
	}
}
