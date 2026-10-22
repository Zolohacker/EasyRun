package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) CreatePlan(ctx context.Context, plan platform.RunningPlan) (platform.RunningPlan, error) {
	profile, err := json.Marshal(plan.Profile)
	if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("encode plan profile: %w", err)
	}
	if plan.ID == "" {
		plan.ID = platform.NewID("plan")
	}
	rules, err := json.Marshal(plan.Rules)
	if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("encode plan rules: %w", err)
	}
	const query = `INSERT INTO running_plans (id,source_account_id,remote_code,semester_code,name,description,target_distance_m,profile,rules)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, plan.ID, plan.SourceAccountID, plan.RemoteCode, plan.SemesterCode, plan.Name, plan.Description, plan.TargetDistanceM, profile, rules).Scan(&plan.CreatedAt, &plan.UpdatedAt); err != nil {
		return platform.RunningPlan{}, fmt.Errorf("insert plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) ListPlans(ctx context.Context) ([]platform.RunningPlan, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+planColumns+` FROM running_plans WHERE ($1='' OR source_account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$1)) ORDER BY created_at DESC`, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	var plans []platform.RunningPlan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *PostgresStore) GetPlan(ctx context.Context, id string) (platform.RunningPlan, error) {
	return scanPlan(s.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM running_plans WHERE id=$1`, id))
}

func (s *PostgresStore) UpdatePlan(ctx context.Context, plan platform.RunningPlan) (platform.RunningPlan, error) {
	profile, err := json.Marshal(plan.Profile)
	if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("encode plan profile: %w", err)
	}
	rules, err := json.Marshal(plan.Rules)
	if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("encode plan rules: %w", err)
	}
	const query = `UPDATE running_plans SET source_account_id=$2,remote_code=$3,semester_code=$4,name=$5,description=$6,target_distance_m=$7,profile=$8,rules=$9,updated_at=now()
WHERE id=$1 RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, plan.ID, plan.SourceAccountID, plan.RemoteCode, plan.SemesterCode, plan.Name, plan.Description, plan.TargetDistanceM, profile, rules).Scan(&plan.CreatedAt, &plan.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.RunningPlan{}, ErrNotFound
	} else if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("update plan: %w", err)
	}
	return plan, nil
}

func (s *PostgresStore) DeletePlan(ctx context.Context, id string) error {
	return deleteByID(ctx, s.pool, "running_plans", id)
}

func (s *PostgresStore) CreateArea(ctx context.Context, area platform.RunningArea) (platform.RunningArea, error) {
	boundary, err := json.Marshal(area.Boundary)
	if err != nil {
		return platform.RunningArea{}, fmt.Errorf("encode area boundary: %w", err)
	}
	if area.ID == "" {
		area.ID = platform.NewID("area")
	}
	const query = `INSERT INTO running_areas (id,source_account_id,remote_code,school_code,sub_school_code,name,description,center_lat,center_lng,boundary,heading_deg,candidate_count)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, area.ID, area.SourceAccountID, area.RemoteCode, area.SchoolCode, area.SubSchoolCode, area.Name, area.Description, area.Center.Lat, area.Center.Lng, boundary, area.HeadingDeg, area.CandidateCount).Scan(&area.CreatedAt, &area.UpdatedAt); err != nil {
		return platform.RunningArea{}, fmt.Errorf("insert area: %w", err)
	}
	return area, nil
}

func (s *PostgresStore) ListAreas(ctx context.Context) ([]platform.RunningArea, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+areaColumns+` FROM running_areas WHERE ($1='' OR source_account_id IN(SELECT id FROM runner_accounts WHERE owner_user_id=$1)) ORDER BY created_at DESC`, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list areas: %w", err)
	}
	defer rows.Close()
	var areas []platform.RunningArea
	for rows.Next() {
		area, err := scanArea(rows)
		if err != nil {
			return nil, err
		}
		areas = append(areas, area)
	}
	return areas, rows.Err()
}

func (s *PostgresStore) GetArea(ctx context.Context, id string) (platform.RunningArea, error) {
	return scanArea(s.pool.QueryRow(ctx, `SELECT `+areaColumns+` FROM running_areas WHERE id=$1`, id))
}

func (s *PostgresStore) UpdateArea(ctx context.Context, area platform.RunningArea) (platform.RunningArea, error) {
	boundary, err := json.Marshal(area.Boundary)
	if err != nil {
		return platform.RunningArea{}, fmt.Errorf("encode area boundary: %w", err)
	}
	const query = `UPDATE running_areas SET source_account_id=$2,remote_code=$3,school_code=$4,sub_school_code=$5,name=$6,description=$7,center_lat=$8,center_lng=$9,boundary=$10,heading_deg=$11,candidate_count=$12,updated_at=now()
WHERE id=$1 RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query, area.ID, area.SourceAccountID, area.RemoteCode, area.SchoolCode, area.SubSchoolCode, area.Name, area.Description, area.Center.Lat, area.Center.Lng, boundary, area.HeadingDeg, area.CandidateCount).Scan(&area.CreatedAt, &area.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.RunningArea{}, ErrNotFound
	} else if err != nil {
		return platform.RunningArea{}, fmt.Errorf("update area: %w", err)
	}
	return area, nil
}

func (s *PostgresStore) DeleteArea(ctx context.Context, id string) error {
	return deleteByID(ctx, s.pool, "running_areas", id)
}

func (s *PostgresStore) CreateAccount(ctx context.Context, account platform.Account) (platform.Account, error) {
	if account.ID == "" {
		account.ID = platform.NewID("acct")
	}
	const query = `INSERT INTO runner_accounts (
id,login_name,name,note,enabled,default_plan_id,default_area_id,fence_index,device_id,model_name,system_version,
school_code,school_name,student_number,auth_status,auth_message,last_auth_at,password_cipher,session_cipher,
face_enabled,face_auth,face_similarity_value,face_time,face_body_enabled,face_body_rate,face_model_type,
face_base_url,face_base_cipher,face_base_mime,face_custom_cipher,face_custom_mime,face_synced_at,owner_user_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
        COALESCE($18, ''::bytea), COALESCE($19, ''::bytea),$20,$21,$22,$23,$24,$25,$26,$27,
        COALESCE($28, ''::bytea),$29,COALESCE($30, ''::bytea),$31,$32,$33)
RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query,
		account.ID, account.LoginName, account.Name, account.Note, account.Enabled, account.DefaultPlanID, account.DefaultAreaID,
		account.FenceIndex, account.DeviceID, account.ModelName, account.SystemVersion, account.SchoolCode, account.SchoolName,
		account.StudentNumber, account.AuthStatus, account.AuthMessage, account.LastAuthAt, account.PasswordCipher, account.SessionCipher,
		account.FaceEnabled, account.FaceAuth, account.FaceSimilarityValue, account.FaceTime, account.FaceBodyEnabled,
		account.FaceBodyRate, account.FaceModelType, account.FaceBaseURL, account.FaceBaseCipher, account.FaceBaseMIME,
		account.FaceCustomCipher, account.FaceCustomMIME, account.FaceSyncedAt, account.OwnerUserID,
	).Scan(&account.CreatedAt, &account.UpdatedAt); err != nil {
		return platform.Account{}, fmt.Errorf("insert account: %w", err)
	}
	account.PasswordConfigured = len(account.PasswordCipher) > 0
	account.SessionConfigured = len(account.SessionCipher) > 0
	setAccountFaceState(&account)
	return account, nil
}

func (s *PostgresStore) ListAccounts(ctx context.Context) ([]platform.Account, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+accountColumns+` FROM runner_accounts WHERE ($1='' OR owner_user_id=$1) ORDER BY created_at DESC`, ownerFilter(ctx))
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var accounts []platform.Account
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *PostgresStore) GetAccount(ctx context.Context, id string) (platform.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM runner_accounts WHERE id=$1`, id))
}

func (s *PostgresStore) UpdateAccount(ctx context.Context, account platform.Account) (platform.Account, error) {
	const query = `UPDATE runner_accounts SET login_name=$2,name=$3,note=$4,enabled=$5,default_plan_id=$6,default_area_id=$7,
fence_index=$8,device_id=$9,model_name=$10,system_version=$11,school_code=$12,school_name=$13,
student_number=$14,auth_status=$15,auth_message=$16,last_auth_at=$17,password_cipher=COALESCE($18, ''::bytea),session_cipher=COALESCE($19, ''::bytea),
face_enabled=$20,face_auth=$21,face_similarity_value=$22,face_time=$23,face_body_enabled=$24,face_body_rate=$25,
face_model_type=$26,face_base_url=$27,face_base_cipher=COALESCE($28, ''::bytea),face_base_mime=$29,
face_custom_cipher=COALESCE($30, ''::bytea),face_custom_mime=$31,face_synced_at=$32,updated_at=now()
WHERE id=$1 RETURNING created_at,updated_at`
	if err := s.pool.QueryRow(ctx, query,
		account.ID, account.LoginName, account.Name, account.Note, account.Enabled, account.DefaultPlanID, account.DefaultAreaID,
		account.FenceIndex, account.DeviceID, account.ModelName, account.SystemVersion, account.SchoolCode, account.SchoolName,
		account.StudentNumber, account.AuthStatus, account.AuthMessage, account.LastAuthAt, account.PasswordCipher, account.SessionCipher,
		account.FaceEnabled, account.FaceAuth, account.FaceSimilarityValue, account.FaceTime, account.FaceBodyEnabled,
		account.FaceBodyRate, account.FaceModelType, account.FaceBaseURL, account.FaceBaseCipher, account.FaceBaseMIME,
		account.FaceCustomCipher, account.FaceCustomMIME, account.FaceSyncedAt,
	).Scan(&account.CreatedAt, &account.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.Account{}, ErrNotFound
	} else if err != nil {
		return platform.Account{}, fmt.Errorf("update account: %w", err)
	}
	account.PasswordConfigured = len(account.PasswordCipher) > 0
	account.SessionConfigured = len(account.SessionCipher) > 0
	setAccountFaceState(&account)
	return account, nil
}

func (s *PostgresStore) DeleteAccount(ctx context.Context, id string) error {
	return deleteByID(ctx, s.pool, "runner_accounts", id)
}

type scanner interface{ Scan(...any) error }

const planColumns = `id,source_account_id,remote_code,semester_code,name,description,target_distance_m,profile,rules,created_at,updated_at`
const areaColumns = `id,source_account_id,remote_code,school_code,sub_school_code,name,description,center_lat,center_lng,boundary,heading_deg,candidate_count,created_at,updated_at`

func scanPlan(row scanner) (platform.RunningPlan, error) {
	var plan platform.RunningPlan
	var profile, rules []byte
	if err := row.Scan(&plan.ID, &plan.SourceAccountID, &plan.RemoteCode, &plan.SemesterCode, &plan.Name, &plan.Description, &plan.TargetDistanceM, &profile, &rules, &plan.CreatedAt, &plan.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.RunningPlan{}, ErrNotFound
	} else if err != nil {
		return platform.RunningPlan{}, fmt.Errorf("scan plan: %w", err)
	}
	if err := json.Unmarshal(profile, &plan.Profile); err != nil {
		return platform.RunningPlan{}, fmt.Errorf("decode plan profile: %w", err)
	}
	if err := json.Unmarshal(rules, &plan.Rules); err != nil {
		return platform.RunningPlan{}, fmt.Errorf("decode plan rules: %w", err)
	}
	return plan, nil
}

func scanArea(row scanner) (platform.RunningArea, error) {
	var area platform.RunningArea
	var boundary []byte
	if err := row.Scan(&area.ID, &area.SourceAccountID, &area.RemoteCode, &area.SchoolCode, &area.SubSchoolCode, &area.Name, &area.Description, &area.Center.Lat, &area.Center.Lng, &boundary, &area.HeadingDeg, &area.CandidateCount, &area.CreatedAt, &area.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return platform.RunningArea{}, ErrNotFound
	} else if err != nil {
		return platform.RunningArea{}, fmt.Errorf("scan area: %w", err)
	}
	if err := json.Unmarshal(boundary, &area.Boundary); err != nil {
		return platform.RunningArea{}, fmt.Errorf("decode area boundary: %w", err)
	}
	return area, nil
}

const accountColumns = `id,login_name,name,note,enabled,default_plan_id,default_area_id,fence_index,device_id,model_name,
system_version,school_code,school_name,student_number,auth_status,auth_message,last_auth_at,password_cipher,session_cipher,
face_enabled,face_auth,face_similarity_value,face_time,face_body_enabled,face_body_rate,face_model_type,
face_base_url,face_base_cipher,face_base_mime,face_custom_cipher,face_custom_mime,face_synced_at,owner_user_id,created_at,updated_at`

func scanAccount(row scanner) (platform.Account, error) {
	var account platform.Account
	if err := row.Scan(
		&account.ID, &account.LoginName, &account.Name, &account.Note, &account.Enabled, &account.DefaultPlanID, &account.DefaultAreaID,
		&account.FenceIndex, &account.DeviceID, &account.ModelName, &account.SystemVersion, &account.SchoolCode, &account.SchoolName,
		&account.StudentNumber, &account.AuthStatus, &account.AuthMessage, &account.LastAuthAt, &account.PasswordCipher, &account.SessionCipher,
		&account.FaceEnabled, &account.FaceAuth, &account.FaceSimilarityValue, &account.FaceTime, &account.FaceBodyEnabled,
		&account.FaceBodyRate, &account.FaceModelType, &account.FaceBaseURL, &account.FaceBaseCipher, &account.FaceBaseMIME,
		&account.FaceCustomCipher, &account.FaceCustomMIME, &account.FaceSyncedAt, &account.OwnerUserID,
		&account.CreatedAt, &account.UpdatedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return platform.Account{}, ErrNotFound
	} else if err != nil {
		return platform.Account{}, fmt.Errorf("scan account: %w", err)
	}
	account.PasswordConfigured = len(account.PasswordCipher) > 0
	account.SessionConfigured = len(account.SessionCipher) > 0
	setAccountFaceState(&account)
	return account, nil
}

func setAccountFaceState(account *platform.Account) {
	profile := account.FaceProfile()
	account.FaceBaseConfigured = profile.BaseConfigured
	account.FaceCustomConfigured = profile.CustomConfigured
	account.FaceEffectiveSource = profile.EffectiveSource
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func deleteByID(ctx context.Context, db execer, table, id string) error {
	allowed := map[string]bool{"running_plans": true, "running_areas": true, "runner_accounts": true, "automation_jobs": true}
	if !allowed[table] {
		return errors.New("unsupported table")
	}
	result, err := db.Exec(ctx, `DELETE FROM `+table+` WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", table, err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
