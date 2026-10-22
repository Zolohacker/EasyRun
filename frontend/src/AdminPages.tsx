import SearchSelect from './SearchSelect'
import WalletNodeSettings from './WalletNodeSettings'
import WalletBackupPanel from './WalletBackupPanel'
import {ListViewport,usePagination} from './Pagination'
import {useCallback,useEffect,useState,type FormEvent} from 'react'
import {ArrowUpRight,CreditCard,Download,Mail,RefreshCw,ShieldCheck,UsersRound,Wallet} from 'lucide-react'
import {Field,Panel,Toggle} from './PortalPages'
import {externalHTTPURL,money,portalRequest,type PortalSettings,type PortalUser,type Sweep} from './portal'

type AdminProps={user:PortalUser;notify:(message:string)=>void}
type SettingsSection='access'|'mail'|'pricing'|'recharge'|'collection'
type RechargeCode={label:string;amount_micro:number;used_by:string;expires_at:string;batch_id:string}
const date=(value:string)=>new Date(value).toLocaleString('zh-CN')
const unitInput=(value:number,set:(value:number)=>void)=><input type="number" min="0" step="0.000001" value={value/1e6} onChange={e=>set(Math.round(Number(e.target.value)*1e6))}/>
function downloadText(text:string,name:string){const url=URL.createObjectURL(new Blob([text],{type:'text/plain;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}

function useAdminSettings({user,notify}:AdminProps){
  const superadmin=user.role==='superadmin'
  const [settings,setSettings]=useState<PortalSettings|null>(null),[password,setPassword]=useState(''),[busy,setBusy]=useState(false)
  const load=useCallback(async()=>{if(!superadmin)return;try{setSettings(await portalRequest<PortalSettings>('settings'))}catch(e){notify((e as Error).message)}},[superadmin,notify])
  useEffect(()=>{void load()},[load])
  const action=async(fn:()=>Promise<unknown>,message:string,reload=false)=>{
    setBusy(true)
    try{await fn();if(reload)await load();notify(message);return true}catch(e){notify((e as Error).message);return false}finally{setBusy(false)}
  }
  const save=async(event:FormEvent,section:SettingsSection)=>{
    event.preventDefault()
    if(await action(async()=>setSettings(await portalRequest<PortalSettings>('settings',{settings,password,section},'PUT')),'配置已保存并生效'))setPassword('')
  }
  return {settings,setSettings,password,setPassword,busy,superadmin,load,action,save}
}

function SettingsSave({password,setPassword,busy,label}:{password:string;setPassword:(value:string)=>void;busy:boolean;label:string}){
  return <section className="panel portal-save"><Field label="当前超级管理员密码"><input required type="password" autoComplete="current-password" value={password} onChange={e=>setPassword(e.target.value)}/></Field><button className="button primary" disabled={busy||!password}>{busy?'保存中…':label}</button></section>
}
function SuperadminNotice(){return <Panel kicker="SUPERADMIN ONLY" title="此配置由超级管理员维护"><p className="portal-hint">普通管理员可查看用户、管理账号、发行充值码和查看归集流水；站点模式、邮件、价格与钱包密钥由超管配置。</p></Panel>}
function SettingsLoading({onRetry}:{onRetry:()=>void}){return <Panel kicker="LOADING SETTINGS" title="正在读取配置"><button className="button subtle" onClick={onRetry}><RefreshCw/>重新读取</button></Panel>}

export function PortalSystemSettings({user,notify,section}:{section:'access'|'mail'}&AdminProps){
  const {settings:cfg,setSettings,password,setPassword,busy,superadmin,action,save,load}=useAdminSettings({user,notify})
  if(!superadmin)return <SuperadminNotice/>
  if(!cfg)return <SettingsLoading onRetry={()=>void load()}/>
  return <form className="page-stack" onSubmit={e=>void save(e,section)}>
    {section==='access'?<Panel kicker="DEPLOYMENT MODE" title="使用模式与客户入口"><div className="form-grid"><Field label="部署模式"><SearchSelect value={cfg.mode} onChange={e=>setSettings({...cfg,mode:e.target.value as PortalSettings['mode']})}><option value="personal">个人使用</option><option value="commercial">商业使用</option></SearchSelect></Field><div className="portal-config-links"><a href="/admin">管理后台 <ArrowUpRight/></a><a href="/app" target="_blank" rel="noreferrer">客户中心 <ArrowUpRight/></a></div></div><div className="portal-toggle-grid"><Toggle label="开启客户前台" checked={cfg.frontend_enabled} onChange={v=>setSettings({...cfg,frontend_enabled:v})}/><Toggle label="允许自助注册" checked={cfg.registration_enabled} onChange={v=>setSettings({...cfg,registration_enabled:v})}/><Toggle label="注册需要邮箱验证码" checked={cfg.email_verification} onChange={v=>setSettings({...cfg,email_verification:v})}/></div><p className="portal-hint">个人模式始终关闭客户注册与计费；新增管理员仍可使用管理后台。</p></Panel>:<Panel kicker="MAIL DELIVERY" title="邮件服务器"><div className="form-grid"><Field label="发信连接方式"><SearchSelect value={cfg.mail.protocol} onChange={e=>{const protocol=e.target.value as PortalSettings['mail']['protocol'];setSettings({...cfg,mail:{...cfg.mail,protocol,port:protocol==='smtps'?465:protocol==='starttls'?587:25}})}}><option value="starttls">STARTTLS · 加密发信（常用 587）</option><option value="smtps">SMTPS · 隐式 TLS（常用 465）</option><option value="smtp">SMTP · 可信中继（常用 25）</option></SearchSelect></Field><Field label="服务器主机"><input value={cfg.mail.host} onChange={e=>setSettings({...cfg,mail:{...cfg.mail,host:e.target.value}})} placeholder="smtp.example.com"/></Field><Field label="端口"><input type="number" min="1" max="65535" value={cfg.mail.port} onChange={e=>setSettings({...cfg,mail:{...cfg.mail,port:Number(e.target.value)}})}/></Field><Field label="发件邮箱"><input type="email" value={cfg.mail.from} onChange={e=>setSettings({...cfg,mail:{...cfg.mail,from:e.target.value}})}/></Field><Field label="SMTP 用户名"><input autoComplete="off" value={cfg.mail.username} onChange={e=>setSettings({...cfg,mail:{...cfg.mail,username:e.target.value}})}/></Field><Field label={`SMTP 密码 / 授权码${cfg.mail.password_configured?'（已保存，留空不变）':''}`}><input type="password" autoComplete="new-password" value={cfg.mail.password||''} onChange={e=>setSettings({...cfg,mail:{...cfg.mail,password:e.target.value}})}/></Field></div><div className="portal-panel-footer"><small>POP3（110/995）和 IMAP（143/993）用于收信，不负责发送注册验证码。发信请选择 SMTP 系列连接，并在邮箱服务商处开启 SMTP、获取授权码；SMTPS 与 STARTTLS 均验证服务器证书。请先保存，再测试已保存的配置。</small><button type="button" className="button subtle" disabled={busy} onClick={()=>void action(()=>portalRequest('mail/test',{}),'测试邮件已发送到超管邮箱')}><Mail/>测试已保存的配置</button></div></Panel>}
    <SettingsSave password={password} setPassword={setPassword} busy={busy} label={section==='access'?'保存使用模式与客户入口':'保存邮件配置'}/>
  </form>
}

export function BillingAdminPage(props:AdminProps){
  const [tab,setTab]=useState<'pricing'|'wallet'|'collection'|'codes'>('pricing')
  return <div className="page-stack"><div className="portal-page-tabs" role="tablist" aria-label="计费管理分类">{([['pricing','计费与发卡网'],['wallet','充值方式与钱包'],['collection','资金归集'],['codes','充值码']] as const).map(([key,label])=><button type="button" role="tab" aria-selected={tab===key} key={key} className={tab===key?'active':''} onClick={()=>setTab(key)}>{label}</button>)}</div>{tab==='pricing'?<PricingAdmin {...props}/>:tab==='wallet'?<WalletAdmin {...props}/>:tab==='collection'?<CollectionAdmin {...props}/>:<RechargeCodesAdmin {...props}/>}</div>
}

function PricingAdmin({user,notify}:AdminProps){
  const {settings:cfg,setSettings,password,setPassword,busy,superadmin,save,load}=useAdminSettings({user,notify})
  if(!superadmin)return <SuperadminNotice/>
  if(!cfg)return <SettingsLoading onRetry={()=>void load()}/>
  const shopURL=externalHTTPURL(cfg.pricing.recharge_code_shop_url)
  return <form className="page-stack" onSubmit={e=>void save(e,'pricing')}>
    <Panel kicker="PRICING ENGINE" title="多种计费方式"><Toggle label="开启商业计费（管理员任务不收费）" checked={cfg.pricing.enabled} onChange={v=>setSettings({...cfg,pricing:{...cfg.pricing,enabled:v}})}/><div className="portal-prices"><Field label="按次价格（USDT / 次）">{unitInput(cfg.pricing.per_run_micro,v=>setSettings({...cfg,pricing:{...cfg.pricing,per_run_micro:v}}))}</Field><Field label="按公里价格（USDT / km）">{unitInput(cfg.pricing.per_km_micro,v=>setSettings({...cfg,pricing:{...cfg.pricing,per_km_micro:v}}))}</Field><Field label="完整计划套餐（USDT / 账号计划）">{unitInput(cfg.pricing.plan_package_micro,v=>setSettings({...cfg,pricing:{...cfg.pricing,plan_package_micro:v}}))}</Field></div><p className="portal-hint">按次 / 公里在执行前预扣，提交失败或跳过退回；成功提交结算。完整计划套餐绑定具体账号和具体计划，不承诺考核结果；新计划需重新购买。</p></Panel>
    <Panel kicker="RECHARGE CODE SHOP" title="发卡网购买入口"><Field label="发卡网链接（可选）"><input type="url" maxLength={2048} value={cfg.pricing.recharge_code_shop_url||''} onChange={e=>setSettings({...cfg,pricing:{...cfg.pricing,recharge_code_shop_url:e.target.value}})} placeholder="https://你的发卡网/商品链接"/></Field><div className="portal-panel-footer"><p className="portal-hint">保存后，客户的“余额与计费 → 充值码兑换”会显示购买入口。购买后返回本平台兑换；留空则隐藏入口，不会自动向发卡网传递账号或登录凭据。</p>{shopURL&&<a className="button subtle" href={shopURL} target="_blank" rel="noopener noreferrer">预览链接<ArrowUpRight/></a>}</div></Panel>
    <SettingsSave password={password} setPassword={setPassword} busy={busy} label="保存计费与发卡网配置"/>
  </form>
}

function WalletAdmin({user,notify}:AdminProps){
  const {settings:cfg,setSettings,password,setPassword,busy,superadmin,action,save,load}=useAdminSettings({user,notify})
  const createSeed=()=>action(()=>portalRequest('wallet/seed',{password}),'钱包已创建，请导出助记词离线备份',true)
  if(!superadmin)return <SuperadminNotice/>
  if(!cfg)return <SettingsLoading onRetry={()=>void load()}/>
  return <form className="page-stack" onSubmit={e=>void save(e,'recharge')}>
    <Panel kicker="RECHARGE METHODS" title="客户可用充值方式"><div className="portal-toggle-grid"><Toggle label="启用 USDT TRC20 自助充值" checked={cfg.wallet.enabled} onChange={v=>setSettings({...cfg,wallet:{...cfg.wallet,enabled:v}})}/><Toggle label="开启充值码兑换" checked={cfg.pricing.recharge_code_enabled} onChange={value=>setSettings({...cfg,pricing:{...cfg.pricing,recharge_code_enabled:value}})}/></div><p className="portal-hint">两种充值方式独立开启；启用 USDT 前请先完成下方钱包配置。未开启的方式不会在客户端显示，也拒绝对应充值操作。关闭充值码后，发卡网购买入口一并隐藏。</p></Panel>
    <Panel kicker="HD WALLET · TRC20" title="自助充值钱包"><WalletNodeSettings wallet={cfg.wallet} onChange={wallet=>setSettings({...cfg,wallet})}/><WalletBackupPanel wallet={cfg.wallet} creating={busy} canCreate={Boolean(password)} onCreate={()=>void createSeed()}/>{!cfg.wallet.seed_configured&&<Field label="或导入现有 HD seed（十六进制，仅初始化一次）"><input type="password" autoComplete="new-password" value={cfg.wallet.seed_hex||''} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,seed_hex:e.target.value}})}/></Field>}<p className="portal-hint">仅固化成功且合约、收款地址、Transfer 事件匹配的充值入账。助记词 / 旧版 seed、数据库和 ACCOUNT_MASTER_KEY 请分别离线备份；自动归集默认关闭。</p></Panel>
    <SettingsSave password={password} setPassword={setPassword} busy={busy} label="保存充值方式与钱包配置"/>
  </form>
}

function CollectionAdmin({user,notify}:AdminProps){
  const {settings:cfg,setSettings,password,setPassword,busy,superadmin,action,save,load}=useAdminSettings({user,notify})
  const [sweeps,setSweeps]=useState<Sweep[]>([]),[manualPassword,setManualPassword]=useState('')
  const sweepPages=usePagination(sweeps)
  const loadSweeps=useCallback(async()=>{try{setSweeps(await portalRequest<Sweep[]>('wallet/sweeps'))}catch(e){notify((e as Error).message)}},[notify])
  useEffect(()=>{void loadSweeps();const timer=setInterval(()=>void loadSweeps(),15000);return()=>clearInterval(timer)},[loadSweeps])
  const run=async(fn:()=>Promise<unknown>,message:string)=>{if(await action(fn,message))await loadSweeps()}
  return <div className="page-stack">
    {superadmin?(cfg?<form className="page-stack" onSubmit={e=>void save(e,'collection')}>
      <Panel kicker="COLLECTION & ENERGY" title="能量租赁与资金归集"><div className="form-grid"><Field label="归集钱包地址"><input value={cfg.wallet.collection_address} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,collection_address:e.target.value}})} placeholder="TRON 钱包地址"/></Field><Field label={`Feee API key${cfg.wallet.feee_api_key_configured?'（已保存）':''}`}><input type="password" autoComplete="new-password" value={cfg.wallet.feee_api_key||''} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,feee_api_key:e.target.value}})}/></Field><Field label="最低归集余额（USDT）">{unitInput(cfg.wallet.min_collect_micro,v=>setSettings({...cfg,wallet:{...cfg.wallet,min_collect_micro:v}}))}</Field><Field label="单笔手续费上限（TRX）">{unitInput(cfg.wallet.fee_limit_sun,v=>setSettings({...cfg,wallet:{...cfg.wallet,fee_limit_sun:v}}))}</Field><Field label="能量报价上限（SUN / 能量）"><input type="number" min="1" value={cfg.wallet.energy_price_sun} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,energy_price_sun:Number(e.target.value)}})}/></Field><Field label="归集时区"><input value={cfg.wallet.timezone} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,timezone:e.target.value}})}/></Field><Field label="定时归集 Cron（分 时 日 月 周）"><input value={cfg.wallet.collect_cron} onChange={e=>setSettings({...cfg,wallet:{...cfg.wallet,collect_cron:e.target.value}})} placeholder="0 3 * * *"/></Field></div><Toggle label="开启定时归集（仅主网）" checked={cfg.wallet.auto_collect} onChange={v=>setSettings({...cfg,wallet:{...cfg.wallet,auto_collect:v}})}/><p className="portal-hint">主网归集先估算能量、检查报价，再租赁并等待到账。收款地址仍需要激活和少量 TRX 支付带宽；交易签名只在本机进行，私钥不提交到节点或 Feee。</p></Panel>
      <SettingsSave password={password} setPassword={setPassword} busy={busy} label="保存能量租赁与归集配置"/>
    </form>:<SettingsLoading onRetry={()=>void load()}/>):<SuperadminNotice/>}
    <Panel kicker="TREASURY OPERATIONS" title="手动归集与链上扫描"><p className="portal-hint">只有链上确认才标记归集完成。广播不等于到账；异常订单保留交易 ID 并等待核对，不会自动另签一笔。</p><div className="portal-inline">{superadmin&&<input type="password" aria-label="手动归集的超管密码" value={manualPassword} autoComplete="current-password" onChange={e=>setManualPassword(e.target.value)} placeholder="超级管理员密码（手动归集需要）"/>}<button className="button subtle" disabled={busy} onClick={()=>void run(()=>portalRequest('wallet/scan',{}),'充值扫描已排队')}><RefreshCw/>扫描充值</button>{superadmin&&<button className="button primary" disabled={busy||!manualPassword} onClick={()=>void run(async()=>{await portalRequest('wallet/collect',{password:manualPassword});setManualPassword('')},'归集任务已排队，请关注下方流水')}><Wallet/>一键归集</button>}</div></Panel>
    <Panel kicker="COLLECTION LEDGER" title="归集流水"><ListViewport pagination={sweepPages} className="table-scroll"><table><thead><tr><th>地址</th><th>金额 USDT</th><th>状态</th><th>交易与说明</th></tr></thead><tbody>{sweepPages.items.map(s=><tr key={s.id}><td><code className="portal-break">{s.address}</code></td><td>{money(s.amount_micro)}</td><td>{({queued:'排队',renting:'等待能量',signed:'已签名',broadcast:'等待确认',confirmed:'已确认',failed:'失败',needs_review:'待人工核对'} as Record<string,string>)[s.state]||s.state}</td><td><code className="portal-break">{s.tx_id}</code><span>{s.message}</span></td></tr>)}</tbody></table>{!sweeps.length&&<p className="portal-hint">暂无归集记录。钱包默认关闭，需要超管配置后启用。</p>}</ListViewport></Panel>
  </div>
}

function RechargeCodesAdmin({notify}:AdminProps){
  const [codes,setCodes]=useState<RechargeCode[]>([]),[generated,setGenerated]=useState<string[]>([]),[busy,setBusy]=useState(false)
  const [codeCount,setCodeCount]=useState(10),[codeAmount,setCodeAmount]=useState(10),[codeDays,setCodeDays]=useState(365)
  const codePages=usePagination(codes)
  const load=useCallback(async()=>{try{setCodes(await portalRequest<RechargeCode[]>('codes'))}catch(e){notify((e as Error).message)}},[notify])
  useEffect(()=>{void load()},[load])
  const action=async(fn:()=>Promise<unknown>,message:string)=>{setBusy(true);try{await fn();notify(message);await load()}catch(e){notify((e as Error).message)}finally{setBusy(false)}}
  return <div className="page-stack"><><Panel kicker="RECHARGE CODES" title="一键生成充值码"><form className="portal-user-form" onSubmit={e=>{e.preventDefault();void action(async()=>{const value=await portalRequest<{codes:string[]}>('codes',{count:codeCount,amount_micro:Math.round(codeAmount*1e6),days:codeDays});setGenerated(value.codes)},'充值码已生成，仅本次展示完整内容')}}><Field label="数量（1–100）"><input required type="number" min="1" max="100" value={codeCount} onChange={e=>setCodeCount(Number(e.target.value))}/></Field><Field label="每张面值（USDT）"><input required type="number" min="0.000001" step="0.000001" value={codeAmount} onChange={e=>setCodeAmount(Number(e.target.value))}/></Field><Field label="有效天数"><input required type="number" min="1" max="3650" value={codeDays} onChange={e=>setCodeDays(Number(e.target.value))}/></Field><button className="button primary" disabled={busy}><CreditCard/>生成</button></form>{generated.length>0&&<div className="portal-secret"><div className="action-row"><strong>{generated.length} 张充值码，完整码仅展示本次</strong><button className="button subtle" onClick={()=>downloadText(generated.join('\n'),'easyrun-recharge-codes.txt')}><Download/>下载</button><button className="button subtle" onClick={()=>setGenerated([])}>隐藏</button></div><pre>{generated.join('\n')}</pre></div>}</Panel><Panel kicker="ISSUANCE HISTORY" title="充值码发行记录"><ListViewport pagination={codePages} className="table-scroll"><table><thead><tr><th>码前缀</th><th>面值 USDT</th><th>到期时间</th><th>状态</th></tr></thead><tbody>{codePages.items.map((c,i)=><tr key={c.batch_id+c.label+i}><td><code>{c.label}…</code></td><td>{money(c.amount_micro)}</td><td>{date(c.expires_at)}</td><td>{c.used_by?'已兑换':new Date(c.expires_at).getTime()<Date.now()?'已过期':'未使用'}</td></tr>)}</tbody></table></ListViewport></Panel></></div>
}

export function UsersAdminPage({user,notify}:AdminProps){
  const superadmin=user.role==='superadmin'
  const [users,setUsers]=useState<PortalUser[]>([]),[busy,setBusy]=useState(false)
  const [newEmail,setNewEmail]=useState(''),[newPassword,setNewPassword]=useState(''),[newRole,setNewRole]=useState('admin')
  const userPages=usePagination(users)
  const load=useCallback(async()=>{try{setUsers(await portalRequest<PortalUser[]>('users'))}catch(e){notify((e as Error).message)}},[notify])
  useEffect(()=>{void load()},[load])
  const action=async(fn:()=>Promise<unknown>,message:string)=>{setBusy(true);try{await fn();notify(message);await load()}catch(e){notify((e as Error).message)}finally{setBusy(false)}}
  return <div className="page-stack"><section className="page-toolbar"><button className="button subtle" disabled={busy} onClick={()=>void load()}><RefreshCw/>刷新用户</button></section><Panel kicker="IDENTITY & ROLES" title="用户与管理员">{superadmin&&<form className="portal-user-form" onSubmit={e=>{e.preventDefault();void action(async()=>{await portalRequest('users',{email:newEmail,password:newPassword,role:newRole});setNewEmail('');setNewPassword('')},'用户已创建')}}><Field label="邮箱"><input required type="email" value={newEmail} onChange={e=>setNewEmail(e.target.value)}/></Field><Field label="初始密码（至少 12 字节）"><input required type="password" minLength={12} maxLength={72} autoComplete="new-password" value={newPassword} onChange={e=>setNewPassword(e.target.value)}/></Field><Field label="角色"><SearchSelect value={newRole} onChange={e=>setNewRole(e.target.value)}><option value="admin">管理员</option><option value="customer">客户</option></SearchSelect></Field><button className="button primary" disabled={busy}><UsersRound/>添加</button></form>}<ListViewport pagination={userPages} className="table-scroll"><table><thead><tr><th>邮箱</th><th>角色</th><th>余额 USDT</th><th>状态</th><th>操作</th></tr></thead><tbody>{userPages.items.map(u=><tr key={u.id}><td>{u.email}</td><td>{{superadmin:'超级管理员',admin:'管理员',customer:'客户'}[u.role]}</td><td>{money(u.balance_micro)}</td><td>{u.enabled?'启用':'已停用'}</td><td>{superadmin&&u.role!=='superadmin'&&<button className="button subtle" disabled={busy} onClick={()=>void action(()=>portalRequest(`users/${u.id}`,{enabled:!u.enabled},'PUT'),'用户状态已更新')}>{u.enabled?'停用':'启用'}</button>}</td></tr>)}</tbody></table></ListViewport></Panel></div>
}
