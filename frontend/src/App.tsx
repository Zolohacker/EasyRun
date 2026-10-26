import {availableRouteMode,routeMapError} from './manualRoutes'
import {isOpaqueWireValue,accountDisplayName,accountStudentNumber,accountSelectorLabel} from './accountLabels'
import SearchSelect from './SearchSelect'
import Modal from './Modal'
import BrandMark from './BrandMark'
import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import {
  Workflow, AreaChart, BarChart3, Bot, CalendarClock, Check, ChevronRight, CircleGauge, CloudCog, Cpu, CreditCard, Database, LayoutDashboard, ListChecks, UserRoundCog, WalletCards, Info,
  Eye, EyeOff, Footprints, Gauge, ImagePlus, KeyRound, Layers3, Link2, ListRestart, LockKeyhole, LogOut, Map as MapIcon, MapPin, MoreHorizontal, Pencil, Play, Ruler,
  Plus, RadioTower, RefreshCw, Route as RouteIcon, Save, ScanFace, Send, Server, Settings2, ShieldCheck,
  TimerReset, ToggleLeft, ToggleRight, Trash2, UsersRound, X, Shuffle,
} from 'lucide-react'
import { api } from './api'
import AmapRouteViewport from './AmapRouteViewport'
import { WalletPage } from './PortalPages'
import { BillingAdminPage, PortalSystemSettings, UsersAdminPage } from './AdminPages'
import TaskConfigLinkPanel from './TaskConfigLinkPanel'
import TaskPrepayPanel from './TaskPrepayPanel'
import {newCreditRequestID,prepaidAmount,type PrepayMode} from './taskPrepay'
import RouteManagement from './RouteManagement'
import {matchingRoutes,routeSelectionError} from './manualRoutes'
import VersionManagementPanel from './VersionManagementPanel'
import {ListViewport,Pagination,usePagination} from './Pagination'
import { money, portalRequest, type BillingSummary, type PortalUser } from './portal'
import type {
  Account, Area, Config, Execution, FaceDetectionResult, FaceProfile, Job, ManualRoute, MapMetrics, Plan, Point,
  RemoteRecordData, RemoteRecordInspection, Route, Run, RunListItem, RunTimeWindow, Sample, SystemSettings as SystemSettingsData, TargetSelection,
} from './types'

type Tab = 'overview' | 'accounts' | 'route_test' | 'routes' | 'automation' | 'logs' | 'settings' | 'billing' | 'users' | 'wallet'
type NavigationItem={key:Tab;label:string;icon:ReactNode;count?:number}

export default function App({user,onLogout}:{user:PortalUser;onLogout:()=>Promise<void>}) {
  const customer=user.role==='customer'
  const [tab, setTab] = useState<Tab>('overview')
  const [config, setConfig] = useState<Config | null>(null)
  const [metrics, setMetrics] = useState<MapMetrics | null>(null)
  const [plans, setPlans] = useState<Plan[]>([])
  const [areas, setAreas] = useState<Area[]>([])
  const [accounts, setAccounts] = useState<Account[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [executions, setExecutions] = useState<Execution[]>([])
  const [runs, setRuns] = useState<RunListItem[]>([])
  const [loading, setLoading] = useState(true)
  const [toast, setToast] = useState('')
  const navigation:NavigationItem[]=[
    {key:'overview',label:customer?'我的总览':'运行总览',icon:<LayoutDashboard/>},
    {key:'accounts',label:customer?'我的账号':'账号矩阵',icon:<UsersRound/>,count:accounts.length},
    ...(!customer?[{key:'route_test' as const,label:'路线测试',icon:<RouteIcon/>}]:[]),
    {key:'routes',label:'路线管理',icon:<MapIcon/>},
    {key:'automation',label:'自动任务',icon:<CalendarClock/>,count:jobs.filter(job=>job.enabled).length},
    {key:'logs',label:'执行日志',icon:<ListChecks/>},
    ...(customer?[{key:'wallet' as const,label:'余额与计费',icon:<WalletCards/>}]:[
      {key:'settings' as const,label:'系统配置',icon:<Settings2/>},
      {key:'billing' as const,label:'计费管理',icon:<CreditCard/>},
      {key:'users' as const,label:'用户管理',icon:<UserRoundCog/>},
    ]),
  ]
  useEffect(()=>{window.scrollTo({top:0,behavior:'auto'})},[tab])

  const notify = useCallback((message: string) => {
    setToast(message)
    window.setTimeout(() => setToast(''), 4200)
  }, [])

  const refresh = useCallback(async () => {
    try {
      const values = await Promise.all([
        api.config(), customer?Promise.resolve(null):api.metrics(), api.plans(), api.areas(), api.accounts(), api.jobs(), api.executions(), api.runs(),
      ])
      setConfig(values[0]); setMetrics(values[1]); setPlans(values[2]); setAreas(values[3])
      setAccounts(values[4]); setJobs(values[5]); setExecutions(values[6]); setRuns(values[7])
    } catch (error) {
      notify((error as Error).message)
    } finally {
      setLoading(false)
    }
  }, [notify,customer])

  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    const handleSessionExpired=()=>void refresh()
    window.addEventListener('runner-session-expired',handleSessionExpired)
    return ()=>window.removeEventListener('runner-session-expired',handleSessionExpired)
  },[refresh])
  useEffect(() => {
    const timer = window.setInterval(async () => {
      try {
        const [nextMetrics, nextExecutions, nextJobs, nextAccounts] = await Promise.all([customer?Promise.resolve(null):api.metrics(), api.executions(), api.jobs(), api.accounts()])
        setMetrics(nextMetrics); setExecutions(nextExecutions); setJobs(nextJobs); setAccounts(nextAccounts)
      } catch { /* the next poll retries */ }
    }, 7000)
    return () => window.clearInterval(timer)
  }, [customer])

  const page = {
    overview: <DashboardOverview user={user} notify={notify} customer={customer} accounts={accounts} plans={plans} areas={areas} jobs={jobs} executions={executions} runs={runs} metrics={metrics} config={config} onNavigate={setTab}/>,
    accounts: <Accounts accounts={accounts} config={config} user={user} notify={notify} onRefresh={refresh}/>,
    route_test: <RouteTest accounts={accounts} plans={plans} areas={areas} runs={runs} metrics={metrics} config={config} notify={notify} onRefresh={refresh}/>,
    routes: <RouteManagement accounts={accounts} plans={plans} areas={areas} customer={customer} notify={notify}/>,
    automation: <Automation jobs={jobs} accounts={accounts} plans={plans} areas={areas} executions={executions} config={config} customer={customer} notify={notify} onRefresh={refresh}/>,
    logs: <ExecutionLogs accounts={accounts} plans={plans} jobs={jobs} notify={notify}/>,
    settings: <SystemSettingsPage config={config} user={user} notify={notify} onRefresh={refresh}/>,
    billing: <BillingAdminPage user={user} notify={notify}/>,
    users: <UsersAdminPage user={user} notify={notify}/>,
    wallet: <WalletPage accounts={accounts} plans={plans} jobs={jobs} notify={notify}/>,
  }[tab]

  return <div className={`app-shell ${customer?'customer-shell':''}`}>
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark"><BrandMark/></div><div><strong>EasyRun</strong><span>{customer?'客户中心':'管理后台'}</span></div></div>
      <nav aria-label="主导航">{navigation.map(item=><NavItem key={item.key} active={tab===item.key} icon={item.icon} label={item.label} count={item.count} onClick={()=>setTab(item.key)}/>)}</nav>
      {!customer&&<div className="system-card">
        <SystemRow icon={<Database/>} label="PostgreSQL" live={config?.database_configured}/>
        <SystemRow icon={<RadioTower/>} label="地图网关" live={config?.map_api_configured}/>
        <SystemRow icon={<LockKeyhole/>} label="目标协议" live={config?.protocol_configured}/>
        <div className="system-row"><Bot size={15}/><span>任务引擎</span><b>{config?.automation?.workers ?? 0} slots</b></div>
      </div>}
      <div className="portal-signed-in"><strong>{user.email}</strong><span>{({superadmin:'超级管理员',admin:'管理员',customer:'客户'} as const)[user.role]}</span><button onClick={()=>void onLogout().catch(e=>notify(e.message))}>退出登录</button></div>
    </aside>
    <main className="main-stage">
      <header className="topbar">
        <div><span className="mobile-brand-label">EasyRun · {customer?'客户中心':'管理后台'}</span><h1>{navigation.find(item=>item.key===tab)?.label||'工作空间'}</h1></div>
        <button className="icon-button" onClick={() => void refresh()} title="刷新" aria-label="刷新"><RefreshCw size={17} className={loading ? 'spin' : ''}/></button>
      </header>
      {page}
    </main>
    <MobileNavigation items={navigation} active={tab} customer={customer} user={user} onNavigate={setTab} onLogout={()=>void onLogout().catch(error=>notify(error.message))}/>
    {toast && <div className="toast" role="status"><Info size={16}/>{toast}</div>}
  </div>
}

function NavItem({active, icon, label, count, onClick}: {active:boolean; icon:ReactNode; label:string; count?:number; onClick:()=>void}) {
  return <button className={`nav-item ${active ? 'active' : ''}`} aria-current={active?'page':undefined} aria-label={label} title={label} onClick={onClick}>{icon}<span>{label}</span>{count !== undefined && <b>{count}</b>}</button>
}

function MobileNavigation({items,active,customer,user,onNavigate,onLogout}:{items:NavigationItem[];active:Tab;customer:boolean;user:PortalUser;onNavigate:(tab:Tab)=>void;onLogout:()=>void}) {
  const [open,setOpen]=useState(false)
  const primary=(customer?['overview','accounts','automation','wallet']:['overview','accounts','automation','logs']) as Tab[]
  useEffect(()=>{
    const query=window.matchMedia('(max-width:760px)')
    const resized=()=>{if(!query.matches)setOpen(false)}
    query.addEventListener('change',resized);return()=>query.removeEventListener('change',resized)
  },[])
  const navigate=(key:Tab)=>{setOpen(false);onNavigate(key)}
  return <>
    <nav className="mobile-bottom-nav" aria-label="移动端主导航">
      {primary.map(key=>{const item=items.find(item=>item.key===key);return item&&<button key={key} type="button" aria-current={active===key?'page':undefined} className={active===key?'active':''} onClick={()=>navigate(key)}>{item.icon}<span>{key==='wallet'?'余额充值':key==='accounts'?'账号':key==='overview'?'总览':key==='automation'?'任务':'日志'}</span>{!!item.count&&<b>{item.count>99?'99+':item.count}</b>}</button>})}
      <button type="button" aria-label="更多导航" aria-expanded={open} className={!primary.includes(active)||open?'active':''} onClick={()=>setOpen(true)}><MoreHorizontal/><span>更多</span></button>
    </nav>
    {open&&<Modal title="导航与账号" className="mobile-menu-modal" onClose={()=>setOpen(false)}>
      <div className="mobile-menu-grid">{items.map(item=><button key={item.key} type="button" className={active===item.key?'active':''} aria-current={active===item.key?'page':undefined} onClick={()=>navigate(item.key)}>{item.icon}<span>{item.label}</span>{item.count!==undefined&&<b>{item.count}</b>}</button>)}</div>
      <div className="mobile-user-card"><div><strong>{user.email}</strong><span>{{superadmin:'超级管理员',admin:'管理员',customer:'客户'}[user.role]}</span></div><button type="button" className="button subtle" onClick={onLogout}><LogOut size={17}/>退出登录</button></div>
    </Modal>}
  </>
}

function SystemRow({icon, label, live}: {icon:ReactNode; label:string; live?:boolean}) {
  return <div className="system-row">{icon}<span>{label}</span><i className={live ? 'live' : ''}/></div>
}

function DashboardOverview({accounts, plans, areas, jobs, executions, runs, metrics, config, onNavigate,customer,user,notify}: {
  user:PortalUser;notify:(message:string)=>void
  customer:boolean
  accounts:Account[]; plans:Plan[]; areas:Area[]; jobs:Job[]; executions:Execution[]; runs:RunListItem[]
  metrics:MapMetrics|null; config:Config|null; onNavigate:(tab:Tab)=>void
}) {
  const readyAccounts=accounts.filter(account=>account.enabled&&account.session_configured)
  const enabledJobs=jobs.filter(job=>job.enabled)
  const activeExecutions=executions.filter(item=>item.status==='queued'||item.status==='running')
  const succeeded=executions.filter(item=>item.status==='succeeded').length
  const failed=executions.filter(item=>item.status==='failed').length
  const totalDistanceM=runs.reduce((total,item)=>total+Math.max(0,item.distance_m||0),0)
  const nextJobs=[...enabledJobs].filter(job=>job.next_run_at).sort((left,right)=>new Date(left.next_run_at || 0).getTime()-new Date(right.next_run_at || 0).getTime()).slice(0,6)
  const recentExecutions=executions.slice(0,8)
  const systemReady=[config?.database_configured,config?.map_api_configured,config?.protocol_configured].filter(Boolean).length
  return <div className="page-stack overview-page">
    {!customer&&config&&!config.map_api_configured&&<section className="panel first-run-map-notice" role="status"><div className="first-run-map-icon"><KeyRound/></div><div><h2>地图服务待配置</h2><p>添加 Web 服务 Key 并设置 QPS 后，即可规划路线。</p></div><button className="button cyan" onClick={()=>onNavigate('settings')}><Settings2/>前往配置</button></section>}
    <section className="panel overview-command">
      <div className="overview-command-copy">
        <h2>{customer?'我的任务':'任务调度'}</h2>
        <div className="overview-actions">
          <button className="button primary" onClick={()=>onNavigate('automation')}><CalendarClock/>管理任务</button>
          <button className="button cyan" onClick={()=>onNavigate('accounts')}><UsersRound/>查看账号</button>
          {!customer&&<button className="button subtle" onClick={()=>onNavigate('route_test')}><RouteIcon/>路线测试</button>}
        </div>
      </div>
      <div className="overview-live-orbit">
        <div className="overview-live-ring"><Workflow aria-hidden="true"/><strong>{activeExecutions.length}</strong><span>执行中 / 排队</span></div>
        <div className="overview-live-caption"><i className={(customer?activeExecutions.length>0:systemReady===3)?'live':''}/><span>{customer?(activeExecutions.length?'任务运行中':'暂无执行任务'):systemReady===3?'服务正常':`${systemReady}/3 服务在线`}</span></div>
      </div>
    </section>

    <section className="overview-kpis">
      <OverviewKPI tone="green" icon={<UsersRound/>} label="就绪账号" value={`${readyAccounts.length}/${accounts.length}`} hint={accounts.length?'可参与自动任务':'等待添加账号'}/>
      <OverviewKPI tone="cyan" icon={<CalendarClock/>} label="启用任务" value={String(enabledJobs.length)} hint={nextJobs[0]?.next_run_at?`最近 ${dateTimeInZone(nextJobs[0].next_run_at,nextJobs[0].timezone)}`:'暂无排期'}/>
      <OverviewKPI tone="violet" icon={<Check/>} label="成功执行" value={String(succeeded)} hint={failed?`${failed} 次失败需处理`:'当前无失败记录'}/>
      <OverviewKPI tone="amber" icon={<BarChart3/>} label="本地里程" value={(totalDistanceM/1000).toFixed(1)} unit="km" hint={`${runs.length} 条本地运行记录`}/>
    </section>

    {customer&&plans.length>0&&<section className="panel portal-section"><div className="panel-heading"><div><h2>我的计划进度</h2></div></div><div className="record-progress-grid">{plans.map(plan=><div key={plan.id}><p className="portal-hint">{accounts.find(a=>a.id===plan.source_account_id)?.name}</p><PlanProgressCard plan={plan}/></div>)}</div></section>}
    {!customer&&<VersionManagementPanel user={user} notify={notify}/>}
    <section className="overview-primary-grid">
      <div className="panel overview-grid-card overview-schedule-panel">
        <div className="panel-heading"><h2>即将执行</h2><button className="text-action" onClick={()=>onNavigate('automation')}>全部任务 <ChevronRight/></button></div>
        <div className="overview-scroll-list overview-schedule-list" role="region" aria-label="即将执行列表" tabIndex={0}>
          {nextJobs.map(job=>{
            const owner=accounts.find(item=>item.id===job.account_id)
            const plan=plans.find(item=>item.id===job.plan_id)
            return <div className="overview-schedule-row" key={job.id}>
              <div className="schedule-date" title={job.next_run_at?dateTimeInZone(job.next_run_at,job.timezone):undefined}><strong>{job.next_run_at?dateDay(job.next_run_at,job.timezone):'—'}</strong><span>{job.next_run_at?dateClock(job.next_run_at,job.timezone):'—'}</span></div>
              <div className="overview-row-main">
                <strong title={owner?accountSelectorLabel(owner):undefined}>{owner?accountDisplayName(owner):'账号已移除'}</strong>
                <span title={plan?.name}>{plan?.name||'计划待同步'}</span>
                <small title={quotaLabel(job,plan)}>{quotaLabel(job,plan)}</small>
              </div>
              <div className="schedule-distance"><b>{automationDistanceLabel(job,plan)}</b><span>{scheduleLabel(job)}</span></div>
            </div>
          })}
          {!nextJobs.length&&<Empty text="暂无已启用的任务排期"/>}
        </div>
      </div>
      <div className="panel overview-grid-card overview-health-panel">
        <div className="panel-heading"><h2>服务状态</h2><span className={`status-pill ${systemReady===3?'ok':'warn'}`}>{systemReady}/3 核心服务在线</span></div>
        <div className="overview-scroll-list overview-health-list" role="region" aria-label="服务状态" tabIndex={0}>
          <HealthRow icon={<Database/>} label="PostgreSQL" detail="账号、任务与缓存持久化" live={!!config?.database_configured}/>
          <HealthRow icon={<RadioTower/>} label="地图服务" detail={`${metrics?.qps??config?.map_rate_limit.qps??0} 汇总 QPS · ${metrics?.key_count??config?.map_rate_limit.key_count??0} 个 Key · 当前 ${metrics?.inflight??0} 并发`} live={!!config?.map_api_configured}/>
          <HealthRow icon={<Server/>} label="目标协议" detail={config?`${config.client_protocol.app_version} · ${config.client_protocol.environment_name}`:'正在读取配置'} live={!!config?.protocol_configured}/>
          <HealthRow icon={<Bot/>} label="任务引擎" detail={`${config?.automation.worker_processes??0} workers × ${config?.automation.worker_concurrency??0} threads · 活跃 ${config?.automation.active??0}`} live={(config?.automation.workers??0)>0}/>
        </div>
      </div>
    </section>

    <section className="overview-secondary-grid">
      <div className="panel overview-grid-card overview-execution-panel">
        <div className="panel-heading"><h2>最近执行</h2><button className="text-action" onClick={()=>onNavigate('logs')}>执行日志 <ChevronRight/></button></div>
        <div className="overview-scroll-list overview-execution-list" role="region" aria-label="最近执行列表" tabIndex={0}>
          {recentExecutions.map(execution=>{
            const job=jobs.find(item=>item.id===execution.job_id)
            const plan=plans.find(item=>item.id===job?.plan_id)
            const owner=accounts.find(item=>item.id===execution.account_id)
            const result=execution.remote_run_id||execution.error||'等待执行结果'
            return <div className="overview-execution-row" key={execution.id}>
              <span className={`exec-status ${execution.status}`}>{executionStatusLabel(execution.status)}</span>
              <div className="overview-row-main">
                <div className="overview-execution-meta"><strong title={owner?accountSelectorLabel(owner):execution.account_id}>{owner?accountDisplayName(owner):execution.account_id}</strong><time dateTime={execution.started_at}>{dateTime(execution.started_at)}</time></div>
                <span title={plan?.name}>{plan?.name||'计划已移除'}</span>
                <code title={result}>{result}</code>
              </div>
            </div>
          })}
          {!recentExecutions.length&&<Empty text="暂无任务执行记录"/>}
        </div>
      </div>
      <div className="panel overview-grid-card overview-account-panel">
        <div className="panel-heading"><h2>账号状态</h2><button className="text-action" onClick={()=>onNavigate('accounts')}>账号管理 <ChevronRight/></button></div>
        <div className="overview-scroll-list overview-account-list" role="region" aria-label="账号状态列表" tabIndex={0}>
          {accounts.slice(0,6).map(account=>{
            const accountPlans=plans.filter(item=>item.source_account_id===account.id).length
            const accountAreas=areas.filter(item=>item.source_account_id===account.id).length
            const detail=`${accountStudentNumber(account)} · ${accountSchoolName(account)}`
            return <div className="overview-account-row" key={account.id}>
              <div className="avatar compact">{accountInitials(account)}</div>
              <div className="overview-row-main"><strong title={accountSelectorLabel(account)}>{accountDisplayName(account)}</strong><span title={detail}>{detail}</span></div>
              <div className="account-readiness"><b className={account.session_configured?'ready':''}>{account.session_configured?'已就绪':'待登录'}</b><span>{accountPlans} 计划 · {accountAreas} 区域</span></div>
            </div>
          })}
          {!accounts.length&&<Empty text="暂无账号，请先添加并登录"/>}
        </div>
      </div>
    </section>
  </div>
}

function RouteTest({accounts, plans, areas, runs, metrics, config, notify, onRefresh}: {
  accounts:Account[]; plans:Plan[]; areas:Area[]; runs:RunListItem[]; metrics:MapMetrics|null; config:Config|null
  notify:(message:string)=>void; onRefresh:()=>Promise<void>
}) {
  const [accountID, setAccountID] = useState('')
  const [planID, setPlanID] = useState('')
  const [areaID, setAreaID] = useState('')
  const [route, setRoute] = useState<Route|null>(null)
  const [run, setRun] = useState<Run|null>(null)
  const [targets, setTargets] = useState<TargetSelection|null>(null)
  const [selectedDistanceM, setSelectedDistanceM] = useState(0)
  const [busy, setBusy] = useState<'route'|'preview'|''>('')
  const account = accounts.find(item => item.id === accountID)
  const accountPlans = plans.filter(item => item.source_account_id === accountID)
  const accountAreas = areas.filter(item => item.source_account_id === accountID)
  const plan = accountPlans.find(item => item.id === planID)
  const area = accountAreas.find(item => item.id === areaID)

  useEffect(() => { if (!accountID && accounts[0]) setAccountID(accounts[0].id) }, [accounts, accountID])
  useEffect(() => {
    if (account?.default_plan_id && accountPlans.some(item => item.id === account.default_plan_id)) setPlanID(account.default_plan_id)
    else setPlanID(current => accountPlans.some(item => item.id === current) ? current : (accountPlans[0]?.id || ''))
    if (account?.default_area_id && accountAreas.some(item => item.id === account.default_area_id)) setAreaID(account.default_area_id)
    else setAreaID(current => accountAreas.some(item => item.id === current) ? current : (accountAreas[0]?.id || ''))
  }, [account, accounts, plans, areas])

  useEffect(() => {
    if (!plan) { setSelectedDistanceM(0); setTargets(null); setRoute(null); setRun(null); return }
    const distance=selectableDistanceRange(plan).max
    setSelectedDistanceM(distance)
    setTargets({policy:'manual_distance_100m', target_distance_m:distance, profile:plan.profile, window:middleWindow(plan)})
    setRoute(null); setRun(null)
  }, [planID, plan?.updated_at])

  const chooseDistance = (distance:number) => {
    if (!plan) return
    const range=selectableDistanceRange(plan)
    const selected=Math.max(range.min,Math.min(range.max,Math.round(distance/100)*100))
    setSelectedDistanceM(selected)
    setTargets(current => ({policy:'manual_distance_100m',target_distance_m:selected,profile:current?.profile || plan.profile,window:middleWindow(plan)}))
    setRoute(null); setRun(null)
  }

  const planRoute = async () => {
    if (!plan || !area) return notify('先选择跑步计划和跑步区域')
    setBusy('route')
    try {
      const seed = Date.now()
      const sampled = await api.samplePlan(plan.id, seed, selectedDistanceM)
      const value = await api.planRoute({start:area.center, target_distance_m:sampled.target_distance_m, max_distance_m:selectableDistanceRange(plan).max, candidate_count:area.candidate_count, boundary:area.boundary || [], seed})
      const delta=value.distance_m-sampled.target_distance_m
      setTargets(sampled); setRoute(value); setRun(null); notify(`路线已规划：目标 ${(sampled.target_distance_m/1000).toFixed(1)} km，实际偏差 +${Math.max(0,delta).toFixed(1)} m`)
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const preview = async () => {
    if (!route || !plan || !targets) return notify('先完成路线规划')
    setBusy('preview')
    try {
      const value = await api.simulate({route, profile:targets.profile, account_id:accountID, persist:false, seed:Date.now()})
      setRun(value); notify(`预检完成：${value.samples.length.toLocaleString()} 个轨迹点`); await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  return <div className="page-stack">
    <section className="hero-grid">
      <div className="panel mission-panel">
        <div className="panel-heading"><div><h2>路线规划</h2></div><span className={`status-pill ${account?.session_configured ? 'ok' : 'warn'}`}>{account?.session_configured ? '会话就绪' : '账号待登录'}</span></div>
        <div className="selector-grid">
          <Field label="运动账号"><SearchSelect value={accountID} onChange={event => setAccountID(event.target.value)}><option value="">选择账号</option>{accounts.map(item => <option key={item.id} value={item.id}>{accountSelectorLabel(item)}</option>)}</SearchSelect></Field>
          <Field label="账号跑步计划"><SearchSelect value={planID} onChange={event => setPlanID(event.target.value)}><option value="">选择账号内计划</option>{accountPlans.map(item => <option key={item.id} value={item.id}>{item.name} · {distanceRuleShort(item)}</option>)}</SearchSelect></Field>
          <Field label="账号校园区域"><SearchSelect value={areaID} onChange={event => setAreaID(event.target.value)}><option value="">选择账号内区域</option>{accountAreas.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</SearchSelect></Field>
        </div>
        {plan && <DistancePicker plan={plan} value={selectedDistanceM} onChange={chooseDistance}/>}
        <div className="mission-facts">
          <Fact label="目标距离" value={targets ? `${(targets.target_distance_m/1000).toFixed(2)} km` : '—'} hint={plan ? '100 米一档 · 默认最高距离' : ''}/>
          <Fact label="抽样配速" value={targets ? pace(targets.profile.target_pace_sec_per_km) : '—'} hint={plan ? paceRule(plan) : ''}/>
          <Fact label="抽样步频" value={targets ? `${targets.profile.cadence_spm.toFixed(0)} spm` : '—'} hint={plan ? cadenceRule(plan) : ''}/>
          <Fact label="联动步幅" value={targets ? `${(targets.profile.target_stride_m*100).toFixed(1)} cm` : '—'} hint={plan ? strideRule(plan) : ''}/>
        </div>
        <div className="action-row">
          <button className="button primary" disabled={!plan || !area || !!busy} onClick={() => void planRoute()}><RouteIcon size={17}/>{busy === 'route' ? '自动识别场地与规划中…' : '全自动场地规划'}</button>
          <button className="button cyan" disabled={!route || !!busy} onClick={() => void preview()}><ShieldCheck size={16}/>{busy === 'preview' ? '校验中…' : '生成预检'}</button>
        </div>
      </div>
      <div className="panel quota-panel">
        <div className="panel-heading"><div><h2>地图限流态势</h2></div><RadioTower size={20}/></div>
        <div className="quota-orbit"><div><strong>{metrics?.qps ?? config?.map_rate_limit.qps ?? 0}</strong><span>汇总 QPS</span></div><svg viewBox="0 0 120 120"><circle cx="60" cy="60" r="48"/><circle className="progress" cx="60" cy="60" r="48" style={{strokeDashoffset:302 - Math.min(1, (metrics?.waiting || 0) / Math.max(1, metrics?.burst || config?.map_rate_limit.burst || 1)) * 302}}/></svg></div>
        <div className="metric-lines"><MetricLine label="密钥池" value={`${metrics?.key_count ?? config?.map_rate_limit.key_count ?? 0} 个 · 独立 QPS`}/><MetricLine label="当前并发" value={`${metrics?.inflight ?? 0} · 不设上限`}/><MetricLine label="公平队列" value={`${metrics?.waiting ?? 0} 请求 · ${metrics?.fairness_keys ?? 0} 组`}/><MetricLine label="请求 / 重试" value={`${metrics?.requests ?? 0} / ${metrics?.retries ?? 0}`}/><MetricLine label="全局令牌发放" value={String(metrics?.dispatched ?? 0)}/><MetricLine label="L1 内存命中" value={String(metrics?.memory_cache_hits ?? 0)}/><MetricLine label="L2 数据库命中" value={String(metrics?.persistent_cache_hits ?? 0)}/><MetricLine label="持久缓存写入" value={String(metrics?.persistent_cache_writes ?? 0)}/><MetricLine label="限流等待" value={String(metrics?.rate_limited ?? 0)}/></div>
      </div>
    </section>
    <section className="panel map-panel">
        <div className="panel-heading"><div><h2>{route ? '路线与定位轨迹' : '等待路线'}</h2></div>{route && <div className="route-badges"><span>{route.source.startsWith('auto_field_graph') ? '场内自动路网' : '高德道路网'}</span><span>距离偏差 +{Math.max(0,route.distance_m-route.target_distance_m).toFixed(1)} m</span><span>重叠 {(route.quality.retrace_ratio*100).toFixed(1)}%</span><span>越界 {((route.quality.outside_area_ratio || 0)*100).toFixed(1)}%</span></div>}</div>
      <AmapRouteViewport route={route} samples={run?.samples || []} area={area}/>
      <div className="stat-strip">
        <Stat icon={<RouteIcon/>} label="融合距离" value={run ? `${(run.summary.distance_m/1000).toFixed(2)} km` : route ? `${(route.distance_m/1000).toFixed(2)} km` : '—'}/>
        <Stat icon={<TimerReset/>} label="运动时长" value={run ? duration(run.summary.duration_s) : '—'}/>
        <Stat icon={<Gauge/>} label="平均配速" value={run ? pace(run.summary.average_pace_sec_per_km) : '—'}/>
        <Stat icon={<Footprints/>} label="累计步数" value={run ? run.summary.total_steps.toLocaleString() : '—'}/>
        <Stat icon={<AreaChart/>} label="平均步频" value={run ? `${run.summary.average_cadence_spm.toFixed(0)} spm` : '—'}/>
      </div>
    </section>
    <section className="analytics-grid">
      <div className="panel chart-panel"><div className="panel-heading"><div><h2>速度与步频</h2></div>{run && <span className={`status-pill ${run.validation.valid ? 'ok' : 'warn'}`}>{run.validation.valid ? '一致性通过' : '需要复核'}</span>}</div><SignalChart samples={run?.samples || []}/></div>
      <div className="panel recent-panel"><div className="panel-heading"><div><h2>本地执行记录</h2></div><span className="count-badge">{runs.length}</span></div><div className="recent-list">{runs.slice(0, 6).map(item => <div className="recent-row" key={item.id}><div className={`run-dot ${item.valid ? 'valid' : ''}`}/><div><strong>{(item.distance_m/1000).toFixed(2)} km</strong><span>{dateTime(item.started_at)}</span></div><div><b>{pace(item.pace_sec_per_km)}</b><span>{duration(item.duration_s)}</span></div></div>)}{!runs.length && <Empty text="暂无执行记录"/>}</div></div>
    </section>
    {run && <section className="panel split-panel"><div className="panel-heading"><div><h2>公里分段</h2></div><span className="count-badge">{run.kilometre_splits.length}</span></div><div className="table-scroll"><table><thead><tr><th>分段</th><th>距离</th><th>用时</th><th>配速</th><th>步数</th><th>步频</th></tr></thead><tbody>{run.kilometre_splits.map(split => <tr key={split.index}><td><b>#{String(split.index).padStart(2,'0')}</b></td><td>{split.distance_m.toFixed(0)} m</td><td>{duration(split.duration_s)}</td><td>{pace(split.pace_sec_per_km)}</td><td>{split.steps}</td><td>{split.average_cadence_spm.toFixed(0)} spm</td></tr>)}</tbody></table></div></section>}
  </div>
}

function Accounts({accounts,config,user,notify,onRefresh}: {accounts:Account[];config:Config|null;user:PortalUser;notify:(message:string)=>void;onRefresh:()=>Promise<void>}) {
  const emptyForm = {login_name:'', password:'', note:'', fence_index:0, device_id:'', model_name:'Redmi|23013RK75C', system_version:'14'}
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState(emptyForm)
  const [autoTask,setAutoTask]=useState(false)
  const [loginMode, setLoginMode] = useState<'password'|'sms'>('password')
  const [edit, setEdit] = useState<(Account & {password?:string})|null>(null)
  const [busy, setBusy] = useState('')
  const [sms, setSMS] = useState<{kind:'existing'|'onboarding'; account:Account; password?:string; ticket:string; code:string; score:number;autoTask?:boolean}|null>(null)
  const [reauth, setReauth] = useState<{account:Account; mode:'password'|'sms'; password:string; error:string}|null>(null)
  const [records, setRecords] = useState<{account:Account; data:RemoteRecordData; plans:Plan[]}|null>(null)
  const [faceSettings, setFaceSettings] = useState<{account:Account; profile:FaceProfile; version:string; detection?:FaceDetectionResult; detectionError?:string}|null>(null)
  const [owners,setOwners]=useState<PortalUser[]>([]),[ownerID,setOwnerID]=useState('')
  const admin=user.role!=='customer'
  useEffect(()=>{
    if(!admin)return
    let active=true
    void portalRequest<PortalUser[]>('users').then(value=>{if(active)setOwners(value)}).catch(e=>{if(active)notify(e.message)})
    return()=>{active=false}
  },[admin,notify])
  const filteredAccounts=accounts.filter(account=>!admin||!ownerID||(ownerID==='__unassigned__'?!account.owner_user_id:account.owner_user_id===ownerID))
  const accountPages=usePagination(filteredAccounts,ownerID)
  const ownerName=(id?:string)=>owners.find(owner=>owner.id===id)?.email||(id?`用户 ${id}`:'未分配用户')

  const finishOnboarding=async(account:Account,createTask:boolean)=>{
    let message='登录成功，账号已添加'
    if(createTask){
      try{
        const [saved,localPlans,localAreas,existingJobs,mapConfig]=await Promise.all([api.account(account.id),api.plans(),api.areas(),api.jobs(),api.config()])
        const plan=localPlans.find(p=>p.id===saved.default_plan_id)||localPlans.find(p=>p.source_account_id===saved.id)
        const area=localAreas.find(a=>a.id===saved.default_area_id)||localAreas.find(a=>a.source_account_id===saved.id)
        if(existingJobs.some(job=>job.account_id===saved.id))message+='；已有任务，未重复创建'
        else if(!mapConfig.map_api_configured)message+='；尚未配置地图 Key，请先在路线管理绘制路线，再创建自定义路线任务'
        else if(!plan||!area)message+='；尚无可用计划或区域，请同步后手动创建任务'
        else{
          await api.createJob({account_id:saved.id,plan_id:plan.id,area_id:area.id,target_distance_m:automationDefaultDistance(plan),daily_run_limit:defaultDailyLimit(plan),weekly_run_limit:defaultWeeklyLimit(plan),daily_distance_limit_m:defaultDailyDistanceLimit(plan),weekly_distance_limit_m:defaultWeeklyDistanceLimit(plan),schedule_mode:plan.rules?.time_windows?.length?'plan_random':'manual',manual_time:plan.rules?.time_windows?.length?'':'06:30',day_scope:plan.rules?.time_windows?.length?'':'every_day',cron_expr:'',timezone:'Asia/Shanghai',enabled:false})
          message+='；自动任务已创建（暂停），请检查后启用'
        }
      }catch(e){message+=`；任务创建未完成：${(e as Error).message}`}
    }
    setOpen(false);setForm(emptyForm);setAutoTask(false);notify(message);await onRefresh()
  }

  const add = async (event:FormEvent) => {
    event.preventDefault(); setBusy('add')
    try {
      const draft={login_name:form.login_name, name:form.login_name, note:form.note, enabled:true, fence_index:form.fence_index, device_id:form.device_id.trim().toLowerCase(), model_name:form.model_name, system_version:form.system_version}
      const result=loginMode==='password'
        ? await api.createAccount({...draft,password:form.password})
        : await api.requestOnboardSMS(draft)
      if (result.verification_required && result.challenge && result.draft) {
        setSMS({kind:'onboarding',account:result.draft,password:loginMode==='password'?form.password:undefined,ticket:result.challenge.ticket,code:'',score:result.challenge.solver_score,autoTask})
        setOpen(false)
        notify(loginMode==='password'?'该账号需要短信验证，请输入验证码':'滑块已自动通过，请输入短信验证码')
      } else if (result.account) {
        await finishOnboarding(result.account,autoTask)
      }
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const save = async (event:FormEvent) => {
    event.preventDefault(); if (!edit) return; setBusy(edit.id)
    try { await api.updateAccount(edit); setEdit(null); notify('账号配置已更新'); await onRefresh() }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const passwordLogin = async (account:Account) => {
    setBusy(account.id + ':login')
    try {
      const result=await api.loginAccount(account.id)
      if (result.verification_required && result.challenge) {
        setSMS({kind:'existing',account:result.account || account,ticket:result.challenge.ticket,code:'',score:result.challenge.solver_score})
        notify('该账号需要短信验证，请输入验证码')
      } else {
        notify('登录成功'); await onRefresh()
      }
    }
    catch (error) {
      const message=(error as Error).message
      setReauth({account,mode:'password',password:'',error:`已保存的密码重新登录失败：${message}`})
      notify(message); await onRefresh()
    }
    finally { setBusy('') }
  }

  const beginSMS = async (account:Account) => {
    setBusy(account.id + ':sms')
    try {
      const challenge = await api.requestSMS(account.id)
      setSMS({kind:'existing', account, ticket:challenge.ticket, code:'', score:challenge.solver_score})
      notify('滑块已自动通过，请输入短信验证码')
    } catch (error) { notify((error as Error).message); await onRefresh() }
    finally { setBusy('') }
  }

  const relogin = async (account:Account) => {
    if (account.password_configured) {
      await passwordLogin(account)
      return
    }
    await beginSMS(account)
  }

  const submitReauth = async (event:FormEvent) => {
    event.preventDefault(); if (!reauth) return
    if (reauth.mode === 'sms') {
      const account=reauth.account
      setReauth(null)
      await beginSMS(account)
      return
    }
    setBusy(reauth.account.id + ':reauth')
    try {
      const updated=await api.updateAccount({...reauth.account,password:reauth.password})
      const result=await api.loginAccount(updated.id)
      if (result.verification_required && result.challenge) {
        setSMS({kind:'existing',account:result.account||updated,ticket:result.challenge.ticket,code:'',score:result.challenge.solver_score})
        notify('新密码需要短信验证，请输入验证码')
      } else {
        notify('重新登录成功')
      }
      setReauth(null)
      await onRefresh()
    } catch (error) {
      const message=(error as Error).message
      setReauth(current=>current?{...current,error:message}:current)
      notify(message); await onRefresh()
    } finally { setBusy('') }
  }

  const verifySMS = async (event:FormEvent) => {
    event.preventDefault(); if (!sms) return; setBusy('verify-sms')
    try {
      if (sms.kind==='onboarding') {
        const account=await api.verifyOnboardSMS(sms.account,sms.code,sms.ticket,sms.password)
        await finishOnboarding(account,!!sms.autoTask)
      } else {
        await api.verifySMS(sms.account.id, sms.code, sms.ticket)
        notify('短信登录成功');await onRefresh()
      }
      setSMS(null)
    }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const closeSMS = () => {
    const onboarding=sms?.kind==='onboarding'
    setSMS(null)
    if(onboarding)setOpen(true)
  }

  const loadRecords = async (account:Account) => {
    setBusy(account.id + ':records')
    try {
      const resources = await api.syncAccount(account.id)
      const data = await api.accountRecords(account.id)
      const refreshed = await api.account(account.id).catch(() => account)
      setRecords({account:refreshed, data, plans:resources.plans})
      await onRefresh()
    } catch (error) { notify((error as Error).message); await onRefresh() }
    finally { setBusy('') }
  }

  const syncResources = async (account:Account) => {
    setBusy(account.id + ':sync')
    try {
      const value = await api.syncAccount(account.id)
      notify(`已从账号读取 ${value.plans.length} 个计划、${value.areas.length} 个区域`)
      await onRefresh()
    } catch (error) { notify((error as Error).message); await onRefresh() }
    finally { setBusy('') }
  }

  const toggle = async (account:Account) => {
    setBusy(account.id + ':toggle')
    try { await api.updateAccount({...account, enabled:!account.enabled}); await onRefresh() }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const openFaceSettings = async (account:Account) => {
    setFaceSettings({account, profile:faceProfileFromAccount(account), version:String(Date.now())})
    if (!account.session_configured) return
    setBusy(account.id + ':face-sync')
    try {
      const profile=await api.syncFace(account.id)
      setFaceSettings({account,profile,version:String(Date.now())})
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const refreshFace = async () => {
    if (!faceSettings) return
    setBusy(faceSettings.account.id + ':face-sync')
    try {
      const profile=await api.syncFace(faceSettings.account.id)
      setFaceSettings({...faceSettings,profile,version:String(Date.now()),detection:undefined,detectionError:undefined})
      notify('已重新读取账号绑定人脸底图')
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const uploadFace = async (file?:File) => {
    if (!faceSettings || !file) return
    setBusy(faceSettings.account.id + ':face-upload')
    try {
      const profile=await api.setFace(faceSettings.account.id,file)
      setFaceSettings({...faceSettings,profile,version:String(Date.now()),detection:undefined,detectionError:undefined})
      notify('该账号的自定义识别人脸已加密保存')
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const clearFace = async () => {
    if (!faceSettings) return
    setBusy(faceSettings.account.id + ':face-clear')
    try {
      const profile=await api.clearFace(faceSettings.account.id)
      setFaceSettings({...faceSettings,profile,version:String(Date.now()),detection:undefined,detectionError:undefined})
      notify(profile.base_configured?'已清除自定义人脸，后续自动使用绑定底图':'已清除自定义人脸')
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }

  const checkFace = async () => {
    if (!faceSettings) return
    setBusy(faceSettings.account.id + ':face-check')
    try {
      const detection=await api.checkFace(faceSettings.account.id,'effective')
      setFaceSettings(current=>current ? {...current,detection,detectionError:undefined} : current)
      notify(detection.passed?`主动人脸检测通过，相似度 ${detection.confidence.toFixed(2)}`:`主动人脸检测未通过，相似度 ${detection.confidence.toFixed(2)}`)
    } catch (error) {
      const message=(error as Error).message
      setFaceSettings(current=>current ? {...current,detection:undefined,detectionError:message} : current)
      notify(message)
    }
    finally { setBusy('') }
  }

  return <div className="page-stack">
    <section className="page-toolbar"><button className="button primary" onClick={() => setOpen(true)}><Plus size={17}/>添加账号</button></section>
    {admin&&<section className="panel account-owner-filter"><Field label="所属用户"><SearchSelect value={ownerID} onChange={e=>setOwnerID(e.target.value)}><option value="">全部用户</option>{owners.map(owner=><option key={owner.id} value={owner.id}>{owner.email} · {({superadmin:'超级管理员',admin:'管理员',customer:'客户'} as const)[owner.role]}</option>)}{accounts.some(account=>!account.owner_user_id)&&<option value="__unassigned__">未分配用户</option>}</SearchSelect></Field><div><strong>{filteredAccounts.length}<span> / {accounts.length} 个账号</span></strong></div>{ownerID&&<button className="button subtle" onClick={()=>setOwnerID('')}>清除筛选</button>}</section>}
    <section className="panel account-list-panel">
      <ListViewport pagination={accountPages} className="account-list">{!!filteredAccounts.length && <div className="account-list-head"><span>绑定身份</span><span>学号</span><span>人脸</span><span>操作</span></div>}{accountPages.items.map(account => <article className={`account-list-row ${account.enabled ? '' : 'disabled'}`} key={account.id}>
        <div className="account-identity"><div className="avatar">{accountInitials(account)}</div><div><strong title={accountSelectorLabel(account)}>{accountDisplayName(account)}</strong><span title={accountSchoolName(account)}>{accountSchoolName(account)} · {authLabel(account.auth_status)}</span><small className="account-mobile-login">账号 {maskLogin(account.login_name)}</small>{admin&&<small className="account-owner-name" title={ownerName(account.owner_user_id)}>{ownerName(account.owner_user_id)}</small>}{account.auth_status==='expired'&&<small className="account-auth-expired">{account.auth_message||'登录状态已失效'}</small>}</div></div>
        <div className="account-student"><span>绑定学号</span><b>{accountStudentNumber(account)}</b></div>
        <button className="account-face-state" title="查看和设置账号人脸" onClick={() => void openFaceSettings(account)}>{account.face_base_configured ? <img src={api.faceImageURL(account.id,'base',account.face_synced_at || account.updated_at)} alt="绑定人脸底图"/> : <span className="face-placeholder"><ScanFace/></span>}<span><b>{account.face_custom_configured?'自定义人脸':account.face_base_configured?'自动使用底图':'待同步'}</b><small>{account.face_auth?'计划要求识别':account.face_enabled?'已绑定':'未启用'}</small></span></button>
        <div className="account-row-actions">{(!account.session_configured||account.auth_status!=='authenticated')&&<button className="button primary" disabled={!!busy} onClick={() => void relogin(account)}><KeyRound size={14}/>{busy === account.id + ':login'||busy === account.id + ':sms' ? '登录中…' : account.auth_status==='expired'?'重新登录':'登录账号'}</button>}<button className="button subtle" disabled={!account.session_configured||account.auth_status!=='authenticated'||!!busy} onClick={()=>void loadRecords(account)}><ListRestart size={14}/>跑步记录</button><button className="mini-button" title="人脸设置" aria-label="人脸设置" onClick={()=>void openFaceSettings(account)}><ScanFace size={16}/><span className="mobile-action-label">人脸设置</span></button><SearchSelect className="account-more-select" aria-label="更多账号操作" defaultValue="" disabled={!!busy} onChange={event=>{const value=event.target.value;event.currentTarget.value='';if(value==='sync')void syncResources(account);if(value==='edit')setEdit({...account,password:''});if(value==='toggle')void toggle(account)}}><option value="" disabled>更多操作</option><option value="sync" disabled={!account.session_configured||account.auth_status!=='authenticated'}>读取账号资料</option><option value="edit">设备与登录配置</option><option value="toggle">{account.enabled?'停用账号':'启用账号'}</option></SearchSelect></div>
      </article>)}{!filteredAccounts.length && <div className="empty-large"><UsersRound/><h3>{ownerID?'该用户暂无绑定账号':'还没有账号'}</h3><p>{ownerID?'切换用户或清除筛选。':'添加账号后查看计划、记录与任务。'}</p></div>}</ListViewport>
    </section>

    {open && <Modal title="添加账号" className="account-modal" onClose={() => setOpen(false)}>
      <form className="form-stack account-form" onSubmit={add}>
        <p className="portal-hint">登录成功后自动同步账号资料。</p>
        <div className="login-mode-grid"><button type="button" className={loginMode==='password'?'selected':''} onClick={()=>setLoginMode('password')}><KeyRound/><strong>密码登录</strong></button><button type="button" className={loginMode==='sms'?'selected':''} onClick={()=>setLoginMode('sms')}><Send/><strong>验证码登录</strong></button></div>
        <div className={`account-login-fields ${loginMode==='sms'?'single':''}`}>
          <Field label="登录账号 / 手机号"><input required autoComplete="username" value={form.login_name} onChange={event => setForm({...form, login_name:event.target.value})}/></Field>
          {loginMode==='password' && <Field label="账号密码"><input required type="password" autoComplete="new-password" value={form.password} onChange={event => setForm({...form, password:event.target.value})} placeholder="请输入账号密码"/></Field>}
        </div>
        <details className="account-device-section advanced-options" onInvalidCapture={event=>{event.currentTarget.open=true}}><summary><CloudCog size={17}/><span>高级选项 · 设备身份</span><small>建议保持默认</small></summary>
          <p className="portal-hint">Android ID 留空时自动生成并保存。</p>
          <div className="account-device-grid">
            <Field label="Android ID"><input value={form.device_id} maxLength={16} pattern="[0-9a-fA-F]{16}" autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={event => setForm({...form, device_id:event.target.value.replace(/[^0-9a-fA-F]/g,'').slice(0,16)})} placeholder="留空自动生成"/></Field>
            <Field label="设备机型"><input value={form.model_name} onChange={event => setForm({...form, model_name:event.target.value})}/></Field>
            <Field label="Android 版本"><input value={form.system_version} onChange={event => setForm({...form, system_version:event.target.value})}/></Field>
          </div>
        </details>
        <label className="portal-toggle"><input type="checkbox" checked={autoTask&&!!config?.map_api_configured} disabled={!config?.map_api_configured} onChange={event=>setAutoTask(event.target.checked)}/><span>自动添加任务（默认暂停，确认后启用）</span></label>{!config?.map_api_configured&&<p className="portal-hint">尚未配置地图 Key，请添加账号后先绘制路线，再创建任务。</p>}
        <Field label="备注"><input value={form.note} onChange={event => setForm({...form, note:event.target.value})}/></Field>
        <button className="button primary wide" disabled={busy === 'add'} type="submit">{busy === 'add' ? (loginMode==='password'?'登录验证中…':'正在发送验证码…') : (loginMode==='password'?'登录并添加账号':'发送验证码')}</button>
      </form>
    </Modal>}

    {edit && <Modal title="账号配置" className="account-modal" onClose={() => setEdit(null)}>
      <form className="form-stack account-form" onSubmit={save}>
        <div className="protocol-note"><UsersRound size={17}/><div><strong>{accountDisplayName(edit)} · {accountStudentNumber(edit)}</strong><span>姓名、学号与学校来自已绑定资料。</span></div></div>
        <div className="account-login-fields"><Field label="登录账号"><input required value={edit.login_name} onChange={event => setEdit({...edit, login_name:event.target.value})}/></Field><Field label="更新密码"><input type="password" value={edit.password || ''} onChange={event => setEdit({...edit, password:event.target.value})} placeholder="留空则保持原密码"/></Field></div>
        <section className="account-device-section">
          <div className="account-device-head"><span><CloudCog/></span><div><strong>Android 设备身份</strong><small>修改后用于后续登录与请求。</small></div></div>
          <div className="account-device-grid">
            <Field label="Android ID"><input required value={edit.device_id} maxLength={16} pattern="[0-9a-fA-F]{16}" autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={event => setEdit({...edit, device_id:event.target.value.replace(/[^0-9a-fA-F]/g,'').slice(0,16).toLowerCase()})}/></Field>
            <Field label="设备机型"><input value={edit.model_name} onChange={event => setEdit({...edit, model_name:event.target.value})}/></Field>
            <Field label="Android 版本"><input value={edit.system_version} onChange={event => setEdit({...edit, system_version:event.target.value})}/></Field>
          </div>
        </section>
        <Field label="备注"><input value={edit.note || ''} onChange={event => setEdit({...edit, note:event.target.value})}/></Field>
        <button className="button primary wide" disabled={busy === edit.id} type="submit">保存配置</button>
      </form>
    </Modal>}

    {reauth && <Modal title={`${accountDisplayName(reauth.account)} · 重新登录`} className="account-modal" onClose={()=>setReauth(null)}>
      <form className="form-stack account-form" onSubmit={submitReauth}>
        <div className="protocol-note"><LockKeyhole/><div><strong>已保存的登录态不再有效</strong><span>{reauth.error||'请选择一种方式恢复该账号的登录态。'}</span></div></div>
        <div className="login-mode-grid"><button type="button" className={reauth.mode==='password'?'selected':''} onClick={()=>setReauth({...reauth,mode:'password',error:''})}><KeyRound/><strong>账号密码</strong><span>重新输入密码</span></button><button type="button" className={reauth.mode==='sms'?'selected':''} onClick={()=>setReauth({...reauth,mode:'sms',error:''})}><Send/><strong>短信登录</strong><span>自动滑块后输入验证码</span></button></div>
        {reauth.mode==='password'?<Field label="新的账号密码"><input required autoFocus type="password" autoComplete="current-password" value={reauth.password} onChange={event=>setReauth({...reauth,password:event.target.value,error:''})} placeholder="请重新输入密码"/></Field>:<div className="reauth-sms-note"><Send/><span>将为 {maskLogin(reauth.account.login_name)} 自动完成滑块并发送短信验证码。</span></div>}
        <button className="button primary wide" disabled={busy===reauth.account.id+':reauth'} type="submit">{busy===reauth.account.id+':reauth'?'登录中…':reauth.mode==='password'?'更新密码并登录':'发送短信验证码'}</button>
      </form>
    </Modal>}

    {sms && <Modal title="输入短信验证码" onClose={closeSMS}><form className="form-stack" onSubmit={verifySMS}><div className="protocol-note"><Check size={17}/><div><strong>{sms.kind==='onboarding'?'验证通过后才会保存账号':'完成本次登录验证'}</strong><span>滑块已自动完成，求解置信度 {sms.score.toFixed(3)}，短信已发送至 {maskLogin(sms.account.login_name)}</span></div></div><Field label="短信验证码"><input required autoFocus inputMode="numeric" maxLength={8} value={sms.code} onChange={event => setSMS({...sms, code:event.target.value})} placeholder="请输入收到的验证码"/></Field><button className="button primary wide" disabled={busy === 'verify-sms'} type="submit">{busy === 'verify-sms' ? '验证中…' : '验证并完成登录'}</button></form></Modal>}

    {faceSettings && <Modal title={`${accountDisplayName(faceSettings.account)} · 人脸设置`} onClose={() => setFaceSettings(null)} wide><div className="face-settings"><div className="face-explain"><ScanFace/><div><strong>{faceSettings.profile.custom_configured?'跑步时使用自定义人脸':'跑步时自动使用账号绑定底图'}</strong><span>未设置自定义照片时使用绑定底图。</span></div></div><div className="face-preview-grid"><FacePreview title="账号绑定底图" description="由 getUserFaceImg 自动读取并加密缓存" available={faceSettings.profile.base_configured} src={api.faceImageURL(faceSettings.account.id,'base',faceSettings.version)}/><FacePreview title="当前识别人脸" description={faceSettings.profile.custom_configured?'使用本地自定义图片':'未设置时自动回落到绑定底图'} available={faceSettings.profile.effective_source!=='none'} src={api.faceImageURL(faceSettings.account.id,'effective',faceSettings.version)}/></div><div className="face-facts"><div><span>识别开关</span><b>{faceSettings.profile.face_auth?'本计划要求':faceSettings.profile.face_enabled?'账号已启用':'当前未要求'}</b></div><div><span>比对阈值</span><b>{faceSettings.profile.similarity_value || 50}</b></div><div><span>识别模型</span><b>{faceSettings.profile.face_model_type===2?'LOCAL MNN':'APP ALIYUN'}</b></div><div><span>活体/人体</span><b>{faceSettings.profile.body_enabled?`启用 · ${faceSettings.profile.body_rate}`:'未启用'}</b></div></div>{faceSettings.profile.face_model_type===2 && <div className="face-api-notice"><RadioTower/><div><strong>当前使用端内同源模型包</strong><span>本地模型用于照片比对。</span></div></div>}{faceSettings.detection && <div className={`face-detection-result ${faceSettings.detection.passed?'passed':'failed'}`}><div className="face-detection-icon">{faceSettings.detection.passed?<ShieldCheck/>:<X/>}</div><div><span>主动人脸检测</span><strong>{faceSettings.detection.passed?'识别通过':'识别未通过'}</strong><small>{faceSettings.detection.provider} · {faceSettings.detection.source==='custom'?'自定义人脸':'绑定底图'} · {faceSettings.detection.elapsed_ms} ms</small></div><div><span>相似度</span><strong>{faceSettings.detection.confidence.toFixed(2)}</strong><small>阈值 {faceSettings.detection.threshold}</small></div><div><span>Request ID</span><code>{faceSettings.detection.request_id || '—'}</code><small>{dateTime(faceSettings.detection.checked_at)}</small></div></div>}{faceSettings.detectionError && <div className="face-api-error"><X/><div><strong>人脸检测未执行</strong><span>{faceSettings.detectionError}</span></div></div>}<div className="face-actions"><button className="button primary" disabled={!faceSettings.account.session_configured || !faceSettings.profile.base_configured || faceSettings.profile.effective_source==='none' || !!busy} onClick={() => void checkFace()}><ScanFace/>{busy===faceSettings.account.id+':face-check'?'检测中…':'主动检测'}</button><button className="button subtle" disabled={!faceSettings.account.session_configured || !!busy} onClick={() => void refreshFace()}><RefreshCw className={busy===faceSettings.account.id+':face-sync'?'spin':''}/>{busy===faceSettings.account.id+':face-sync'?'读取中…':'重新读取绑定底图'}</button><label className={`button cyan ${busy?'disabled':''}`}><ImagePlus/>选择自定义人脸<input type="file" accept="image/jpeg,image/png" disabled={!!busy} onChange={event => { const file=event.target.files?.[0]; event.currentTarget.value=''; void uploadFace(file) }}/></label>{faceSettings.profile.custom_configured && <button className="button danger" disabled={!!busy} onClick={() => void clearFace()}><Trash2/>清除自定义并使用底图</button>}</div><p className="face-storage-note">照片加密保存在本机。</p></div></Modal>}

    {records && <RecordsModal account={records.account} data={records.data} plans={records.plans} notify={notify} onClose={() => setRecords(null)}/>}
  </div>
}

function RecordsModal({account, data, plans, notify, onClose}: {account:Account; data:RemoteRecordData; plans:Plan[]; notify:(message:string)=>void; onClose:()=>void}) {
  const [pageData,setPageData]=useState(data),[recordPage,setRecordPage]=useState(1),[recordPageSize,setRecordPageSize]=useState(20),[pageLoading,setPageLoading]=useState(false)
  const items = extractRecords(pageData)
  const [selected, setSelected] = useState<Record<string,unknown>|null>(null)
  const [detail, setDetail] = useState<RemoteRecordData>(null)
  const [inspection, setInspection] = useState<RemoteRecordInspection|null>(null)
  const [detailBusy, setDetailBusy] = useState(false)
  const [detailError, setDetailError] = useState('')
  const [reasonByRecord, setReasonByRecord] = useState<Record<string,{loading:boolean; reasons:string[]}>>({})
  const changeRecordPage=async(next:number,size=recordPageSize)=>{setPageLoading(true);try{setPageData(await api.accountRecords(account.id,next,size));setRecordPage(next);setRecordPageSize(size);setSelected(null);setDetail(null);setInspection(null)}catch(e){notify((e as Error).message)}finally{setPageLoading(false)}}
  const metrics = remoteRecordMetrics(detail, selected)
  const route = remoteRecordRoute(detail, selected)
  const detailObject = asRecord(detail)
  const selectedStatus = recordStatusInfo({...selected || {}, ...detailObject || {}})
  useEffect(() => {
    let cancelled=false
    const invalidItems=extractRecords(pageData).filter(recordNeedsInvalidReason)
    const initial:Record<string,{loading:boolean; reasons:string[]}>={}
    for(const item of invalidItems){const code=recordCode(item);if(code)initial[code]={loading:true,reasons:[]}}
    setReasonByRecord(initial)
    void (async() => {
      for(const item of invalidItems){
        if(cancelled)return
        const code=recordCode(item)
        if(!code)continue
        try{
          const value=await api.accountRecord(account.id,code,String(item.semesterCode || item.semester_code || ''))
          const reasons=remoteRecordReasons(asRecord(value) || {})
          if(!cancelled)setReasonByRecord(current=>({...current,[code]:{loading:false,reasons:reasons.length?reasons:['服务端未返回具体无效原因']}}))
        }catch{
          if(!cancelled)setReasonByRecord(current=>({...current,[code]:{loading:false,reasons:['无效原因读取失败，点击记录可重试']}}))
        }
        await new Promise(resolve=>setTimeout(resolve,350))
      }
    })()
    return () => {cancelled=true}
  },[account.id,pageData])
  const loadDetail = async (item:Record<string,unknown>) => {
    const code=recordCode(item)
    if (!code) return
    setSelected(item); setDetail(null); setInspection(null); setDetailError(''); setDetailBusy(true)
    try { const result=await api.accountRecordInspection(account.id, code, String(item.semesterCode || item.semester_code || '')); setDetail(result.record); setInspection(result.inspection); if(recordNeedsInvalidReason(item)){const reasons=remoteRecordReasons(asRecord(result.record) || {});setReasonByRecord(current=>({...current,[code]:{loading:false,reasons:reasons.length?reasons:['服务端未返回具体无效原因']}}))} }
    catch (error) { const message=(error as Error).message; setDetailError(message); notify(message) }
    finally { setDetailBusy(false) }
  }
  return <Modal title={`${accountDisplayName(account)} · 跑步记录`} onClose={onClose} wide>
    <section className="record-plan-progress"><div className="record-progress-head"><div><h3>跑步计划完成进度</h3></div><span>{plans.length} 个计划</span></div><div className="record-progress-grid">{plans.map(plan=><PlanProgressCard key={plan.id} plan={plan}/>)}{!plans.length&&<Empty text="该账号尚未返回跑步计划"/>}</div></section>
    <div className="records-workspace">
      <div className="record-list-pane"><div className="table-scroll remote-records list-viewport"><table className="mobile-card-table" aria-label="跑步记录"><thead><tr><th>时间</th><th>距离</th><th>时长</th><th>状态与原因</th><th/></tr></thead><tbody>{items.map((item, index) => { const code=recordCode(item) || `#${index + 1}`; const active=recordCode(selected || {})===code; const status=recordStatusInfo(item); const reasonState=reasonByRecord[code]; return <tr key={code} role="button" tabIndex={0} aria-selected={active} className={active ? 'selected' : ''} onClick={() => void loadDetail(item)} onKeyDown={event => { if(event.key==='Enter' || event.key===' '){event.preventDefault();void loadDetail(item)} }}><td data-label="时间" className="mobile-wide-cell"><b>{recordTime(item)}</b><small>{shortRecordCode(code)}</small></td><td data-label="距离">{recordDistance(item)}</td><td data-label="时长">{recordDuration(item)}</td><td data-label="状态与原因" className="record-status-cell mobile-wide-cell"><span className={`record-status-text ${status.tone}`} title={status.description}>{status.label}</span>{recordNeedsInvalidReason(item) && <small className={reasonState?.loading?'loading':''}>{reasonState?.loading?'正在读取无效原因…':reasonState?.reasons.join('；') || '等待读取无效原因'}</small>}</td><td><ChevronRight size={15}/></td></tr>})}</tbody></table>{!items.length && <Empty text="服务端暂无阳光跑记录"/>}</div><footer className="list-pagination"><span>第 {recordPage} 页 · 本页 {items.length} 条</span><SearchSelect aria-label="跑步记录每页条数" value={recordPageSize} disabled={pageLoading} onChange={event=>void changeRecordPage(1,Number(event.target.value))}>{[10,20,50].map(size=><option value={size} key={size}>{size} 条 / 页</option>)}</SearchSelect><div><button className="button subtle" disabled={pageLoading||recordPage===1} onClick={()=>void changeRecordPage(recordPage-1)}>上一页</button><button className="button subtle" disabled={pageLoading||items.length<recordPageSize} onClick={()=>void changeRecordPage(recordPage+1)}>下一页</button></div></footer></div>
      <div className="record-detail-pane">
        {!selected && <div className="record-detail-empty"><RouteIcon/><strong>选择一条跑步记录</strong><span>查看步幅、步频、步数与路线。</span></div>}
        {selected && <>
          <div className="record-detail-head"><div><h3>{recordTime(selected)}</h3><code>{shortRecordCode(recordCode(selected))}</code></div><span className={`status-pill ${selectedStatus.tone}`}>{selectedStatus.label}</span></div>
          {detailBusy && <div className="record-detail-loading"><RefreshCw className="spin"/><span>正在读取详细数据与轨迹…</span></div>}
          {detailError && <div className="record-detail-error">{detailError}</div>}
          {!detailBusy && detail && <>
            <RecordServerStatus detail={detail} fallback={selected}/>
            <div className="record-metric-grid">
              <RecordMetric icon={<RouteIcon/>} label="有效距离" value={metrics.distance}/>
              <RecordMetric icon={<TimerReset/>} label="运动时长" value={metrics.duration}/>
              <RecordMetric icon={<Gauge/>} label="平均配速" value={metrics.pace}/>
              <RecordMetric icon={<Footprints/>} label="总步数" value={metrics.steps}/>
              <RecordMetric icon={<AreaChart/>} label="平均步频" value={metrics.cadence}/>
              <RecordMetric icon={<Ruler/>} label="平均步幅" value={metrics.stride}/>
            </div>
            {inspection && <RecordInspectionPanel inspection={inspection}/>}
            <div className="record-map-heading"><div><h3>实际跑步路线</h3></div><span>{route?.points.length.toLocaleString() || 0} 个定位点</span></div>
            <div className="record-route-map"><AmapRouteViewport route={route} samples={[]} variant="record"/></div>
            <RecordSegments detail={detail}/>
          </>}
        </>}
      </div>
    </div>
  </Modal>
}

function PlanProgressCard({plan}:{plan:Plan}) {
  const rules=plan.rules || ({} as Plan['rules'])
  const countBased=isCountBasedPlan(plan)
  const completed=countBased?Number(rules.done_target_times||0):Number(rules.done_distance_m||0)
  const target=countBased?Number(rules.total_times||0):Number(rules.total_distance_m||0)
  const upstream=Number(rules.completion_rate||0)
  const ratio=Math.max(0,Math.min(1,upstream>1?upstream/100:upstream>0?upstream:target>0?completed/target:0))
  const completedLabel=countBased?`${Math.round(completed)} 次`:`${(completed/1000).toFixed(2)} km`
  const targetLabel=countBased?(target?`${Math.round(target)} 次`:'未返回'):(target?`${(target/1000).toFixed(2)} km`:'未返回')
  const weekRule=rules.week_max_times?`${rules.week_min_times||0}–${rules.week_max_times} 次/周`:'周次数未限制'
  const distanceRule=[rules.day_max_distance_m?`${(rules.day_max_distance_m/1000).toFixed(1)} km/日`:'日公里数未限制',rules.week_max_distance_m?`${(rules.week_max_distance_m/1000).toFixed(1)} km/周`:'周公里数未限制'].join(' · ')
  return <article className="record-progress-card"><div className="record-progress-title"><div><strong>{plan.name}</strong><span>{countBased?'按次数考核':'按公里数考核'} · {planDateRange(plan)}</span></div><b>{(ratio*100).toFixed(1)}%</b></div><div className="record-progress-track"><i style={{width:`${ratio*100}%`}}/></div><div className="record-progress-facts"><span>已完成 <b>{completedLabel}</b></span><span>目标 <b>{targetLabel}</b></span><span>次数限额 <b>{rules.day_max_times?`${rules.day_max_times} 次/日`:'日次数未限制'} · {weekRule}</b></span><span>公里数限额 <b>{distanceRule}</b></span>{Number(rules.morning_times||0)>0&&<span>早操 <b>{rules.morning_times} 次</b></span>}</div></article>
}

function RecordServerStatus({detail, fallback}: {detail:RemoteRecordData; fallback:Record<string,unknown>}) {
  const object={...fallback,...asRecord(detail) || {}}
  const status=recordStatusInfo(object)
  const reasons=remoteRecordReasons(object)
  return <section className={`record-server-status ${status.tone}`}>
    <div><span>服务端判定</span><strong>{status.label}</strong></div>
    <p>{status.description}</p>
    {!!reasons.length && <ul>{reasons.map(reason=><li key={reason}>{reason}</li>)}</ul>}
  </section>
}

function RecordInspectionPanel({inspection}: {inspection:RemoteRecordInspection}) {
  const errors=inspection.checks.filter(check=>check.status==='error').length
  const warnings=inspection.checks.filter(check=>check.status==='warning').length
  const applied=inspection.checks.filter(check=>check.status!=='skipped').length
  return <section className={`record-inspection ${inspection.valid?'passed':'failed'}`}>
    <div className="record-inspection-head"><div><h3>{inspection.valid ? (warnings ? '预检通过 · 有提醒' : '预检通过') : '预检发现异常'}</h3><p>{inspection.rule_source ? `按「${inspection.rule_source}」规则与记录内部关系检测` : '按记录内部一致性检测'}</p></div><div className="inspection-score"><strong>{inspection.score}</strong><span>一致性分</span></div></div>
    <div className="inspection-meta"><span>{applied} 项已检测</span><span>{errors} 项异常</span><span>{warnings} 项提醒</span><span>轨迹 {inspection.metrics.point_count} 点</span></div>
    <div className="inspection-checks">{inspection.checks.map(check=><div className={`inspection-check ${check.status}`} key={check.code}><i>{check.status==='pass'?'✓':check.status==='warning'?'!':check.status==='error'?'×':'–'}</i><div><strong>{check.label}</strong><span>{check.message}</span></div></div>)}</div>
  </section>
}

function RecordMetric({icon, label, value}: {icon:ReactNode; label:string; value:string}) {
  return <div className="record-metric"><div>{icon}</div><span>{label}</span><strong>{value}</strong></div>
}

function RecordSegments({detail}: {detail:RemoteRecordData}) {
  const object=asRecord(detail)
  const steps=numberList(object?.stepList)
  const paces=numberList(object?.paceList)
  if (!steps.length && !paces.length) return null
  return <div className="record-segments"><div><span>步数分段</span><b>{Number(object?.stepInterval || 0) || '—'} 秒 / 段</b><div>{steps.map((value,index)=><i key={index}>#{index+1} {Math.round(value)} 步</i>)}</div></div><div><span>配速分段</span><b>{Number(object?.paceInterval || 0) || '—'} 米 / 段</b><div>{paces.map((value,index)=><i key={index}>#{index+1} {pace(value)}</i>)}</div></div></div>
}

function Catalog({accounts, plans, areas, notify, onRefresh}: {accounts:Account[]; plans:Plan[]; areas:Area[]; notify:(message:string)=>void; onRefresh:()=>Promise<void>}) {
  const [accountID, setAccountID] = useState(accounts[0]?.id || '')
  const [busy, setBusy] = useState('')
  useEffect(() => { if (!accounts.some(account => account.id === accountID)) setAccountID(accounts[0]?.id || '') }, [accounts, accountID])
  const account = accounts.find(item => item.id === accountID)
  const accountPlans = plans.filter(item => item.source_account_id === accountID)
  const accountAreas = areas.filter(item => item.source_account_id === accountID)
  const sync = async () => {
    if (!account) return
    setBusy('sync')
    try { const value=await api.syncAccount(account.id); notify(`已从 ${accountDisplayName(account)} 读取 ${value.plans.length} 个计划、${value.areas.length} 个区域`); await onRefresh() }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }
  const chooseDefault = async (kind:'plan'|'area', id:string) => {
    if (!account) return
    setBusy(`${kind}:${id}`)
    try {
      await api.updateAccount({...account, ...(kind === 'plan' ? {default_plan_id:id} : {default_area_id:id})})
      notify(kind === 'plan' ? '已设为该账号的默认跑步计划' : '已设为该账号的默认校园区域')
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }
  return <div className="page-stack">
    <section className="page-toolbar"><div className="resource-toolbar"><SearchSelect value={accountID} onChange={event => setAccountID(event.target.value)}><option value="">选择账号</option>{accounts.map(item => <option key={item.id} value={item.id}>{accountSelectorLabel(item)}</option>)}</SearchSelect><button className="button primary" disabled={!account?.session_configured || !!busy} onClick={() => void sync()}><RefreshCw size={16} className={busy === 'sync' ? 'spin' : ''}/>{busy === 'sync' ? '读取中…' : '从账号重新读取'}</button></div></section>
    {!account?.session_configured && <div className="panel resource-hint"><LockKeyhole/><div><strong>先完成账号登录</strong><span>登录成功后会自动读取可用计划和学校围栏，也可在这里手动刷新。</span></div></div>}
    <section className="catalog-grid"><div><div className="section-label"><CalendarClock/>账号跑步计划<b>{accountPlans.length}</b></div><div className="catalog-list">{accountPlans.map(item => { const selected=account?.default_plan_id === item.id; return <div className={`panel catalog-card ${selected ? 'selected-resource' : ''}`} key={item.id}><div className="catalog-icon"><Gauge/></div><div className="catalog-content"><h3>{item.name}</h3><p>{planAccountingDescription(item)}</p><div className="chip-row"><span>{planAccountingLabel(item)}</span><span>{distanceRule(item)}</span><span>{paceRule(item)}</span><span>{cadenceRule(item)}</span><span>{strideRule(item)}</span><span>{timingRule(item)}</span><span>{planQuotaRule(item)}</span><span>{planTimeRule(item)}</span>{selected && <span>当前选择</span>}</div></div><button className={`resource-select ${selected ? 'selected' : ''}`} disabled={selected || !!busy} onClick={() => void chooseDefault('plan', item.id)}>{busy === `plan:${item.id}` ? <RefreshCw className="spin"/> : <Check/>}<span>{selected ? '已选' : '设为默认'}</span></button></div>})}{!accountPlans.length && <Empty text="该账号尚未同步计划"/>}</div></div><div><div className="section-label"><MapIcon/>账号校园区域<b>{accountAreas.length}</b></div><div className="catalog-list">{accountAreas.map(item => { const selected=account?.default_area_id === item.id; return <div className={`panel catalog-card ${selected ? 'selected-resource' : ''}`} key={item.id}><div className="catalog-icon cyan"><MapPin/></div><div className="catalog-content"><h3>{item.name}</h3><p>围栏代码 {item.remote_code || '—'} · {item.center.lat.toFixed(6)}, {item.center.lng.toFixed(6)}</p><div className="chip-row"><span>{item.boundary?.length || 0} 围栏点</span><span>平台规划 {item.candidate_count} 条候选</span>{selected && <span>当前选择</span>}</div></div><button className={`resource-select ${selected ? 'selected' : ''}`} disabled={selected || !!busy} onClick={() => void chooseDefault('area', item.id)}>{busy === `area:${item.id}` ? <RefreshCw className="spin"/> : <Check/>}<span>{selected ? '已选' : '设为默认'}</span></button></div>})}{!accountAreas.length && <Empty text="该账号尚未同步校园区域"/>}</div></div></section>
  </div>
}

type AutomationForm = {
  routeMode:'auto'|'custom'; manualRouteID:string
  billingMode:string; orderQuantity:number
  account:string; plan:string; area:string; distance:number; dailyLimit:number; weeklyLimit:number
  dailyDistanceLimit:number; weeklyDistanceLimit:number
  scheduleMode:'manual'|'plan_random'; manualTime:string
  dayScope:'every_day'|'workday'|'weekend'; timezone:string
}

const emptyAutomationForm = ():AutomationForm => ({
  routeMode:'auto', manualRouteID:'',
  billingMode:'per_run', orderQuantity:1,
  account:'', plan:'', area:'', distance:0, dailyLimit:0, weeklyLimit:0,
  dailyDistanceLimit:0, weeklyDistanceLimit:0,
  scheduleMode:'manual', manualTime:'06:30', dayScope:'every_day', timezone:'Asia/Shanghai',
})

function Automation({jobs, accounts, plans, areas, executions, config, customer, notify, onRefresh}: {jobs:Job[]; accounts:Account[]; plans:Plan[]; areas:Area[]; executions:Execution[]; config:Config|null; customer:boolean; notify:(message:string)=>void; onRefresh:()=>Promise<void>}) {
  const jobPages=usePagination(jobs)
  const [manualRoutes,setManualRoutes]=useState<ManualRoute[]>([])
  const [mapAvailable,setMapAvailable]=useState(config?.map_api_configured===true)
  const [linkJob,setLinkJob]=useState<Job|null>(null)
  useEffect(()=>{void api.manualRoutes().then(setManualRoutes).catch(e=>notify(e.message))},[notify])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Job|null>(null)
  const [busy, setBusy] = useState('')
  const [form, setForm] = useState<AutomationForm>(emptyAutomationForm)
  const [billing,setBilling]=useState<BillingSummary|null>(null)
  const pendingOrder=useRef<{signature:string;id:string}|null>(null)
  useEffect(()=>{try{pendingOrder.current=JSON.parse(sessionStorage.getItem('easyrun-task-checkout')||'null')}catch{/* optional retry recovery */}},[])
  useEffect(()=>{if(customer)void portalRequest<BillingSummary>('billing').then(setBilling).catch(e=>notify(e.message))},[customer,notify,jobs])
  const formPlans = plans.filter(item => item.source_account_id === form.account)
  const formAreas = areas.filter(item => item.source_account_id === form.account)
  const formPlan = formPlans.find(item => item.id === form.plan)
  const planHasWindows = Boolean(formPlan?.rules?.time_windows?.length)
  const countBasedPlan = isCountBasedPlan(formPlan)
  const choices=matchingRoutes(manualRoutes,form.account,form.plan,form.area)
  const selectedRoute=choices.find(r=>r.id===form.manualRouteID)
  const routeError=routeMapError(mapAvailable,form.routeMode)||routeSelectionError(form.routeMode,selectedRoute,formPlan?automationDefaultDistance(formPlan,form.distance):form.distance,formPlan)
  const ownedPackage=Boolean(billing?.entitlements.some(p=>p.account_id===form.account&&p.plan_id===form.plan))
  const checkout=customer&&!editing&&Boolean(billing?.pricing.enabled)
  const orderUnits=form.billingMode==='per_km'?Math.round(form.orderQuantity*1000):form.orderQuantity
  const orderAmount=ownedPackage?0:billing?prepaidAmount(form.billingMode as PrepayMode,orderUnits,billing.pricing):null
  const orderError=checkout&&!ownedPackage?(orderAmount===null?'请输入有效的下单总量':form.billingMode==='per_km'&&orderUnits<(formPlan?automationDefaultDistance(formPlan,form.distance):form.distance)?'下单里程至少覆盖一次任务距离':orderAmount>(billing?.user.balance_micro||0)?'可用余额不足，请先充值':''):''

  const submit = async (event:FormEvent) => {
    event.preventDefault(); if(busy)return;if(routeError||orderError){notify(routeError||orderError);return};if(customer&&!editing&&!billing){notify('请等待计费信息加载');return};setBusy('save')
    try {
      const payload = {
        account_id:form.account, plan_id:form.plan, area_id:form.area,
        route_mode:form.routeMode, manual_route_id:form.routeMode==='custom'?form.manualRouteID:'',
        target_distance_m:formPlan ? automationDefaultDistance(formPlan, form.distance) : form.distance, schedule_mode:form.scheduleMode,
        daily_run_limit:form.dailyLimit, weekly_run_limit:form.weeklyLimit,
        daily_distance_limit_m:form.dailyDistanceLimit, weekly_distance_limit_m:form.weeklyDistanceLimit,
        manual_time:form.scheduleMode === 'manual' ? form.manualTime : '',
        day_scope:form.scheduleMode === 'manual' ? form.dayScope : '',
        cron_expr:'', timezone:form.timezone,
      }
      if (editing) {
        await api.updateJob({...editing,...payload})
      } else if(checkout) {
        const order={job:{...payload,enabled:true},mode:ownedPackage?'plan_package':form.billingMode,units:ownedPackage?0:orderUnits,expected_amount_micro:orderAmount}
        const signature=JSON.stringify(order)
        if(pendingOrder.current?.signature!==signature)pendingOrder.current={signature,id:newCreditRequestID()}
        try{sessionStorage.setItem('easyrun-task-checkout',JSON.stringify(pendingOrder.current))}catch{/* memory retries remain idempotent */}
        await portalRequest<Job>('task-orders',{...order,request_id:pendingOrder.current.id})
        pendingOrder.current=null;try{sessionStorage.removeItem('easyrun-task-checkout')}catch{/* optional */}
      } else {
        await api.createJob({...payload,enabled:true})
      }
      setOpen(false); setEditing(null); setForm(emptyAutomationForm())
      notify(editing ? '任务配置已保存' : checkout&&!ownedPackage?`下单成功，已预扣 ${money(orderAmount||0)} USDT`:'任务已创建')
      await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }
  const prepareEditor=async(draft:AutomationForm,job:Job|null)=>{
    if(busy)return
    setBusy('options')
    try{
      const [latest,routes]=await Promise.all([api.config(),api.manualRoutes()])
      setMapAvailable(latest.map_api_configured);setManualRoutes(routes);setEditing(job)
      setForm({...draft,routeMode:availableRouteMode(latest.map_api_configured,draft.routeMode)});setOpen(true)
    }catch(e){notify((e as Error).message)}finally{setBusy('')}
  }
  const openCreate = () => {void prepareEditor(emptyAutomationForm(),null)}
  const openEdit = (job:Job) => {
    const plan=plans.find(item=>item.id===job.plan_id)
    void prepareEditor({
      routeMode:job.route_mode||'auto',manualRouteID:job.manual_route_id||'',
      billingMode:billing?.job_modes[job.id]||'per_run',orderQuantity:1,
      account:job.account_id, plan:job.plan_id, area:job.area_id,
      distance:plan?automationDefaultDistance(plan,job.target_distance_m):job.target_distance_m, scheduleMode:job.schedule_mode==='plan_random'?'plan_random':'manual',
      dailyLimit:job.daily_run_limit||defaultDailyLimit(plan), weeklyLimit:job.weekly_run_limit||defaultWeeklyLimit(plan),
      dailyDistanceLimit:job.daily_distance_limit_m||defaultDailyDistanceLimit(plan), weeklyDistanceLimit:job.weekly_distance_limit_m||defaultWeeklyDistanceLimit(plan),
      manualTime:job.manual_time||'06:30',
      dayScope:job.day_scope==='workday'||job.day_scope==='weekend'?job.day_scope:'every_day',
      timezone:job.timezone||'Asia/Shanghai',
    },job)
  }
  const closeEditor = () => { if(busy==='save')return;setOpen(false); setEditing(null); setForm(emptyAutomationForm()) }
  const runNow = async (job:Job) => {
    setBusy(job.id + ':run')
    try { await api.runJob(job.id); notify('任务已入队，将按 8.7.9 协议执行'); await onRefresh() }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }
  const toggle = async (job:Job) => {
    setBusy(job.id + ':toggle')
    try { await api.updateJob({...job,enabled:!job.enabled}); await onRefresh() }
    catch (error) { notify((error as Error).message) }
    finally { setBusy('') }
  }
  return <div className="page-stack">
    <section className="page-toolbar"><button className="button primary" disabled={!!busy} onClick={openCreate}><Plus size={17}/>新建任务</button></section>
    <section className="automation-summary"><SummaryTile tone="green" icon={<CalendarClock/>} label="启用任务" value={jobs.filter(job => job.enabled).length}/><SummaryTile tone="cyan" icon={<Bot/>} label="工作线程" value={config?.automation.workers || 0}/><SummaryTile tone="violet" icon={<ListRestart/>} label="队列深度" value={config?.automation.queue_depth || 0}/><SummaryTile tone="success" icon={<Check/>} label="成功执行" value={config?.automation.succeeded || 0}/></section>
    <section className="panel jobs-panel"><div className="panel-heading"><div><h2>账号任务列表</h2></div><span className="count-badge">{jobs.length}</span></div><ListViewport pagination={jobPages} className="job-list">{jobPages.items.map(job => { const owner=accounts.find(item => item.id === job.account_id); const plan=plans.find(item => item.id === job.plan_id); return <div className="job-row" key={job.id}><div className={`job-state ${job.enabled ? 'active' : ''}`}><CalendarClock/></div><div className="job-title"><strong title={owner ? accountDisplayName(owner) : job.account_id}>{owner ? accountDisplayName(owner) : job.account_id}</strong><span>{owner ? accountStudentNumber(owner) : '账号已移除'}</span></div><div className="job-bind job-plan"><span>计划</span><b title={plan?.name}>{plan?.name || '—'}</b><small title={areas.find(item => item.id === job.area_id)?.name}><MapPin aria-hidden="true"/>{areas.find(item => item.id === job.area_id)?.name || '区域待同步'} · {job.route_mode==='custom'?'自定义路线':'自动路线'}</small></div><div className="job-distance"><span>{isCountBasedPlan(plan)?'COUNT PLAN':'DISTANCE'}</span><b>{automationDistanceLabel(job,plan)}</b><small title={quotaLabel(job,plan)}>{quotaLabel(job,plan)}</small></div><div className="job-schedule"><code>{scheduleLabel(job)}</code><span title={job.timezone}>{job.next_run_at ? dateTimeInZone(job.next_run_at,job.timezone) : '已暂停'}</span></div><div className="job-actions"><button className="mini-button" title="生成独立配置链接" disabled={!!busy} onClick={()=>setLinkJob(job)}><Link2/><span className="mobile-action-label">配置</span></button><button className="mini-button" title="编辑任务" disabled={!!busy} onClick={() => openEdit(job)}><Pencil/><span className="mobile-action-label">编辑</span></button><button className="mini-button" title="立即运行" disabled={!!busy} onClick={() => void runNow(job)}><Play/><span className="mobile-action-label">运行</span></button><button className={`mini-button ${job.enabled ? 'on' : ''}`} title="启停" disabled={!!busy} onClick={() => void toggle(job)}>{job.enabled ? <ToggleRight/> : <ToggleLeft/>}<span className="mobile-action-label">{job.enabled?'暂停':'启用'}</span></button></div></div>})}{!jobs.length && <Empty text="暂无自动任务"/>}</ListViewport></section>
    {linkJob&&<Modal title="独立配置链接" onClose={()=>setLinkJob(null)}><TaskConfigLinkPanel key={linkJob.id} job={linkJob} notify={notify}/></Modal>}
    {open && <Modal title={editing?'编辑自动任务':'新建自动任务'} wide onClose={closeEditor}><form className="form-stack" onSubmit={submit}>
      <div className="form-grid">
        <Field label="账号"><SearchSelect required disabled={!!editing||busy==='save'} value={form.account} onChange={event => { const account=accounts.find(item=>item.id===event.target.value); const nextPlan=plans.find(item=>item.id===account?.default_plan_id); setForm({...form,routeMode:availableRouteMode(mapAvailable),manualRouteID:'',account:event.target.value,plan:account?.default_plan_id||'',area:account?.default_area_id||'',distance:nextPlan?automationDefaultDistance(nextPlan):0,dailyLimit:defaultDailyLimit(nextPlan),weeklyLimit:defaultWeeklyLimit(nextPlan),dailyDistanceLimit:defaultDailyDistanceLimit(nextPlan),weeklyDistanceLimit:defaultWeeklyDistanceLimit(nextPlan),scheduleMode:nextPlan?.rules?.time_windows?.length?form.scheduleMode:'manual'}) }}><option value="">选择账号</option>{accounts.map(account => <option key={account.id} value={account.id}>{accountSelectorLabel(account)}</option>)}</SearchSelect></Field>
        <Field label="账号跑步计划"><SearchSelect required disabled={!!editing||busy==='save'} value={form.plan} onChange={event => { const nextPlan=formPlans.find(item=>item.id===event.target.value); setForm({...form,routeMode:availableRouteMode(mapAvailable),manualRouteID:'',plan:event.target.value,distance:nextPlan?automationDefaultDistance(nextPlan):0,dailyLimit:defaultDailyLimit(nextPlan),weeklyLimit:defaultWeeklyLimit(nextPlan),dailyDistanceLimit:defaultDailyDistanceLimit(nextPlan),weeklyDistanceLimit:defaultWeeklyDistanceLimit(nextPlan),scheduleMode:nextPlan?.rules?.time_windows?.length?form.scheduleMode:'manual'}) }}><option value="">选择账号内计划</option>{formPlans.map(plan => <option key={plan.id} value={plan.id}>{plan.name} · {planAccountingLabel(plan)}</option>)}</SearchSelect></Field>
        <Field label="账号校园区域"><SearchSelect required value={form.area} onChange={event => setForm({...form,routeMode:availableRouteMode(mapAvailable),manualRouteID:'',area:event.target.value})}><option value="">选择账号内区域</option>{formAreas.map(area => <option key={area.id} value={area.id}>{area.name}</option>)}</SearchSelect></Field>
        <Field label="时区"><input required value={form.timezone} onChange={event => setForm({...form,timezone:event.target.value})}/></Field>
      </div>
      {formPlan && (countBasedPlan ? <CountPlanDistance plan={formPlan}/> : <DistancePicker title="任务跑步距离" plan={formPlan} value={form.distance} onChange={distance=>setForm({...form,distance})}/>)}
      <div className="route-choice">{!mapAvailable&&<div className="portal-notice" role="status">尚未配置地图 API Key，当前仅支持自定义路线。请先在「路线管理」中绘制并保存路线。</div>}<div className="form-grid"><Field label="路线模式"><SearchSelect value={form.routeMode} onChange={e=>setForm({...form,routeMode:e.target.value as 'auto'|'custom',manualRouteID:''})}><option value="auto" disabled={!mapAvailable}>自动路线{!mapAvailable?'（未配置地图 Key）':''}</option><option value="custom">自定义路线</option></SearchSelect></Field>{form.routeMode==='custom'&&<Field label="已保存路线"><SearchSelect required value={form.manualRouteID} onChange={e=>setForm({...form,manualRouteID:e.target.value})}><option value="">选择手绘路线</option>{choices.map(r=><option value={r.id} key={r.id}>{r.name} · {(r.distance_m/1000).toFixed(3)} km</option>)}</SearchSelect></Field>}</div>{form.routeMode==='custom'&&<p className="portal-hint">在「路线管理」中绘制与保存；执行时从起点截取任务距离。当前有 {choices.length} 条匹配路线。</p>}{routeError&&<div className="portal-error" role="alert">{routeError}</div>}</div>
      {formPlan && <AutomationQuotaEditor plan={formPlan} dailyLimit={form.dailyLimit} weeklyLimit={form.weeklyLimit} dailyDistanceLimit={form.dailyDistanceLimit} weeklyDistanceLimit={form.weeklyDistanceLimit} onChange={(dailyLimit,weeklyLimit,dailyDistanceLimit,weeklyDistanceLimit)=>setForm({...form,dailyLimit,weeklyLimit,dailyDistanceLimit,weeklyDistanceLimit})}/>}
      {customer&&editing&&<TaskPrepayPanel key={editing.id} jobID={editing.id} notify={notify}/>}
      {checkout&&billing&&<section className="task-order-checkout">{ownedPackage?<p className="portal-notice"><Check size={16}/>已购买此账号的完整计划套餐，本次下单不再预扣。</p>:<><div className="form-grid"><Field label="下单计费方式"><SearchSelect disabled={busy==='save'} value={form.billingMode} onChange={e=>setForm({...form,billingMode:e.target.value,orderQuantity:e.target.value==='per_km'?Math.ceil((formPlan?automationDefaultDistance(formPlan,form.distance):1000)/100)/10:1})}><option value="per_run">按次 · {money(billing.pricing.per_run_micro)} USDT / 次</option><option value="per_km">按公里 · {money(billing.pricing.per_km_micro)} USDT / km</option></SearchSelect></Field><Field label={form.billingMode==='per_run'?'下单总次数':'下单总公里数'}><input required type="number" disabled={busy==='save'} min={form.billingMode==='per_run'?1:.1} max={form.billingMode==='per_run'?10000:1000} step={form.billingMode==='per_run'?1:.1} value={form.orderQuantity} onChange={e=>setForm({...form,orderQuantity:Number(e.target.value)})}/></Field></div><div className="escrow-checkout-total"><span>本次预扣 <strong>{orderAmount===null?'—':money(orderAmount)} <small>USDT</small></strong></span><span>可用余额 {money(billing.user.balance_micro)} USDT</span></div><p className="escrow-rule"><LockKeyhole/>下单预扣，成功结算。未执行额度可增减，减少退回原预扣金额。</p>{orderError&&<div className="portal-error" role="alert">{orderError}</div>}</>}</section>}
      <div className="schedule-editor">
        <div className="schedule-editor-head"><span>执行时间策略</span><small>随机时间在保存任务时确定</small></div>
        <div className="schedule-mode-grid">
          <button type="button" className={form.scheduleMode==='manual'?'selected':''} onClick={()=>setForm({...form,scheduleMode:'manual'})}><CalendarClock/><strong>固定时间</strong><span>手动选择时刻和执行日</span></button>
          <button type="button" className={form.scheduleMode==='plan_random'?'selected':''} disabled={!planHasWindows} title={planHasWindows?'从计划规定时段随机':'该计划尚未同步规定时段'} onClick={()=>setForm({...form,scheduleMode:'plan_random'})}><Shuffle/><strong>计划时段随机</strong><span>{planHasWindows?`${formPlan?.rules.time_windows?.length} 个规定时段`:'先同步计划时段'}</span></button>
        </div>
        {form.scheduleMode === 'manual' ? <div className="form-grid schedule-fields">
          <Field label="执行时间"><input required type="time" value={form.manualTime} onChange={event=>setForm({...form,manualTime:event.target.value})}/></Field>
          <Field label="执行日"><SearchSelect value={form.dayScope} onChange={event=>setForm({...form,dayScope:event.target.value as AutomationForm['dayScope']})}><option value="every_day">每天</option><option value="workday">工作日</option><option value="weekend">周末</option></SearchSelect></Field>
        </div> : formPlan && <div className="plan-window-panel"><div><strong>服务器计划有效期</strong><span>{planDateRange(formPlan)}</span></div><div className="plan-window-chips">{formPlan.rules.time_windows?.map((window,index)=><span key={`${window.day_type}-${window.start_minute}-${index}`}>{timeWindowLabel(window)}</span>)}</div><small>在计划时段内随机安排，预留跑步与上传时间；执行前复查限额。</small></div>}
      </div>
      <button className="button primary wide" disabled={busy === 'save' || !!routeError || !!orderError || (customer&&!editing&&!billing) || (form.scheduleMode === 'plan_random' && !planHasWindows)} type="submit">{busy==='save'?'正在保存…':editing?'保存任务配置':checkout&&!ownedPackage?`确认下单 · 预扣 ${orderAmount===null?'—':money(orderAmount)} USDT`:'创建并启用'}</button>
    </form></Modal>}
  </div>
}

function ExecutionLogs({accounts,plans,jobs,notify}:{accounts:Account[];plans:Plan[];jobs:Job[];notify:(message:string)=>void}){
  const [page,setPage]=useState(1),[pageSize,setPageSize]=useState(10),[accountID,setAccountID]=useState(''),[status,setStatus]=useState(''),[revision,setRevision]=useState(0),[loading,setLoading]=useState(false)
  const [data,setData]=useState<{items:Execution[];total:number}>({items:[],total:0})
  useEffect(()=>{
    const controller=new AbortController()
    const load=async()=>{setLoading(true);try{const result=await api.executionPage({page,page_size:pageSize,account_id:accountID,status},controller.signal);if(!controller.signal.aborted){setData(result);if(result.page!==page)setPage(result.page)}}catch(e){if(!controller.signal.aborted)notify((e as Error).message)}finally{if(!controller.signal.aborted)setLoading(false)}}
    void load();const timer=setInterval(()=>void load(),10000)
    return()=>{controller.abort();clearInterval(timer)}
  },[page,pageSize,accountID,status,revision,notify])
  return <div className="page-stack"><section className="panel compact-filter-bar"><Field label="账号"><SearchSelect value={accountID} onChange={e=>{setAccountID(e.target.value);setPage(1)}}><option value="">全部账号</option>{accounts.map(account=><option key={account.id} value={account.id}>{accountSelectorLabel(account)}</option>)}</SearchSelect></Field><Field label="执行状态"><SearchSelect value={status} onChange={e=>{setStatus(e.target.value);setPage(1)}}><option value="">全部状态</option><option value="running">执行中</option><option value="succeeded">成功</option><option value="failed">失败</option><option value="skipped">已跳过</option></SearchSelect></Field><button className="button subtle" disabled={loading} onClick={()=>setRevision(v=>v+1)}><RefreshCw className={loading?'spin':''}/>刷新日志</button></section><section className="panel execution-logs-panel"><ListViewport pagination={{page,pageSize,total:data.total,setPage,setPageSize}} className="table-scroll"><table className="mobile-card-table" aria-label="执行日志"><thead><tr><th>开始时间</th><th>状态</th><th>账号</th><th>计划</th><th>服务端记录</th><th>消息</th></tr></thead><tbody>{data.items.map(execution=>{const owner=accounts.find(account=>account.id===execution.account_id),job=jobs.find(item=>item.id===execution.job_id),plan=plans.find(item=>item.id===job?.plan_id);return <tr key={execution.id}><td data-label="开始时间">{dateTime(execution.started_at)}</td><td data-label="状态"><span className={`exec-status ${execution.status}`}>{executionStatusLabel(execution.status)}</span></td><td data-label="账号" className="mobile-wide-cell" title={owner?accountSelectorLabel(owner):execution.account_id}>{owner?accountSelectorLabel(owner):execution.account_id}</td><td data-label="计划" className="mobile-wide-cell" title={plan?.name}>{plan?.name||'计划已移除'}</td><td data-label="服务端记录" className="mobile-wide-cell" title={execution.remote_run_id}>{execution.remote_run_id||'—'}</td><td data-label="消息" className="execution-message mobile-wide-cell" title={execution.error||''}>{execution.error||'—'}</td></tr>})}</tbody></table>{!data.items.length&&<Empty text={loading?'正在读取执行日志…':'暂无匹配的执行日志'}/>}</ListViewport></section></div>
}

type MapKeyDraft = {clientID:string;id:string;masked:string;apiKey:string;qps:number;show:boolean}
const mapKeyDraft = (id='', masked='', qps=3):MapKeyDraft => ({clientID:id||`new-${Date.now()}-${Math.random()}`,id,masked,apiKey:'',qps,show:false})

function SystemSettingsPage({config,user,notify,onRefresh}:{config:Config|null;user:PortalUser;notify:(message:string)=>void;onRefresh:()=>Promise<void>}){
  const [section,setSection]=useState<'runtime'|'access'|'mail'>('runtime')
  return <div className="page-stack system-settings-page"><div className="portal-page-tabs" role="tablist" aria-label="系统配置分类">{([['runtime','地图与执行器'],['access','使用模式与客户入口'],['mail','邮件服务器']] as const).map(([key,label])=><button type="button" role="tab" aria-selected={section===key} key={key} className={section===key?'active':''} onClick={()=>setSection(key)}>{label}</button>)}</div>{section==='runtime'?<RuntimeSettingsPage config={config} notify={notify} onRefresh={onRefresh}/>:<PortalSystemSettings key={section} section={section} user={user} notify={notify}/>}</div>
}

function RuntimeSettingsPage({config: _config, notify, onRefresh}: {config:Config|null; notify:(message:string)=>void; onRefresh:()=>Promise<void>}) {
  const [settings, setSettings] = useState<SystemSettingsData|null>(null)
  const [mapKeys, setMapKeys] = useState<MapKeyDraft[]>([])
  const [workerProcesses, setWorkerProcesses] = useState(_config?.automation.worker_processes || 2)
  const [workerConcurrency, setWorkerConcurrency] = useState(_config?.automation.worker_concurrency || 1)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const value=await api.systemSettings()
      setSettings(value); setMapKeys(value.map_api_keys.map(key=>mapKeyDraft(key.id,key.masked,key.qps))); setWorkerProcesses(value.worker_processes); setWorkerConcurrency(value.worker_concurrency)
    } catch (error) { notify((error as Error).message) }
  },[notify])
  useEffect(()=>{ void load() },[load])

  const updateMapKey=(clientID:string,patch:Partial<MapKeyDraft>)=>setMapKeys(current=>current.map(key=>key.clientID===clientID?{...key,...patch}:key))
  const save = async (event:FormEvent) => {
    event.preventDefault()
    if (mapKeys.some(key=>!key.id&&!key.apiKey.trim())) { notify('新增的高德地图 API Key 不能为空'); return }
    setBusy(true)
    try {
      const value=await api.updateSystemSettings({map_api_keys:mapKeys.map(key=>({id:key.id||undefined,api_key:key.apiKey.trim(),qps:key.qps})),worker_processes:workerProcesses,worker_concurrency:workerConcurrency})
      setSettings(value); setMapKeys(value.map_api_keys.map(key=>mapKeyDraft(key.id,key.masked,key.qps))); notify('配置已保存并生效'); await onRefresh()
    } catch (error) { notify((error as Error).message) }
    finally { setBusy(false) }
  }
  const totalConcurrency=workerProcesses*workerConcurrency
  const totalMapQPS=mapKeys.reduce((sum,key)=>sum+(Number.isFinite(key.qps)?key.qps:0),0)
  return <div className="page-stack settings-page">
    {_config?.face_runtime&&<section className="panel face-runtime-summary"><div><ScanFace/><h2>本地人脸推理</h2></div><span className={`status-pill ${_config.face_runtime.ready?'ok':'warn'}`}>{_config.face_runtime.ready?'已就绪':'待检查'}</span><p>{_config.face_runtime.status}</p><small>模型：{_config.face_runtime.model} · 自动匹配 Windows / Linux</small></section>}
    <form className="settings-grid" onSubmit={save}>
      <section className="panel settings-card map-keys-card">
        <div className="settings-card-head"><div className="settings-card-icon cyan"><RadioTower/></div><div><h2>高德地图密钥池</h2><p>每个 Key 独立限流，请求公平分配。</p></div><span className={`status-pill ${settings?.map_api_key_configured?'ok':'warn'}`}>{settings?settings.map_api_key_configured?`${settings.map_api_keys.length} 个已保存`:'尚未配置':'读取中…'}</span></div>
        <div className="map-key-toolbar"><div><strong>{mapKeys.length} 个 Key</strong><span>汇总上限 {Number(totalMapQPS.toFixed(1))} QPS</span></div><button className="button subtle" type="button" disabled={mapKeys.length>=32} onClick={()=>setMapKeys(current=>[...current,mapKeyDraft()])}><Plus/>添加 API Key</button></div>
        <div className="map-key-list" role="region" aria-label="地图 API Key 列表" tabIndex={0}>
          {mapKeys.length===0&&<div className="map-key-empty"><KeyRound/><strong>尚未配置地图密钥</strong><span>请添加 Web 服务 API Key。</span></div>}
          {mapKeys.map((key,index)=><div className="map-key-row" key={key.clientID}>
            <div className="map-key-order"><span>KEY</span><strong>{String(index+1).padStart(2,'0')}</strong></div>
            <div className="map-key-secret"><label htmlFor={`map-secret-${key.clientID}`}>API Key</label><div className="secret-input"><input id={`map-secret-${key.clientID}`} aria-label={`第 ${index+1} 个 API Key`} required={!key.id} type={key.show?'text':'password'} autoComplete="off" value={key.apiKey} placeholder={key.id?`${key.masked} · 留空保留`:'输入 Web 服务 API Key'} onChange={event=>updateMapKey(key.clientID,{apiKey:event.target.value})}/><button type="button" aria-label={`${key.show?'隐藏':'显示'}第 ${index+1} 个密钥`} title={key.show?'隐藏密钥':'显示密钥'} onClick={()=>updateMapKey(key.clientID,{show:!key.show})}>{key.show?<EyeOff/>:<Eye/>}</button></div></div>
            <label className="map-key-qps"><span>独立 QPS</span><input aria-label={`第 ${index+1} 个 Key 的 QPS 上限`} required type="number" inputMode="decimal" min="0.1" max="100" step="0.1" value={key.qps} onChange={event=>updateMapKey(key.clientID,{qps:Number(event.target.value)})}/></label>
            <button className="map-key-remove" type="button" title="移除此 Key" aria-label={`移除第 ${index+1} 个 API Key`} onClick={()=>setMapKeys(current=>current.filter(item=>item.clientID!==key.clientID))}><Trash2/></button>
          </div>)}
        </div>
        <div className="settings-footnote"><Database/><span>密钥已加密保存，留空保留原值。</span></div>
      </section>

      <section className="panel settings-card worker-settings-card">
        <div className="settings-card-head"><div className="settings-card-icon"><Cpu/></div><div><h2>Worker 执行器</h2><p>总并发 = Worker 数 × 单 Worker 并发数。</p></div></div>
        <div className="worker-settings-body">
          <div className="settings-fields two-column">
            <Field label="Worker 进程数"><input required type="number" min="1" max="16" step="1" value={workerProcesses} onChange={event=>setWorkerProcesses(Number(event.target.value))}/><small>允许 1–16 个 Worker</small></Field>
            <Field label="单 Worker 并发数"><input required type="number" min="1" max="32" step="1" value={workerConcurrency} onChange={event=>setWorkerConcurrency(Number(event.target.value))}/><small>每个 Worker 允许 1–32 路</small></Field>
          </div>
          <div className="capacity-preview" aria-label={`执行容量：${workerProcesses} 个 Worker，每个 ${workerConcurrency} 路，共 ${totalConcurrency} 个任务槽位`}><div><span>进程</span><strong>{workerProcesses}</strong></div><i aria-hidden="true">×</i><div><span>线程</span><strong>{workerConcurrency}</strong></div><i aria-hidden="true">=</i><div className="total"><span>任务槽位</span><strong>{totalConcurrency}</strong></div></div>
        </div>
      </section>

      <section className="panel settings-savebar">
        <div><ShieldCheck/><span><strong>数据库持久化</strong><small>{settings?.updated_at?`上次更新 ${dateTime(settings.updated_at)}`:'正在读取当前配置'}</small></span></div>
        <div className="settings-live-facts"><span>地图 <b>{mapKeys.length} Key · {Number(totalMapQPS.toFixed(1))} QPS</b></span><span>任务 <b>{totalConcurrency} 并发</b></span></div>
        <button className="button primary" type="submit" disabled={busy||!settings}><Save/>{busy?'保存中…':'保存并应用'}</button>
      </section>
    </form>
  </div>
}

function SignalChart({samples}: {samples:Sample[]}) {
  const values = samples.filter((_, index) => index % Math.max(1, Math.ceil(samples.length/180)) === 0)
  if (values.length < 2) return <div className="chart-empty"><AreaChart/><span>完成数据预检后显示信号曲线</span></div>
  const width=900, height=190, pad=16, maxSpeed=Math.max(...values.map(value => value.speed_mps),1), maxCadence=Math.max(...values.map(value => value.cadence_spm),1)
  const path=(key:'speed_mps'|'cadence_spm',max:number)=>values.map((value,index)=>`${index?'L':'M'} ${(pad+index/(values.length-1)*(width-pad*2)).toFixed(1)} ${(height-pad-(value[key]/max)*(height-pad*2)).toFixed(1)}`).join(' ')
  const speed=path('speed_mps',maxSpeed), cadence=path('cadence_spm',maxCadence)
  return <div className="signal-chart"><svg viewBox={`0 0 ${width} ${height}`}>{[.25,.5,.75].map(value => <line key={value} x1="0" x2={width} y1={height*value} y2={height*value}/>)}<defs><linearGradient id="areaFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--accent)" stopOpacity=".18"/><stop offset="1" stopColor="var(--accent)" stopOpacity="0"/></linearGradient></defs><path className="speed-area" d={`${speed} L ${width-pad} ${height-pad} L ${pad} ${height-pad} Z`}/><path className="speed-path" d={speed}/><path className="cadence-path" d={cadence}/></svg><div className="chart-legend"><span><i className="green"/>速度 m/s</span><span><i className="pink"/>步频 spm</span><b>{values.length} / {samples.length} 个采样点</b></div></div>
}

function DistancePicker({plan, value, onChange, title='本次目标距离'}: {plan:Plan; value:number; onChange:(value:number)=>void; title?:string}) {
  const range=selectableDistanceRange(plan)
  return <div className="distance-picker">
    <div className="distance-picker-head"><div><span>{title}</span><small>服务器允许 {(range.min/1000).toFixed(1)}–{(range.max/1000).toFixed(1)} km · 100 米一档</small></div><strong>{(value/1000).toFixed(1)}<em> km</em></strong></div>
    <div className="distance-picker-control">
      <button type="button" aria-label="减少100米" disabled={value<=range.min} onClick={()=>onChange(value-100)}>−</button>
      <input aria-label="目标跑步距离" type="range" min={range.min} max={range.max} step="100" value={value || range.max} onChange={event=>onChange(Number(event.target.value))}/>
      <button type="button" aria-label="增加100米" disabled={value>=range.max} onClick={()=>onChange(value+100)}>+</button>
    </div>
    <div className="distance-picker-scale"><span>{(range.min/1000).toFixed(1)} km</span><b>提交结果控制在目标 +0～+50 m</b><span>{(range.max/1000).toFixed(1)} km</span></div>
  </div>
}

function CountPlanDistance({plan}: {plan:Plan}) {
  const distance=automationDefaultDistance(plan)
  return <div className="count-plan-distance"><div className="count-plan-distance-icon"><ListRestart/></div><div><span>按次数考核</span><strong>自动按最低限制执行</strong><small>按次数计入进度，每次使用计划允许的最低距离。</small></div><b>{(distance/1000).toFixed(1)}<em> km / 次</em></b></div>
}

function AutomationQuotaEditor({plan,dailyLimit,weeklyLimit,dailyDistanceLimit,weeklyDistanceLimit,onChange}:{plan:Plan;dailyLimit:number;weeklyLimit:number;dailyDistanceLimit:number;weeklyDistanceLimit:number;onChange:(daily:number,weekly:number,dailyDistance:number,weeklyDistance:number)=>void}) {
  const dayMax=Math.max(0,Number(plan.rules?.day_max_times||0))
  const hasDayRule=dayMax>0
  const rawWeekMin=Math.max(0,Number(plan.rules?.week_min_times||0))
  const rawWeekMax=Math.max(0,Number(plan.rules?.week_max_times||0))
  const hasWeekRule=rawWeekMin>0||rawWeekMax>0
  const weekMin=hasWeekRule?Math.max(1,rawWeekMin||1):0
  const weekMax=hasWeekRule?Math.max(weekMin,rawWeekMax||weekMin):0
  const dayDistanceMax=Math.max(0,Number(plan.rules?.day_max_distance_m||0))
  const weekDistanceMax=Math.max(0,Number(plan.rules?.week_max_distance_m||0))
  const clampDistance=(value:number,max:number)=>max>0?Math.max(100,Math.min(max,Math.round(value/100)*100)):0
  return <section className="automation-quota-editor"><div className="schedule-editor-head"><span>每日 / 每周执行限额</span><small>次数与公里数均取自服务端规则，每次执行前都会重新统计有效记录</small></div><div className="automation-quota-grid"><Field label={hasDayRule?`每日最多（服务端 1–${dayMax} 次）`:'每日次数（服务端未限制）'}><input type="number" min={hasDayRule?1:0} max={hasDayRule?dayMax:undefined} step="1" disabled={!hasDayRule} value={hasDayRule?Math.max(1,Math.min(dayMax,dailyLimit||dayMax)):0} onChange={event=>onChange(Math.max(1,Math.min(dayMax,Number(event.target.value)||dayMax)),weeklyLimit,dailyDistanceLimit,weeklyDistanceLimit)}/></Field><Field label={hasWeekRule?`每周目标（服务端 ${weekMin}–${weekMax} 次）`:'每周目标（服务端未限制）'}><input type="number" min={weekMin} max={weekMax||undefined} step="1" disabled={!hasWeekRule} value={hasWeekRule?Math.max(weekMin,Math.min(weekMax,weeklyLimit||weekMin)):0} onChange={event=>onChange(dailyLimit,Math.max(weekMin,Math.min(weekMax,Number(event.target.value)||weekMin)),dailyDistanceLimit,weeklyDistanceLimit)}/></Field><Field label={dayDistanceMax>0?`每日公里数上限（服务端 ${(dayDistanceMax/1000).toFixed(1)} km）`:'每日公里数（服务端未限制）'}><input type="number" min="0.1" max={dayDistanceMax>0?dayDistanceMax/1000:undefined} step="0.1" disabled={dayDistanceMax<=0} value={dayDistanceMax>0?(clampDistance(dailyDistanceLimit||dayDistanceMax,dayDistanceMax)/1000).toFixed(1):0} onChange={event=>onChange(dailyLimit,weeklyLimit,clampDistance((Number(event.target.value)||dayDistanceMax/1000)*1000,dayDistanceMax),weeklyDistanceLimit)}/></Field><Field label={weekDistanceMax>0?`每周公里数上限（服务端 ${(weekDistanceMax/1000).toFixed(1)} km）`:'每周公里数（服务端未限制）'}><input type="number" min="0.1" max={weekDistanceMax>0?weekDistanceMax/1000:undefined} step="0.1" disabled={weekDistanceMax<=0} value={weekDistanceMax>0?(clampDistance(weeklyDistanceLimit||weekDistanceMax,weekDistanceMax)/1000).toFixed(1):0} onChange={event=>onChange(dailyLimit,weeklyLimit,dailyDistanceLimit,clampDistance((Number(event.target.value)||weekDistanceMax/1000)*1000,weekDistanceMax))}/></Field></div><div className="automation-quota-note"><ShieldCheck/><span>执行前校验次数与里程限额，超出时跳过。里程边界容差为 100 米。</span></div></section>
}

function Field({label, children}: {label:string; children:ReactNode}) { return <label className="field"><span>{label}</span>{children}</label> }

function FacePreview({title,description,available,src}:{title:string;description:string;available:boolean;src:string}) { return <div className="face-preview-card"><div className="face-preview-image">{available?<img src={src} alt={title}/>:<div><ScanFace/><span>暂无图片</span></div>}</div><strong>{title}</strong><span>{description}</span></div> }
function Fact({label, value, hint}: {label:string; value:string; hint?:string}) { return <div className="fact"><span>{label}</span><b>{value}</b>{hint && <small>{hint}</small>}</div> }
function MetricLine({label, value}: {label:string; value:string}) { return <div><span>{label}</span><b>{value}</b></div> }
function Stat({icon, label, value}: {icon:ReactNode; label:string; value:string}) { return <div className="stat"><div className="stat-icon">{icon}</div><div><span>{label}</span><strong>{value}</strong></div></div> }
function SummaryTile({tone, icon, label, value}: {tone:string; icon:ReactNode; label:string; value:number}) { return <div className={`panel summary-tile ${tone}`}><div>{icon}</div><span>{label}</span><strong>{value}</strong></div> }
function OverviewKPI({tone, icon, label, value, unit, hint}: {tone:string; icon:ReactNode; label:string; value:string; unit?:string; hint:string}) { return <div className={`panel overview-kpi ${tone}`}><div className="overview-kpi-icon">{icon}</div><div className="overview-kpi-copy"><span>{label}</span><strong>{value}{unit&&<span className="metric-unit">{unit}</span>}</strong><small title={hint}>{hint}</small></div></div> }
function HealthRow({icon, label, detail, live}: {icon:ReactNode; label:string; detail:string; live:boolean}) { return <div className="overview-health-row"><div className="health-icon">{icon}</div><div className="overview-health-copy"><strong>{label}</strong><span>{detail}</span></div><i className={live?'live':''} role="img" aria-label={live?'服务在线':'服务未就绪'}/></div> }
function Empty({text}: {text:string}) { return <div className="empty"><Layers3/><span>{text}</span></div> }

function pace(seconds:number) { if (!Number.isFinite(seconds) || seconds <= 0) return '—'; const minutes=Math.floor(seconds/60), rest=Math.round(seconds%60); return `${minutes}'${String(rest).padStart(2,'0')}"` }
function duration(seconds:number) { if (!Number.isFinite(seconds)) return '—'; const hours=Math.floor(seconds/3600), minutes=Math.floor((seconds%3600)/60), rest=Math.round(seconds%60); return hours ? `${hours}:${String(minutes).padStart(2,'0')}:${String(rest).padStart(2,'0')}` : `${minutes}:${String(rest).padStart(2,'0')}` }
function dateTime(value?:string) { if (!value) return '—'; return new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date(value)) }
function dateTimeInZone(value:string, timezone:string) { try { return new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hour12:false,timeZone:timezone}).format(new Date(value)) } catch { return dateTime(value) } }
function dateDay(value:string, timezone:string) { try { return new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',timeZone:timezone}).format(new Date(value)) } catch { return new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit'}).format(new Date(value)) } }
function dateClock(value:string, timezone:string) { try { return new Intl.DateTimeFormat('zh-CN',{hour:'2-digit',minute:'2-digit',hour12:false,timeZone:timezone}).format(new Date(value)) } catch { return new Intl.DateTimeFormat('zh-CN',{hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date(value)) } }
function executionStatusLabel(status:string) { return ({queued:'排队',running:'执行中',succeeded:'成功',failed:'失败',skipped:'跳过'} as Record<string,string>)[status]||status }
function authLabel(status:string) { return ({authenticated:'已登录',configured:'待登录',sms_pending:'待验证码',expired:'登录失效',error:'登录异常',unconfigured:'未配置'} as Record<string,string>)[status] || status }
function maskLogin(value:string) { if (value.length < 7) return value; return `${value.slice(0,3)}****${value.slice(-4)}` }
function accountSchoolName(account:Account) { const value=(account.school_name || '').trim(); return value && !isOpaqueWireValue(value) ? value : '学校待同步' }
function accountInitials(account:Account) { const value=accountDisplayName(account); return value==='姓名待同步' ? '账' : value.slice(0,2).toUpperCase() }
function faceProfileFromAccount(account:Account):FaceProfile { return {account_id:account.id,face_enabled:account.face_enabled||0,face_auth:!!account.face_auth,similarity_value:account.face_similarity_value||50,face_time:account.face_time||0,body_enabled:!!account.face_body_enabled,body_rate:account.face_body_rate||0,face_model_type:account.face_model_type||1,base_configured:!!account.face_base_configured,custom_configured:!!account.face_custom_configured,effective_source:account.face_effective_source||'none',synced_at:account.face_synced_at} }
function dayScopeLabel(value?:string) { return ({every_day:'每天',workday:'工作日',weekend:'周末'} as Record<string,string>)[value || ''] || '自定义' }
function scheduleLabel(job:Job) {
  if (job.schedule_mode === 'plan_random') return '计划时段随机'
  if (job.schedule_mode === 'manual') return `${dayScopeLabel(job.day_scope)} ${job.manual_time || '—'}`
  return `Cron ${job.cron_expr}`
}
function clockMinute(value:number) { if (value === 1440) return '24:00'; const minute=Math.max(0,Math.min(1439,value)); return `${String(Math.floor(minute/60)).padStart(2,'0')}:${String(minute%60).padStart(2,'0')}` }
function timeWindowLabel(window:RunTimeWindow) { return `${dayScopeLabel(window.day_type)} ${window.times || `${clockMinute(window.start_minute)}–${clockMinute(window.end_minute)}`}` }
function planDateRange(plan:Plan) { const start=plan.rules?.active_start_date || '未限定'; const end=plan.rules?.active_end_date || '未限定'; return `${start} — ${end}` }
function planTimeRule(plan:Plan) { const windows=plan.rules?.time_windows || []; return windows.length ? `${windows.length} 个规定时段 · ${planDateRange(plan)}` : '计划时段待同步' }
function planQuotaRule(plan:Plan) { const rules=plan.rules; const parts=[rules?.day_max_times?`${rules.day_max_times} 次/日`:'',rules?.week_max_times?`${rules.week_max_times} 次/周`:'',rules?.day_max_distance_m?`${(rules.day_max_distance_m/1000).toFixed(1)} km/日`:'',rules?.week_max_distance_m?`${(rules.week_max_distance_m/1000).toFixed(1)} km/周`:''].filter(Boolean); return parts.length?`限额 ${parts.join(' · ')}`:'次数/公里数限额未返回' }

function middleWindow(plan:Plan) {
  const trim=(low:number,high:number):[number,number]=>{const margin=(high-low)*.15;return[low+margin,high-margin]}
  const rules=plan.rules
  const distance=selectableDistanceRange(plan)
  const [minDistance,maxDistance]=[distance.min,distance.max]
  const [fastest,slowest]=rules?.fastest_pace_sec_per_km>0&&rules.slowest_pace_sec_per_km>=rules.fastest_pace_sec_per_km?trim(rules.fastest_pace_sec_per_km,rules.slowest_pace_sec_per_km):[plan.profile.target_pace_sec_per_km,plan.profile.target_pace_sec_per_km]
  const [minCadence,maxCadence]=rules?.min_cadence_spm>0&&rules.max_cadence_spm>=rules.min_cadence_spm?trim(rules.min_cadence_spm,rules.max_cadence_spm):[plan.profile.cadence_spm,plan.profile.cadence_spm]
  return {min_distance_m:minDistance,max_distance_m:maxDistance,fastest_pace_sec_per_km:fastest,slowest_pace_sec_per_km:slowest,min_cadence_spm:minCadence,max_cadence_spm:maxCadence,min_stride_m:plan.profile.target_stride_m,max_stride_m:plan.profile.target_stride_m}
}
function selectableDistanceRange(plan:Plan) {
  const rules=plan.rules
  const rawMin=rules?.min_distance_m>0?rules.min_distance_m:plan.target_distance_m
  const rawMax=rules?.max_distance_m>=rawMin?rules.max_distance_m:plan.target_distance_m
  const min=Math.ceil((rawMin-1e-6)/100)*100
  const max=Math.floor((rawMax+1e-6)/100)*100
  return max>=min?{min,max}:{min:Math.round(plan.target_distance_m/100)*100,max:Math.round(plan.target_distance_m/100)*100}
}
function isCountBasedPlan(plan?:Plan|null) { return Number(plan?.rules?.plan_type || 0) === 2 }
function automationDefaultDistance(plan:Plan, selectedDistanceM=0) { const range=selectableDistanceRange(plan); return isCountBasedPlan(plan)?range.min:(selectedDistanceM||range.max) }
function automationDistanceLabel(job:Job, plan?:Plan) { const distance=plan?automationDefaultDistance(plan,job.target_distance_m):job.target_distance_m; return isCountBasedPlan(plan)?`最低 ${(distance/1000).toFixed(1)} km/次`:`${(distance/1000).toFixed(1)} km` }
function defaultDailyLimit(plan?:Plan|null) { return Math.max(0,Number(plan?.rules?.day_max_times||0)) }
function defaultWeeklyLimit(plan?:Plan|null) { const rules=plan?.rules; const min=Number(rules?.week_min_times||0); const max=Number(rules?.week_max_times||0); return min>0?min:max>0?1:0 }
function defaultDailyDistanceLimit(plan?:Plan|null) { return Math.max(0,Number(plan?.rules?.day_max_distance_m||0)) }
function defaultWeeklyDistanceLimit(plan?:Plan|null) { return Math.max(0,Number(plan?.rules?.week_max_distance_m||0)) }
function quotaLabel(job:Job, plan?:Plan) { const serverDayMax=defaultDailyLimit(plan); const daily=serverDayMax>0?(job.daily_run_limit||serverDayMax):0; const weekly=job.weekly_run_limit||defaultWeeklyLimit(plan); const dayDistance=job.daily_distance_limit_m||defaultDailyDistanceLimit(plan); const weekDistance=job.weekly_distance_limit_m||defaultWeeklyDistanceLimit(plan); return [daily>0?`${daily} 次/日`:'日次数不限',weekly>0?`${weekly} 次/周`:'',dayDistance>0?`${(dayDistance/1000).toFixed(1)} km/日`:'',weekDistance>0?`${(weekDistance/1000).toFixed(1)} km/周`:''].filter(Boolean).join(' · ') }
function planAccountingLabel(plan:Plan) { const rules=plan.rules; if(isCountBasedPlan(plan))return rules?.total_times?`按次数 · 共 ${rules.total_times} 次`:'按次数'; if(Number(rules?.plan_type||0)===3)return rules?.total_distance_m?`按公里数 · 共 ${(rules.total_distance_m/1000).toFixed(1)} km`:'按公里数'; return '考核方式待同步' }
function planAccountingDescription(plan:Plan) { if(isCountBasedPlan(plan))return `规则来源 ${plan.rules?.source||'待同步'} · 自动任务固定使用最低距离，其他指标在中间 70% 联动抽样`; return `规则来源 ${plan.rules?.source||'待同步'} · 距离按 100 米档位手选，其他指标在中间 70% 联动抽样` }
function distanceRule(plan:Plan) { const r=plan.rules; return r?.min_distance_m>0 ? `规定 ${(r.min_distance_m/1000).toFixed(2)}–${(r.max_distance_m/1000).toFixed(2)} km` : '距离规则待同步' }
function distanceRuleShort(plan:Plan) { const r=plan.rules; return r?.min_distance_m>0 ? `${(r.min_distance_m/1000).toFixed(1)}–${(r.max_distance_m/1000).toFixed(1)} km` : '规则待同步' }
function paceRule(plan:Plan) { const r=plan.rules; return r?.fastest_pace_sec_per_km>0 ? `规定 ${pace(r.fastest_pace_sec_per_km)}–${pace(r.slowest_pace_sec_per_km)}` : '配速规则待同步' }
function cadenceRule(plan:Plan) { const r=plan.rules; return r?.min_cadence_spm>0 ? `规定 ${r.min_cadence_spm.toFixed(0)}–${r.max_cadence_spm.toFixed(0)} spm` : '服务端未限制 · 联动推导' }
function strideRule(plan:Plan) { const limit=plan.rules?.max_segment_stride_m || 0; return limit>0 ? `分段上限 ${(limit*100).toFixed(1)} cm/步` : '步幅由配速×步频联动' }
function timingRule(plan:Plan) { const r=plan.rules; return r?.location_sample_interval_s ? `采样 ${r.location_sample_interval_s}s · 轨迹 ${r.poi_upload_interval_s}s · 配速 ${r.pace_segment_distance_m}m · 步数 ${r.step_segment_interval_s}s · 步幅 ${r.stride_segment_distance_m}m` : '采样/上传规则待同步' }

function extractRecords(value:RemoteRecordData): Record<string,unknown>[] {
  if (Array.isArray(value)) return value.filter(item => typeof item === 'object' && item !== null) as Record<string,unknown>[]
  if (!value || typeof value !== 'object') return []
  const object=value as Record<string,unknown>
  for(const key of ['list','records','items','data']){const nested=object[key];const result=extractRecords(nested as RemoteRecordData);if(result.length || Array.isArray(nested))return result}
  return []
}

function recordTime(item:Record<string,unknown>) {
  const raw=item.startTime || item.runStartTime || item.createTime || item.runTime
  if (typeof raw === 'number' || typeof raw === 'string') { const numeric=Number(raw); const source=Number.isFinite(numeric) ? (numeric<1e12 ? numeric*1000 : numeric) : raw; const date=new Date(source); if(!Number.isNaN(date.getTime()))return dateTime(date.toISOString()) }
  return '—'
}
function recordDistance(item:Record<string,unknown>) { const value=Number(item.distance ?? item.validDistance ?? item.runDistance); return Number.isFinite(value) ? `${(value/1000).toFixed(2)} km` : '—' }
function recordDuration(item:Record<string,unknown>) { const value=Number(item.duration ?? item.runDuration ?? item.time); return Number.isFinite(value) ? duration(value) : '—' }

function asRecord(value:unknown):Record<string,unknown>|null {
  if (!value || typeof value!=='object' || Array.isArray(value)) return null
  const object=value as Record<string,unknown>
  if (object.data && typeof object.data==='object' && !Array.isArray(object.data) && !('runRecordCode' in object) && !('pois' in object)) return asRecord(object.data)
  return object
}

function numberValue(object:Record<string,unknown>|null, keys:string[]) {
  if (!object) return NaN
  for (const key of keys) { const value=Number(object[key]); if (Number.isFinite(value)) return value }
  return NaN
}

function numberList(value:unknown) {
  if (!Array.isArray(value)) return []
  return value.map(item=>Number(item)).filter(item=>Number.isFinite(item))
}

function recordCode(item:Record<string,unknown>) { return String(item.runRecordCode || item.recordCode || item.id || '') }
function shortRecordCode(value:string) { if (!value) return '—'; return value.length>20 ? `${value.slice(0,8)}…${value.slice(-7)}` : value }
type RecordStatusTone='ok'|'pending'|'error'|'warn'|'neutral'

function recordStatusInfo(item:Record<string,unknown>): {label:string; description:string; tone:RecordStatusTone} {
  const value=Number(item.sunRunRecordStatus ?? item.status)
  switch(value){
    case 0:return {label:'处理中',description:'服务端尚未完成本次记录判定。',tone:'pending'}
    case 1:return {label:'计入成绩',description:'服务端已判定成绩有效并计入。',tone:'ok'}
    case 2:return {label:'跑步无效 · 不可申诉',description:'本次记录未计入成绩，客户端明确标记为不可申诉。',tone:'error'}
    case 3:return {label:'跑步无效 · 可申诉',description:'本次记录未计入成绩，可查看无效原因并发起申诉。',tone:'error'}
    case 4:return {label:'跑步无效 · 申诉中',description:'申诉已经提交并正在审核，当前记录仍未计入成绩。',tone:'pending'}
    case 5:return {label:'跑步无效 · 不可申诉',description:'本次记录未计入成绩，客户端当前不提供申诉入口。',tone:'error'}
    case 6:return {label:'计入成绩',description:'服务端已判定成绩有效并计入。',tone:'ok'}
    case 10:return {label:'无感识别中',description:'服务端正在进行无感识别，最终结果尚未确定。',tone:'pending'}
  }
  const label=item.validDesc || item.statusDesc || item.result
  if (typeof label==='string' && label.trim()) return {label:label.trim(),description:label.trim(),tone:item.warn===true?'warn':'neutral'}
  if (item.warn===true) return {label:'需复核',description:'服务端将本次记录标记为需要复核。',tone:'warn'}
  return Number.isFinite(value)
    ? {label:'未识别状态',description:`服务端返回了客户端尚未定义的状态代码 ${value}。`,tone:'neutral'}
    : {label:'—',description:'服务端未返回记录状态。',tone:'neutral'}
}

function recordNeedsInvalidReason(item:Record<string,unknown>) {
  const value=Number(item.sunRunRecordStatus ?? item.status)
  return value===2 || value===3 || value===4 || value===5
}

function remoteRecordReasons(item:Record<string,unknown>) {
  const result:string[]=[]
  const append=(value:unknown) => { if(typeof value==='string'){const text=value.trim();if(text && !result.includes(text))result.push(text)} }
  if(Array.isArray(item.invalidReasons)) for(const entry of item.invalidReasons){const reason=asRecord(entry);append(reason?.invalidDetail)}
  if(Array.isArray(item.statusLogRecords)) for(const entry of item.statusLogRecords){const log=asRecord(entry);if(Array.isArray(log?.remark))for(const remark of log.remark)append(remark)}
  if(Array.isArray(item.auditDetail)) for(const detail of item.auditDetail)append(detail)
  append(item.unAppealRemark)
  return result
}

function remoteRecordMetrics(detail:RemoteRecordData, fallback:Record<string,unknown>|null) {
  const source={...(fallback || {}),...(asRecord(detail) || {})}
  const distanceM=numberValue(source,['validDistance','distance','runDistance'])
  const durationS=numberValue(source,['duration','runDuration','time'])
  const paceSeconds=numberValue(source,['pace','averagePace','avgPace'])
  const steps=numberValue(source,['totalStep','totalSteps','stepCount','steps'])
  const cadence=numberValue(source,['frequency','cadence','averageCadence','avgCadence'])
  const rawStride=numberValue(source,['stride','averageStride','avgStride','stepLength'])
  const strideM=Number.isFinite(rawStride) && rawStride>0 ? (rawStride>10 ? rawStride/100 : rawStride) : distanceM>0 && steps>0 ? distanceM/steps : NaN
  return {
    distance:distanceM>0?`${(distanceM/1000).toFixed(2)} km`:'—',
    duration:durationS>=0?duration(durationS):'—',
    pace:paceSeconds>0?pace(paceSeconds):'—',
    steps:steps>=0?Math.round(steps).toLocaleString('zh-CN'):'—',
    cadence:cadence>0?`${Math.round(cadence)} spm`:'—',
    stride:strideM>0?`${(strideM*100).toFixed(1)} cm/步`:'—',
  }
}

function findPointArray(value:unknown, depth=0):unknown[] {
  if (depth>4 || !value || typeof value!=='object') return []
  if (Array.isArray(value)) {
    if (value.some(item=>asRecord(item) && ('lat' in (item as Record<string,unknown>) || 'latitude' in (item as Record<string,unknown>)))) return value
    for (const item of value) { const result=findPointArray(item,depth+1); if(result.length)return result }
    return []
  }
  const object=value as Record<string,unknown>
  for (const key of ['pois','pointList','trackList','locationList','routePoints','path','route']) { const result=findPointArray(object[key],depth+1); if(result.length)return result }
  return []
}

function remoteRecordRoute(detail:RemoteRecordData, fallback:Record<string,unknown>|null):Route|null {
  const object=asRecord(detail)
  const raw=findPointArray(object)
  const ordered=raw.map((value,index)=>({value:asRecord(value),index})).filter(item=>item.value).sort((left,right)=>{
    const a=numberValue(left.value,['index','collectTime','time']), b=numberValue(right.value,['index','collectTime','time'])
    return Number.isFinite(a)&&Number.isFinite(b)?a-b:left.index-right.index
  })
  const points:Point[]=ordered.map(item=>({lat:numberValue(item.value,['lat','latitude','y']),lng:numberValue(item.value,['lng','lon','longitude','x'])})).filter(point=>Number.isFinite(point.lat)&&Number.isFinite(point.lng)&&Math.abs(point.lat)<=90&&Math.abs(point.lng)<=180&&(point.lat!==0||point.lng!==0))
  if (points.length<2) return null
  const source={...(fallback || {}),...(object || {})}
  const distanceM=numberValue(source,['validDistance','distance','runDistance'])
  const durationS=numberValue(source,['duration','runDuration','time'])
  return {id:`record_${recordCode(source) || Date.now()}`,source:'server-record',coordinate_system:'GCJ-02',generated_at:new Date().toISOString(),target_distance_m:Number.isFinite(distanceM)?distanceM:0,distance_m:Number.isFinite(distanceM)?distanceM:0,estimated_duration_s:Number.isFinite(durationS)?durationS:0,points,quality:{distance_error_pct:0,retrace_ratio:0,outside_area_ratio:0,score:1}}
}
