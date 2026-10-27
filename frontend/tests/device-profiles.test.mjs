import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const source=new URL('../src/deviceProfiles.ts',import.meta.url)
const compiled=ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,esModuleInterop:true}}).outputText
const loaded={exports:{}}
new Function('require','module','exports',compiled)(createRequire(source),loaded,loaded.exports)
const {DEVICE_PROFILES,activeDeviceProfiles,deviceProfile,randomDeviceIdentity,newAccountForm}=loaded.exports
const now=new Date('2026-10-27T12:00:00Z')

test('frontend shares the exact source-backed Go catalog',()=>{
  const data=JSON.parse(readFileSync(new URL('../../internal/deviceprofile/catalog.json',import.meta.url),'utf8'))
  assert.deepEqual(DEVICE_PROFILES,data.devices)
  assert.equal(data.verified_at,'2026-10-27')
  assert.equal(new Set(DEVICE_PROFILES.map(p=>p.brand)).size,7)
  assert.equal(DEVICE_PROFILES.length,28)
  assert.equal(activeDeviceProfiles(now).length,28)
})

test('every possible sampled model and Android version forms a verified pair',()=>{
  const profiles=activeDeviceProfiles(now)
  for(let i=0;i<profiles.length;i++){
    for(let v=0;v<profiles[i].android_versions.length;v++){
      const sequence=[i,v]
      const actual=randomDeviceIdentity(now,()=>sequence.shift())
      assert.deepEqual(actual,{model_name:profiles[i].model_name,system_version:profiles[i].android_versions[v]})
      assert.equal(deviceProfile(actual.model_name),profiles[i])
    }
  }
})

test('a deliberate shuffle changes the model without touching credentials or Android ID',()=>{
  const previous=DEVICE_PROFILES[0]
  const next=randomDeviceIdentity(now,()=>0,previous.model_name)
  assert.notEqual(next.model_name,previous.model_name)
  assert.deepEqual(Object.keys(next).sort(),['model_name','system_version'])
  const draft={login_name:'example',password:'fixture',device_id:'1234567890abcdef',...next}
  assert.equal(draft.device_id,'1234567890abcdef')
  assert.equal(draft.password,'fixture')
})

test('old/future models are filtered and an expired catalog never falls back to an old phone',()=>{
  assert.ok(activeDeviceProfiles(new Date('2025-01-01T00:00:00Z')).every(p=>p.release_month<='2025-01'))
  assert.ok(!activeDeviceProfiles(new Date('2026-11-02T00:00:00Z')).some(p=>p.id==='vivo-x90-cn'))
  assert.equal(randomDeviceIdentity(new Date('2031-01-01T00:00:00Z')),null)
  assert.equal(activeDeviceProfiles(new Date('invalid')).length,0)
  assert.throws(()=>randomDeviceIdentity(now,()=>28),RangeError)
})

test('new account forms have paired defaults but leave Android ID to existing generation',()=>{
  const form=newAccountForm(),profile=deviceProfile(form.model_name)
  assert.ok(profile)
  assert.ok(profile.android_versions.includes(form.system_version))
  assert.equal(form.device_id,'')
  assert.equal(form.login_name,'')
  assert.equal(form.password,'')
})

test('opening a new form randomizes once, while SMS continuation retains the current form',()=>{
  const app=readFileSync(new URL('../src/App.tsx',import.meta.url),'utf8')
  const accounts=app.slice(app.indexOf('function Accounts('),app.indexOf('function RecordsModal('))
  assert.match(accounts,/useState\(newAccountForm\)/)
  assert.match(accounts,/const openAdd=\(\)=>\{setForm\(newAccountForm\(\)\)/)
  assert.match(accounts,/onClick=\{openAdd\}/)
  assert.match(accounts,/if\(onboarding\)setOpen\(true\)/)
  assert.doesNotMatch(accounts,/model_name:'Redmi\|23013RK75C'/)
  assert.match(accounts,/model_name:form.model_name, system_version:form.system_version/)
})
