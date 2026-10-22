package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateJob(ctx context.Context, job platform.AutomationJob) (platform.AutomationJob, error) {
	if job.ID == "" {
		job.ID = platform.NewID("job")
	}
	const query = `INSERT INTO automation_jobs
(id,account_id,plan_id,area_id,target_distance_m,daily_run_limit,weekly_run_limit,daily_distance_limit_m,weekly_distance_limit_m,schedule_mode,manual_time,day_scope,cron_expr,timezone,enabled,next_run_at,last_run_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, job.ID, job.AccountID, job.PlanID, job.AreaID, job.TargetDistanceM, job.DailyRunLimit, job.WeeklyRunLimit, job.DailyDistanceLimitM, job.WeeklyDistanceLimitM, job.ScheduleMode, job.ManualTime, job.DayScope, job.CronExpr, job.Timezone, job.Enabled, job.NextRunAt, job.LastRunAt).Scan(&job.CreatedAt, &job.UpdatedAt); err != nil {
		return platform.AutomationJob{}, fmt.Errorf("insert automation job: %w", err)
	}
	return job, nil
}

func (s *PostgresStore) ListJobs(ctx context.Context) ([]platform.AutomationJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM automation_jobs WHERE ($1='' OR account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$1)) ORDER BY created_at DESC`, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list automation jobs: %w", err)
	}
	defer rows.Close()
	var jobs []platform.AutomationJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) GetJob(ctx context.Context, id string) (platform.AutomationJob, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM automation_jobs WHERE id=$1`, id))
}

func (s *PostgresStore) UpdateJob(ctx context.Context, job platform.AutomationJob) (platform.AutomationJob, error) {
	const query = `UPDATE automation_jobs SET account_id=$2,plan_id=$3,area_id=$4,target_distance_m=$5,daily_run_limit=$6,weekly_run_limit=$7,daily_distance_limit_m=$8,weekly_distance_limit_m=$9,schedule_mode=$10,manual_time=$11,day_scope=$12,cron_expr=$13,timezone=$14,enabled=$15,next_run_at=$16,updated_at=now()
WHERE id=$1 RETURNING last_run_at,created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, job.ID, job.AccountID, job.PlanID, job.AreaID, job.TargetDistanceM, job.DailyRunLimit, job.WeeklyRunLimit, job.DailyDistanceLimitM, job.WeeklyDistanceLimitM, job.ScheduleMode, job.ManualTime, job.DayScope, job.CronExpr, job.Timezone, job.Enabled, job.NextRunAt).Scan(&job.LastRunAt, &job.CreatedAt, &job.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.AutomationJob{}, ErrNotFound
	} else if err != nil {
		return platform.AutomationJob{}, fmt.Errorf("update automation job: %w", err)
	}
	return job, nil
}

func (s *PostgresStore) DeleteJob(ctx context.Context, id string) error {
	return deleteByID(ctx, s.pool, "automation_jobs", id)
}

func (s *PostgresStore) DueJobs(ctx context.Context, now time.Time, limit int) ([]platform.AutomationJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+`
FROM automation_jobs WHERE enabled=true AND next_run_at IS NOT NULL AND next_run_at <= $1 ORDER BY next_run_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due jobs: %w", err)
	}
	defer rows.Close()
	var jobs []platform.AutomationJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) ClaimJob(ctx context.Context, id string, expected, next time.Time) (bool, error) {
	result, err := s.pool.Exec(ctx, `UPDATE automation_jobs SET last_run_at=$2,next_run_at=$3,updated_at=now()
WHERE id=$1 AND enabled=true AND next_run_at=$2`, id, expected, next)
	if err != nil {
		return false, fmt.Errorf("claim automation job: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *PostgresStore) GetJobContext(ctx context.Context, id string) (platform.JobContext, error) {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return platform.JobContext{}, err
	}
	account, err := s.GetAccount(ctx, job.AccountID)
	if err != nil {
		return platform.JobContext{}, err
	}
	plan, err := s.GetPlan(ctx, job.PlanID)
	if err != nil {
		return platform.JobContext{}, err
	}
	area, err := s.GetArea(ctx, job.AreaID)
	if err != nil {
		return platform.JobContext{}, err
	}
	return platform.JobContext{Job: job, Account: account, Plan: plan, Area: area}, nil
}

func (s *PostgresStore) CreateExecution(ctx context.Context, execution platform.Execution) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO automation_executions
(id,job_id,account_id,scheduled_for,started_at,finished_at,status,run_id,remote_run_id,error) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		execution.ID, execution.JobID, execution.AccountID, execution.ScheduledFor, execution.StartedAt, execution.FinishedAt, execution.Status, execution.RunID, execution.RemoteRunID, execution.Error)
	if err != nil {
		return fmt.Errorf("insert automation execution: %w", err)
	}
	return nil
}

func (s *PostgresStore) FinishExecution(ctx context.Context, id, status string, runID *string, remoteRunID, message string, finished time.Time) error {
	result, err := s.pool.Exec(ctx, `UPDATE automation_executions SET status=$2,run_id=$3,remote_run_id=$4,error=$5,finished_at=$6 WHERE id=$1`, id, status, runID, remoteRunID, message, finished)
	if err != nil {
		return fmt.Errorf("finish automation execution: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListExecutions(ctx context.Context, limit int) ([]platform.Execution, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,job_id,account_id,scheduled_for,started_at,finished_at,status,run_id,remote_run_id,error
FROM automation_executions WHERE ($2='' OR account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$2)) ORDER BY started_at DESC LIMIT $1`, limit, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list automation executions: %w", err)
	}
	defer rows.Close()
	var executions []platform.Execution
	for rows.Next() {
		var execution platform.Execution
		if err := rows.Scan(&execution.ID, &execution.JobID, &execution.AccountID, &execution.ScheduledFor, &execution.StartedAt, &execution.FinishedAt, &execution.Status, &execution.RunID, &execution.RemoteRunID, &execution.Error); err != nil {
			return nil, fmt.Errorf("scan automation execution: %w", err)
		}
		executions = append(executions, execution)
	}
	return executions, rows.Err()
}

func (s *PostgresStore) ListExecutionPage(ctx context.Context, q platform.ExecutionQuery) (platform.ExecutionPage, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 10
	}
	p := platform.ExecutionPage{Items: []platform.Execution{}, Page: q.Page, PageSize: q.PageSize}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	const filter = ` WHERE ($1='' OR account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$1)) AND ($2='' OR account_id=$2) AND ($3='' OR status=$3)`
	args := []any{ownerFilter(ctx), q.AccountID, q.Status}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM automation_executions`+filter, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	maxPage := max(1, (p.Total+q.PageSize-1)/q.PageSize)
	p.Page = min(p.Page, maxPage)
	args = append(args, q.PageSize, (p.Page-1)*q.PageSize)
	rows, err := tx.Query(ctx, `SELECT id,job_id,account_id,scheduled_for,started_at,finished_at,status,run_id,remote_run_id,error FROM automation_executions`+filter+` ORDER BY started_at DESC,id DESC LIMIT $4 OFFSET $5`, args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var x platform.Execution
		if err = rows.Scan(&x.ID, &x.JobID, &x.AccountID, &x.ScheduledFor, &x.StartedAt, &x.FinishedAt, &x.Status, &x.RunID, &x.RemoteRunID, &x.Error); err != nil {
			return p, err
		}
		p.Items = append(p.Items, x)
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func scanJob(row scanner) (platform.AutomationJob, error) {
	var job platform.AutomationJob
	if err := row.Scan(&job.ID, &job.AccountID, &job.PlanID, &job.AreaID, &job.TargetDistanceM, &job.DailyRunLimit, &job.WeeklyRunLimit, &job.DailyDistanceLimitM, &job.WeeklyDistanceLimitM, &job.ScheduleMode, &job.ManualTime, &job.DayScope, &job.CronExpr, &job.Timezone, &job.Enabled, &job.NextRunAt, &job.LastRunAt, &job.CreatedAt, &job.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.AutomationJob{}, ErrNotFound
	} else if err != nil {
		return platform.AutomationJob{}, fmt.Errorf("scan automation job: %w", err)
	}
	return job, nil
}

const jobColumns = `id,account_id,plan_id,area_id,target_distance_m,daily_run_limit,weekly_run_limit,daily_distance_limit_m,weekly_distance_limit_m,schedule_mode,manual_time,day_scope,cron_expr,timezone,enabled,next_run_at,last_run_at,created_at,updated_at`
