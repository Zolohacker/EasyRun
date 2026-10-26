import {useEffect,useRef,useState} from 'react'
import {Clock3,RefreshCw} from 'lucide-react'
import {money,portalRequest} from './portal'
import {remainingScanSeconds,scanCountdown,scanPending,startDepositPolling,type DepositScanStatus} from './depositScan'

// Mount only inside the USDT dialog; unmounting cancels timers and HTTP reads.
export default function DepositScanPanel({onRefresh}:{onRefresh:()=>Promise<void>}){
  const [snapshot,setSnapshot]=useState<{value:DepositScanStatus;receivedAt:number}|null>(null)
  const [now,setNow]=useState(()=>performance.now()),[requesting,setRequesting]=useState(false),[error,setError]=useState('')
  const refresh=useRef(onRefresh);refresh.current=onRefresh
  useEffect(()=>{
    const timer=setInterval(()=>setNow(performance.now()),1000)
    const stop=startDepositPolling({
      request:(method,signal)=>portalRequest<DepositScanStatus>('wallet/scan',method==='POST'?{}:undefined,method,signal),
      onStatus:(value,receivedAt)=>setSnapshot({value,receivedAt}),onBusy:setRequesting,onError:setError,
      onComplete:()=>{void refresh.current()},
    })
    return()=>{stop();clearInterval(timer)}
  },[])
  const status=snapshot?.value||null,pending=requesting||scanPending(status)
  const remaining=snapshot?remainingScanSeconds(snapshot.value,now-snapshot.receivedAt):0
  return <div className="deposit-scan-panel">
    <div className="deposit-scan-heading"><span className={`deposit-scan-indicator ${pending?'checking':''}`}><RefreshCw className={pending?'spin':''}/></span><div><strong>{pending?'正在检查到账':'自动检查已开启'}</strong><span>{error?'连接中断后会自动重试':!snapshot?'正在读取检查状态…':pending?'正在核对充值记录':remaining>0?`${scanCountdown(remaining)} 后再次检查`:'即将开始下一次检查'}</span></div></div>
    <p className="deposit-keep-open"><Clock3 size={16}/><span>每分钟检查一次，请勿关闭此窗口，以便及时到账。</span></p>
    {status?.message&&<p className={`deposit-scan-result ${status.state}`} role="status">{status.message}</p>}
    {!!status?.credited_micro&&<b className="deposit-scan-credit">本次已入账 {money(status.credited_micro)} USDT · {status.deposit_count} 笔</b>}
    {error&&<p className="portal-error" role="alert">{error}</p>}
    <small>关闭后停止发起新检查，已受理的检查仍会完成。</small>
  </div>
}
