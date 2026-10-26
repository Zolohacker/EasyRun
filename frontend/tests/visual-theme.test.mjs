import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'
import {createElement} from 'react'
import {renderToStaticMarkup} from 'react-dom/server'

const css=readFileSync(new URL('../src/visual-theme.css',import.meta.url),'utf8')
const token=name=>css.match(new RegExp(`--${name}:\\s*(#[\\da-f]{3,6})`,'i'))?.[1]
function luminance(hex){
  if(hex.length===4)hex='#'+[...hex.slice(1)].map(char=>char+char).join('')
  const channels=hex.slice(1).match(/../g).map(value=>parseInt(value,16)/255)
    .map(value=>value<=.04045?value/12.92:((value+.055)/1.055)**2.4)
  return channels[0]*.2126+channels[1]*.7152+channels[2]*.0722
}
function contrast(a,b){const values=[luminance(a),luminance(b)].sort((a,b)=>b-a);return (values[0]+.05)/(values[1]+.05)}

test('core text, primary buttons and status colors meet AA normal-text contrast',()=>{
  for(const [foreground,background] of [
    [token('ink'),token('bg')],[token('muted'),token('panel')],
    ['#ffffff',token('accent')],[token('success'),token('success-soft')],
    [token('amber'),'#fcf6e9'],[token('danger'),'#fceff1'],['#b3bdd3','#20283c'],
  ]) assert.ok(contrast(foreground,background)>=4.5,`${foreground} on ${background}`)
})

test('shared brand is a decorative vector with a stable aspect ratio',()=>{
  const source=new URL('../src/BrandMark.tsx',import.meta.url)
  const compiled=ts.transpileModule(readFileSync(source,'utf8'),{
    compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX},
  }).outputText
  const loaded={exports:{}}
  new Function('require','module','exports',compiled)(createRequire(source),loaded,loaded.exports)
  const markup=renderToStaticMarkup(createElement(loaded.exports.default))
  assert.match(markup,/viewBox="0 0 32 32"/)
  assert.match(markup,/aria-hidden="true"/)
  assert.doesNotMatch(markup,/<image|https?:/)
})

test('the theme loads after component styles and keeps a single-column mobile KPI',()=>{
  const main=readFileSync(new URL('../src/main.tsx',import.meta.url),'utf8')
  assert.ok(main.indexOf("'./visual-theme.css'")>main.indexOf("'./versions.css'"))
  assert.match(css,/@container overview \(max-width: 520px\)\s*\{\s*\.overview-kpi\s*\{\s*grid-template-columns: minmax\(0,1fr\)/)
  assert.match(css,/@media \(prefers-reduced-motion: reduce\)/)
})

test('page titles no longer have duplicate English slogans',()=>{
  for(const file of ['App.tsx','PortalRoot.tsx','PortalPages.tsx','AdminPages.tsx']){
    const source=readFileSync(new URL(`../src/${file}`,import.meta.url),'utf8')
    assert.doesNotMatch(source,/<span className="kicker">/)
    assert.doesNotMatch(source,/ONE PLATFORM\. EVERY RUN\.|自动运行状态一屏掌握/)
  }
})

test('dashboard and route metrics use task/step icons rather than heartbeat imagery',()=>{
  const app=readFileSync(new URL('../src/App.tsx',import.meta.url),'utf8')
  assert.doesNotMatch(app,/\bActivity\b|HeartPulse|Heartbeat/)
  assert.match(app,/overview-live-ring"><Workflow aria-hidden="true"/)
  assert.match(app,/<Stat icon=\{<Footprints\/>\} label="累计步数"/)
  assert.match(app,/label="本地里程"/)
  assert.match(app,/activeExecutions\.length\?'任务运行中':'暂无执行任务'/)
})

test('metric units have their own visual level, and counts remain unitless',()=>{
  const file=new URL('../src/App.tsx',import.meta.url)
  const source=readFileSync(file,'utf8')
  const tree=ts.createSourceFile('App.tsx',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
  const component=tree.statements.find(node=>ts.isFunctionDeclaration(node)&&node.name?.text==='OverviewKPI')
  const compiled=ts.transpileModule(`${component.getText(tree)}\nexport {OverviewKPI}`,{
    compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX},
  }).outputText
  const module={exports:{}}
  new Function('require','module','exports',compiled)(createRequire(file),module,module.exports)
  const props={tone:'amber',icon:null,label:'本地里程',value:'3.0',hint:'1 条本地运行记录'}
  const distance=renderToStaticMarkup(createElement(module.exports.OverviewKPI,{...props,unit:'km'}))
  assert.match(distance,/<strong>3.0<span class="metric-unit">km<\/span><\/strong>/)
  const count=renderToStaticMarkup(createElement(module.exports.OverviewKPI,{...props,label:'就绪账号',value:'3/4'}))
  assert.doesNotMatch(count,/metric-unit/)
  assert.match(count,/<strong>3\/4<\/strong>/)
})

test('task rows retain plan, area, quota and all four actions without a forced desktop width',()=>{
  const app=readFileSync(new URL('../src/App.tsx',import.meta.url),'utf8')
  const portal=readFileSync(new URL('../src/portal.css',import.meta.url),'utf8')
  const layout=readFileSync(new URL('../src/workspace-layout.css',import.meta.url),'utf8')
  const row=app.slice(app.indexOf('<div className="job-row"'),app.indexOf('{!jobs.length &&'))
  for(const title of ['生成独立配置链接','编辑任务','立即运行','启停'])assert.ok(row.includes(`title="${title}"`))
  assert.match(row,/className="job-plan"|className="job-bind job-plan"/)
  assert.match(row,/<MapPin aria-hidden="true"/)
  assert.match(row,/title=\{quotaLabel\(job,plan\)\}/)
  assert.match(row,/title=\{job.timezone\}/)
  assert.doesNotMatch(portal,/\.main-stage \.job-row\{[^}]*min-width:1000px/)
  assert.match(layout,/@container task-list \(max-width: 920px\)/)
  assert.match(layout,/"state title distance actions"/)
})

test('controls use one focus ring and consistent icon sizes without shrinking touch targets',()=>{
  const mobile=readFileSync(new URL('../src/mobile.css',import.meta.url),'utf8')
  assert.match(css,/\.button > \.lucide \{ width: 17px; height: 17px; flex: none;/)
  assert.match(css,/\.search-select-search:focus-within \{[^}]*box-shadow:/)
  assert.match(css,/\.search-select-search > input, \.search-select-search > input:focus \{ box-shadow: none;/)
  assert.match(css,/@media \(min-width: 761px\) \{\s*\.main-stage \.job-actions \.mini-button \{ width: 32px/)
  assert.match(mobile,/\.main-stage \.job-actions \.mini-button \{[^}]*min-height: 44px/)
  assert.match(mobile,/grid-template-areas: "title title" "plan plan" "distance schedule" "actions actions"/)
})

test('table labels and body copy retain AA contrast on the glass table surface',()=>{
  assert.ok(contrast('#5b687e','#f3f6fb')>=4.5)
  assert.ok(contrast('#344156','#eaf0f8')>=4.5)
  assert.match(css,/\.list-viewport td.credit \{ color: var\(--success\);/)
  assert.match(css,/\.list-viewport td.debit \{ color: var\(--amber\);/)
})

test('long execution identifiers do not push the message column out of view',()=>{
  const app=readFileSync(new URL('../src/App.tsx',import.meta.url),'utf8')
  const layout=readFileSync(new URL('../src/workspace-layout.css',import.meta.url),'utf8')
  assert.match(layout,/\.execution-logs-panel table \{ table-layout: fixed;/)
  assert.match(app,/data-label="服务端记录" className="mobile-wide-cell" title=\{execution.remote_run_id\}/)
  assert.match(app,/data-label="计划" className="mobile-wide-cell" title=\{plan\?\.name\}/)
})
