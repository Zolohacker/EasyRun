import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'
const read=name=>readFileSync(new URL(`../src/${name}`,import.meta.url),'utf8')
function load(file){const source=new URL(`../src/${file}`,import.meta.url),m={exports:{}};new Function('require','module','exports',ts.transpileModule(read(file),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText)(createRequire(source),m,m.exports);return m.exports}

test('payment scanning is only mounted inside the dismissible QR dialog',()=>{
  const wallet=read('PortalPages.tsx'),dialog=read('DepositPaymentModal.tsx'),panel=read('DepositScanPanel.tsx')
  assert.match(wallet,/depositOpen&&data.wallet_enabled&&data.deposit_address&&<DepositPaymentModal/)
  assert.doesNotMatch(wallet,/<DepositScanPanel|<DepositQRCode/)
  assert.match(dialog,/<Modal title="USDT 充值" onClose=\{onClose\}/)
  assert.match(dialog,/<DepositQRCode address=\{address\}/)
  assert.match(dialog,/<DepositScanPanel onRefresh=\{onRefresh\}/)
  assert.match(panel,/stop\(\);clearInterval\(timer\)/)
  assert.match(panel,/每分钟检查一次，请勿关闭此窗口/)
  assert.match(wallet,/data.ledger.length>0\?<ListViewport/)
  assert.match(wallet,/wallet-ledger-empty/)
  assert.doesNotMatch(wallet,/暂无流水/)
  assert.match(read('wallet.css'),/align-items:stretch/)
  assert.match(read('wallet.css'),/@media\(max-width:560px\)/)
})
test('map capability selects manual only while retaining valid saved manual choices',()=>{
  const {availableRouteMode,routeMapError}=load('manualRoutes.ts')
  assert.equal(availableRouteMode(false),'custom');assert.equal(availableRouteMode(false,'auto'),'custom')
  assert.equal(availableRouteMode(true),'auto');assert.equal(availableRouteMode(true,'custom'),'custom')
  assert.ok(routeMapError(false,'auto'));assert.equal(routeMapError(false,'custom'),'');assert.equal(routeMapError(true,'auto'),'')
  assert.match(read('App.tsx'),/Promise.all\(\[api.config\(\),api.manualRoutes\(\)\]\)/)
  assert.match(read('App.tsx'),/value="auto" disabled=\{!mapAvailable\}/)
  assert.match(read('TaskConfigPage.tsx'),/value="auto" disabled=\{!data.map_api_configured\}/)
})
test('map markers use centered, upright labels and anchors for one to four digits',()=>{
  const {routeMarkerIcon}=load('routeMarkers.ts')
  for(const [kind,index,label] of [['start',0,'起'],['finish',2,'终'],['point',2,'2'],['point',10,'10'],['point',100,'100'],['point',1999,'1999']]){
    const icon=routeMarkerIcon(kind,index)
    assert.deepEqual(icon.iconAnchor,icon.iconSize.map(n=>n/2))
    assert.match(icon.html,new RegExp(`class="route-node-label">${label}</span>`))
    assert.doesNotMatch(icon.html,/rotate|style=/)
    const expectedSize=kind!=='point'?20:label.length>=4?22:label.length>=3?18:14
    assert.deepEqual(icon.iconSize,[expectedSize,expectedSize])
  }
  const css=read('route-markers.css')
  assert.match(css,/box-sizing:border-box/);assert.match(css,/align-items:center;justify-content:center/)
  assert.match(css,/writing-mode:horizontal-tb;transform:none/);assert.doesNotMatch(css,/rotate\(/)
  assert.doesNotMatch(css,/scale:/)
  assert.match(css,/@media\(pointer:coarse\)/)
  assert.doesNotMatch(read('styles.css'),/\.route-map-marker/)
  for(const file of ['RouteSketchEditor.tsx','AmapRouteViewport.tsx'])assert.match(read(file),/L.divIcon\(routeMarkerIcon\(/)
})
