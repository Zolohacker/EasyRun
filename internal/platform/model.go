package platform

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
)

const (
	ScheduleModeCron       = "cron"
	ScheduleModeManual     = "manual"
	ScheduleModePlanRandom = "plan_random"

	DayScopeEveryDay = "every_day"
	DayScopeWorkday  = "workday"
	DayScopeWeekend  = "weekend"
)

type RunningPlan struct {
	ID              string            `json:"id"`
	SourceAccountID string            `json:"source_account_id,omitempty"`
	RemoteCode      string            `json:"remote_code,omitempty"`
	SemesterCode    string            `json:"semester_code,omitempty"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	TargetDistanceM float64           `json:"target_distance_m"`
	Profile         activity.Profile  `json:"profile"`
	Rules           activity.RunRules `json:"rules"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type RunningArea struct {
	ID              string      `json:"id"`
	SourceAccountID string      `json:"source_account_id,omitempty"`
	RemoteCode      string      `json:"remote_code,omitempty"`
	SchoolCode      string      `json:"school_code,omitempty"`
	SubSchoolCode   string      `json:"sub_school_code,omitempty"`
	Name            string      `json:"name"`
	Description     string      `json:"description,omitempty"`
	Center          geo.Point   `json:"center"`
	Boundary        []geo.Point `json:"boundary,omitempty"`
	HeadingDeg      float64     `json:"heading_deg"`
	CandidateCount  int         `json:"candidate_count"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type Account struct {
	ID                   string     `json:"id"`
	LoginName            string     `json:"login_name"`
	Name                 string     `json:"name"`
	StudentNumber        string     `json:"student_number,omitempty"`
	Note                 string     `json:"note,omitempty"`
	Enabled              bool       `json:"enabled"`
	DefaultPlanID        *string    `json:"default_plan_id,omitempty"`
	DefaultAreaID        *string    `json:"default_area_id,omitempty"`
	FenceIndex           int        `json:"fence_index"`
	DeviceID             string     `json:"device_id"`
	ModelName            string     `json:"model_name"`
	SystemVersion        string     `json:"system_version"`
	SchoolCode           string     `json:"school_code,omitempty"`
	SchoolName           string     `json:"school_name,omitempty"`
	AuthStatus           string     `json:"auth_status"`
	AuthMessage          string     `json:"auth_message,omitempty"`
	LastAuthAt           *time.Time `json:"last_auth_at,omitempty"`
	PasswordConfigured   bool       `json:"password_configured"`
	SessionConfigured    bool       `json:"session_configured"`
	FaceEnabled          int        `json:"face_enabled"`
	FaceAuth             bool       `json:"face_auth"`
	FaceSimilarityValue  int        `json:"face_similarity_value"`
	FaceTime             int        `json:"face_time"`
	FaceBodyEnabled      bool       `json:"face_body_enabled"`
	FaceBodyRate         int        `json:"face_body_rate"`
	FaceModelType        int        `json:"face_model_type"`
	FaceBaseConfigured   bool       `json:"face_base_configured"`
	FaceCustomConfigured bool       `json:"face_custom_configured"`
	FaceEffectiveSource  string     `json:"face_effective_source"`
	FaceSyncedAt         *time.Time `json:"face_synced_at,omitempty"`
	PasswordCipher       []byte     `json:"-"`
	SessionCipher        []byte     `json:"-"`
	FaceBaseURL          string     `json:"-"`
	FaceBaseCipher       []byte     `json:"-"`
	FaceBaseMIME         string     `json:"-"`
	FaceCustomCipher     []byte     `json:"-"`
	FaceCustomMIME       string     `json:"-"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// FaceProfile is the non-secret account face state returned to the UI. Image
// bytes and the upstream image URL are intentionally served through the image
// endpoint instead of being exposed in account JSON.
type FaceProfile struct {
	AccountID        string     `json:"account_id"`
	FaceEnabled      int        `json:"face_enabled"`
	FaceAuth         bool       `json:"face_auth"`
	SimilarityValue  int        `json:"similarity_value"`
	FaceTime         int        `json:"face_time"`
	BodyEnabled      bool       `json:"body_enabled"`
	BodyRate         int        `json:"body_rate"`
	FaceModelType    int        `json:"face_model_type"`
	BaseConfigured   bool       `json:"base_configured"`
	CustomConfigured bool       `json:"custom_configured"`
	EffectiveSource  string     `json:"effective_source"`
	SyncedAt         *time.Time `json:"synced_at,omitempty"`
}

func (a Account) FaceProfile() FaceProfile {
	source := "none"
	if len(a.FaceCustomCipher) > 0 {
		source = "custom"
	} else if len(a.FaceBaseCipher) > 0 {
		source = "base"
	}
	return FaceProfile{
		AccountID: a.ID, FaceEnabled: a.FaceEnabled, FaceAuth: a.FaceAuth,
		SimilarityValue: a.FaceSimilarityValue, FaceTime: a.FaceTime,
		BodyEnabled: a.FaceBodyEnabled, BodyRate: a.FaceBodyRate,
		FaceModelType: a.FaceModelType, BaseConfigured: len(a.FaceBaseCipher) > 0,
		CustomConfigured: len(a.FaceCustomCipher) > 0, EffectiveSource: source,
		SyncedAt: a.FaceSyncedAt,
	}
}

type AutomationJob struct {
	ID                   string     `json:"id"`
	AccountID            string     `json:"account_id"`
	PlanID               string     `json:"plan_id"`
	AreaID               string     `json:"area_id"`
	TargetDistanceM      float64    `json:"target_distance_m"`
	DailyRunLimit        int        `json:"daily_run_limit"`
	WeeklyRunLimit       int        `json:"weekly_run_limit"`
	DailyDistanceLimitM  float64    `json:"daily_distance_limit_m"`
	WeeklyDistanceLimitM float64    `json:"weekly_distance_limit_m"`
	ScheduleMode         string     `json:"schedule_mode"`
	ManualTime           string     `json:"manual_time,omitempty"`
	DayScope             string     `json:"day_scope,omitempty"`
	CronExpr             string     `json:"cron_expr"`
	Timezone             string     `json:"timezone"`
	Enabled              bool       `json:"enabled"`
	NextRunAt            *time.Time `json:"next_run_at,omitempty"`
	LastRunAt            *time.Time `json:"last_run_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type Execution struct {
	ID           string     `json:"id"`
	JobID        string     `json:"job_id"`
	AccountID    string     `json:"account_id"`
	ScheduledFor time.Time  `json:"scheduled_for"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Status       string     `json:"status"`
	RunID        *string    `json:"run_id,omitempty"`
	RemoteRunID  string     `json:"remote_run_id,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type JobContext struct {
	Job     AutomationJob
	Account Account
	Plan    RunningPlan
	Area    RunningArea
}

const SystemSettingsMapKeyAAD = "system-settings:amap-api-key"

// MapAPIKey is an encrypted member of the AMap credential pool. Every member
// carries its own provider QPS budget.
type MapAPIKey struct {
	ID        string  `json:"id"`
	KeyCipher []byte  `json:"key_cipher"`
	QPS       float64 `json:"qps"`
}

// SystemSettings contains the runtime-tunable settings persisted in PostgreSQL.
// Map API keys are encrypted by securestore before they reach this model. The
// singular fields are retained only to migrate databases created before the
// credential pool was introduced.
type SystemSettings struct {
	MapAPIKeys        []MapAPIKey `json:"-"`
	MapAPIKeyCipher   []byte      `json:"-"`
	MapQPS            float64     `json:"-"`
	WorkerProcesses   int         `json:"worker_processes"`
	WorkerConcurrency int         `json:"worker_concurrency"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

func NewID(prefix string) string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return prefix + "_" + hex.EncodeToString(value[:])
}
