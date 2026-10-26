import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const source=new URL('../src/manualRoutes.ts',import.meta.url)
const compiled=ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText
const loaded={exports:{}}
new Function('require','module','exports',compiled)(createRequire(source),loaded,loaded.exports)
const {routeDistance,minimumRouteDistance,matchingRoutes,routeSelectionError}=loaded.exports
const plan={id:'p',rules:{min_distance_m:1000},target_distance_m:3000}
const route={id:'r',account_id:'a',plan_id:'p',area_id:'z',distance_m:2000}

test('drawing length is measured as an open polyline, not a forced loop',()=>{
  assert.equal(routeDistance([]),0)
  assert.equal(routeDistance([{lat:35,lng:116}]),0)
  const a={lat:35,lng:116},b={lat:35,lng:116.01}
  const length=routeDistance([a,b])
  assert.ok(length>900&&length<920)
  assert.equal(routeDistance([a,b,a]),length*2)
})
test('manual choices are scoped to account, plan and area',()=>{
  assert.deepEqual(matchingRoutes([route,{...route,id:'r2',account_id:'other'},{...route,id:'r3',plan_id:'other'},{...route,id:'r4',area_id:'other'}],'a','p','z'),[route])
})
test('manual selection rejects short/missing/mismatched routes and excess distance',()=>{
  assert.equal(minimumRouteDistance(plan),1000)
  assert.equal(routeSelectionError('auto',undefined,3000,plan),'')
  assert.equal(routeSelectionError('custom',route,2000,plan,{id:'z'}),'')
  assert.ok(routeSelectionError('custom',undefined,2000,plan))
  assert.ok(routeSelectionError('custom',route,2000.01,plan))
  assert.ok(routeSelectionError('custom',{...route,distance_m:1000},1000,plan))
  assert.ok(routeSelectionError('custom',route,1900,plan,{id:'other'}))
})
test('route management is a separate menu and capability generator lives with tasks',()=>{
  const app=readFileSync(new URL('../src/App.tsx',import.meta.url),'utf8')
  assert.match(app,/key:'routes',label:'路线管理'/)
  assert.match(app,/routes: <RouteManagement/)
  assert.match(app,/TaskConfigLinkPanel key=\{linkJob.id\} job=\{linkJob\}/)
  assert.doesNotMatch(app,/AccountFaceLinkPanel|setFaceLinkAccount/)
})
