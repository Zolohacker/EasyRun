export type Point = { lat: number; lng: number }

export type Profile = {
  target_pace_sec_per_km: number
  cadence_spm: number
  target_stride_m: number
  sample_interval_s: number
  pace_variation_pct: number
  gps_quality: 'open' | 'mixed' | 'urban'
  terrain: 'flat' | 'rolling' | 'hilly'
  start_altitude_m: number
  pause_rate_per_hour: number
}

export type RunTimeWindow = {
  day_type: 'every_day' | 'workday' | 'weekend' | string
  raw_time_type?: string
  times: string
  start_minute: number
  end_minute: number
  tag_type?: number
}

export type RunRules = {
  plan_type?: number; total_distance_m?: number; total_times?: number
  day_max_times?: number; day_max_distance_m?: number
  week_min_times?: number; week_max_times?: number; week_max_distance_m?: number
  completion_rate?: number; done_distance_m?: number; done_target_times?: number; morning_times?: number
  min_distance_m: number; max_distance_m: number
  fastest_pace_sec_per_km: number; slowest_pace_sec_per_km: number
  min_cadence_spm: number; max_cadence_spm: number
  max_segment_stride_m: number; segment_stride_exception_num: number
  swing_amplitude_threshold: number
  location_sample_interval_s: number; poi_upload_interval_s: number
  pace_upload_check_interval_s: number; pace_segment_distance_m: number
  step_segment_interval_s: number; stride_segment_distance_m: number
  active_start_date?: string; active_end_date?: string
  time_windows?: RunTimeWindow[]
  source?: string
}

export type TargetWindow = {
  min_distance_m: number; max_distance_m: number
  fastest_pace_sec_per_km: number; slowest_pace_sec_per_km: number
  min_cadence_spm: number; max_cadence_spm: number
  min_stride_m: number; max_stride_m: number
}

export type TargetSelection = {
  policy: 'middle_70_percent' | string
  target_distance_m: number
  profile: Profile
  window: TargetWindow
}

export type Plan = {
  id: string; source_account_id?: string; remote_code?: string; semester_code?: string
  name: string; description?: string; target_distance_m: number
  profile: Profile; rules: RunRules; created_at: string; updated_at: string
}

export type Area = {
  id: string; source_account_id?: string; remote_code?: string; school_code?: string; sub_school_code?: string
  name: string; description?: string; center: Point; boundary?: Point[]
  heading_deg: number; candidate_count: number; created_at: string; updated_at: string
}

export type Account = {
  id: string; login_name: string; name: string; note?: string; enabled: boolean
  owner_user_id?: string
  student_number?: string
  default_plan_id?: string; default_area_id?: string; fence_index: number
  device_id: string; model_name: string; system_version: string
  school_code?: string; school_name?: string
  auth_status: 'unconfigured' | 'configured' | 'sms_pending' | 'authenticated' | 'error' | string
  auth_message?: string; last_auth_at?: string
  password_configured: boolean; session_configured: boolean
  face_enabled: number; face_auth: boolean; face_similarity_value: number; face_time: number
  face_body_enabled: boolean; face_body_rate: number; face_model_type: number
  face_base_configured: boolean; face_custom_configured: boolean
  face_effective_source: 'none' | 'base' | 'custom' | string; face_synced_at?: string
  created_at: string; updated_at: string
}

export type FaceProfile = {
  account_id: string; face_enabled: number; face_auth: boolean; similarity_value: number; face_time: number
  body_enabled: boolean; body_rate: number; face_model_type: number
  base_configured: boolean; custom_configured: boolean
  effective_source: 'none' | 'base' | 'custom' | string; synced_at?: string
}

export type FaceDetectionResult = {
  source: 'base' | 'custom' | string
  provider: string
  request_id: string
  confidence: number
  threshold: number
  passed: boolean
  checked_at: string
  elapsed_ms: number
}

export type AccountInput = Partial<Account> & { login_name: string; password?: string }

export type Job = {
  id: string; account_id: string; plan_id: string; area_id: string
  target_distance_m: number; daily_run_limit: number; weekly_run_limit: number
  daily_distance_limit_m: number; weekly_distance_limit_m: number
  schedule_mode: 'manual' | 'plan_random' | 'cron' | string
  manual_time?: string; day_scope?: 'every_day' | 'workday' | 'weekend' | string
  cron_expr: string; timezone: string; enabled: boolean; next_run_at?: string
  last_run_at?: string; created_at: string; updated_at: string
}

export type Execution = {
  id: string; job_id: string; account_id: string; scheduled_for: string; started_at: string
  finished_at?: string; status: string; run_id?: string; remote_run_id?: string; error?: string
}

export type Route = {
  id: string; source: string; coordinate_system: string; generated_at: string
  target_distance_m: number; distance_m: number; estimated_duration_s: number; points: Point[]
  quality: { distance_error_pct: number; retrace_ratio: number; outside_area_ratio: number; score: number }
}

export type Sample = Point & {
  index: number; recorded_at: string; elapsed_s: number; distance_m: number; gps_distance_m: number
  speed_mps: number; pace_sec_per_km: number; cadence_spm: number; steps: number; stride_m: number
  altitude_m: number; horizontal_accuracy_m: number; satellites: number; heading_deg: number; paused: boolean
}

export type Split = {
  index: number; distance_m: number; duration_s: number; pace_sec_per_km: number
  steps: number; average_cadence_spm: number
}

export type Run = {
  id: string; account_id?: string; synthetic: boolean; started_at: string; finished_at: string; seed: number
  route: Route; profile: Profile
  summary: {
    distance_m: number; gps_distance_m: number; duration_s: number; moving_duration_s: number
    average_pace_sec_per_km: number; average_speed_mps: number; max_speed_mps: number
    total_steps: number; average_cadence_spm: number; average_stride_m: number
    elevation_gain_m: number; elevation_loss_m: number
  }
  samples: Sample[]; kilometre_splits: Split[]
  submission?: { status: string; remote_run_id?: string; message?: string; submitted_at: string }
  validation: { valid: boolean; findings: {level: string; code: string; message: string}[] }
}

export type RunListItem = {
  id: string; started_at: string; distance_m: number; duration_s: number
  pace_sec_per_km: number; total_steps: number; valid: boolean
}

export type MapMetrics = {
  requests: number; cache_hits: number; cache_misses: number; retries: number; rate_limited: number
  provider_errors: number; inflight: number; waiting: number; fairness_keys: number; dispatched: number
  qps: number; burst: number; burst_per_key: number; key_count: number
  keys: {id:string;qps:number;dispatched:number}[]
  cache_entries: number; cache_capacity: number; cache_ttl_seconds: number
  memory_cache_hits: number; persistent_cache_hits: number; persistent_cache_writes: number
  persistent_cache_errors: number; persistent_cache_enabled: boolean
}

export type Config = {
  face_runtime?:{ready:boolean;status:string;model:string}
  map_api_configured: boolean; database_configured: boolean; protocol_configured: boolean
  route_candidates: number
  client_protocol: { app_version: string; build_version: string; app_code: string; environment: number; environment_name: string; base_url: string }
  map_rate_limit: {qps:number;burst:number;burst_per_key:number;key_count:number}
  automation: {workers:number;worker_processes:number;worker_concurrency:number;active:number;queue_depth:number;queued:number;succeeded:number;failed:number;skipped:number}
}

export type SystemSettings = {
  map_api_key_configured: boolean
  map_api_keys: {id:string;masked:string;qps:number}[]; total_map_qps: number
  worker_processes: number; worker_concurrency: number; total_concurrency: number; updated_at: string
}

export type SystemSettingsInput = {
  map_api_keys: {id?:string;api_key:string;qps:number}[]
  worker_processes: number; worker_concurrency: number
}

export type SMSChallenge = { ticket: string; solver_x: number; solver_score: number }
export type RemoteRecordData = Record<string, unknown> | unknown[] | null
export type AccountResources = { plans: Plan[]; areas: Area[]; face: FaceProfile; face_sync_error?: string }

export type AccountLoginResult = {
  account?: Account
  draft?: Account
  verification_required: boolean
  challenge?: SMSChallenge
}

export type RemoteRecordCheck = {
  code: string
  label: string
  status: 'pass' | 'warning' | 'error' | 'skipped' | string
  message: string
}

export type RemoteRecordInspection = {
  engine: string
  valid: boolean
  score: number
  rule_source?: string
  metrics: {
    distance_m: number; duration_s: number
    reported_pace_sec_per_km: number; derived_pace_sec_per_km: number
    total_steps: number; reported_cadence_spm: number; derived_cadence_spm: number
    derived_stride_m: number; track_distance_m: number; point_count: number
    step_segment_count: number; pace_segment_count: number; median_sample_period_s: number
  }
  checks: RemoteRecordCheck[]
  findings: {level:string;code:string;message:string}[]
}

export type RemoteRecordInspectionResponse = {
  record: RemoteRecordData
  inspection: RemoteRecordInspection
}
