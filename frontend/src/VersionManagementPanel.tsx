import {useCallback,useEffect,useRef,useState,type FormEvent} from 'react'
import {ArrowDownToLine,RefreshCw,RotateCcw,Settings2,ShieldAlert,ShieldCheck} from 'lucide-react'
import Modal from './Modal'
import {portalRequest,type PortalUser} from './portal'
import {clientVersionLabel,updatePhaseLabel,updating,type Versions} from './versions'
import './versions.css'

const time=(value?:string)=>value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'尚未检查'

export default function VersionManagementPanel({user,notify}:{user:PortalUser;notify:(message:string)=>void}) {
  const [data,setData]=useState<Versions|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState('')
  const [modal,setModal]=useState<'settings'|'install'|'rollback'|null>(null),[password,setPassword]=useState('')
  const [token,setToken]=useState(''),[clearToken,setClearToken]=useState(false),[auto,setAuto]=useState(false)
  const [watch,setWatch]=useState(false),[reconnecting,setReconnecting]=useState(false)
  const initialBuild=useRef(''),watchStarted=useRef(0)
  const superadmin=user.role==='superadmin'
  const refresh=useCallback(async()=>{
    try {
      const value=await portalRequest<Versions>('system/versions')
      setData(value);setError('');setReconnecting(false)
      const identity=value.platform.current.version+':'+value.platform.current.commit
      if(!initialBuild.current)initialBuild.current=identity
      if(watchStarted.current&&identity!==initialBuild.current&&value.platform.phase==='succeeded'){window.location.reload();return}
      if(watchStarted.current&&(['succeeded','rolled_back','failed'].includes(value.platform.phase))){setWatch(false);watchStarted.current=0;notify(value.platform.message||updatePhaseLabel(value.platform.phase))}
    } catch(e) { if(watchStarted.current)setReconnecting(true);else setError((e as Error).message) }
  },[notify])
  useEffect(()=>{void refresh();const timer=window.setInterval(()=>void refresh(),watch?2000:15000);return()=>window.clearInterval(timer)},[refresh,watch])
  const check=async(kind:'client'|'platform')=>{
    setBusy(kind)
    try{await portalRequest(`system/versions/${kind}/check`,{});await refresh()}catch(e){notify((e as Error).message)}finally{setBusy('')}
  }
  const open=(kind:'settings'|'install'|'rollback')=>{setPassword('');setToken('');setClearToken(false);setAuto(!!data?.platform.settings.auto_install);setModal(kind)}
  const submit=async(event:FormEvent)=>{
    event.preventDefault();if(!modal)return;setBusy('action')
    try {
      if(modal==='settings'){
        await portalRequest('system/versions/settings',{auto_install:auto,token,clear_token:clearToken,password},'PUT')
        notify('更新配置已保存');await refresh()
      } else {
        await portalRequest(`system/versions/platform/${modal}`,{password})
        watchStarted.current=Date.now();setWatch(true);notify('已开始准备，下载、校验后会自动重启并验收')
      }
      setModal(null);setPassword('');setToken('')
    } catch(e) {notify((e as Error).message)}finally{setBusy('')}
  }
  if(!data)return <section className="panel version-loading"><h2>版本与更新</h2><p role={error?'alert':undefined}>{error||'正在读取客户端与平台版本…'}</p>{error&&<button className="button subtle" onClick={()=>void refresh()}>重试</button>}</section>
  const {client,platform}=data
  const pending=watch||updating(platform.phase)
  return <section className="version-grid" aria-label="版本与更新">
    <article className={`panel version-card ${!client.automation_allowed?'version-blocked':''}`}>
      <div className="version-heading"><h2>闪动校园版本</h2><span className={`status-pill ${client.automation_allowed?'ok':'warn'}`}>{clientVersionLabel(client)}</span></div>
      <div className="version-number"><strong>{client.latest_version||'待核实'}</strong><span>{client.have_update?'接口返回的最新版本':'当前渠道可用版本'}</span></div>
      <dl className="version-facts"><div><dt>当前适配</dt><dd>{client.current_version} <small>Build {client.current_build}</small></dd></div>{client.support_version&&<div><dt>最低支持</dt><dd>{client.support_version}</dd></div>}<div><dt>最后检查</dt><dd>{time(client.checked_at)}</dd></div></dl>
      <p className={`version-gate ${client.automation_allowed?'allowed':''}`} role={client.automation_allowed?'status':'alert'}>{client.automation_allowed?<ShieldCheck/>:<ShieldAlert/>}<span>{client.reason}</span></p>
      {client.last_error&&<p className="version-error">{client.last_error}</p>}
      {client.description&&<details className="version-notes"><summary>{client.title||'客户端更新说明'}</summary><p>{client.description}</p></details>}
      <div className="version-footer"><span>每 5 分钟检查 · 服务端执行门禁</span><button className="button subtle" disabled={!!busy} onClick={()=>void check('client')}><RefreshCw className={busy==='client'?'spin':''}/>检查版本</button></div>
    </article>
    <article className="panel version-card">
      <div className="version-heading"><h2>EasyRun 平台更新</h2><span className={`status-pill ${platform.has_update||platform.last_error?'warn':'ok'}`}>{pending?updatePhaseLabel(platform.phase):platform.last_error?'更新源待就绪':platform.has_update?'发现新版本':'当前版本'}</span></div>
      <div className="version-number"><strong>v{platform.current.version.replace(/^v/,'')}</strong><span>{platform.current.os} / {platform.current.arch} · {platform.current.commit.slice(0,10)}</span></div>
      <dl className="version-facts"><div><dt>最新发行</dt><dd>{platform.latest_version||'暂未获取'}</dd></div><div><dt>最后检查</dt><dd>{time(platform.checked_at)}</dd></div><div><dt>闲时自动安装</dt><dd>{platform.settings.auto_install?'已开启':'未开启'}</dd></div></dl>
      {(platform.message||pending)&&<p className="version-update-message" role="status">{reconnecting?'平台正在重启，连接恢复后自动确认结果…':platform.message||'正在准备更新…'}</p>}
      {platform.last_error&&<p className="version-error">{platform.last_error}</p>}
      {platform.capability_reason&&<p className="version-muted">{platform.capability_reason}</p>}
      {platform.notes&&<details className="version-notes"><summary>发行说明</summary><p>{platform.notes}</p></details>}
      <div className="version-actions"><button className="button subtle" disabled={!!busy||pending} onClick={()=>void check('platform')}><RefreshCw className={busy==='platform'?'spin':''}/>检查更新</button>{superadmin&&<><button className="button primary" disabled={!platform.can_install||!!busy||pending} onClick={()=>open('install')}><ArrowDownToLine/>一键更新</button><button className="button subtle" disabled={!platform.can_rollback||!!busy||pending} onClick={()=>open('rollback')}><RotateCcw/>回滚</button><button className="icon-button" aria-label="平台更新设置" title="平台更新设置" disabled={!!busy||pending} onClick={()=>open('settings')}><Settings2/></button></>}</div>
      <p className="version-muted version-source">更新源 {platform.settings.repository} · 每 20 分钟检查</p>
    </article>
    {error&&<p className="version-error" role="alert">{error}</p>}
    {modal&&<Modal title={modal==='settings'?'平台更新设置':modal==='install'?'确认更新平台':'确认回滚平台'} onClose={()=>{if(!busy){setModal(null);setPassword('');setToken('')}}}>
      <form className="version-settings" onSubmit={event=>void submit(event)}>
        {modal==='settings'?<>
          <p className="portal-hint">从固定仓库 {platform.settings.repository} 获取稳定 Release。私有仓库需配置仅此仓库 Contents: Read 权限的 Token，保存后加密存储且不回显。</p>
          <label>只读 GitHub Token<input type="password" autoComplete="off" value={token} onChange={e=>setToken(e.target.value)} placeholder={platform.settings.token_configured?'已配置，留空保持不变':'私有仓库填写，公开仓库可留空'} disabled={clearToken}/></label>
          {platform.settings.token_configured&&<label className="version-checkbox"><input type="checkbox" checked={clearToken} onChange={e=>setClearToken(e.target.checked)}/>清除已保存的 Token</label>}
          <label className="version-checkbox"><input type="checkbox" checked={auto} onChange={e=>setAuto(e.target.checked)} disabled={!!platform.capability_reason}/>发现稳定版后，仅在任务空闲时自动安装</label>
          <p className="portal-hint">运行中或排队任务不会被平台更新打断。发行包须通过 SHA-256 校验，重启验收失败将自动回滚。客户端强更门禁始终独立生效。</p>
        </>:<p className="portal-hint">{modal==='install'?`将下载 ${platform.latest_version} 并校验后安装。`:'将恢复上一份通过校验的程序备份。'} 仅在无运行/排队任务时执行；期间暂停新任务，更新程序不会替换数据库、钱包、配置和模型文件。</p>}
        <label>当前超级管理员密码<input type="password" autoComplete="current-password" required value={password} onChange={e=>setPassword(e.target.value)}/></label>
        <button className="button primary" type="submit" disabled={!!busy}>{busy==='action'?'处理中…':modal==='settings'?'保存配置':'确认并执行'}</button>
      </form>
    </Modal>}
  </section>
}
