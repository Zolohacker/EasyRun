import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import {createElement} from 'react'
import {renderToStaticMarkup} from 'react-dom/server'
import ts from 'typescript'

const read=name=>readFileSync(new URL(`../src/${name}`,import.meta.url),'utf8')
function load(name,overrides={}){
  const source=new URL(`../src/${name}`,import.meta.url),localRequire=createRequire(source),module={exports:{}}
  const compiled=ts.transpileModule(read(name),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText
  new Function('require','module','exports',compiled)(id=>Object.hasOwn(overrides,id)?overrides[id]:localRequire(id),module,module.exports)
  return module.exports.default
}
const QR=load('DepositQRCode.tsx')
const Modal=load('DepositPaymentModal.tsx',{
  './Modal':{default:({title,children,className})=>createElement('section',{className,'aria-label':title},children)},
  './DepositQRCode':{default:QR},
  './DepositScanPanel':{default:()=>createElement('div',{'data-scan-panel':true})},
})
const address='TULcovQZV8HXGUryMYkDeLTfjyhdUGTcnH'
const render=network=>renderToStaticMarkup(createElement(Modal,{address,network,onClose(){},async onRefresh(){},notify(){}}))

test('payment dialog keeps the full selectable address, local QR, copy action and network in one layout',()=>{
  const html=render('mainnet')
  assert.match(html,/class="deposit-payment-layout"/)
  assert.match(html,/TRON · TRC20/)
  assert.match(html,/>主网</)
  assert.match(html,new RegExp(`<code[^>]*aria-label="专属收款地址"[^>]*dir="ltr"[^>]*>${address}</code>`))
  assert.match(html,/class="button primary deposit-copy-button"/)
  assert.match(html,/复制收款地址/)
  assert.equal((html.match(/role="img"/g)||[]).length,1)
  assert.equal((html.match(/data-scan-panel="true"/g)||[]).length,1)
  assert.doesNotMatch(html,/role="alert"|合约：|仅支持 TRON 网络的 USDT/)
})

test('testnet payment retains an explicit warning before the QR and never displays a mainnet badge',()=>{
  const html=render('shasta')
  assert.match(html,/role="alert">当前为测试网络，请勿转入真实资金。/)
  assert.match(html,/>shasta 测试网</)
  assert.doesNotMatch(html,/>主网</)
  assert.ok(html.indexOf('role="alert"')<html.indexOf('role="img"'))
})

test('payment layout puts network first on mobile and preserves full address wrapping and touch targets',()=>{
  const css=read('wallet.css')
  assert.match(css,/grid-template-areas:"code network" "code address" "code note"/)
  assert.match(css,/grid-template-areas:"network" "code" "address" "note"/)
  assert.match(css,/\.deposit-payment-modal \.portal-address \{[^}]*overflow-wrap:anywhere;white-space:normal;user-select:all/)
  assert.match(css,/\.deposit-payment-modal \.deposit-copy-button \{[^}]*min-height:44px/)
  assert.match(css,/env\(safe-area-inset-bottom,0px\)/)
  assert.doesNotMatch(css,/\.deposit-payment-modal[^{}]*\{[^}]*text-overflow:ellipsis/)
})

test('scan presentation keeps retry failures visible without duplicating stale result messages',()=>{
  const source=read('DepositScanPanel.tsx')
  assert.match(source,/const failed=!!error\|\|status\?\.state==='failed'/)
  assert.match(source,/status\?\.message&&!error&&/)
  assert.match(source,/error&&<p className="portal-error" role="alert"/)
  assert.match(source,/每分钟检查一次，请勿关闭此窗口。/)
  assert.doesNotMatch(source,/以便及时到账|<small>/)
  assert.match(source,/return\(\)=>\{stop\(\);clearInterval\(timer\)\}/)
  // A successful scan with zero credits is neutral, not a payment-success accent.
  assert.match(read('wallet.css'),/\.deposit-payment-modal \.deposit-scan-panel \.deposit-scan-result \{[^}]*color:var\(--muted\)/)
})
