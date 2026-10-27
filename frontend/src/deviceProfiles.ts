import catalog from '../../internal/deviceprofile/catalog.json'

export type DeviceIdentity = {model_name:string;system_version:string}
export type DeviceProfile = {
  readonly id:string
  readonly brand:string
  readonly name:string
  readonly certified_name:string
  readonly model_name:string
  readonly release_month:string
  readonly android_versions:readonly string[]
}

// The Go onboarding service embeds this same file, keeping both default pickers
// on one catalog of paired device models and Android versions.
export const DEVICE_PROFILES:readonly DeviceProfile[]=catalog.devices

export function activeDeviceProfiles(at=new Date()):readonly DeviceProfile[]{
  if(!Number.isFinite(at.getTime()))return []
  const today=new Date(Date.UTC(at.getUTCFullYear(),at.getUTCMonth(),at.getUTCDate()))
  const cutoff=new Date(today)
  cutoff.setUTCFullYear(cutoff.getUTCFullYear()-catalog.window_years)
  return DEVICE_PROFILES.filter(profile=>{
    const released=new Date(`${profile.release_month}-01T00:00:00Z`)
    return released>=cutoff&&released<=today
  })
}

export function deviceProfile(modelName:string):DeviceProfile|undefined{
  return DEVICE_PROFILES.find(profile=>profile.model_name.toLowerCase()===modelName.trim().toLowerCase())
}

export function randomDeviceIdentity(at=new Date(),index:(length:number)=>number=randomIndex,previousModel=''):DeviceIdentity|null{
  const active=activeDeviceProfiles(at)
  if(!active.length)return null
  // A deliberate "换一组" action should visibly change the model when possible.
  const others=active.filter(profile=>profile.model_name.toLowerCase()!==previousModel.trim().toLowerCase())
  const profiles=others.length?others:active
  const choose=<T,>(items:readonly T[]):T=>{
    const selected=index(items.length)
    if(!Number.isInteger(selected)||selected<0||selected>=items.length)throw new RangeError('Invalid device selection index')
    return items[selected]
  }
  const profile=choose(profiles)
  return {model_name:profile.model_name,system_version:choose(profile.android_versions)}
}

function randomIndex(length:number):number{
  const value=new Uint32Array(1),range=0x100000000
  const limit=range-(range%length)
  do{globalThis.crypto.getRandomValues(value)}while(value[0]>=limit)
  return value[0]%length
}

export function newAccountForm(){
  return {login_name:'',password:'',note:'',fence_index:0,device_id:'',
    ...(randomDeviceIdentity()??{model_name:'',system_version:''})}
}
