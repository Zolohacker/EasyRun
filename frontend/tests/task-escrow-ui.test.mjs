import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const read=name=>readFileSync(new URL(`../src/${name}`,import.meta.url),'utf8')
const source=new URL('../src/accountLabels.ts',import.meta.url),loaded={exports:{}}
new Function('require','module','exports',ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText)(createRequire(source),loaded,loaded.exports)

test('route and billing account labels use a name plus account, never an opaque name',()=>{
  const {accountSelectorLabel}=loaded.exports
  assert.equal(accountSelectorLabel({name:'  测试用户  ',login_name:'sample-login'}),'测试用户 · sample-login')
  assert.equal(accountSelectorLabel({name:'1111111111111111111111==',login_name:'sample-login'}),'姓名待同步 · sample-login')
  assert.equal(accountSelectorLabel({name:'sample-login',login_name:'sample-login'}),'姓名待同步 · sample-login')
  assert.match(read('RouteManagement.tsx'),/accountSelectorLabel\(a\)/)
})

test('customer checkout uses the atomic order endpoint and binds confirmation to amount and request ID',()=>{
  const app=read('App.tsx'),automation=app.slice(app.indexOf('function Automation('),app.indexOf('function ExecutionLogs('))
  assert.match(automation,/portalRequest<Job>\('task-orders'/)
  assert.match(automation,/expected_amount_micro:orderAmount/)
  assert.match(automation,/request_id:pendingOrder.current.id/)
  assert.match(automation,/sessionStorage.setItem\('easyrun-task-checkout'/)
  assert.match(automation,/disabled=\{!!editing\|\|busy==='save'\} value=\{form.account\}/)
  assert.match(automation,/disabled=\{!!editing\|\|busy==='save'\} value=\{form.plan\}/)
  assert.doesNotMatch(automation,/jobs\/\$\{created.id\}\/billing/)
})

test('adjustment confirmation sends the server quote revision and uses protected quantity floors',()=>{
  const panel=read('TaskPrepayPanel.tsx')
  assert.match(panel,/settled_units\|\|0\)\+\(info\?\.credit.reserved_units/)
  assert.match(panel,/expected_revision:quote.revision/)
  assert.match(panel,/expected_delta_micro:quote.delta_micro/)
  assert.match(panel,/total_units:units/)
  assert.match(panel,/info.credit.revision>0/)
  assert.match(panel,/退回全部未执行额度/)
  assert.match(panel,/已成功 · 已结算/)
  assert.match(read('PortalPages.tsx'),/task_settle:'成功结算'/)
  assert.match(read('PortalPages.tsx'),/不再扣余额/)
})

test('route library supports both roles, source filtering, preview and responsive bounded lists',()=>{
  const library=read('RouteManagement.tsx'),css=read('routes.css')
  assert.match(library,/customer\?'我的路线库':'全部路线'/)
  assert.match(library,/source_job_id/)
  assert.match(library,/!customer&&<SearchSelect aria-label="所属用户"/)
  assert.match(library,/preview disabled area=\{selectedArea\}/)
  assert.match(library,/ListViewport pagination=\{pages\}/)
  assert.match(css,/manual-route-list \{height:450px;max-height:450px/)
  assert.match(css,/@media\(max-width:860px\)/)
  assert.match(css,/@media\(prefers-reduced-motion:reduce\)/)
})
