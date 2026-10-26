export type PrepayMode='per_run'|'per_km'
export type TaskCredit={job_id:string;mode:PrepayMode;remaining_units:number;reserved_units:number;settled_units:number;total_units:number;available_micro:number;reserved_micro:number;settled_micro:number;revision:number}
export type PrepayInfo={credit:TaskCredit;can_purchase:boolean;can_adjust:boolean;package_owned:boolean;personal:boolean;pricing_enabled:boolean;per_run_micro:number;per_km_micro:number;balance_micro?:number}
export type TaskQuote={mode:PrepayMode;total_units:number;minimum_units:number;delta_units:number;delta_micro:number;revision:number;balance_micro:number}
export function prepaidAmount(mode:PrepayMode,units:number,info:Pick<PrepayInfo,'per_run_micro'|'per_km_micro'>):number|null {
  if(mode!=='per_run'&&mode!=='per_km')return null
  if(!Number.isSafeInteger(units)||units<1)return null
  if(mode==='per_run'&&units>10000||mode==='per_km'&&(units<100||units>1000000||units%100!==0))return null
  const price=mode==='per_run'?info.per_run_micro:info.per_km_micro
  if(!Number.isSafeInteger(price)||price<0||price>100000e6)return null
  const numerator=BigInt(price)*BigInt(units)
  return Number(mode==='per_run'?numerator:(numerator+999n)/1000n)
}
export const creditLabel=(mode:string,units:number)=>mode==='per_km'?`${(units/1000).toFixed(2)} km`:`${units} 次`

export function newCreditRequestID(source:Pick<Crypto,'getRandomValues'>=crypto):string {
 return Array.from(source.getRandomValues(new Uint8Array(24)),b=>b.toString(16).padStart(2,'0')).join('')
}
