import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import test from 'node:test'
import assert from 'node:assert/strict'
import ts from 'typescript'

const source=new URL('../src/depositScan.ts',import.meta.url),loaded={exports:{}}
new Function('require','module','exports',ts.transpileModule(readFileSync(source,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText)(createRequire(source),loaded,loaded.exports)
const {startDepositPolling,DEPOSIT_SCAN_INTERVAL_MS}=loaded.exports
const flush=async()=>{for(let i=0;i<20;i++)await Promise.resolve()}
function fixture({previous=null,pendingUntil=2500,override}={}){
  let time=0,id=0,last=previous,completed=0,errors=[],inflight=0,maxInflight=0
  const timers=new Map(),calls=[],updates=[]
  const iso=ms=>new Date(Date.UTC(2026,9,26)+ms).toISOString()
  const snapshot=()=>({state:last===null?'idle':time<last+pendingUntil?'running':'succeeded',message:'',requested_at:last===null?undefined:iso(last),server_time:iso(time),next_allowed_at:last===null?undefined:iso(last+60000),retry_after_seconds:last===null?0:Math.max(0,Math.ceil((last+60000-time)/1000)),credited_micro:0,deposit_count:0})
  const request=async(method,signal)=>{
    calls.push([method,time]);inflight++;maxInflight=Math.max(maxInflight,inflight)
    try{
      if(override){const result=await override({method,signal,calls,time,accept:()=>{last=time},snapshot});if(result)return result}
      if(method==='POST')last=time
      return snapshot()
    }finally{inflight--}
  }
  const options={request,now:()=>time,schedule:(fn,ms)=>{const n=++id;timers.set(n,{fn,due:time+ms});return n},cancel:n=>timers.delete(n),onStatus:value=>updates.push(value),onBusy:()=>{},onError:e=>{if(e)errors.push(e)},onComplete:()=>completed++}
  const stop=startDepositPolling(options)
  return {stop,calls,timers,updates,errors,get completed(){return completed},get maxInflight(){return maxInflight},
    posts:()=>calls.filter(([method])=>method==='POST').map(([,time])=>time),
    async advance(ms){await flush();const target=time+ms;for(let i=0;i<1000;i++){const entry=[...timers].sort((a,b)=>a[1].due-b[1].due)[0];if(!entry||entry[1].due>target)break;time=entry[1].due;timers.delete(entry[0]);entry[1].fn();await flush()}time=target;await flush()},
    async suspend(ms){await flush();time+=ms;const due=[...timers].filter(([,t])=>t.due<=time);for(const [id,timer] of due){timers.delete(id);timer.fn()}await flush()},
  }
}
test('opening checks immediately, refreshes completed result once, and posts at most once a minute',async()=>{
  assert.equal(DEPOSIT_SCAN_INTERVAL_MS,60000)
  const f=fixture();await flush();assert.deepEqual(f.posts(),[0])
  await f.advance(2500);assert.equal(f.completed,1)
  await f.advance(57499);assert.deepEqual(f.posts(),[0])
  await f.advance(1);assert.deepEqual(f.posts(),[0,60000]);assert.equal(f.completed,1)
  await f.advance(2500);assert.equal(f.completed,2);assert.equal(f.maxInflight,1)
  f.stop();assert.equal(f.timers.size,0);await f.advance(180000);assert.equal(f.posts().length,2)
})
test('reopening respects persisted cooldown and a long-running scan is never duplicated',async()=>{
  const f=fixture({previous:-30000});await flush();assert.deepEqual(f.posts(),[])
  await f.advance(29999);assert.equal(f.posts().length,0);await f.advance(1);assert.deepEqual(f.posts(),[30000]);f.stop()
  const long=fixture({pendingUntil:90000});await long.advance(89999);assert.deepEqual(long.posts(),[0]);await long.advance(1);assert.deepEqual(long.posts(),[0,90000]);long.stop()
})
test('lost accepted response is reconciled with GET, not a burst of POST retries',async()=>{
  let lost=false
  const f=fixture({override:({method,accept})=>{if(method==='POST'&&!lost){lost=true;accept();throw new Error('lost response')}}})
  await f.advance(10000);assert.equal(f.errors.length,1);assert.equal(f.completed,1);assert.deepEqual(f.posts(),[0])
  await f.advance(50000);assert.deepEqual(f.posts(),[0,60000]);f.stop()
})
test('failed status reads retry without submitting, then recover automatically',async()=>{
  let failed=false
  const f=fixture({override:({method})=>{if(method==='GET'&&!failed){failed=true;throw new Error('offline')}}})
  await f.advance(9999);assert.deepEqual(f.posts(),[]);await f.advance(1);assert.deepEqual(f.posts(),[10000]);f.stop()
})
test('unmount aborts outstanding read and suppresses late results and all timers',async()=>{
  let release,signal
  const f=fixture({override:args=>{signal=args.signal;return new Promise(resolve=>{release=()=>resolve(args.snapshot())})}})
  await flush();f.stop();assert.equal(signal.aborted,true);release();await flush()
  assert.deepEqual(f.posts(),[]);assert.equal(f.updates.length,0);assert.equal(f.timers.size,0)
})
test('StrictMode-style cleanup before initial read prevents a duplicate scan',async()=>{
  const first=fixture();first.stop();const active=fixture();await flush()
  assert.deepEqual(first.posts(),[]);assert.deepEqual(active.posts(),[0]);active.stop()
})
test('resuming a sleeping tab performs one check instead of catching up missed minutes',async()=>{
  const f=fixture();await f.advance(2500);await f.suspend(600000)
  assert.deepEqual(f.posts(),[0,602500]);assert.equal(f.maxInflight,1);f.stop()
})
test('a hanging request is aborted on timeout and the serial loop can recover',async()=>{
  let hanging=true
  const f=fixture({override:({signal})=>{if(hanging){hanging=false;return new Promise((_,reject)=>signal.addEventListener('abort',()=>reject(new Error('timeout')),{once:true}))}}})
  await f.advance(20000);assert.equal(f.errors.length,1);assert.deepEqual(f.posts(),[])
  await f.advance(10000);assert.deepEqual(f.posts(),[30000]);assert.equal(f.maxInflight,1);f.stop()
})
