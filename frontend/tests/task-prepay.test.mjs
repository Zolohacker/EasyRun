import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'
const source=new URL('../src/taskPrepay.ts',import.meta.url)
const loaded={exports:{}}
new Function('require','module','exports',ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(createRequire(source),loaded,loaded.exports)
const {prepaidAmount,creditLabel,newCreditRequestID}=loaded.exports
const prices={per_run_micro:1234567,per_km_micro:1234567}
test('prepaid quotation uses integer micro-USDT rounding and matches the server',()=>{
  assert.equal(prepaidAmount('per_run',3,prices),3703701)
  assert.equal(prepaidAmount('per_km',100,prices),123457)
  assert.equal(prepaidAmount('per_km',1100,prices),1358024)
  assert.equal(prepaidAmount('per_km',1000000,prices),1234567000)
  assert.equal(prepaidAmount('per_km',999900,{...prices,per_km_micro:100000000000}),99990000000000)
})
test('prepaid limits reject fractions, illegal slots and non-finite input',()=>{
  for(const units of [0,-1,1.5,10001,Infinity,NaN])assert.equal(prepaidAmount('per_run',units,prices),null)
  for(const units of [0,99,150,1000100,Infinity,NaN])assert.equal(prepaidAmount('per_km',units,prices),null)
  assert.equal(prepaidAmount('invalid',100,prices),null)
  for(const price of [-1,1.5,100000000001,Infinity,NaN])assert.equal(prepaidAmount('per_run',1,{...prices,per_run_micro:price}),null)
  assert.equal(creditLabel('per_run',2),'2 次')
  assert.equal(creditLabel('per_km',1500),'1.50 km')
})
test('prepay request IDs need only random values, not secure-context randomUUID',()=>{
  const id=newCreditRequestID({getRandomValues(bytes){assert.equal(bytes.length,24);bytes.set(Array.from({length:24},(_,i)=>i));return bytes}})
  assert.equal(id,'000102030405060708090a0b0c0d0e0f1011121314151617')
  assert.match(id,/^[a-f0-9]{48}$/)
})
