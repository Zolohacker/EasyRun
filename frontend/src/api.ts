import type { Account, AccountInput, AccountLoginResult, AccountResources, Area, Config, Execution, FaceDetectionResult, FaceProfile, Job, MapMetrics, Plan, RemoteRecordData, RemoteRecordInspectionResponse, Route, Run, RunListItem, SMSChallenge, SystemSettings, SystemSettingsInput, TargetSelection } from './types'

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, { ...options, headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } })
  const body = response.status === 204 ? null : await response.json().catch(() => null)
  if (!response.ok) {
    if (body?.error?.code === 'session_expired') window.dispatchEvent(new CustomEvent('runner-session-expired'))
    throw new Error(body?.error?.message || `请求失败：HTTP ${response.status}`)
  }
  return body as T
}
const list = async <T>(url: string) => (await request<{items:T[]}>(url)).items || []

async function upload<T>(url: string, form: FormData, method = 'PUT'): Promise<T> {
  const response = await fetch(url, {method, body:form})
  const body = await response.json().catch(() => null)
  if (!response.ok) throw new Error(body?.error?.message || `请求失败：HTTP ${response.status}`)
  return body as T
}

export const api = {
  config: () => request<Config>('/api/v1/config'),
  systemSettings: () => request<SystemSettings>('/api/v1/system/settings'),
  updateSystemSettings: (value:SystemSettingsInput) => request<SystemSettings>('/api/v1/system/settings', {method:'PUT',body:JSON.stringify(value)}),
  metrics: () => request<MapMetrics>('/api/v1/map/metrics'),
  plans: () => list<Plan>('/api/v1/plans'),
  samplePlan: (id: string, seed: number, targetDistanceM: number) => request<TargetSelection>(`/api/v1/plans/${id}/sample`, {method:'POST', body:JSON.stringify({seed,target_distance_m:targetDistanceM})}),
  createPlan: (value: Partial<Plan>) => request<Plan>('/api/v1/plans', {method:'POST', body:JSON.stringify(value)}),
  areas: () => list<Area>('/api/v1/areas'),
  createArea: (value: Partial<Area>) => request<Area>('/api/v1/areas', {method:'POST', body:JSON.stringify(value)}),
  accounts: () => list<Account>('/api/v1/accounts'),
  account: (id: string) => request<Account>(`/api/v1/accounts/${id}`),
  createAccount: (value: AccountInput) => request<AccountLoginResult>('/api/v1/accounts/onboard/password', {method:'POST', body:JSON.stringify(value)}),
  requestOnboardSMS: (value: AccountInput) => request<AccountLoginResult>('/api/v1/accounts/onboard/sms/request', {method:'POST', body:JSON.stringify(value)}),
  verifyOnboardSMS: (account: Account, code: string, ticket: string, password?: string) => request<Account>('/api/v1/accounts/onboard/sms/verify', {method:'POST', body:JSON.stringify({account,code,ticket,password})}),
  updateAccount: (value: Account | (Partial<Account> & {id:string;password?:string})) => request<Account>(`/api/v1/accounts/${value.id}`, {method:'PUT', body:JSON.stringify(value)}),
  loginAccount: (id: string) => request<AccountLoginResult>(`/api/v1/accounts/${id}/login`, {method:'POST'}),
  requestSMS: (id: string) => request<SMSChallenge>(`/api/v1/accounts/${id}/sms/request`, {method:'POST'}),
  verifySMS: (id: string, code: string, ticket: string) => request<Account>(`/api/v1/accounts/${id}/sms/verify`, {method:'POST', body:JSON.stringify({code,ticket})}),
  syncAccount: (id: string) => request<AccountResources>(`/api/v1/accounts/${id}/sync`, {method:'POST'}),
  faceProfile: (id: string) => request<FaceProfile>(`/api/v1/accounts/${id}/face`),
  syncFace: (id: string) => request<FaceProfile>(`/api/v1/accounts/${id}/face/sync`, {method:'POST'}),
  checkFace: (id: string, source: 'base'|'custom'|'effective' = 'effective') => request<FaceDetectionResult>(`/api/v1/accounts/${id}/face/check`, {method:'POST', body:JSON.stringify({source})}),
  setFace: (id: string, file: File) => { const form=new FormData(); form.append('image',file); return upload<FaceProfile>(`/api/v1/accounts/${id}/face/image`,form) },
  clearFace: (id: string) => request<FaceProfile>(`/api/v1/accounts/${id}/face/image`, {method:'DELETE'}),
  faceImageURL: (id: string, source: 'base'|'custom'|'effective' = 'effective', version = '') => `/api/v1/accounts/${id}/face/image?source=${source}${version ? `&v=${encodeURIComponent(version)}` : ''}`,
  accountRecords: (id: string, page = 1, pageSize = 20) => request<RemoteRecordData>(`/api/v1/accounts/${id}/records?page=${page}&page_size=${pageSize}`),
  accountRecord: (id: string, recordID: string, semesterCode = '') => request<RemoteRecordData>(`/api/v1/accounts/${id}/records/${encodeURIComponent(recordID)}${semesterCode ? `?semester_code=${encodeURIComponent(semesterCode)}` : ''}`),
  accountRecordInspection: (id: string, recordID: string, semesterCode = '') => request<RemoteRecordInspectionResponse>(`/api/v1/accounts/${id}/records/${encodeURIComponent(recordID)}/inspection${semesterCode ? `?semester_code=${encodeURIComponent(semesterCode)}` : ''}`),
  jobs: () => list<Job>('/api/v1/jobs'),
  createJob: (value: Partial<Job>) => request<Job>('/api/v1/jobs', {method:'POST', body:JSON.stringify(value)}),
  updateJob: (value: Job) => request<Job>(`/api/v1/jobs/${value.id}`, {method:'PUT', body:JSON.stringify(value)}),
  runJob: (id: string) => request<{execution_id:string,status:string}>(`/api/v1/jobs/${id}/run`, {method:'POST'}),
  executions: () => list<Execution>('/api/v1/executions?limit=100'),
  runs: () => list<RunListItem>('/api/v1/runs?limit=50'),
  planRoute: (body: object) => request<Route>('/api/v1/routes/plan', {method:'POST', body:JSON.stringify(body)}),
  simulate: (body: object) => request<Run>('/api/v1/runs/simulate', {method:'POST', body:JSON.stringify(body)}),
}
