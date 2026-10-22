import {useEffect,useState} from 'react'
import {CheckCircle2,RefreshCw,Server,XCircle} from 'lucide-react'
import SearchSelect from './SearchSelect'
import {Field} from './PortalPages'
import {portalRequest,type PortalSettings,type WalletNodeProbe} from './portal'

type WalletConfig=PortalSettings['wallet']
const tokenPocketURL='https://trx.mytokenpocket.vip'
const indexers={mainnet:tokenPocketURL,shasta:'https://api.shasta.trongrid.io',nile:'https://nile.trongrid.io'}
const providerNames={tokenpocket:'TokenPocket',trongrid:'TronGrid',nownodes:'NOWNodes',quicknode:'QuickNode',getblock:'GetBlock',custom:'自定义 / 自建节点'}
const endpointHint={nownodes:'https://trx.nownodes.io',quicknode:'https://你的端点.tron-mainnet.quiknode.pro/访问令牌',getblock:'https://go.getblock.io/访问令牌',custom:'https://你的 TRON REST 节点'}

export default function WalletNodeSettings({wallet:w,onChange}:{wallet:WalletConfig;onChange:(wallet:WalletConfig)=>void}){
  const [probing,setProbing]=useState(false),[probe,setProbe]=useState<WalletNodeProbe|null>(null),[error,setError]=useState('')
  const provider=w.node_provider||'trongrid'
  useEffect(()=>{setProbe(null);setError('')},[w.network,w.node_provider,w.node_url,w.node_api_key,w.rpc_url,w.rpc_api_key,w.rpc_api_header,w.rpc_url_configured])
  const selectProvider=(next:WalletConfig['node_provider'])=>onChange({...w,node_provider:next,...(next==='tokenpocket'?{node_url:tokenPocketURL,node_api_key:'',node_api_key_configured:false}:next==='trongrid'?{node_url:w.network==='mainnet'?'https://api.trongrid.io':indexers[w.network],node_api_key:'',node_api_key_configured:false}:{}),rpc_url:next==='nownodes'?'https://trx.nownodes.io':'',rpc_url_configured:false,rpc_url_host:'',rpc_api_key:'',rpc_api_key_configured:false,rpc_api_header:next==='nownodes'?'api-key':next==='custom'?'TRON-PRO-API-KEY':''})
  const test=async()=>{
    setProbing(true);setError('');setProbe(null)
    try{setProbe(await portalRequest<WalletNodeProbe>('wallet/probe',{}))}catch(e){setError((e as Error).message)}finally{setProbing(false)}
  }
  return <div className="wallet-node-settings">
    <div className="form-grid">
      <Field label="网络"><SearchSelect value={w.network} onChange={e=>{const network=e.target.value as WalletConfig['network'];onChange({...w,network,node_url:indexers[network],node_api_key:'',node_api_key_configured:false,node_provider:network==='mainnet'?'tokenpocket':'trongrid',rpc_url:'',rpc_url_configured:false,rpc_url_host:'',rpc_api_key:'',rpc_api_key_configured:false,rpc_api_header:'',contract:network==='mainnet'?'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t':''})}}><option value="shasta">Shasta 测试网</option><option value="nile">Nile 测试网</option><option value="mainnet">TRON 主网</option></SearchSelect></Field>
      <Field label="TRC20 USDT 合约（主网固定）"><input readOnly={w.network==='mainnet'} value={w.contract} onChange={e=>onChange({...w,contract:e.target.value})}/></Field>
      <Field label="链上 RPC 服务商"><SearchSelect value={provider} onChange={e=>selectProvider(e.target.value as WalletConfig['node_provider'])}><option value="tokenpocket" disabled={w.network!=='mainnet'}>TokenPocket · 默认主网 · 免 Key</option><option value="trongrid">TronGrid · 索引与 RPC 一体</option><option value="nownodes" disabled={w.network!=='mainnet'}>NOWNodes · 主网</option><option value="quicknode" disabled={w.network==='shasta'}>QuickNode · 主网 / Nile</option><option value="getblock" disabled={w.network==='shasta'}>GetBlock · 主网 / Nile</option><option value="custom">自定义 / 自建 · TRON HTTP REST</option></SearchSelect></Field>
    </div>
    {provider==='tokenpocket'&&<div className="wallet-node-preset"><Server/><div><strong>TokenPocket · 默认主网节点</strong><code>{tokenPocketURL}</code><p>RPC 固定使用此地址，默认索引与固化确认也使用同一服务，无需 API Key。第三方服务可用性与限流规则可能变化，可随时切换其他节点。</p></div></div>}
    {provider!=='trongrid'&&provider!=='tokenpocket'&&<div className="wallet-node-rpc">
      <div className="form-grid">
        <Field label={`RPC 完整 HTTPS 端点${w.rpc_url_configured?`（已保存：${w.rpc_url_host}）`:''}`}><input type="password" autoComplete="new-password" value={w.rpc_url||''} readOnly={provider==='nownodes'} placeholder={w.rpc_url_configured?'留空保留已保存的完整端点':endpointHint[provider]} onChange={e=>onChange({...w,rpc_url:e.target.value})}/></Field>
        {provider==='custom'&&<Field label="RPC 认证请求头"><SearchSelect value={w.rpc_api_header} onChange={e=>onChange({...w,rpc_api_header:e.target.value,rpc_api_key:'',rpc_api_key_configured:false})}><option value="">无需请求头 / 令牌已在 URL 路径</option><option value="TRON-PRO-API-KEY">TRON-PRO-API-KEY</option><option value="api-key">api-key</option><option value="X-API-Key">X-API-Key</option><option value="Authorization">Authorization（填写完整 Bearer 值）</option></SearchSelect></Field>}
        {(provider==='nownodes'||(provider==='custom'&&w.rpc_api_header))&&<Field label={`RPC API Key${w.rpc_api_key_configured?'（已保存，同端点留空不变）':''}`}><input type="password" autoComplete="new-password" value={w.rpc_api_key||''} onChange={e=>onChange({...w,rpc_api_key:e.target.value})}/></Field>}
      </div>
      <p className="portal-hint">{provider==='quicknode'||provider==='getblock'?'请从服务商控制台复制所选网络的 TRON HTTP REST 完整端点（含路径令牌），不要使用 EVM JSON-RPC / WebSocket 地址。':'RPC 与下方索引服务使用各自的 API Key，请分别配置。'} 完整端点与密钥均加密保存，不回显路径令牌；更换服务商或端点时请重新填写对应密钥。</p>
    </div>}
    <div className="wallet-node-indexer">
      <div className="wallet-node-heading"><Server/><div><strong>交易索引与确认服务</strong><p>充值发现使用 TronGrid V1；最终入账核对使用固化交易回执。更换 RPC 不替换此服务。</p></div></div>
      <div className="form-grid">
        <Field label="索引服务 HTTPS 地址（TronGrid V1 兼容）"><input type="url" value={w.node_url} onChange={e=>onChange({...w,node_url:e.target.value,node_api_key:'',node_api_key_configured:false})}/></Field>
        <Field label={`索引服务 API Key${w.node_api_key_configured?'（已保存，同地址留空不变）':''}`}><input type="password" autoComplete="new-password" value={w.node_api_key||''} onChange={e=>onChange({...w,node_api_key:e.target.value})}/></Field>
      </div>
      <p className="portal-hint">索引服务与 RPC 必须连接同一网络；普通 FullNode 不提供 /v1 交易历史接口。更换索引服务地址后需重新填写密钥。TronGrid 模式同时使用本地址作为 RPC。</p>
    </div>
    <div className="wallet-node-test">
      <div className="portal-panel-footer"><p className="portal-hint">仅检测已保存配置；修改后请先保存。检测不签名、不转账、不租赁能量，也不代表实际充值已验收。</p><button type="button" className="button subtle" disabled={probing} onClick={()=>void test()}><RefreshCw className={probing?'spin':''}/>{probing?'检测中…':'检测已保存的节点（只读）'}</button></div>
      {error&&<p className="wallet-node-error" role="alert">{error}</p>}
      {probe&&<div className="wallet-node-results" role="status"><div className="wallet-node-result-title"><strong>{probe.passed?'节点基础连通检测通过':'部分节点检查未通过'}</strong><span>{providerNames[probe.provider as keyof typeof providerNames]||probe.provider} · {probe.network} · {new Date(probe.checked_at).toLocaleString('zh-CN')}</span></div>{probe.checks.map(check=><div key={check.name} className={`wallet-node-result ${check.passed?'passed':'failed'}`}>{check.passed?<CheckCircle2/>:<XCircle/>}<div><strong>{check.name}</strong><span>{check.message}{check.block_height?` · 区块 ${check.block_height.toLocaleString()}`:''}</span></div><small>{check.latency_ms.toLocaleString()} ms</small></div>)}<p className="portal-hint">此处验证接口连通及响应格式，不核验端点实际网络；启用前仍需在所选网络完成小额到账与重复扫描验收。</p></div>}
    </div>
  </div>
}
