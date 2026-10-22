package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("resource not found")

type Store interface {
	Save(context.Context, activity.Run) error
	Get(context.Context, string) (activity.Run, error)
	List(context.Context, int) ([]activity.ListItem, error)
	Ping(context.Context) error
	CreatePlan(context.Context, platform.RunningPlan) (platform.RunningPlan, error)
	ListPlans(context.Context) ([]platform.RunningPlan, error)
	GetPlan(context.Context, string) (platform.RunningPlan, error)
	UpdatePlan(context.Context, platform.RunningPlan) (platform.RunningPlan, error)
	DeletePlan(context.Context, string) error
	CreateArea(context.Context, platform.RunningArea) (platform.RunningArea, error)
	ListAreas(context.Context) ([]platform.RunningArea, error)
	GetArea(context.Context, string) (platform.RunningArea, error)
	UpdateArea(context.Context, platform.RunningArea) (platform.RunningArea, error)
	DeleteArea(context.Context, string) error
	CreateAccount(context.Context, platform.Account) (platform.Account, error)
	ListAccounts(context.Context) ([]platform.Account, error)
	GetAccount(context.Context, string) (platform.Account, error)
	UpdateAccount(context.Context, platform.Account) (platform.Account, error)
	DeleteAccount(context.Context, string) error
	CreateJob(context.Context, platform.AutomationJob) (platform.AutomationJob, error)
	ListJobs(context.Context) ([]platform.AutomationJob, error)
	GetJob(context.Context, string) (platform.AutomationJob, error)
	UpdateJob(context.Context, platform.AutomationJob) (platform.AutomationJob, error)
	DeleteJob(context.Context, string) error
	DueJobs(context.Context, time.Time, int) ([]platform.AutomationJob, error)
	ClaimJob(context.Context, string, time.Time, time.Time) (bool, error)
	GetJobContext(context.Context, string) (platform.JobContext, error)
	CreateExecution(context.Context, platform.Execution) error
	FinishExecution(context.Context, string, string, *string, string, string, time.Time) error
	ListExecutions(context.Context, int) ([]platform.Execution, error)
	GetSystemSettings(context.Context) (platform.SystemSettings, error)
	UpdateSystemSettings(context.Context, platform.SystemSettings) (platform.SystemSettings, error)
}

type PostgresOptions struct {
	URL      string
	MaxConns int32
	MinConns int32
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, options PostgresOptions) (*PostgresStore, error) {
	if options.URL == "" || options.URL == "POSTGRES_DSN" {
		return nil, errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(options.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if options.MaxConns > 0 {
		config.MaxConns = options.MaxConns
	}
	if options.MinConns >= 0 {
		config.MinConns = options.MinConns
	}
	config.MaxConnIdleTime = 10 * time.Minute
	config.MaxConnLifetime = time.Hour
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

// Pool is shared by the transactional identity and billing services.
func (s *PostgresStore) Pool() *pgxpool.Pool { return s.pool }

func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *PostgresStore) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS running_plans (
    id text PRIMARY KEY,
    source_account_id text NOT NULL DEFAULT '',
    remote_code text NOT NULL DEFAULT '',
    semester_code text NOT NULL DEFAULT '',
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    target_distance_m double precision NOT NULL CHECK (target_distance_m BETWEEN 300 AND 100000),
    profile jsonb NOT NULL,
    rules jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS running_areas (
    id text PRIMARY KEY,
    source_account_id text NOT NULL DEFAULT '',
    remote_code text NOT NULL DEFAULT '',
    school_code text NOT NULL DEFAULT '',
    sub_school_code text NOT NULL DEFAULT '',
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    center_lat double precision NOT NULL CHECK (center_lat BETWEEN -90 AND 90),
    center_lng double precision NOT NULL CHECK (center_lng BETWEEN -180 AND 180),
    boundary jsonb NOT NULL DEFAULT '[]'::jsonb,
    heading_deg double precision NOT NULL DEFAULT 0,
    candidate_count integer NOT NULL DEFAULT 4 CHECK (candidate_count BETWEEN 1 AND 8),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS runner_accounts (
    id text PRIMARY KEY,
    login_name text NOT NULL DEFAULT '',
    name text NOT NULL,
    note text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    default_plan_id text REFERENCES running_plans(id) ON DELETE SET NULL,
    default_area_id text REFERENCES running_areas(id) ON DELETE SET NULL,
    fence_index integer NOT NULL DEFAULT 0 CHECK (fence_index >= 0),
    device_id text NOT NULL DEFAULT '',
    model_name text NOT NULL DEFAULT 'Redmi|23013RK75C',
    system_version text NOT NULL DEFAULT '14',
    school_code text NOT NULL DEFAULT '',
    school_name text NOT NULL DEFAULT '',
    student_number text NOT NULL DEFAULT '',
    auth_status text NOT NULL DEFAULT 'unconfigured',
    auth_message text NOT NULL DEFAULT '',
    last_auth_at timestamptz,
    password_cipher bytea NOT NULL DEFAULT ''::bytea,
    session_cipher bytea NOT NULL DEFAULT ''::bytea,
	face_enabled integer NOT NULL DEFAULT 0,
	face_auth boolean NOT NULL DEFAULT false,
	face_similarity_value integer NOT NULL DEFAULT 50,
	face_time integer NOT NULL DEFAULT 0,
	face_body_enabled boolean NOT NULL DEFAULT false,
	face_body_rate integer NOT NULL DEFAULT 0,
	face_model_type integer NOT NULL DEFAULT 1,
	face_base_url text NOT NULL DEFAULT '',
	face_base_cipher bytea NOT NULL DEFAULT ''::bytea,
	face_base_mime text NOT NULL DEFAULT '',
	face_custom_cipher bytea NOT NULL DEFAULT ''::bytea,
	face_custom_mime text NOT NULL DEFAULT '',
	face_synced_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS automation_jobs (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES runner_accounts(id) ON DELETE CASCADE,
    plan_id text NOT NULL REFERENCES running_plans(id) ON DELETE RESTRICT,
    area_id text NOT NULL REFERENCES running_areas(id) ON DELETE RESTRICT,
	target_distance_m double precision NOT NULL DEFAULT 0 CHECK (target_distance_m >= 0),
	daily_run_limit integer NOT NULL DEFAULT 0 CHECK (daily_run_limit >= 0),
	weekly_run_limit integer NOT NULL DEFAULT 0 CHECK (weekly_run_limit >= 0),
	daily_distance_limit_m double precision NOT NULL DEFAULT 0 CHECK (daily_distance_limit_m >= 0),
	weekly_distance_limit_m double precision NOT NULL DEFAULT 0 CHECK (weekly_distance_limit_m >= 0),
    schedule_mode text NOT NULL DEFAULT 'cron',
    manual_time text NOT NULL DEFAULT '',
    day_scope text NOT NULL DEFAULT 'every_day',
    cron_expr text NOT NULL DEFAULT '',
    timezone text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    next_run_at timestamptz,
    last_run_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS running_activities (
    id text PRIMARY KEY,
    schema_version integer NOT NULL,
    synthetic boolean NOT NULL,
    generated_at timestamptz NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    distance_m double precision NOT NULL CHECK (distance_m >= 0),
    duration_s double precision NOT NULL CHECK (duration_s >= 0),
    average_pace_sec_per_km double precision NOT NULL CHECK (average_pace_sec_per_km >= 0),
    total_steps integer NOT NULL CHECK (total_steps >= 0),
    valid boolean NOT NULL,
    account_id text REFERENCES runner_accounts(id) ON DELETE SET NULL,
    automation_job_id text REFERENCES automation_jobs(id) ON DELETE SET NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS map_route_cache (
    cache_key text PRIMARY KEY,
    distance_m double precision NOT NULL CHECK (distance_m >= 0),
    duration_s double precision NOT NULL CHECK (duration_s >= 0),
    points jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    hit_count bigint NOT NULL DEFAULT 0 CHECK (hit_count >= 0),
    last_hit_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS system_settings (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1),
    map_api_key_cipher bytea NOT NULL DEFAULT ''::bytea,
    map_qps double precision NOT NULL DEFAULT 3 CHECK (map_qps > 0 AND map_qps <= 100),
    map_api_keys jsonb NOT NULL DEFAULT '[]'::jsonb,
    worker_processes integer NOT NULL DEFAULT 2 CHECK (worker_processes BETWEEN 1 AND 16),
    worker_concurrency integer NOT NULL DEFAULT 1 CHECK (worker_concurrency BETWEEN 1 AND 32),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS running_activities_started_at_idx ON running_activities (started_at DESC);
CREATE INDEX IF NOT EXISTS running_activities_valid_idx ON running_activities (valid, started_at DESC);
CREATE INDEX IF NOT EXISTS map_route_cache_expires_idx ON map_route_cache (expires_at);
CREATE INDEX IF NOT EXISTS map_route_cache_lru_idx ON map_route_cache (last_hit_at DESC, updated_at DESC);
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS map_api_keys jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE system_settings ALTER COLUMN map_qps SET DEFAULT 3;
ALTER TABLE running_activities ADD COLUMN IF NOT EXISTS account_id text;
ALTER TABLE running_activities ADD COLUMN IF NOT EXISTS automation_job_id text;
ALTER TABLE running_plans ADD COLUMN IF NOT EXISTS source_account_id text NOT NULL DEFAULT '';
ALTER TABLE running_plans ADD COLUMN IF NOT EXISTS remote_code text NOT NULL DEFAULT '';
ALTER TABLE running_plans ADD COLUMN IF NOT EXISTS semester_code text NOT NULL DEFAULT '';
ALTER TABLE running_plans ADD COLUMN IF NOT EXISTS rules jsonb NOT NULL DEFAULT '{}'::jsonb;
	ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS target_distance_m double precision NOT NULL DEFAULT 0;
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS daily_run_limit integer NOT NULL DEFAULT 0;
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS weekly_run_limit integer NOT NULL DEFAULT 0;
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS daily_distance_limit_m double precision NOT NULL DEFAULT 0;
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS weekly_distance_limit_m double precision NOT NULL DEFAULT 0;
ALTER TABLE automation_jobs ALTER COLUMN daily_run_limit SET DEFAULT 0;
ALTER TABLE automation_jobs DROP CONSTRAINT IF EXISTS automation_jobs_daily_run_limit_check;
ALTER TABLE automation_jobs ADD CONSTRAINT automation_jobs_daily_run_limit_check CHECK (daily_run_limit >= 0);
UPDATE automation_jobs AS job
SET daily_run_limit = 0
FROM running_plans AS plan
WHERE job.plan_id = plan.id
  AND COALESCE(
    CASE
      WHEN COALESCE(plan.rules->>'day_max_times', '') ~ '^[0-9]+$'
        THEN (plan.rules->>'day_max_times')::integer
      ELSE 0
    END,
    0
  ) <= 0;
ALTER TABLE automation_jobs DROP COLUMN IF EXISTS name;
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS schedule_mode text NOT NULL DEFAULT 'cron';
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS manual_time text NOT NULL DEFAULT '';
ALTER TABLE automation_jobs ADD COLUMN IF NOT EXISTS day_scope text NOT NULL DEFAULT 'every_day';
ALTER TABLE running_areas ADD COLUMN IF NOT EXISTS source_account_id text NOT NULL DEFAULT '';
ALTER TABLE running_areas ADD COLUMN IF NOT EXISTS remote_code text NOT NULL DEFAULT '';
ALTER TABLE running_areas ADD COLUMN IF NOT EXISTS school_code text NOT NULL DEFAULT '';
ALTER TABLE running_areas ADD COLUMN IF NOT EXISTS sub_school_code text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS automation_executions (
    id text PRIMARY KEY,
    job_id text NOT NULL REFERENCES automation_jobs(id) ON DELETE CASCADE,
    account_id text NOT NULL REFERENCES runner_accounts(id) ON DELETE CASCADE,
    scheduled_for timestamptz NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    status text NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
    run_id text REFERENCES running_activities(id) ON DELETE SET NULL,
    remote_run_id text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS automation_executions_started_page_idx ON automation_executions(started_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS automation_executions_account_page_idx ON automation_executions(account_id,started_at DESC,id DESC);
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS login_name text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS fence_index integer NOT NULL DEFAULT 0;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS device_id text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS model_name text NOT NULL DEFAULT 'Redmi|23013RK75C';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS system_version text NOT NULL DEFAULT '14';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS school_code text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS school_name text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS student_number text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS auth_status text NOT NULL DEFAULT 'unconfigured';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS auth_message text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS last_auth_at timestamptz;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS password_cipher bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS session_cipher bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_enabled integer NOT NULL DEFAULT 0;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_auth boolean NOT NULL DEFAULT false;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_similarity_value integer NOT NULL DEFAULT 50;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_time integer NOT NULL DEFAULT 0;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_body_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_body_rate integer NOT NULL DEFAULT 0;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_model_type integer NOT NULL DEFAULT 1;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_base_url text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_base_cipher bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_base_mime text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_custom_cipher bytea NOT NULL DEFAULT ''::bytea;
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_custom_mime text NOT NULL DEFAULT '';
ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS face_synced_at timestamptz;
ALTER TABLE automation_executions ADD COLUMN IF NOT EXISTS remote_run_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS automation_jobs_due_idx ON automation_jobs (next_run_at) WHERE enabled;
CREATE INDEX IF NOT EXISTS automation_executions_started_idx ON automation_executions (started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS runner_accounts_login_name_idx ON runner_accounts (login_name) WHERE login_name <> '';
CREATE UNIQUE INDEX IF NOT EXISTS running_plans_remote_idx ON running_plans (source_account_id, remote_code) WHERE source_account_id <> '' AND remote_code <> '';
CREATE UNIQUE INDEX IF NOT EXISTS running_areas_remote_idx ON running_areas (source_account_id, remote_code) WHERE source_account_id <> '' AND remote_code <> '';
CREATE UNIQUE INDEX IF NOT EXISTS automation_jobs_account_idx ON automation_jobs (account_id);
CREATE UNIQUE INDEX IF NOT EXISTS automation_one_running_per_job_idx ON automation_executions (job_id) WHERE status='running';`
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate PostgreSQL schema: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE runner_accounts ADD COLUMN IF NOT EXISTS owner_user_id text NOT NULL DEFAULT ''; CREATE INDEX IF NOT EXISTS runner_accounts_owner_idx ON runner_accounts(owner_user_id);`); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) Save(ctx context.Context, run activity.Run) error {
	if !validID(run.ID) {
		return errors.New("run ID contains unsupported characters")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	const query = `
INSERT INTO running_activities (
    id, schema_version, synthetic, generated_at, started_at, finished_at,
    distance_m, duration_s, average_pace_sec_per_km, total_steps, valid,
    account_id, automation_job_id, payload
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),$14)`
	_, err = s.pool.Exec(ctx, query,
		run.ID, run.SchemaVersion, run.Synthetic, run.GeneratedAt, run.StartedAt, run.FinishedAt,
		run.Summary.DistanceM, run.Summary.DurationS, run.Summary.AveragePaceSecPerKM,
		run.Summary.TotalSteps, run.Validation.Valid, run.AccountID, run.AutomationJobID, payload,
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (activity.Run, error) {
	if !validID(id) {
		return activity.Run{}, ErrNotFound
	}
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT payload FROM running_activities WHERE id=$1`, id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return activity.Run{}, ErrNotFound
	}
	if err != nil {
		return activity.Run{}, fmt.Errorf("select run: %w", err)
	}
	var run activity.Run
	if err := json.Unmarshal(payload, &run); err != nil {
		return activity.Run{}, fmt.Errorf("decode run: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) List(ctx context.Context, limit int) ([]activity.ListItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, started_at, generated_at, distance_m, duration_s,
       average_pace_sec_per_km, total_steps, valid
FROM running_activities
WHERE ($2='' OR account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$2))
ORDER BY started_at DESC
LIMIT $1`, limit, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	items := make([]activity.ListItem, 0, limit)
	for rows.Next() {
		var item activity.ListItem
		if err := rows.Scan(
			&item.ID, &item.StartedAt, &item.GeneratedAt, &item.DistanceM, &item.DurationS,
			&item.PaceSecPerKM, &item.TotalSteps, &item.Valid,
		); err != nil {
			return nil, fmt.Errorf("scan run list: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run list: %w", err)
	}
	return items, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
