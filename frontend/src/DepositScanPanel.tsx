import {useEffect,useRef,useState} from 'react'
import {RefreshCw} from 'lucide-react'
import {money,portalRequest} from './portal'
import {remainingScanSeconds,scanCountdown,scanPending,type DepositScanStatus} from './depositScan'

export default function DepositScanPanel({onRefresh}:{onRefresh:()=>Promise<void>}){
  const [snapshot,setSnapshot]=useState<{value:DepositScanStatus;receivedAt:number}|null>(null)
  const [now,setNow]=useState(()=>performance.now()),[requesting,setRequesting]=useState(false),[error,setError]=useState(''),[reload,setReload]=useState(0)
  const refreshed=useRef('')
  const status=snapshot?.value||null,pending=scanPending(status)
  const remaining=snapshot?remainingScanSeconds(snapshot.value,now-snapshot.receivedAt):0
  useEffect(()=>{const timer=setInterval(()=>setNow(performance.now()),1000);return()=>clearInterval(timer)},[])
  useEffect(()=>{
    let current=true
    let timer:ReturnType<typeof setTimeout>|undefined
    const read=async()=>{
      try{const value=await portalRequest<DepositScanStatus>('wallet/scan');if(current){setSnapshot({value,receivedAt:performance.now()});setError('')}}
      catch(e){if(current)setError((e as Error).message)}
      finally{if(current&&pending)timer=setTimeout(()=>void read(),2500)}
    }
    void read()
    // Poll only the local persisted result while an explicit check is pending.
    // GET never scans the blockchain and does not consume/reset the cooldown.
    return()=>{current=false;if(timer)clearTimeout(timer)}
  },[pending,reload])
  useEffect(()=>{
    if(!status?.requested_at||(status.state!=='succeeded'&&status.state!=='failed')||refreshed.current===status.requested_at)return
    refreshed.current=status.requested_at
    void onRefresh()
  },[status,onRefresh])
  const check=async()=>{
    if(requesting||pending||remaining>0)return
    setRequesting(true);setError('')
    try{const value=await portalRequest<DepositScanStatus>('wallet/scan',{});setSnapshot({value,receivedAt:performance.now()})}
    catch(e){
      setError((e as Error).message)
      // A different tab may have started a check, or the response may be lost.
      try{const value=await portalRequest<DepositScanStatus>('wallet/scan');setSnapshot({value,receivedAt:performance.now()})}catch{/* Keep the request error and allow status to be reloaded. */}
    }finally{setRequesting(false)}
  }
  return <div className="deposit-scan-panel">
    <div><strong>转账后，主动检查充值</strong><p>平台不会定时扫链。每位用户每 3 分钟可检查一次，刷新页面不会重置间隔。</p></div>
    {!snapshot&&error?<button type="button" className="button subtle" onClick={()=>setReload(v=>v+1)}>重新读取检查状态</button>:<button type="button" className="button primary" disabled={!snapshot||requesting||pending||remaining>0} onClick={()=>void check()}><RefreshCw className={requesting||pending?'spin':''}/>{requesting?'正在提交…':status?.state==='queued'?'排队中…':status?.state==='running'?'正在检查充值…':remaining>0?`${scanCountdown(remaining)} 后可再次检查`:'检查充值'}</button>}
    {pending&&remaining>0&&<small>本次检查已受理 · 冷却剩余 {scanCountdown(remaining)}</small>}
    {status?.message&&<p className={`deposit-scan-result ${status.state}`} role="status">{status.message}</p>}
    {!!status?.credited_micro&&<b className="deposit-scan-credit">本次已入账 {money(status.credited_micro)} USDT · {status.deposit_count} 笔</b>}
    {error&&<p className="portal-error" role="alert">{error}</p>}
  </div>
}
