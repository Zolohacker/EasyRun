import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const source=new URL('../src/depositScan.ts',import.meta.url)
const compiled=ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText
const loaded={exports:{}}
new Function('require','module','exports',compiled)(createRequire(source),loaded,loaded.exports)
const {remainingScanSeconds,scanPending,scanCountdown}=loaded.exports
const status={state:'succeeded',server_time:'2026-10-23T00:00:00Z',next_allowed_at:'2026-10-23T00:01:00Z',retry_after_seconds:60}

test('deposit check cooldown counts down using server time and elapsed time',()=>{
  assert.equal(remainingScanSeconds(status,0),60)
  assert.equal(remainingScanSeconds(status,29999),31)
  assert.equal(remainingScanSeconds(status,60000),0)
  assert.equal(remainingScanSeconds(status,999999),0)
  assert.equal(scanCountdown(180),'3:00')
  assert.equal(scanCountdown(9),'0:09')
})
test('clock differences, missing timestamps and idle state are handled',()=>{
  assert.equal(remainingScanSeconds({...status,server_time:'invalid',next_allowed_at:undefined},30000),30)
  assert.equal(remainingScanSeconds({...status,server_time:'2026-10-23T00:04:00Z'},0),0)
  assert.equal(remainingScanSeconds({state:'idle',server_time:status.server_time,retry_after_seconds:0},0),0)
})
test('only accepted pending requests need local status polling',()=>{
  assert.equal(scanPending(null),false)
  for(const state of ['idle','succeeded','failed'])assert.equal(scanPending({state}),false)
  for(const state of ['queued','running'])assert.equal(scanPending({state}),true)
})
