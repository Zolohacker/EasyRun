import {accountSelectorLabel} from './accountLabels'
import Modal from './Modal'
import TaskPrepayPanel from './TaskPrepayPanel'
import DepositPaymentModal from './DepositPaymentModal'
import './wallet.css'
import {ListViewport,usePagination} from './Pagination'
import {useCallback,useEffect,useState,type FormEvent,type ReactNode} from 'react'
import BrandMark from './BrandMark'
import {ArrowUpRight,Check,CreditCard,ImagePlus,ShieldCheck,Wallet,CalendarCheck,QrCode,ReceiptText} from 'lucide-react'
import {externalHTTPURL,money,portalRequest,type BillingSummary} from './portal'
import type {Account,Job,Plan} from './types'

export function Field({label,children}:{label:string;children:ReactNode}){return <label className="field"><span>{label}</span>{children}</label>}
export function Toggle({label,checked,onChange}:{label:string;checked:boolean;onChange:(value:boolean)=>void}){return <label className="portal-toggle"><input type="checkbox" checked={checked} onChange={e=>onChange(e.target.checked)}/><span>{label}</span></label>}
export function Panel({title,children}:{title:string;children:ReactNode}){return <section className="panel portal-section"><div className="panel-heading"><div><h2>{title}</h2></div></div>{children}</section>}
const date=(v:string)=>new Date(v).toLocaleString('zh-CN')

export function WalletPage({accounts,plans,jobs,notify}:{accounts:Account[];plans:Plan[];jobs:Job[];notify:(message:string)=>void}){
  const [data,setData]=useState<BillingSummary|null>(null),[code,setCode]=useState(''),[busy,setBusy]=useState(false)
  const load=useCallback(async()=>{try{setData(await portalRequest<BillingSummary>('billing'))}catch(e){notify((e as Error).message)}},[notify]);useEffect(()=>{void load()},[load])
  const act=async(fn:()=>Promise<unknown>,message:string)=>{setBusy(true);try{await fn();notify(message);await load()}catch(e){notify((e as Error).message)}finally{setBusy(false)}}
  const ledgerPages=usePagination(data?.ledger||[])
  const packagePages=usePagination(plans.filter(plan=>accounts.some(account=>account.id===plan.source_account_id)))
  const [tab,setTab]=useState<'topup'|'billing'|'packages'|'ledger'>('topup')
  const [depositOpen,setDepositOpen]=useState(false)
  const openDeposit=async()=>{
    if(busy||!data?.wallet_enabled)return
    setBusy(true)
    try{
      if(!data.deposit_address){const value=await portalRequest<{address:string}>('wallet/address',{});if(!value.address)throw new Error('收款地址尚未生成，请稍后重试');setData(current=>current?{...current,deposit_address:value.address}:current)}
      setDepositOpen(true)
    }catch(e){notify((e as Error).message)}finally{setBusy(false)}
  }
  const [creditJob,setCreditJob]=useState<Job|null>(null)
  if(!data)return <div className="panel portal-section">正在读取钱包…</div>
  const shopURL=externalHTTPURL(data.pricing.recharge_code_shop_url)
  const methods=Number(data.wallet_enabled)+Number(data.pricing.recharge_code_enabled)
  return <div className="page-stack customer-wallet">
    <div className="portal-balance-card"><div><h2>{money(data.user.balance_micro)} <small>USDT</small></h2><p>账户余额 · 支出与充值逐笔记录</p></div><Wallet/></div>
    <div className="portal-page-tabs" role="tablist" aria-label="余额与计费分类">{([['topup','充值'],['billing','任务计费'],['packages','计划套餐'],['ledger','余额流水']] as const).map(([key,label])=><button type="button" role="tab" aria-selected={tab===key} key={key} className={tab===key?'active':''} onClick={()=>setTab(key)}>{label}</button>)}</div>
    {tab==='topup'&&methods===0&&<div className="portal-notice">当前未开放自助充值，请联系管理员。</div>}
    {tab==='topup'&&methods>0&&<div className={`payment-method-grid ${methods===1?'single':''}`}>
      {data.wallet_enabled&&<section className="panel payment-method-card">
        <header className="payment-method-head"><span className="payment-method-icon"><QrCode/></span><div><h2>USDT 充值</h2><p>专属地址 · 链上确认到账</p></div></header>
        <div className="payment-method-content"><div className="payment-method-description"><strong>扫码或复制地址转账</strong><p>打开收款窗口后自动检查充值，到账后同步更新余额。</p></div><button type="button" className="button primary" disabled={busy} onClick={()=>void openDeposit()}><QrCode size={17}/>{busy?'正在准备…':'打开收款窗口'}</button></div>
        <footer className="payment-method-footer"><span className={`status-pill ${data.network==='mainnet'?'ok':'warn'}`}>{data.network==='mainnet'?'TRON 主网':`${data.network} 测试网 · 请勿转入真实资金`}</span><span>TRC20</span></footer>
      </section>}
      {data.pricing.recharge_code_enabled&&<section className="panel payment-method-card">
        <header className="payment-method-head"><span className="payment-method-icon code"><CreditCard/></span><div><h2>充值码兑换</h2><p>输入充值码 · 余额即时可用</p></div></header>
        <form className="payment-method-content" onSubmit={e=>{e.preventDefault();void act(async()=>{await portalRequest('redeem',{code});setCode('')},'充值码兑换成功')}}><Field label="充值码"><input required value={code} onChange={e=>setCode(e.target.value)} placeholder="输入 ER- 开头的充值码" autoComplete="off"/></Field><button className="button primary" disabled={busy||!code.trim()}><CreditCard size={17}/>兑换到余额</button></form>
        <footer className="payment-method-footer">{shopURL?<><span>还没有充值码？</span><a href={shopURL} target="_blank" rel="noopener noreferrer">前往购买<ArrowUpRight/></a></>:<><ShieldCheck/><span>每张充值码仅可兑换一次</span></>}</footer>
      </section>}
    </div>}
    {depositOpen&&data.wallet_enabled&&data.deposit_address&&<DepositPaymentModal address={data.deposit_address} network={data.network} onClose={()=>setDepositOpen(false)} onRefresh={load} notify={notify}/>}
    {tab==='billing'&&<Panel title="任务下单额度"><div className="portal-rate-cards"><div><CalendarCheck/><span>按次</span><b>{money(data.pricing.per_run_micro)} <small>USDT / 次</small></b></div><div><ArrowUpRight/><span>按公里</span><b>{money(data.pricing.per_km_micro)} <small>USDT / km</small></b></div><div><ShieldCheck/><span>完整跑步计划</span><b>{money(data.pricing.plan_package_micro)} <small>USDT / 账号计划</small></b></div></div>{!data.pricing.enabled&&<p className="portal-hint">当前暂停新预扣；已有订单仍可查看和退回未执行额度。</p>}{jobs.length>0?<div className="portal-job-modes">{jobs.map(j=><div key={j.id}><div><strong>{accounts.find(a=>a.id===j.account_id)?accountSelectorLabel(accounts.find(a=>a.id===j.account_id)!):'账号已移除'}</strong><span>{plans.find(p=>p.id===j.plan_id)?.name}</span></div><button className="button subtle" onClick={()=>setCreditJob(j)}>查看 / 调整额度</button></div>)}</div>:<p className="portal-hint">在自动任务中新建任务并下单。</p>}<p className="portal-hint">下单预扣，成功后结算。可调整次数或公里总量；执行中与已结算部分不退，失败后额度恢复为可用。已购完整套餐优先使用。</p></Panel>}
    {creditJob&&<Modal title="调整下单额度" onClose={()=>{setCreditJob(null);void load()}}><TaskPrepayPanel jobID={creditJob.id} notify={notify}/></Modal>}
    {tab==='packages'&&<Panel title="购买完整跑步计划"><ListViewport pagination={packagePages} className="compact-list"><div className="portal-package-list">{packagePages.items.map(p=>{const a=accounts.find(a=>a.id===p.source_account_id);if(!a)return null;const owned=data.entitlements.some(e=>e.account_id===a.id&&e.plan_id===p.id);return <div key={p.id}><div><strong>{a.name} · {p.name}</strong><span>仅限当前账号与计划使用</span></div><button className="button subtle" disabled={busy||owned||!data.pricing.enabled} onClick={()=>void act(()=>portalRequest('packages',{account_id:a.id,plan_id:p.id}),'完整计划套餐已购买')}>{owned?<><Check/>已购买</>:`购买 · ${money(data.pricing.plan_package_micro)} USDT`}</button></div>})}</div></ListViewport></Panel>}
    {tab==='ledger'&&<Panel title="余额流水">{data.ledger.length>0?<ListViewport pagination={ledgerPages} className="table-scroll"><table className="mobile-card-table" aria-label="余额流水"><thead><tr><th>时间</th><th>类型</th><th>变动 USDT</th><th>流水参考</th></tr></thead><tbody>{ledgerPages.items.map(l=><tr key={l.id}><td data-label="时间" className="mobile-wide-cell">{date(l.created_at)}</td><td data-label="类型">{({code_redeem:'充值码兑换',trc20_deposit:'链上充值',task_prepay:'下单预扣',task_release:'退回未执行预扣',task_settle:'成功结算',run_reserve:'任务预扣',run_refund:'任务退款',plan_package:'完整计划套餐'} as Record<string,string>)[l.kind]||l.kind}</td><td data-label="变动 USDT" className={l.delta_micro>=0?'credit':'debit'}>{l.delta_micro>=0?'+':''}{money(l.delta_micro)}{l.kind==='task_settle'&&<small className="ledger-settlement">结算 {money(l.funding_amount_micro||0)} USDT · 不再扣余额</small>}</td><td data-label="流水参考" className="mobile-wide-cell"><code className="portal-break">{l.reference}</code></td></tr>)}</tbody></table></ListViewport>:<div className="wallet-ledger-empty"><span className="wallet-ledger-empty-icon"><ReceiptText/></span><h3>还没有收支记录</h3><p>充值、下单和退款记录会显示在这里。</p>{methods>0&&<button type="button" className="button subtle" onClick={()=>setTab('topup')}>去充值<ArrowUpRight size={15}/></button>}</div>}</Panel>}
  </div>
}

export function PublicPortraitPage(){
  const id=location.pathname.split('/')[2]||'',token=new URLSearchParams(location.hash.slice(1)).get('token')||''
  const [valid,setValid]=useState(false),[loading,setLoading]=useState(true),[file,setFile]=useState<File|null>(null),[preview,setPreview]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false)
  useEffect(()=>{const controller=new AbortController();void fetch(`/api/v1/public/face/${encodeURIComponent(id)}`,{headers:{Authorization:`Bearer ${token}`},signal:controller.signal}).then(async r=>{const body=await r.json();if(!r.ok)throw new Error(body?.error?.message||'链接无效');setValid(true)}).catch(e=>{if(e.name!=='AbortError')setMessage(e.message)}).finally(()=>setLoading(false));return()=>controller.abort()},[id,token])
  useEffect(()=>{if(!file){setPreview('');return};const u=URL.createObjectURL(file);setPreview(u);return()=>URL.revokeObjectURL(u)},[file])
  const submit=async(e:FormEvent)=>{e.preventDefault();if(!file)return;setBusy(true);setMessage('');try{const r=await fetch(`/api/v1/public/face/${encodeURIComponent(id)}`,{method:'PUT',headers:{Authorization:`Bearer ${token}`,'Content-Type':file.type},body:file});const body=await r.json();if(!r.ok)throw new Error(body?.error?.message||'上传失败');setMessage('照片更新成功。你可以关闭此页面。')}catch(e){setMessage((e as Error).message)}finally{setBusy(false)}}
  return <main className="public-portrait-page"><a className="portal-brand" href="/"><span><BrandMark/></span><b>EasyRun<small>PORTRAIT UPLOAD</small></b></a><form className="panel public-portrait-card" onSubmit={submit}><h1>更新账号人脸照片</h1><p>无需登录平台，仅更新此链接对应账号的照片。</p>{loading?<p>正在检查链接…</p>:valid?<><div className="portal-warning"><ShieldCheck/><span>请勿泄露本链接，持有链接的人可以修改照片。仅上传账号本人已同意使用的照片。</span></div><div className="portal-portrait-preview">{preview?<img src={preview} alt="待上传的完整照片预览"/>:<><ImagePlus/><span>选择照片后在此完整预览</span></>}</div><label className="button subtle portal-file-button"><ImagePlus/>选择 JPEG / PNG 照片<input type="file" required accept="image/jpeg,image/png" onChange={e=>{setFile(e.target.files?.[0]||null);setMessage('')}}/></label><small>最大 6 MB，不超过 1600 万像素；上传后会移除照片元数据。</small><button className="button primary wide" disabled={!file||busy}>{busy?'正在更新…':'确认更新照片'}</button></>:null}{message&&<div className={valid?'portal-notice':'portal-error'} role="status">{message}</div>}</form><small>EasyRun · 照片加密保存</small></main>
}
