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
  return Math.ceil(remainingScanMilliseconds(status,elapsedMs)/1000)
}
function remainingScanMilliseconds(status:DepositScanStatus,elapsedMs:number){
  const next=Date.parse(status.next_allowed_at||''),server=Date.parse(status.server_time)
  const initial=Number.isFinite(next)&&Number.isFinite(server)?next-server:Math.max(0,status.retry_after_seconds||0)*1000
  return Math.max(0,initial-Math.max(0,elapsedMs))
}
export function scanPending(status:DepositScanStatus|null){return status?.state==='queued'||status?.state==='running'}
export function scanCountdown(seconds:number){return `${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')}`}

export const DEPOSIT_SCAN_INTERVAL_MS=60_000
type PollOptions={
  request:(method:'GET'|'POST',signal:AbortSignal)=>Promise<DepositScanStatus>
  onStatus:(status:DepositScanStatus,receivedAt:number)=>void
  onBusy:(busy:boolean)=>void
  onError:(message:string)=>void
  onComplete:()=>void
  now?:()=>number
  schedule?:(callback:()=>void,delay:number)=>ReturnType<typeof setTimeout>
  cancel?:(timer:ReturnType<typeof setTimeout>)=>void
}

// One serial loop, owned by the mounted payment dialog. GET only reads local
// results; POST requests a scoped chain scan. The database enforces the same
// minute budget across tabs/devices. No catch-up bursts after a suspended tab.
export function startDepositPolling(options:PollOptions){
  const now=options.now||(()=>performance.now()),schedule=options.schedule||setTimeout,cancel=options.cancel||clearTimeout
  let active=true,timer:ReturnType<typeof setTimeout>|undefined,requestController:AbortController|undefined
  let nextAttempt=0,lastCompleted='',status:DepositScanStatus|null=null,receivedAt=now()
  const publish=(value:DepositScanStatus)=>{
    if(!active)return
    status=value;receivedAt=now();options.onStatus(value,receivedAt)
    if(value.requested_at&&(value.state==='succeeded'||value.state==='failed')&&lastCompleted!==value.requested_at){lastCompleted=value.requested_at;options.onComplete()}
  }
  const request=async(method:'GET'|'POST')=>{
    const controller=new AbortController();requestController=controller
    const timeout=schedule(()=>controller.abort(),20_000)
    try{return await options.request(method,controller.signal)}
    finally{cancel(timeout);if(requestController===controller)requestController=undefined}
  }
  const tick=async()=>{
    let failed=false
    try{
      const value=await request('GET')
      if(!active)return
      publish(value);options.onError('')
      if(!scanPending(value)&&remainingScanSeconds(value,0)===0&&now()>=nextAttempt){
        // Record attempts before sending: a lost response may still be accepted.
        nextAttempt=now()+DEPOSIT_SCAN_INTERVAL_MS;options.onBusy(true)
        publish(await request('POST'))
      }
    }catch(e){if(active){failed=true;options.onError((e as Error).name==='AbortError'?'请求超时，稍后自动重试':(e as Error).message||'网络暂未连接，稍后自动重试')}}
    finally{
      if(active){
        options.onBusy(false)
        const cooldown=status?remainingScanMilliseconds(status,now()-receivedAt):0
        const delay=failed?10_000:scanPending(status)?2500:Math.max(1000,cooldown,nextAttempt-now())
        timer=schedule(()=>void tick(),delay)
      }
    }
  }
  void tick()
  return()=>{active=false;if(timer!==undefined)cancel(timer);requestController?.abort()}
}
