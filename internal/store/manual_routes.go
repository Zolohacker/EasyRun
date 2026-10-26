package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
)

const manualColumns = `id,account_id,plan_id,area_id,name,points,distance_m,created_at,updated_at,created_by_user_id,source_job_id`

func scanManual(row scanner) (platform.ManualRoute, error) {
	var m platform.ManualRoute
	var raw []byte
	err := row.Scan(&m.ID, &m.AccountID, &m.PlanID, &m.AreaID, &m.Name, &raw, &m.DistanceM, &m.CreatedAt, &m.UpdatedAt, &m.CreatedByUserID, &m.SourceJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(raw, &m.Points)
	return m, err
}
func (s *PostgresStore) GetManualRoute(ctx context.Context, id string) (platform.ManualRoute, error) {
	return scanManual(s.pool.QueryRow(ctx, `SELECT `+manualColumns+` FROM manual_routes WHERE id=$1`, id))
}
func (s *PostgresStore) ListManualRoutes(ctx context.Context) ([]platform.ManualRoute, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+manualColumns+` FROM manual_routes WHERE ($1='' OR account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$1)) ORDER BY created_at DESC`, ownerFilter(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []platform.ManualRoute{}
	for rows.Next() {
		m, e := scanManual(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, m)
	}
	return items, rows.Err()
}
func (s *PostgresStore) validateManual(ctx context.Context, m *platform.ManualRoute) error {
	p, e := s.GetPlan(ctx, m.PlanID)
	if e != nil {
		return e
	}
	a, e := s.GetArea(ctx, m.AreaID)
	if e != nil {
		return e
	}
	return platform.ValidateManualRoute(m, p, a)
}
func InsertManualRouteTx(ctx context.Context, tx pgx.Tx, m platform.ManualRoute) (platform.ManualRoute, error) {
	if m.ID == "" {
		m.ID = platform.NewID("route")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "manual-routes:"+m.AccountID); err != nil {
		return m, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM manual_routes WHERE account_id=$1`, m.AccountID).Scan(&count); err != nil {
		return m, err
	}
	if count >= 100 {
		return m, errors.New("每个账号最多保存 100 条手绘路线，请先删除不用的路线")
	}
	raw, e := json.Marshal(m.Points)
	if e != nil {
		return m, e
	}
	e = tx.QueryRow(ctx, `INSERT INTO manual_routes(id,account_id,plan_id,area_id,name,points,distance_m,created_by_user_id,source_job_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at,updated_at`, m.ID, m.AccountID, m.PlanID, m.AreaID, m.Name, raw, m.DistanceM, m.CreatedByUserID, m.SourceJobID).Scan(&m.CreatedAt, &m.UpdatedAt)
	return m, e
}
func (s *PostgresStore) CreateManualRoute(ctx context.Context, m platform.ManualRoute) (platform.ManualRoute, error) {
	m.SourceJobID = "" // Only the scoped public configuration transaction sets provenance.
	if actor, ok := platform.ActorFrom(ctx); ok {
		m.CreatedByUserID = actor.ID
	} else {
		m.CreatedByUserID = ""
	}
	if err := s.validateManual(ctx, &m); err != nil {
		return m, err
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return m, e
	}
	defer tx.Rollback(ctx)
	m, e = InsertManualRouteTx(ctx, tx, m)
	if e != nil {
		return m, e
	}
	return m, tx.Commit(ctx)
}
func (s *PostgresStore) UpdateManualRoute(ctx context.Context, m platform.ManualRoute) (platform.ManualRoute, error) {
	if err := s.validateManual(ctx, &m); err != nil {
		return m, err
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return m, e
	}
	defer tx.Rollback(ctx)
	old, e := scanManual(tx.QueryRow(ctx, `SELECT `+manualColumns+` FROM manual_routes WHERE id=$1 FOR UPDATE`, m.ID))
	if e != nil {
		return m, e
	}
	if old.AccountID != m.AccountID || old.PlanID != m.PlanID || old.AreaID != m.AreaID {
		return m, errors.New("已保存路线的账号、计划和区域不可更换，请新建路线")
	}
	m.CreatedByUserID, m.SourceJobID = old.CreatedByUserID, old.SourceJobID
	var tooLong bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_jobs WHERE manual_route_id=$1 AND target_distance_m>$2)`, m.ID, m.DistanceM+1e-6).Scan(&tooLong); e != nil {
		return m, e
	}
	if tooLong {
		return m, errors.New("该路线正被任务使用，修改后长度不足；请先调低任务距离")
	}
	raw, e := json.Marshal(m.Points)
	if e != nil {
		return m, e
	}
	e = tx.QueryRow(ctx, `UPDATE manual_routes SET name=$2,points=$3,distance_m=$4,updated_at=now() WHERE id=$1 RETURNING created_at,updated_at`, m.ID, m.Name, raw, m.DistanceM).Scan(&m.CreatedAt, &m.UpdatedAt)
	if e != nil {
		return m, e
	}
	return m, tx.Commit(ctx)
}
func (s *PostgresStore) DeleteManualRoute(ctx context.Context, id string) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var found string
	if e = tx.QueryRow(ctx, `SELECT id FROM manual_routes WHERE id=$1 FOR UPDATE`, id).Scan(&found); errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	} else if e != nil {
		return e
	}
	var used bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_jobs WHERE manual_route_id=$1)`, id).Scan(&used); e != nil {
		return e
	}
	if used {
		return errors.New("该路线正被任务使用，请先更换任务路线")
	}
	if _, e = tx.Exec(ctx, `DELETE FROM manual_routes WHERE id=$1`, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Share-lock route geometry until the job write commits. Editors take an
// exclusive lock, preventing a concurrent shortening from bypassing validation.
func lockJobRoute(ctx context.Context, tx pgx.Tx, j *platform.AutomationJob) error {
	if j.RouteMode == "" {
		j.RouteMode = "auto"
	}
	if j.RouteMode == "auto" {
		j.ManualRouteID = ""
		return nil
	}
	if j.RouteMode != "custom" {
		return errors.New("路线模式不正确")
	}
	m, e := scanManual(tx.QueryRow(ctx, `SELECT `+manualColumns+` FROM manual_routes WHERE id=$1 FOR SHARE`, j.ManualRouteID))
	if e != nil {
		return e
	}
	if m.AccountID != j.AccountID || m.PlanID != j.PlanID || m.AreaID != j.AreaID || j.TargetDistanceM > m.DistanceM+1e-6 {
		return fmt.Errorf("任务距离或绑定范围与手绘路线不一致")
	}
	return nil
}

func (s TenantStore) GetManualRoute(ctx context.Context, id string) (platform.ManualRoute, error) {
	m, e := s.Store.GetManualRoute(ctx, id)
	if e == nil && !s.allowAccount(ctx, m.AccountID) {
		return platform.ManualRoute{}, ErrNotFound
	}
	return m, e
}
func (s TenantStore) ListManualRoutes(ctx context.Context) ([]platform.ManualRoute, error) {
	items, e := s.Store.ListManualRoutes(ctx)
	out := []platform.ManualRoute{}
	for _, m := range items {
		if s.allowAccount(ctx, m.AccountID) {
			out = append(out, m)
		}
	}
	return out, e
}
func (s TenantStore) CreateManualRoute(ctx context.Context, m platform.ManualRoute) (platform.ManualRoute, error) {
	if !s.allowAccount(ctx, m.AccountID) {
		return m, ErrNotFound
	}
	return s.Store.CreateManualRoute(ctx, m)
}
func (s TenantStore) UpdateManualRoute(ctx context.Context, m platform.ManualRoute) (platform.ManualRoute, error) {
	if _, e := s.GetManualRoute(ctx, m.ID); e != nil {
		return m, e
	}
	if !s.allowAccount(ctx, m.AccountID) {
		return m, ErrNotFound
	}
	return s.Store.UpdateManualRoute(ctx, m)
}
func (s TenantStore) DeleteManualRoute(ctx context.Context, id string) error {
	if _, e := s.GetManualRoute(ctx, id); e != nil {
		return e
	}
	return s.Store.DeleteManualRoute(ctx, id)
}
