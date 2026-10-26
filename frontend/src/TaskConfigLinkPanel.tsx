import {useEffect,useState} from 'react'
import {Copy,Link2,RefreshCw,ShieldCheck,Trash2} from 'lucide-react'
import {Field} from './PortalPages'
import {portalRequest,type FaceLink} from './portal'
import type {Job} from './types'
import TaskPrepayPanel from './TaskPrepayPanel'

export default function TaskConfigLinkPanel({job,notify}:{job:Job;notify:(message:string)=>void}){
  const [link,setLink]=useState<FaceLink|null>(null),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[days,setDays]=useState(30),[error,setError]=useState('')
  useEffect(()=>{
    let current=true
    setLoading(true)
    void portalRequest<FaceLink>(`jobs/${job.id}/config-link`).then(value=>{if(current)setLink(value)}).catch(e=>{if(current)setError(e.message)}).finally(()=>{if(current)setLoading(false)})
    return()=>{current=false}
  },[job.id])
  const act=async(fn:()=>Promise<void>)=>{setBusy(true);setError('');try{await fn()}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
  const absolute=link?.url?new URL(link.url,location.origin).href:''
  return <div className="form-stack account-face-link-panel task-config-link-panel">
    <div className="portal-warning"><ShieldCheck/><span>请勿泄露此链接，此链接用于帮他人下单后，由对方配置人脸、路线与跑步参数。仅限已付费且下单者允许的范围；持有者可直接修改配置。轮换后，旧链接立即失效。</span></div>
    <TaskPrepayPanel jobID={job.id} notify={notify}/>
    <p className="portal-hint">客户任务需有预付次数、里程或完整计划套餐。链接按生成时的任务范围锁定上限，并实时检查剩余额度；扩大范围需由下单者调整后轮换链接。</p>
    <Field label="链接有效天数"><input type="number" min="1" max="365" value={days} onChange={e=>setDays(Number(e.target.value))}/></Field>
    {loading?<p className="portal-hint">正在读取该任务的配置链接…</p>:absolute?<>
      <Field label="独立配置链接"><textarea readOnly rows={4} value={absolute}/></Field>
      <small className="portal-hint">到期时间：{link?.expires_at?new Date(link.expires_at).toLocaleString('zh-CN'):'—'}</small>
      <div className="action-row"><button className="button subtle" onClick={()=>void navigator.clipboard.writeText(absolute).then(()=>notify('链接已复制，请勿泄露')).catch(()=>notify('复制失败，请手动复制链接'))}><Copy/>复制链接</button><a className="button subtle" href={absolute} target="_blank" rel="noopener noreferrer"><Link2/>打开配置页</a><button className="button danger" disabled={busy} onClick={()=>void act(async()=>{await portalRequest(`jobs/${job.id}/config-link`,undefined,'DELETE');setLink(null);notify('链接已撤销，原链接立即失效')})}><Trash2/>撤销链接</button></div>
    </>:<p className="portal-hint">尚未生成修改链接。</p>}
    {error&&<div className="portal-error" role="alert">{error}</div>}
    <button className="button primary" disabled={busy||loading||!Number.isInteger(days)||days<1||days>365} onClick={()=>void act(async()=>{setLink(await portalRequest<FaceLink>(`jobs/${job.id}/config-link`,{days}));notify(absolute?'新链接已生成，旧链接立即失效':'独立配置链接已生成，请勿泄露')})}><RefreshCw className={busy?'spin':''}/>{absolute?'轮换链接（旧链接失效）':'生成独立配置链接'}</button>
  </div>
}
