import type {Area, ManualRoute, Plan, Point} from './types'

export const availableRouteMode=(configured:boolean,preferred:'auto'|'custom'='auto')=>configured?preferred:'custom'
export const routeMapError=(configured:boolean,mode:string)=>!configured&&mode!=='custom'?'尚未配置地图 API Key，请选择自定义路线':''

export function routeDistance(points:Point[]):number {
  let sum=0
  for(let i=1;i<points.length;i++) {
    const a=points[i-1],b=points[i],rad=Math.PI/180
    const h=Math.sin((b.lat-a.lat)*rad/2)**2+Math.cos(a.lat*rad)*Math.cos(b.lat*rad)*Math.sin((b.lng-a.lng)*rad/2)**2
    sum+=2*6371008.8*Math.asin(Math.sqrt(Math.min(1,h)))
  }
  return sum
}
export const minimumRouteDistance=(plan?:Plan)=>Math.max(300,plan?.rules.min_distance_m||plan?.target_distance_m||300)
export const matchingRoutes=(routes:ManualRoute[],account:string,plan:string,area:string)=>routes.filter(r=>r.account_id===account&&r.plan_id===plan&&r.area_id===area)
export function routeSelectionError(mode:string,route:ManualRoute|undefined,distance:number,plan?:Plan,area?:Area):string {
  if(mode!=='custom')return ''
  if(!route)return '请选择已保存的自定义路线'
  if(plan&&route.plan_id!==plan.id||area&&route.area_id!==area.id)return '路线与所选计划或区域不匹配'
  if(route.distance_m<=minimumRouteDistance(plan))return '路线长度需大于计划最低距离，请在路线管理中延长'
  if(distance>route.distance_m+1e-6)return `任务距离超过路线长度 ${(route.distance_m/1000).toFixed(3)} km，请调低距离或延长路线`
  return ''
}
