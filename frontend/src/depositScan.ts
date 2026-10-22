export type DepositScanStatus={
  state:'idle'|'queued'|'running'|'succeeded'|'failed'
  message:string
  requested_at?:string
  started_at?:string
  finished_at?:string
  next_allowed_at?:string
  server_time:string
  retry_after_seconds:number
  credited_micro:number
  deposit_count:number
}

// Use server time plus monotonic elapsed time, not the customer's wall clock.
export function remainingScanSeconds(status:DepositScanStatus,elapsedMs:number){
  const next=Date.parse(status.next_allowed_at||''),server=Date.parse(status.server_time)
  const initial=Number.isFinite(next)&&Number.isFinite(server)?next-server:Math.max(0,status.retry_after_seconds||0)*1000
  return Math.max(0,Math.ceil((initial-Math.max(0,elapsedMs))/1000))
}
export function scanPending(status:DepositScanStatus|null){return status?.state==='queued'||status?.state==='running'}
export function scanCountdown(seconds:number){return `${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')}`}
