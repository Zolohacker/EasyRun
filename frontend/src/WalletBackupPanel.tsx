import {useEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import {Download,KeyRound,ShieldCheck,X} from 'lucide-react'
import {Field} from './PortalPages'
import {portalRequest,type PortalSettings,type WalletBackup} from './portal'
import {useOverlayScrollLock} from './Modal'

export default function WalletBackupPanel({wallet,onCreate,creating,canCreate}:{wallet:PortalSettings['wallet'];onCreate:()=>void;creating:boolean;canCreate:boolean}){
  const [open,setOpen]=useState(false),[password,setPassword]=useState(''),[ack,setAck]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[backup,setBackup]=useState<WalletBackup|null>(null)
  const passwordInput=useRef<HTMLInputElement>(null),dialog=useRef<HTMLDivElement>(null),request=useRef(0)
  useOverlayScrollLock(open)
  const close=()=>{request.current++;setOpen(false);setPassword('');setAck(false);setError('');setBackup(null);setBusy(false)}
  useEffect(()=>()=>{request.current++},[])
  useEffect(()=>{
    if(!open)return
    const previous=document.activeElement as HTMLElement|null
    passwordInput.current?.focus()
    const key=(event:KeyboardEvent)=>{
      if(event.key==='Escape'){event.preventDefault();close()}
      if(event.key==='Tab'){
        const elements=Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),a[href]')||[])
        const first=elements[0],last=elements[elements.length-1]
        if(!dialog.current?.contains(document.activeElement)){event.preventDefault();first?.focus()}
        else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus()}
        else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
      }
    }
    document.addEventListener('keydown',key)
    return()=>{document.removeEventListener('keydown',key);previous?.focus()}
  },[open])
  useEffect(()=>{
    if(!backup)return
    dialog.current?.querySelector<HTMLButtonElement>('.wallet-backup-content button')?.focus()
    const timeout=setTimeout(close,60000)
    const hidden=()=>{if(document.hidden)close()}
    document.addEventListener('visibilitychange',hidden)
    return()=>{clearTimeout(timeout);document.removeEventListener('visibilitychange',hidden)}
  },[backup])
  const reveal=async()=>{
    if(!ack||!password||busy)return
    const id=++request.current;setBusy(true);setError('')
    try{const value=await portalRequest<WalletBackup>('wallet/export',{password});if(request.current===id){setBackup(value);setPassword('')}}catch(e){if(request.current===id)setError((e as Error).message)}finally{if(request.current===id)setBusy(false)}
  }
  const download=()=>{
    if(!backup)return
    const text=[`EasyRun 钱包离线备份`, `类型: ${backup.kind==='bip39'?'BIP39 英文助记词':'旧版原始 seed'}`,`网络: ${backup.network}`,`派生路径: ${backup.derivation_path}`,backup.kind==='bip39'?`BIP39 附加口令: 空（不是超级管理员密码）`:`请直接使用原始 seed 恢复，不要将其当作 BIP39 熵。`,``,backup.mnemonic||backup.seed_hex||'',``,`此备份控制本部署全部客户收款地址；请离线妥善保管。另行备份数据库与 ACCOUNT_MASTER_KEY。`].join('\n')
    const url=URL.createObjectURL(new Blob([text],{type:'text/plain;charset=utf-8'}));const anchor=document.createElement('a');anchor.href=url;anchor.download=`easyrun-wallet-${backup.network}-backup.txt`;anchor.click();setTimeout(()=>URL.revokeObjectURL(url),1000)
  }
  return <>
    <div className="portal-wallet-security"><ShieldCheck/><div><strong>{wallet.seed_configured?(wallet.mnemonic_configured?'BIP39 助记词钱包已建立':'旧版 seed 钱包已保留'):'尚未创建收款钱包'}</strong><p>{wallet.seed_configured?(wallet.mnemonic_configured?'24 词助记词已加密保存，请立即导出并离线备份。':'此钱包原先直接使用 seed 创建，请导出原始 seed 备份，不会替换种子或生成另一组地址。'):'输入下方超管密码后创建标准 24 词钱包，或导入现有 seed。'}</p><small>派生路径 m/44'/195'/0'/0/index · 每位客户独立地址 · 已有种子禁止覆盖</small></div>{wallet.seed_configured?<button type="button" className="button subtle" onClick={()=>setOpen(true)}><KeyRound/>{wallet.mnemonic_configured?'导出助记词':'导出 seed 备份'}</button>:<button type="button" className="button subtle" disabled={creating||!canCreate} onClick={onCreate}>创建助记词钱包</button>}</div>
    {open&&createPortal(<div className="modal-backdrop" onClick={e=>{if(e.target===e.currentTarget)close()}}><div ref={dialog} className="modal wallet-backup-modal" role="dialog" aria-modal="true" aria-labelledby="wallet-backup-title"><div className="modal-head"><h2 id="wallet-backup-title">{wallet.mnemonic_configured?'导出钱包助记词':'导出旧版 seed 备份'}</h2><button type="button" className="mini-button" aria-label="关闭钱包备份" onClick={close}><X/></button></div><div className="wallet-backup-warning"><ShieldCheck/><p>此备份可以控制本部署的全部客户收款地址。请仅在可信设备查看并离线保存，不要发送给他人、上传聊天或云盘。</p></div>{!backup?<div className="form-stack"><Field label="重新输入超级管理员密码"><input ref={passwordInput} type="password" autoComplete="current-password" value={password} onChange={e=>setPassword(e.target.value)} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();void reveal()}}}/></Field><label className="portal-toggle"><input type="checkbox" checked={ack} onChange={e=>setAck(e.target.checked)}/><span>我了解此备份控制全部收款地址，并会离线妥善保管</span></label>{error&&<p className="portal-error" role="alert">{error}</p>}<button type="button" className="button primary" disabled={busy||!ack||!password} onClick={()=>void reveal()}>{busy?'校验中…':'验证密码并显示备份'}</button></div>:<div className="wallet-backup-content"><p className="portal-hint">{backup.kind==='bip39'?'24 词英文助记词，BIP39 附加口令为空；不是超管密码。':'旧版原始 seed，请使用支持直接导入 seed 的工具恢复。它不是 BIP39 助记词。'} 内容将在 60 秒后自动关闭，切换页面也会隐藏。</p>{backup.mnemonic?<ol className="wallet-mnemonic-grid">{backup.mnemonic.split(' ').map((word,index)=><li key={index}><span>{index+1}</span><b>{word}</b></li>)}</ol>:<code className="wallet-legacy-seed">{backup.seed_hex}</code>}<div className="wallet-backup-meta"><span>网络：{backup.network}</span><code>{backup.derivation_path}</code></div><div className="action-row"><button type="button" className="button primary" onClick={download}><Download/>下载离线备份</button><button type="button" className="button subtle" onClick={close}>我已备份，隐藏</button></div></div>}</div></div>,document.body)}
  </>
}
