package automation

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/Zolohacker/EasyRun/internal/store"
	"github.com/robfig/cron/v3"
)

type Submitter interface {
	Submit(context.Context, platform.Account, platform.RunningPlan, platform.RunningArea, activity.SubmissionRequest) (protocol.SubmissionResult, activity.Run, error)
}

type Billing interface {
	Reserve(context.Context, string, platform.JobContext) error
	Finish(context.Context, string, bool) error
}

type Options struct {
	Billing           Billing
	Workers           int
	WorkerConcurrency int
	PollInterval      time.Duration
	RunTimeout        time.Duration
	Logger            *slog.Logger
}

type Stats struct {
	Workers           int    `json:"workers"`
	WorkerProcesses   int    `json:"worker_processes"`
	WorkerConcurrency int    `json:"worker_concurrency"`
	Active            int64  `json:"active"`
	QueueDepth        int    `json:"queue_depth"`
	Queued            uint64 `json:"queued"`
	Succeeded         uint64 `json:"succeeded"`
	Failed            uint64 `json:"failed"`
	Skipped           uint64 `json:"skipped"`
}

type workItem struct {
	executionID string
	jobID       string
	scheduled   time.Time
}

type Scheduler struct {
	billing           Billing
	store             store.Store
	submitter         Submitter
	pollInterval      time.Duration
	runTimeout        time.Duration
	logger            *slog.Logger
	queue             chan workItem
	startOnce         sync.Once
	workerMu          sync.RWMutex
	workerProcesses   int
	workerConcurrency int
	rootCtx           context.Context
	workerCancel      context.CancelFunc
	started           bool
	active            atomic.Int64
	queued            atomic.Uint64
	succeeded         atomic.Uint64
	failed            atomic.Uint64
	skipped           atomic.Uint64
}

func New(database store.Store, submitter Submitter, options Options) *Scheduler {
	if options.Workers < 1 {
		options.Workers = 2
	}
	if options.WorkerConcurrency < 1 {
		options.WorkerConcurrency = 1
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 10 * time.Second
	}
	if options.RunTimeout <= 0 {
		options.RunTimeout = 3 * time.Minute
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Scheduler{
		billing: options.Billing,
		store:   database, submitter: submitter, workerProcesses: options.Workers,
		workerConcurrency: options.WorkerConcurrency,
		pollInterval:      options.PollInterval, runTimeout: options.RunTimeout, logger: options.Logger,
		queue: make(chan workItem, 16*32*16),
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		s.workerMu.Lock()
		s.rootCtx = ctx
		s.started = true
		s.restartWorkersLocked()
		s.workerMu.Unlock()
		go s.poll(ctx)
	})
}

func (s *Scheduler) Stats() Stats {
	s.workerMu.RLock()
	processes, concurrency := s.workerProcesses, s.workerConcurrency
	s.workerMu.RUnlock()
	return Stats{
		Workers: processes * concurrency, WorkerProcesses: processes,
		WorkerConcurrency: concurrency, Active: s.active.Load(), QueueDepth: len(s.queue),
		Queued: s.queued.Load(), Succeeded: s.succeeded.Load(), Failed: s.failed.Load(), Skipped: s.skipped.Load(),
	}
}

// Reconfigure changes the logical worker-process count and the execution
// concurrency inside each worker. Running executions are allowed to finish;
// newly started workers immediately use the new limits.
func (s *Scheduler) Reconfigure(processes, concurrency int) error {
	if processes < 1 || processes > 16 {
		return errors.New("worker_processes must be between 1 and 16")
	}
	if concurrency < 1 || concurrency > 32 {
		return errors.New("worker_concurrency must be between 1 and 32")
	}
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workerProcesses == processes && s.workerConcurrency == concurrency {
		return nil
	}
	s.workerProcesses, s.workerConcurrency = processes, concurrency
	if s.started {
		s.restartWorkersLocked()
	}
	return nil
}

func (s *Scheduler) restartWorkersLocked() {
	if s.workerCancel != nil {
		s.workerCancel()
	}
	workerCtx, cancel := context.WithCancel(s.rootCtx)
	s.workerCancel = cancel
	for process := 1; process <= s.workerProcesses; process++ {
		for thread := 1; thread <= s.workerConcurrency; thread++ {
			go s.worker(workerCtx, s.rootCtx, process, thread)
		}
	}
}

func (s *Scheduler) Trigger(ctx context.Context, jobID string) (string, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	executionID := platform.NewID("exec")
	item := workItem{executionID: executionID, jobID: job.ID, scheduled: time.Now().UTC()}
	select {
	case s.queue <- item:
		s.queued.Add(1)
		return executionID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		return "", errors.New("automation queue is full")
	}
}

func (s *Scheduler) poll(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	s.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollOnce(ctx)
		}
	}
}

func (s *Scheduler) pollOnce(ctx context.Context) {
	now := time.Now().UTC()
	jobs, err := s.store.DueJobs(ctx, now, 50)
	if err != nil {
		s.logger.Error("load due automation jobs", "error", err)
		return
	}
	for _, job := range jobs {
		if job.NextRunAt == nil {
			continue
		}
		var plan platform.RunningPlan
		if job.ScheduleMode == platform.ScheduleModePlanRandom {
			loadedPlan, planErr := s.store.GetPlan(ctx, job.PlanID)
			if planErr != nil {
				s.logger.Error("load plan for random schedule", "job_id", job.ID, "error", planErr)
				continue
			}
			plan = loadedPlan
		}
		next, err := NextScheduledRun(job, plan, now)
		if err != nil {
			s.logger.Error("invalid persisted schedule", "job_id", job.ID, "error", err)
			continue
		}
		claimed, err := s.store.ClaimJob(ctx, job.ID, *job.NextRunAt, next)
		if err != nil {
			s.logger.Error("claim automation job", "job_id", job.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		item := workItem{executionID: platform.NewID("exec"), jobID: job.ID, scheduled: *job.NextRunAt}
		select {
		case s.queue <- item:
			s.queued.Add(1)
		default:
			s.skipped.Add(1)
			s.recordSkipped(ctx, item, job.AccountID, "automation queue is full")
		}
	}
}

func (s *Scheduler) worker(workerCtx, executionCtx context.Context, process, thread int) {
	for {
		select {
		case <-workerCtx.Done():
			return
		case item := <-s.queue:
			if workerCtx.Err() != nil {
				select {
				case s.queue <- item:
				case <-executionCtx.Done():
				}
				return
			}
			s.active.Add(1)
			s.execute(executionCtx, item, process, thread)
			s.active.Add(-1)
		}
	}
}

func (s *Scheduler) execute(parent context.Context, item workItem, workerProcess, workerThread int) {
	ctx, cancel := context.WithTimeout(parent, s.runTimeout)
	defer cancel()
	jobContext, err := s.store.GetJobContext(ctx, item.jobID)
	if err != nil {
		s.failed.Add(1)
		s.logger.Error("load automation context", "job_id", item.jobID, "error", err)
		return
	}
	execution := platform.Execution{
		ID: item.executionID, JobID: item.jobID, AccountID: jobContext.Account.ID,
		ScheduledFor: item.scheduled, StartedAt: time.Now().UTC(), Status: "running",
	}
	if err := s.store.CreateExecution(ctx, execution); err != nil {
		s.skipped.Add(1)
		s.logger.Warn("skip overlapping automation execution", "job_id", item.jobID, "error", err)
		return
	}
	finish := func(status, message string, runID *string, remoteRunID string) {
		if err := s.store.FinishExecution(context.WithoutCancel(ctx), execution.ID, status, runID, remoteRunID, message, time.Now().UTC()); err != nil {
			s.logger.Error("finish automation execution", "execution_id", execution.ID, "error", err)
		}
	}
	if !jobContext.Account.Enabled {
		s.skipped.Add(1)
		finish("skipped", "account is disabled", nil, "")
		return
	}
	seed := scheduleSeed(jobContext.Job.ID, item.scheduled)
	selection, err := activity.SampleAutomationTargets(jobContext.Plan.TargetDistanceM, jobContext.Plan.Profile, jobContext.Plan.Rules, jobContext.Job.TargetDistanceM, seed)
	if err != nil {
		s.failed.Add(1)
		finish("failed", err.Error(), nil, "")
		return
	}
	jobContext.Plan.TargetDistanceM = selection.TargetDistanceM
	jobContext.Plan.Profile = selection.Profile
	// LocationComponent in the 8.7.9 client requests a fixed 3000 ms cadence.
	// Force it for older plans persisted before timing metadata was synced.
	jobContext.Plan.Profile.SampleIntervalS = 3
	if s.submitter == nil {
		s.failed.Add(1)
		finish("failed", "目标协议提交服务未配置", nil, "")
		return
	}
	if s.billing != nil {
		if err := s.billing.Reserve(ctx, execution.ID, jobContext); err != nil {
			s.skipped.Add(1)
			finish("skipped", err.Error(), nil, "")
			return
		}
	}
	paidSuccess := false
	defer func() {
		if s.billing != nil {
			if err := s.billing.Finish(context.WithoutCancel(ctx), execution.ID, paidSuccess); err != nil {
				s.logger.Error("finalize execution charge", "execution_id", execution.ID, "error", err)
			}
		}
	}()
	submission, run, err := s.submitter.Submit(ctx, jobContext.Account, jobContext.Plan, jobContext.Area, activity.SubmissionRequest{
		TargetDistanceM:      jobContext.Job.TargetDistanceM,
		Profile:              jobContext.Plan.Profile,
		Seed:                 seed,
		AccountID:            jobContext.Account.ID,
		AutomationJobID:      jobContext.Job.ID,
		DailyRunLimit:        jobContext.Job.DailyRunLimit,
		WeeklyRunLimit:       jobContext.Job.WeeklyRunLimit,
		DailyDistanceLimitM:  jobContext.Job.DailyDistanceLimitM,
		WeeklyDistanceLimitM: jobContext.Job.WeeklyDistanceLimitM,
		Timezone:             jobContext.Job.Timezone,
	})
	if err != nil {
		if errors.Is(err, activity.ErrRunQuotaExceeded) {
			s.skipped.Add(1)
			finish("skipped", err.Error(), nil, "")
			return
		}
		s.failed.Add(1)
		finish("failed", err.Error(), nil, "")
		return
	}
	paidSuccess = true // Upstream succeeded even when local persistence later fails.
	if err := s.store.Save(ctx, run); err != nil {
		s.failed.Add(1)
		finish("failed", err.Error(), nil, submission.RunRecordCode)
		return
	}
	s.succeeded.Add(1)
	finish("succeeded", submission.Message, &run.ID, submission.RunRecordCode)
	s.logger.Info("automation execution completed", "worker_process", workerProcess, "worker_thread", workerThread, "job_id", item.jobID, "run_id", run.ID, "remote_run_id", submission.RunRecordCode)
}

func (s *Scheduler) recordSkipped(ctx context.Context, item workItem, accountID, message string) {
	now := time.Now().UTC()
	execution := platform.Execution{ID: item.executionID, JobID: item.jobID, AccountID: accountID, ScheduledFor: item.scheduled, StartedAt: now, FinishedAt: &now, Status: "skipped", Error: message}
	if err := s.store.CreateExecution(ctx, execution); err != nil {
		s.logger.Error("record skipped execution", "job_id", item.jobID, "error", err)
	}
}

func NextRun(expression, timezone string, after time.Time) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone: %w", err)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron: %w", err)
	}
	return schedule.Next(after.In(location)).UTC(), nil
}

// ManualCron turns the simple fixed-time controls into the cron expression
// retained for backwards compatibility and external API clients.
func ManualCron(manualTime, dayScope string) (string, error) {
	clock, err := time.Parse("15:04", strings.TrimSpace(manualTime))
	if err != nil {
		return "", errors.New("manual_time must use HH:mm")
	}
	dayOfWeek := "*"
	switch strings.TrimSpace(dayScope) {
	case "", platform.DayScopeEveryDay:
		dayScope = platform.DayScopeEveryDay
	case platform.DayScopeWorkday:
		dayOfWeek = "1-5"
	case platform.DayScopeWeekend:
		dayOfWeek = "0,6"
	default:
		return "", errors.New("day_scope must be every_day, workday, or weekend")
	}
	return fmt.Sprintf("%d %d * * %s", clock.Minute(), clock.Hour(), dayOfWeek), nil
}

// NextScheduledRun calculates the next persisted execution time for all
// supported scheduling modes. A plan-random instant is selected once and then
// stored in PostgreSQL, so restarts never reroll an already announced run.
func NextScheduledRun(job platform.AutomationJob, plan platform.RunningPlan, after time.Time) (time.Time, error) {
	switch job.ScheduleMode {
	case "", platform.ScheduleModeCron:
		return NextRun(job.CronExpr, job.Timezone, after)
	case platform.ScheduleModeManual:
		expression, err := ManualCron(job.ManualTime, job.DayScope)
		if err != nil {
			return time.Time{}, err
		}
		return NextRun(expression, job.Timezone, after)
	case platform.ScheduleModePlanRandom:
		// Consuming a persisted random slot must advance to another local date,
		// rather than repeatedly drawing more times from the same day's window.
		if job.NextRunAt != nil && !job.NextRunAt.After(after) {
			location, err := time.LoadLocation(job.Timezone)
			if err != nil {
				return time.Time{}, fmt.Errorf("load timezone: %w", err)
			}
			consumed := job.NextRunAt.In(location)
			endOfConsumedDate := time.Date(consumed.Year(), consumed.Month(), consumed.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), location)
			if endOfConsumedDate.After(after.In(location)) {
				after = endOfConsumedDate.UTC()
			}
		}
		return nextPlanRandomRun(job.ID, job.Timezone, plan.Rules, after, estimatedRunDuration(job, plan))
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule_mode %q", job.ScheduleMode)
	}
}

type scheduleSpan struct {
	start time.Time
	end   time.Time
}

// NextPlanRandomRun chooses a weighted random second from the next applicable
// server time fragment. The date and weekday checks are evaluated in the job's
// timezone and the school rule's start/end dates are inclusive.
func NextPlanRandomRun(jobID, timezone string, rules activity.RunRules, after time.Time) (time.Time, error) {
	return nextPlanRandomRun(jobID, timezone, rules, after, 0)
}

func nextPlanRandomRun(jobID, timezone string, rules activity.RunRules, after time.Time, reserve time.Duration) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone: %w", err)
	}
	if len(rules.TimeWindows) == 0 {
		return time.Time{}, errors.New("跑步计划没有返回可用时间范围，请先重新同步账号计划")
	}
	activeStart, err := parseRuleDate(rules.ActiveStartDate, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析计划开始日期: %w", err)
	}
	activeEnd, err := parseRuleDate(rules.ActiveEndDate, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析计划结束日期: %w", err)
	}
	if !activeStart.IsZero() && !activeEnd.IsZero() && activeEnd.Before(activeStart) {
		return time.Time{}, errors.New("跑步计划的开始日期晚于结束日期")
	}

	localAfter := after.In(location)
	firstDate := localDate(localAfter)
	if !activeStart.IsZero() && firstDate.Before(activeStart) {
		firstDate = activeStart
	}
	searchDays := 370
	if !activeEnd.IsZero() {
		if firstDate.After(activeEnd) {
			return time.Time{}, errors.New("跑步计划已超过规定结束日期")
		}
		searchDays = int(activeEnd.Sub(firstDate).Hours()/24) + 2
	}
	if searchDays > 4000 {
		searchDays = 4000
	}

	for offset := 0; offset < searchDays; offset++ {
		date := firstDate.AddDate(0, 0, offset)
		if !activeEnd.IsZero() && date.After(activeEnd) {
			break
		}
		spans := planSpansForDate(date, rules.TimeWindows, localAfter, reserve)
		if len(spans) == 0 {
			continue
		}
		totalSeconds := int64(0)
		for _, span := range spans {
			totalSeconds += int64(span.end.Sub(span.start) / time.Second)
		}
		if totalSeconds <= 0 {
			continue
		}
		pick := deterministicOffset(jobID, after, date, totalSeconds)
		for _, span := range spans {
			seconds := int64(span.end.Sub(span.start) / time.Second)
			if pick < seconds {
				return span.start.Add(time.Duration(pick) * time.Second).UTC(), nil
			}
			pick -= seconds
		}
	}
	return time.Time{}, errors.New("跑步计划在有效日期内没有未来可用时间范围")
}

func planSpansForDate(date time.Time, windows []activity.RunTimeWindow, after time.Time, reserve time.Duration) []scheduleSpan {
	earliest := after.Truncate(time.Minute).Add(time.Minute)
	spans := make([]scheduleSpan, 0, len(windows))
	for _, window := range windows {
		if !dayScopeMatches(window.DayType, date.Weekday()) || window.StartMinute < 0 || window.StartMinute >= 24*60 || window.EndMinute < 0 || window.EndMinute > 24*60 {
			continue
		}
		start := date.Add(time.Duration(window.StartMinute) * time.Minute)
		end := date.Add(time.Duration(window.EndMinute) * time.Minute)
		if window.EndMinute <= window.StartMinute {
			end = end.AddDate(0, 0, 1)
		}
		end = end.Add(-reserve)
		if start.Before(earliest) {
			start = earliest
		}
		if end.After(start) {
			spans = append(spans, scheduleSpan{start: start, end: end})
		}
	}
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if !span.start.After(last.end) {
			if span.end.After(last.end) {
				last.end = span.end
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func estimatedRunDuration(job platform.AutomationJob, plan platform.RunningPlan) time.Duration {
	distanceM := job.TargetDistanceM
	if resolved, err := activity.AutomationTargetDistance(plan.TargetDistanceM, plan.Rules, distanceM); err == nil {
		distanceM = resolved
	} else if distanceM <= 0 {
		distanceM = plan.TargetDistanceM
	}
	pace := plan.Profile.TargetPaceSecPerKM
	if plan.Rules.SlowestPaceSecPerKM > 0 {
		pace = plan.Rules.SlowestPaceSecPerKM
	}
	if pace <= 0 {
		pace = 360
	}
	// Reserve the conservative rule-bound duration plus a small finish/upload
	// buffer so the full real-time replay remains within the allowed fragment.
	seconds := distanceM/1000*pace*1.08 + 120
	return time.Duration(seconds * float64(time.Second))
}

func dayScopeMatches(dayType string, weekday time.Weekday) bool {
	switch dayType {
	case platform.DayScopeEveryDay:
		return true
	case platform.DayScopeWorkday:
		return weekday >= time.Monday && weekday <= time.Friday
	case platform.DayScopeWeekend:
		return weekday == time.Saturday || weekday == time.Sunday
	default:
		return false
	}
}

func parseRuleDate(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006.01.02", "2006年1月2日", "2006-01-02 15:04:05", "2006/01/02 15:04:05", time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return localDate(parsed.In(location)), nil
		}
	}
	if unixValue, err := strconv.ParseInt(value, 10, 64); err == nil {
		if unixValue > 1_000_000_000_000 {
			unixValue /= 1000
		}
		return localDate(time.Unix(unixValue, 0).In(location)), nil
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", value)
}

func localDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func deterministicOffset(jobID string, after, date time.Time, total int64) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(jobID))
	_, _ = hash.Write([]byte("|" + after.UTC().Format(time.RFC3339Nano)))
	_, _ = hash.Write([]byte("|" + date.Format("2006-01-02")))
	return int64(hash.Sum64() % uint64(total))
}

func scheduleSeed(jobID string, scheduled time.Time) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(jobID))
	return int64(hash.Sum64() ^ uint64(scheduled.UnixNano()))
}
