import {useCallback,useEffect,useRef,useState} from 'react'
import {ArrowDownLeft,ArrowUpRight,Check,LockKeyhole,RefreshCw} from 'lucide-react'
import SearchSelect from './SearchSelect'
import {money,portalRequest} from './portal'
import {creditLabel,newCreditRequestID,type PrepayInfo,type PrepayMode,type TaskQuote} from './taskPrepay'

export default function TaskPrepayPanel({jobID,notify}:{jobID:string;notify:(message:string)=>void}){
  const [info,setInfo]=useState<PrepayInfo|null>(null),[mode,setMode]=useState<PrepayMode>('per_run'),[quantity,setQuantity]=useState(1),[busy,setBusy]=useState(false),[error,setError]=useState(''),[quote,setQuote]=useState<TaskQuote|null>(null)
  const pending=useRef<{signature:string;id:string}|null>(null),key=`easyrun-adjust:${jobID}`
  useEffect(()=>{try{pending.current=JSON.parse(sessionStorage.getItem(key)||'null')}catch{/* optional retry recovery */}},[key])
  const load=useCallback(async()=>{const value=await portalRequest<PrepayInfo>(`jobs/${jobID}/prepay`);setInfo(value);setMode(value.credit.mode);setQuantity(value.credit.revision?(value.credit.mode==='per_km'?value.credit.total_units/1000:value.credit.total_units):1);setQuote(null)},[jobID])
  useEffect(()=>{void load().catch(e=>setError(e.message))},[load])
  const units=mode==='per_run'?quantity:Math.round(quantity*1000),minimum=(info?.credit.settled_units||0)+(info?.credit.reserved_units||0)
  const preview=async()=>{if(busy)return;setBusy(true);setError('');try{setQuote(await portalRequest<TaskQuote>(`jobs/${jobID}/prepay/quote`,{mode,total_units:units}))}catch(e){setError((e as Error).message);setQuote(null)}finally{setBusy(false)}}
  const confirm=async()=>{if(!quote||busy)return;setBusy(true);setError('');try{
    const signature=JSON.stringify([jobID,mode,units,quote.delta_micro,quote.revision])
    if(pending.current?.signature!==signature)pending.current={signature,id:newCreditRequestID()}
    try{sessionStorage.setItem(key,JSON.stringify(pending.current))}catch{/* memory retries are still idempotent */}
    await portalRequest(`jobs/${jobID}/prepay`,{request_id:pending.current.id,mode,total_units:units,expected_revision:quote.revision,expected_delta_micro:quote.delta_micro},'PUT')
    pending.current=null;try{sessionStorage.removeItem(key)}catch{/* optional */}
    notify(quote.delta_units<0?`已退回未执行预扣 ${money(-quote.delta_micro)} USDT`:'下单总量已更新，新增额度已预扣')
    setQuote(null);try{await load()}catch{setError('调整已完成，余额刷新失败，请刷新查看，不要重复提交。')}
  }catch(e){setError((e as Error).message);setQuote(null)}finally{setBusy(false)}}
  const change=(next:number)=>{setQuantity(next);setQuote(null)}
  return <section className="task-prepay-panel" onKeyDown={e=>{if(e.key==='Enter'&&e.target instanceof HTMLInputElement)e.preventDefault()}}><div className="panel-heading"><h3>下单额度</h3><button type="button" className="mini-button" title="刷新额度" aria-label="刷新预扣额度" disabled={busy} onClick={()=>void load().catch(e=>setError(e.message))}><RefreshCw/></button></div>
    {!info?<p className="portal-hint">正在读取…</p>:info.personal?<p className="portal-hint">个人任务，不产生费用。</p>:<>
    {info.package_owned&&<p className="portal-notice">已购完整计划套餐；已有未执行预扣仍可退回。</p>}
    <div className="escrow-metrics"><div><span>未执行 · 可退</span><b>{creditLabel(info.credit.mode,info.credit.remaining_units)}</b><small>{money(info.credit.available_micro)} USDT</small></div><div><span>执行中 · 锁定</span><b>{creditLabel(info.credit.mode,info.credit.reserved_units)}</b><small>{money(info.credit.reserved_micro)} USDT</small></div><div><span>已成功 · 已结算</span><b>{creditLabel(info.credit.mode,info.credit.settled_units)}</b><small>{money(info.credit.settled_micro)} USDT</small></div></div>
    {info.can_adjust?<><div className="form-grid"><label className="field"><span>计费方式</span><SearchSelect disabled={busy||info.credit.revision>0} value={mode} onChange={e=>{setMode(e.target.value as PrepayMode);change(1)}}><option value="per_run">按次</option><option value="per_km">按公里</option></SearchSelect></label><label className="field"><span>下单总{mode==='per_run'?'次数':'公里数'}（含已完成）</span><input type="number" disabled={busy} min={mode==='per_run'?minimum:minimum/1000} step={mode==='per_run'?1:'any'} value={quantity} onChange={e=>change(Number(e.target.value))}/></label></div>
    <div className="escrow-balance-row"><span>可用余额 <b>{money(info.balance_micro||0)} USDT</b></span><button className="button subtle" type="button" disabled={busy||!info.credit.remaining_units} onClick={()=>change(mode==='per_run'?minimum:minimum/1000)}>退回全部未执行额度</button></div>
    {quote?<div className={`escrow-quote ${quote.delta_units<0?'refund':''}`}><span>{quote.delta_units<0?<ArrowDownLeft/>:<ArrowUpRight/>}{quote.delta_units<0?'退回余额':'本次补预扣'}</span><strong>{money(Math.abs(quote.delta_micro))} <small>USDT</small></strong><button type="button" className="button primary" disabled={busy||quote.delta_micro>quote.balance_micro||quote.delta_units===0} onClick={()=>void confirm()}><Check/>{quote.delta_units===0?'总量未变化':quote.delta_units<0?'确认减少并退款':'确认增加并预扣'}</button></div>:<button type="button" className="button primary wide" disabled={busy||!Number.isSafeInteger(units)||units<minimum} onClick={()=>void preview()}>{busy?'正在核算…':'预览金额变动'}</button>}
    </>:!info.package_owned&&<p className="portal-hint">仅所属客户可确认下单或调整总量。</p>}
    <p className="escrow-rule"><LockKeyhole/>仅退未执行部分的原预扣金额。成功后结算，不重复扣余额，已结算不退。</p></>}
    {error&&<div className="portal-error" role="alert">{error}</div>}
  </section>
}
