package activity

import (
	"errors"
	"time"

	"github.com/Zolohacker/EasyRun/internal/route"
)

// ErrRunQuotaExceeded marks an execution that was deliberately skipped because
// the account has already reached its configured daily or weekly allowance.
var ErrRunQuotaExceeded = errors.New("run quota exceeded")

type Profile struct {
	TargetPaceSecPerKM float64 `json:"target_pace_sec_per_km"`
	CadenceSPM         float64 `json:"cadence_spm"`
	TargetStrideM      float64 `json:"target_stride_m"`
	SampleIntervalS    float64 `json:"sample_interval_s"`
	PaceVariationPct   float64 `json:"pace_variation_pct"`
	GPSQuality         string  `json:"gps_quality"`
	Terrain            string  `json:"terrain"`
	StartAltitudeM     float64 `json:"start_altitude_m"`
	PauseRatePerHour   float64 `json:"pause_rate_per_hour"`
}

// RunRules is the effective school rule set returned by
// querySunRunAbstractInfoV2.schoolDemandRule. Pace names use the numeric
// seconds-per-kilometre order: FastestPaceSecPerKM is the lower value and
// SlowestPaceSecPerKM is the higher value. The upstream JSON calls these
// maxPace and minPace respectively.
type RunRules struct {
	PlanType                 int             `json:"plan_type,omitempty"`
	TotalDistanceM           float64         `json:"total_distance_m,omitempty"`
	TotalTimes               int             `json:"total_times,omitempty"`
	DayMaxTimes              int             `json:"day_max_times,omitempty"`
	DayMaxDistanceM          float64         `json:"day_max_distance_m,omitempty"`
	WeekMinTimes             int             `json:"week_min_times,omitempty"`
	WeekMaxTimes             int             `json:"week_max_times,omitempty"`
	WeekMaxDistanceM         float64         `json:"week_max_distance_m,omitempty"`
	CompletionRate           float64         `json:"completion_rate,omitempty"`
	DoneDistanceM            float64         `json:"done_distance_m,omitempty"`
	DoneTargetTimes          int             `json:"done_target_times,omitempty"`
	MorningTimes             int             `json:"morning_times,omitempty"`
	MinDistanceM             float64         `json:"min_distance_m"`
	MaxDistanceM             float64         `json:"max_distance_m"`
	FastestPaceSecPerKM      float64         `json:"fastest_pace_sec_per_km"`
	SlowestPaceSecPerKM      float64         `json:"slowest_pace_sec_per_km"`
	MinCadenceSPM            float64         `json:"min_cadence_spm"`
	MaxCadenceSPM            float64         `json:"max_cadence_spm"`
	MaxSegmentStrideM        float64         `json:"max_segment_stride_m"`
	SegmentStrideExceptions  int             `json:"segment_stride_exception_num"`
	SwingAmplitudeThreshold  int             `json:"swing_amplitude_threshold"`
	LocationSampleIntervalS  float64         `json:"location_sample_interval_s"`
	POIUploadIntervalS       int             `json:"poi_upload_interval_s"`
	PaceUploadCheckIntervalS int             `json:"pace_upload_check_interval_s"`
	PaceSegmentDistanceM     int64           `json:"pace_segment_distance_m"`
	StepSegmentIntervalS     int             `json:"step_segment_interval_s"`
	StrideSegmentDistanceM   int             `json:"stride_segment_distance_m"`
	ActiveStartDate          string          `json:"active_start_date,omitempty"`
	ActiveEndDate            string          `json:"active_end_date,omitempty"`
	TimeWindows              []RunTimeWindow `json:"time_windows,omitempty"`
	Source                   string          `json:"source,omitempty"`
}

// RunTimeWindow is one server-defined time fragment in schoolDemandRule.
// DayType is normalized from the Android model values: 0/every_day,
// 1/workday and 2/weekend. Minutes are local wall-clock minutes in the
// automation job timezone; an end at or before the start spans midnight.
type RunTimeWindow struct {
	DayType     string `json:"day_type"`
	RawTimeType string `json:"raw_time_type,omitempty"`
	Times       string `json:"times"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
	TagType     int    `json:"tag_type,omitempty"`
}

type TargetWindow struct {
	MinDistanceM        float64 `json:"min_distance_m"`
	MaxDistanceM        float64 `json:"max_distance_m"`
	FastestPaceSecPerKM float64 `json:"fastest_pace_sec_per_km"`
	SlowestPaceSecPerKM float64 `json:"slowest_pace_sec_per_km"`
	MinCadenceSPM       float64 `json:"min_cadence_spm"`
	MaxCadenceSPM       float64 `json:"max_cadence_spm"`
	MinStrideM          float64 `json:"min_stride_m"`
	MaxStrideM          float64 `json:"max_stride_m"`
}

type TargetSelection struct {
	Policy          string       `json:"policy"`
	TargetDistanceM float64      `json:"target_distance_m"`
	Profile         Profile      `json:"profile"`
	Window          TargetWindow `json:"window"`
}

type GenerateRequest struct {
	Route     route.Route `json:"route"`
	Profile   Profile     `json:"profile"`
	AccountID string      `json:"account_id,omitempty"`
	StartedAt *time.Time  `json:"started_at,omitempty"`
	Seed      *int64      `json:"seed,omitempty"`
	Persist   bool        `json:"persist"`
}

// SubmissionRequest contains only the parameters that are known before the
// upstream start call. Route planning and sample generation deliberately happen
// later, after startSunRun_v2 has returned the session's clock mode and target
// points.
type SubmissionRequest struct {
	ManualRoute          *route.Route `json:"-"`
	TargetDistanceM      float64      `json:"target_distance_m"`
	Profile              Profile      `json:"profile"`
	Seed                 int64        `json:"seed"`
	AccountID            string       `json:"account_id,omitempty"`
	AutomationJobID      string       `json:"automation_job_id,omitempty"`
	DailyRunLimit        int          `json:"daily_run_limit,omitempty"`
	WeeklyRunLimit       int          `json:"weekly_run_limit,omitempty"`
	DailyDistanceLimitM  float64      `json:"daily_distance_limit_m,omitempty"`
	WeeklyDistanceLimitM float64      `json:"weekly_distance_limit_m,omitempty"`
	Timezone             string       `json:"timezone,omitempty"`
}

type Sample struct {
	Index               int       `json:"index"`
	RecordedAt          time.Time `json:"recorded_at"`
	ElapsedS            float64   `json:"elapsed_s"`
	Lat                 float64   `json:"lat"`
	Lng                 float64   `json:"lng"`
	DistanceM           float64   `json:"distance_m"`
	GPSDistanceM        float64   `json:"gps_distance_m"`
	SpeedMPS            float64   `json:"speed_mps"`
	PaceSecPerKM        float64   `json:"pace_sec_per_km"`
	CadenceSPM          float64   `json:"cadence_spm"`
	Steps               int       `json:"steps"`
	StrideM             float64   `json:"stride_m"`
	AltitudeM           float64   `json:"altitude_m"`
	HorizontalAccuracyM float64   `json:"horizontal_accuracy_m"`
	Satellites          int       `json:"satellites"`
	HeadingDeg          float64   `json:"heading_deg"`
	Paused              bool      `json:"paused"`
}

type DistanceSplit struct {
	Index          int     `json:"index"`
	StartDistanceM float64 `json:"start_distance_m"`
	EndDistanceM   float64 `json:"end_distance_m"`
	DistanceM      float64 `json:"distance_m"`
	StartElapsedS  float64 `json:"start_elapsed_s"`
	EndElapsedS    float64 `json:"end_elapsed_s"`
	DurationS      float64 `json:"duration_s"`
	PaceSecPerKM   float64 `json:"pace_sec_per_km"`
	Steps          int     `json:"steps"`
	AverageCadence float64 `json:"average_cadence_spm"`
}

type TimeSegment struct {
	Index          int     `json:"index"`
	StartElapsedS  float64 `json:"start_elapsed_s"`
	EndElapsedS    float64 `json:"end_elapsed_s"`
	DurationS      float64 `json:"duration_s"`
	StartDistanceM float64 `json:"start_distance_m"`
	EndDistanceM   float64 `json:"end_distance_m"`
	DistanceM      float64 `json:"distance_m"`
	StartSteps     int     `json:"start_steps"`
	EndSteps       int     `json:"end_steps"`
	Steps          int     `json:"steps"`
	CadenceSPM     float64 `json:"cadence_spm"`
}

type Summary struct {
	DistanceM           float64 `json:"distance_m"`
	GPSDistanceM        float64 `json:"gps_distance_m"`
	DurationS           float64 `json:"duration_s"`
	MovingDurationS     float64 `json:"moving_duration_s"`
	AveragePaceSecPerKM float64 `json:"average_pace_sec_per_km"`
	AverageSpeedMPS     float64 `json:"average_speed_mps"`
	MaxSpeedMPS         float64 `json:"max_speed_mps"`
	TotalSteps          int     `json:"total_steps"`
	AverageCadenceSPM   float64 `json:"average_cadence_spm"`
	AverageStrideM      float64 `json:"average_stride_m"`
	ElevationGainM      float64 `json:"elevation_gain_m"`
	ElevationLossM      float64 `json:"elevation_loss_m"`
}

type Finding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Validation struct {
	Valid    bool      `json:"valid"`
	Findings []Finding `json:"findings"`
}

type Run struct {
	SchemaVersion   int             `json:"schema_version"`
	ID              string          `json:"id"`
	AccountID       string          `json:"account_id,omitempty"`
	AutomationJobID string          `json:"automation_job_id,omitempty"`
	Synthetic       bool            `json:"synthetic"`
	GeneratedAt     time.Time       `json:"generated_at"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      time.Time       `json:"finished_at"`
	Seed            int64           `json:"seed"`
	Route           route.Route     `json:"route"`
	Profile         Profile         `json:"profile"`
	Summary         Summary         `json:"summary"`
	Samples         []Sample        `json:"samples"`
	KilometreSplits []DistanceSplit `json:"kilometre_splits"`
	StepSegments    []TimeSegment   `json:"step_segments"`
	Submission      *Submission     `json:"submission,omitempty"`
	Validation      Validation      `json:"validation"`
}

type Submission struct {
	Status      string    `json:"status"`
	RemoteRunID string    `json:"remote_run_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type ListItem struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"started_at"`
	GeneratedAt  time.Time `json:"generated_at"`
	DistanceM    float64   `json:"distance_m"`
	DurationS    float64   `json:"duration_s"`
	PaceSecPerKM float64   `json:"pace_sec_per_km"`
	TotalSteps   int       `json:"total_steps"`
	Valid        bool      `json:"valid"`
}
