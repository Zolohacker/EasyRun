export type ClientVersionStatus = {
  current_version:string;current_build:string;latest_version:string;support_version:string
  have_update:boolean;force_update:boolean;known:boolean;automation_allowed:boolean
  reason:string;last_error:string;checked_at?:string;success_at?:string;title:string;description:string;maintenance:boolean
}
export type BuildInfo = {version:string;commit:string;built_at:string;build_type:string;os:string;arch:string;native_face:boolean;client_version:string;client_build:string}
export type PlatformVersionStatus = {
  current:BuildInfo;latest_version:string;has_update:boolean;notes:string;published_at:string;checked_at?:string
  last_error:string;can_install:boolean;can_rollback:boolean;capability_reason:string;phase:string;message:string
  settings:{repository:string;auto_install:boolean}
}
export type Versions = {client:ClientVersionStatus;platform:PlatformVersionStatus}
export const updating = (phase?:string) => ['downloading','prepared','installing','verifying'].includes(phase||'')
export function clientVersionLabel(status:ClientVersionStatus) {
  if(status.force_update)return '需要强制更新'
  if(!status.known)return '状态待核实'
  if(!status.automation_allowed)return status.maintenance?'平台维护中':'版本状态已过期'
  return status.have_update?'有可选更新':'无需强制更新'
}
export function updatePhaseLabel(phase?:string) {
  return ({downloading:'下载校验中',prepared:'等待重启',installing:'正在安装',verifying:'启动验收中',succeeded:'更新成功',rolled_back:'已回滚',failed:'更新失败'} as Record<string,string>)[phase||'']||'尚未执行更新'
}
