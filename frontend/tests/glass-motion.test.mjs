import {readFileSync} from 'node:fs'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const source=readFileSync(new URL('../src/glassMotion.ts',import.meta.url),'utf8')
const loaded={exports:{}}
new Function('module','exports',ts.transpileModule(source,{
  compilerOptions:{module:ts.ModuleKind.CommonJS},
}).outputText)(loaded,loaded.exports)
const {glassCoordinates,attachGlassPointer}=loaded.exports

class Events {
  listeners=new Map()
  addEventListener(type,fn){
    if(!this.listeners.has(type))this.listeners.set(type,new Set())
    this.listeners.get(type).add(fn)
  }
  removeEventListener(type,fn){this.listeners.get(type)?.delete(fn)}
  emit(type,event={}){for(const fn of this.listeners.get(type)||[])fn(event)}
  count(){return [...this.listeners.values()].reduce((sum,list)=>sum+list.size,0)}
}

function harness(matches=true){
  const doc=new Events(),view=new Events(),preference=new Events(),frames=new Map()
  preference.matches=matches
  let frame=0
  view.matchMedia=query=>{
    assert.match(query,/hover: hover/)
    assert.match(query,/pointer: fine/)
    assert.match(query,/prefers-reduced-motion: no-preference/)
    return preference
  }
  view.requestAnimationFrame=fn=>{frames.set(++frame,fn);return frame}
  view.cancelAnimationFrame=id=>frames.delete(id)
  return {doc,view,preference,frames,
    cleanup:attachGlassPointer(doc,view),
    flush(){const queued=[...frames.values()];frames.clear();queued.forEach(fn=>fn())},
    move(surface,x=60,y=70,options={}){
      doc.emit('pointermove',{
        clientX:x,clientY:y,pointerType:options.pointerType||'mouse',
        target:{closest(selector){return selector.includes('.leaflet-container')?options.blocked||null:surface}},
      })
    },
  }
}

function card(){
  const styles=new Map(),attributes=new Map()
  return {isConnected:true,styles,attributes,
    style:{setProperty:(key,value)=>styles.set(key,value),removeProperty:key=>styles.delete(key)},
    setAttribute:(key,value)=>attributes.set(key,value),
    removeAttribute:key=>attributes.delete(key),
    getBoundingClientRect:()=>({left:10,top:20,width:200,height:100}),
  }
}

test('glass coordinates are local, bounded and stable for empty or invalid rectangles',()=>{
  const rect={left:10,top:20,width:200,height:100}
  assert.deepEqual(glassCoordinates(rect,60,70),{x:'25%',y:'50%'})
  assert.deepEqual(glassCoordinates(rect,-20,400),{x:'0%',y:'100%'})
  assert.deepEqual(glassCoordinates(rect,43.33,53.33),{x:'16.7%',y:'33.3%'})
  assert.deepEqual(glassCoordinates({...rect,width:0,height:-1},60,70),{x:'50%',y:'50%'})
  assert.deepEqual(glassCoordinates({...rect,width:Infinity},60,NaN),{x:'50%',y:'50%'})
})

test('pointer events coalesce into one frame using the newest coordinates',()=>{
  const env=harness(),surface=card()
  env.move(surface,20,30);env.move(surface,60,70)
  assert.equal(env.frames.size,1)
  assert.equal(surface.attributes.size,0)
  env.flush()
  assert.equal(surface.attributes.has('data-glass-active'),true)
  assert.equal(surface.styles.get('--glass-x'),'25%')
  assert.equal(surface.styles.get('--glass-y'),'50%')
  env.cleanup()
  assert.equal(surface.styles.size,0)
})

test('moving between cards removes the old highlight and leaving all cards clears it',()=>{
  const env=harness(),first=card(),second=card()
  env.move(first);env.flush()
  env.move(second);env.flush()
  assert.equal(first.attributes.size,0)
  assert.equal(first.styles.size,0)
  assert.equal(second.attributes.size,1)
  env.move(null);env.flush()
  assert.equal(second.attributes.size,0)
  env.cleanup()
})

test('maps and dropdown popovers never receive the pointer decoration',()=>{
  const env=harness(),surface=card()
  env.move(surface);env.flush()
  env.move(surface,60,70,{blocked:true});env.flush()
  assert.equal(surface.attributes.size,0)
  assert.equal(surface.styles.size,0)
  env.cleanup()
})

test('coarse pointers and reduced motion disable listeners, including live preference changes',()=>{
  const env=harness(false),surface=card()
  assert.equal(env.doc.count(),0)
  env.move(surface);assert.equal(env.frames.size,0)
  env.preference.matches=true;env.preference.emit('change')
  assert.equal(env.doc.count(),2)
  env.move(surface);env.flush()
  env.preference.matches=false;env.preference.emit('change')
  assert.equal(env.doc.count(),0)
  assert.equal(env.view.count(),0)
  assert.equal(surface.attributes.size,0)
  env.preference.matches=true;env.preference.emit('change');env.preference.emit('change')
  assert.equal(env.doc.count(),2)
  env.cleanup()
})

test('blur, scroll, pointer leave and touch reset the surface and pending frame',()=>{
  for(const reset of [
    env=>env.view.emit('blur'),env=>env.view.emit('scroll'),
    env=>env.doc.emit('pointerleave'),env=>env.move(null,0,0,{pointerType:'touch'}),
  ]){
    const env=harness(),surface=card()
    env.move(surface);env.flush();env.move(surface)
    reset(env)
    assert.equal(env.frames.size,0)
    assert.equal(surface.attributes.size,0)
    assert.equal(surface.styles.size,0)
    env.cleanup()
  }
})

test('detached cards and unmounts leave no listeners, frames or styles behind',()=>{
  const env=harness(),surface=card()
  env.move(surface);env.flush()
  env.move(surface);surface.isConnected=false;env.flush()
  assert.equal(surface.attributes.size,0)
  surface.isConnected=true;env.move(surface)
  env.cleanup();env.cleanup()
  assert.equal(env.frames.size,0)
  assert.equal(env.doc.count()+env.view.count()+env.preference.count(),0)
  env.move(surface);env.flush()
  assert.equal(surface.attributes.size,0)
})

test('glass styles preserve sticky save bars, reduced effects and mobile fallbacks',()=>{
  const css=readFileSync(new URL('../src/liquid-glass.css',import.meta.url),'utf8')
  const main=readFileSync(new URL('../src/main.tsx',import.meta.url),'utf8')
  assert.ok(main.indexOf("'./liquid-glass.css'")>main.indexOf("'./visual-theme.css'"))
  assert.match(css,/\.panel:not\(\.portal-save\):not\(\.settings-savebar\)[^{]*\{ position: relative;/)
  assert.match(css,/\.portal-save::before, \.settings-savebar::before \{ content: none;/)
  assert.match(css,/@media \(prefers-reduced-motion: reduce\)/)
  assert.match(css,/@media \(prefers-reduced-transparency: reduce\)/)
  assert.match(css,/@supports not \(\(backdrop-filter:/)
  assert.match(css,/@media \(max-width: 760px\)/)
  assert.doesNotMatch(css,/will-change\s*:/)
})
