export type PortalUser={id:string;email:string;role:'superadmin'|'admin'|'customer';enabled:boolean;email_verified:boolean;balance_micro:number;created_at:string}
export type PortalStatus={setup_required:boolean;mode:'personal'|'commercial';frontend_enabled:boolean;registration_enabled:boolean;email_verification:boolean}
export type PortalSettings={
  mode:'personal'|'commercial';frontend_enabled:boolean;registration_enabled:boolean;email_verification:boolean
  mail:{protocol:'smtp'|'smtps'|'starttls';host:string;port:number;username:string;password?:string;password_configured:boolean;from:string}
  pricing:{enabled:boolean;per_run_micro:number;per_km_micro:number;plan_package_micro:number;recharge_code_shop_url:string;recharge_code_enabled:boolean}
  wallet:{enabled:boolean;network:'mainnet'|'shasta'|'nile';node_url:string;contract:string;node_api_key?:string;node_api_key_configured:boolean;node_provider:'tokenpocket'|'trongrid'|'nownodes'|'quicknode'|'getblock'|'custom';rpc_url?:string;rpc_url_configured:boolean;rpc_url_host:string;rpc_api_key?:string;rpc_api_key_configured:boolean;rpc_api_header:string;seed_hex?:string;seed_configured:boolean;mnemonic_configured:boolean;feee_api_key?:string;feee_api_key_configured:boolean;collection_address:string;auto_collect:boolean;collect_cron:string;timezone:string;min_collect_micro:number;fee_limit_sun:number;energy_price_sun:number}
}
export type WalletBackup={kind:'bip39'|'legacy_seed';mnemonic?:string;seed_hex?:string;network:string;derivation_path:string;passphrase:string}
export type WalletNodeProbe={passed:boolean;network:string;provider:string;checked_at:string;checks:{name:string;passed:boolean;message:string;latency_ms:number;block_height?:number}[]}
export type BillingSummary={user:PortalUser;pricing:PortalSettings['pricing'];ledger:{id:string;delta_micro:number;kind:string;reference:string;created_at:string}[];entitlements:{account_id:string;plan_id:string}[];job_modes:Record<string,string>;deposit_address:string;wallet_enabled:boolean;network:string;contract:string}
export type FaceLink={url:string|null;expires_at?:string}
export type Sweep={id:string;address:string;amount_micro:number;state:string;tx_id:string;message:string;created_at:string}

export async function portalRequest<T>(path:string,body?:unknown,method?:string):Promise<T>{
  const response=await fetch('/api/v1/portal/'+path,{method:method||(body===undefined?'GET':'POST'),headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)})
  const data=await response.json().catch(()=>null)
  if(!response.ok){if(response.status===401&&!path.startsWith('auth/'))window.dispatchEvent(new Event('platform-session-expired'));throw new Error(data?.error?.message||`请求失败：${response.status}`)}
  return data as T
}
export function money(micro:number){return (micro/1e6).toLocaleString('en-US',{minimumFractionDigits:2,maximumFractionDigits:6})}
export function externalHTTPURL(value?:string){
  try{const url=new URL(value?.trim()||'');return (url.protocol==='https:'||url.protocol==='http:')&&!url.username&&!url.password?url.href:''}catch{return ''}
}
