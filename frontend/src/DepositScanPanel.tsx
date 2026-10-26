import {useEffect,useRef,useState} from 'react'
import {Clock3,RefreshCw,TriangleAlert} from 'lucide-react'
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
  const failed=!!error||status?.state==='failed'
  return <div className="deposit-scan-panel">
    <div className="deposit-scan-topline">
      <div className="deposit-scan-heading"><span className={`deposit-scan-indicator ${failed?'warning':pending?'checking':''}`} aria-hidden="true">{failed?<TriangleAlert/>:<RefreshCw className={pending?'spin':''}/>}</span><strong>{error?'连接暂时中断':pending?'正在检查到账':failed?'检查未完成':'自动检查已开启'}</strong></div>
      <span className={`deposit-scan-timing ${failed?'warning':''}`}>{error?'稍后自动重试':!snapshot?'读取状态中…':pending?'核对充值记录中':remaining>0?`${scanCountdown(remaining)} 后检查`:'即将检查'}</span>
    </div>
    {status?.message&&!error&&<p className={`deposit-scan-result ${status.state}`} role="status">{status.message}</p>}
    {!!status?.credited_micro&&<b className="deposit-scan-credit">本次已入账 {money(status.credited_micro)} USDT · {status.deposit_count} 笔</b>}
    {error&&<p className="portal-error" role="alert">{error}</p>}
    <p className="deposit-keep-open"><Clock3 size={15}/><span>每分钟检查一次，请勿关闭此窗口。</span></p>
  </div>
}
