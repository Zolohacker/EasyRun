import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'
const source=new URL('../src/versions.ts',import.meta.url)
const compiled=ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText
const loaded={exports:{}}
new Function('require','module','exports',compiled)(createRequire(source),loaded,loaded.exports)
const {clientVersionLabel,updatePhaseLabel,updating}=loaded.exports
test('forced update takes priority over stale, unknown and maintenance labels',()=>{
  assert.equal(clientVersionLabel({force_update:true,known:false,automation_allowed:false}),'需要强制更新')
  assert.equal(clientVersionLabel({known:false}),'状态待核实')
  assert.equal(clientVersionLabel({known:true,automation_allowed:false}),'版本状态已过期')
  assert.equal(clientVersionLabel({known:true,automation_allowed:false,maintenance:true}),'平台维护中')
  assert.equal(clientVersionLabel({known:true,automation_allowed:true,have_update:true}),'有可选更新')
})
test('all download and supervisor verification stages remain pending',()=>{
  for(const phase of ['downloading','prepared','installing','verifying'])assert.equal(updating(phase),true)
  for(const phase of ['',undefined,'succeeded','failed','rolled_back'])assert.equal(updating(phase),false)
  assert.equal(updatePhaseLabel('rolled_back'),'已回滚')
})
