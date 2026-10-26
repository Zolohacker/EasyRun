import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'
import {createElement} from 'react'
import {renderToStaticMarkup} from 'react-dom/server'
import {QRCodeSVG} from 'qrcode.react'

// Exercise the actual TSX component with the project's existing compiler.
const sourceURL=new URL('../src/DepositQRCode.tsx',import.meta.url)
const compiled=ts.transpileModule(readFileSync(sourceURL,'utf8'),{
  compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX},
}).outputText
const loaded={exports:{}}
new Function('require','module','exports',compiled)(createRequire(sourceURL),loaded,loaded.exports)
const DepositQRCode=loaded.exports.default
const address='TULcovQZV8HXGUryMYkDeLTfjyhdUGTcnH'

test('unallocated deposit address produces no QR code',()=>{
  assert.equal(renderToStaticMarkup(createElement(DepositQRCode,{address:''})),'')
})

test('QR payload is exactly the customer deposit address with a four-module quiet zone',()=>{
  const html=renderToStaticMarkup(createElement(DepositQRCode,{address}))
  const reference=renderToStaticMarkup(createElement(QRCodeSVG,{value:address,size:224,level:'M',marginSize:4}))
  assert.deepEqual([...html.matchAll(/<path[^>]*d="([^"]+)"/g)].map(m=>m[1]),[...reference.matchAll(/<path[^>]*d="([^"]+)"/g)].map(m=>m[1]))
  assert.match(html,/role="img"/)
  assert.ok(html.includes(`收款地址：${address}`))
  assert.doesNotMatch(html,/<img|https?:\/\/|tron:|TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t/)
})

test('different customers receive different QR matrices',()=>{
  const first=renderToStaticMarkup(createElement(DepositQRCode,{address}))
  const second=renderToStaticMarkup(createElement(DepositQRCode,{address:'TSuL6W7SsLJpWkYkJSdXP64LGetttttttt'}))
  const matrix=html=>[...html.matchAll(/<path[^>]*d="([^"]+)"/g)].map(m=>m[1])
  assert.ok(matrix(first).length>0&&matrix(second).length>0)
  assert.notDeepEqual(matrix(first),matrix(second))
})

test('customer recharge view no longer renders a contract address',()=>{
  const source=readFileSync(new URL('../src/PortalPages.tsx',import.meta.url),'utf8')
  assert.doesNotMatch(source,/data\.contract|合约：/)
  assert.match(source,/<DepositPaymentModal address=\{data\.deposit_address\}/)
  const dialog=readFileSync(new URL('../src/DepositPaymentModal.tsx',import.meta.url),'utf8')
  assert.match(dialog,/<DepositQRCode address=\{address\}/)
  assert.doesNotMatch(dialog,/data\.contract|合约：/)
})
