'use strict';
// Read-only live status costs. Rendering uses the already received snapshot;
// the script never starts, arms, pauses, resumes or otherwise controls a run.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const args={};for(let n=2;n<process.argv.length;n+=2)args[process.argv[n].replace(/^--/,'')]=process.argv[n+1];
assert(args.url&&args.out,'--url and --out required');const endpoint=new URL(args.url).origin;assert(/^http:\/\/127\.0\.0\.1:\d+$/.test(endpoint));
const samples=Number(args.samples||8),interval=Number(args.interval||250);assert(Number.isInteger(samples)&&samples>0&&samples<=100&&interval>=0);
fs.mkdirSync(args.out,{recursive:true});const measurements=[],routes=['/api/v1/index/run','/api/v1/index/status',...(args.progress==='true'?['/api/v1/index/progress']:[])];
const stats=values=>{const s=[...values].sort((a,b)=>a-b);return{min:s[0],median:s.length%2?s[(s.length-1)/2]:(s[s.length/2-1]+s[s.length/2])/2,max:s.at(-1),mean:s.reduce((a,b)=>a+b,0)/s.length};};
async function get(route){const started=performance.now(),response=await fetch(endpoint+route,{signal:AbortSignal.timeout(90000)}),headers=performance.now(),text=await response.text(),body=performance.now(),value=JSON.parse(text),parsed=performance.now();assert.equal(response.status,200,route);return{value,measurement:{route,state:value.run?.run?.state||value.run?.state||'unconfigured',response_wait_ms:headers-started,body_read_ms:body-headers,json_parse_ms:parsed-body,total_ms:parsed-started,utf8_payload_bytes:Buffer.byteLength(text)}};}
(async()=>{
 const before=(await get('/api/v1/index/run')).value;
 for(let i=0;i<samples;i++){for(const route of routes)measurements.push({sample:i,...(await get(route)).measurement});if(i+1<samples)await new Promise(resolve=>setTimeout(resolve,interval));}
 const result={endpoint,result:'PASS',read_only:true,scope:'Sequential GET timings; response_wait includes server work and local HTTP overhead. Payload is uncompressed UTF-8. Browser timing is synchronous timeline rendering only, separate from server, parse and paint.',samples:measurements,summary:{}};
 for(const route of routes){const rows=measurements.filter(r=>r.route===route);result.summary[route]=Object.fromEntries(['response_wait_ms','body_read_ms','json_parse_ms','total_ms','utf8_payload_bytes'].map(key=>[key,stats(rows.map(r=>r[key]))]));}
 fs.writeFileSync(path.join(args.out,'server-result.json'),JSON.stringify(result,null,2)+'\n');
 if(args.browser==='true'){
  const pw=require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
  const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
  const browser=await pw.chromium.launch({headless:true,executablePath});try{
   const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],posts=[];page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(request.method()==='POST')posts.push(new URL(request.url()).pathname);});
   await page.goto(endpoint+'/?resolve=indexes.gateway');const frame=page.frameLocator('#indexes-module');await frame.locator('#timeline-selection-title').filter({hasText:/headers/i}).waitFor({state:'attached'});
   result.browser={viewport:{width:1440,height:1000},samples:[],errors,posts};
   for(const expanded of [false,true]){
    const toggle=frame.locator('#timeline-collapse');if((await toggle.getAttribute('aria-expanded')==='true')!==expanded)await toggle.click();
    const timing=await frame.locator('#index-timeline').evaluate(async(root,count)=>{const rows=[];for(let i=0;i<count;i++){await new Promise(requestAnimationFrame);const healthy=!(status.errors||[]).some(x=>String(x).startsWith('live:')),start=performance.now();timeline.health(healthy,{render:false});const health=performance.now();timeline.render(status);const finished=performance.now();rows.push({health_update_ms:health-start,timeline_render_ms:finished-health,total_synchronous_ms:finished-start});}return{rows,run_state:status.run?.run?.state||'unconfigured',tracks:root.querySelectorAll('.timeline-track').length,retained_raw:status.timeline?.tracks?.find(t=>t.id==='blocks')?.retained_coverage,coverage:status.timeline?.tracks?.filter(t=>['blocks','tx-locator','bitmap-districts-canonical-v2','bitmap-parcels-canonical-v2'].includes(t.id)).map(t=>({id:t.id,union:t.coverage,committed:t.sources?.filter(s=>s.kind==='committed_records').flatMap(s=>s.ranges),browsed:t.sources?.filter(s=>s.kind==='browse_records').flatMap(s=>s.ranges)}))};},samples);
    result.browser.samples.push({expanded,...timing,summary:Object.fromEntries(['timeline_render_ms','health_update_ms','total_synchronous_ms'].map(key=>[key,stats(timing.rows.map(r=>r[key]))]))});
   }
   assert.deepEqual(errors,[]);assert.deepEqual(posts,[],'Status observation and rerendering must not mutate the application');
  }finally{await browser.close();}
 }
 const after=(await get('/api/v1/index/run')).value;result.observed_run_unchanged=JSON.stringify(before)===JSON.stringify(after);result.observed_run_before=before;result.observed_run_after=after;
 fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify(result,null,2)+'\n');console.log('Read-only status profile saved: '+args.out+'; status median '+result.summary['/api/v1/index/status'].total_ms.median.toFixed(2)+'ms; run median '+result.summary['/api/v1/index/run'].total_ms.median.toFixed(2)+'ms.');
})().catch(error=>{console.error(error);process.exitCode=1;});
