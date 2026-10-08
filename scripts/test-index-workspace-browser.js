'use strict';
// Real DOM with source-identical scripts and isolated index API fixtures.
// No real indexing, publication, integrations or outside network requests.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..'),pw=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright'))}})();
const exe=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft','Edge','Application','msedge.exe'),path.join(process.env[k],'Google','Chrome','Application','chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const outputArg=process.argv.indexOf('--out'),out=outputArg<0?null:path.resolve(process.argv[outputArg+1]);if(out)fs.mkdirSync(out,{recursive:true});
async function run(){
 const server=http.createServer((req,res)=>{res.writeHead(500);res.end('Unexpected request')});await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port;
 const browser=await pw.chromium.launch({headless:true,executablePath:exe});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:1000},reducedMotion:'reduce'}),errors=[],calls=[];page.on('pageerror',e=>errors.push(e.message));
  const definitions=[['blocks','Bitcoin blocks'],['tx-locator','Transaction locations'],['sat-state','Sat index'],['inscriptions','Inscriptions'],['bitmap','Bitmap districts'],['txo-spender','TXO spenders']].map(([id,name])=>({id,name,buildable:true,theory:'Fixture-derived evidence with explicit coverage.',start_height:id==='bitmap'?792435:0,network:'bitcoin-mainnet',rules:['Only committed evidence establishes coverage.'],dependencies:['headers'],version:1,arbitrary_start:id!=='sat-state'}));
  const active={id:'bitmap-running',index:'bitmap',state:'running',queue_managed:true,from:792435,to:792500,height:792449,retention:'ephemeral',mode:'lean'};
  let measurePolling=false;const pollTimes={status:[],records:[]};
  let jobs=[active],requests=[],rows=[{id:'knowni0',reveal_height:767430,canonical_number:0,sat_number:0},{id:'unknowni0',reveal_height:767431,numbering_state:'missing_dependency'}],discovered=false;
  const instances=[{definition:'inscriptions',checkpoint:{from:767430,height:767431,commitment:'fixture-1'},coverage:[{from:767430,to:767431}],retention:'ephemeral',mode:'full',verification:'selected_chain'}];
  await page.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url()),body=req.method()==='POST'?req.postDataJSON():null;if(url.origin!==origin)throw Error('External request '+url);
   const reply=(value,type='application/json')=>route.fulfill({status:200,contentType:type,body:type==='application/json'?JSON.stringify(value):value});
   if(url.pathname==='/indexes')return reply(fs.readFileSync(path.join(root,'ui/shell/indexes.html'),'utf8').replaceAll('__INDEX_NONCE__','fixture'),'text/html');
   if(url.pathname.startsWith('/shell/'))return route.fulfill({status:200,contentType:url.pathname.endsWith('.js')?'application/javascript':'text/css',body:fs.readFileSync(path.join(root,'ui',url.pathname))});
   if(url.pathname==='/favicon.ico')return reply('','image/x-icon');
   if(url.pathname==='/api/v1/appearance')return reply({theme:'light',default:'light'});
   calls.push({path:url.pathname,body});
   if(measurePolling&&url.pathname==='/api/v1/index/status'){pollTimes.status.push(performance.now());instances[0].checkpoint.commitment='fixture-cadence-'+pollTimes.status.length}
   if(measurePolling&&url.pathname==='/api/v1/index/query')pollTimes.records.push(performance.now());
   if(url.pathname==='/api/v1/index/status')return reply({definitions,instances,providers:definitions.map(d=>({id:d.id,provider:'fixture',queryable:true,coverage:[]})),live:[],job:active,jobs});
   if(url.pathname==='/api/v1/index/plan'){requests.push(body);return reply({definition:definitions.find(d=>d.id===body.index),from:body.from,to:body.to,retention:body.retention,dependencies:[],notes:['Only missing derived outputs are computed.'],outputs:(body.outputs||[]).map(index=>({index,reuse:[{from:0,to:10}],add:[{from:11,to:body.to}],reason:'Compatible fixture coverage reused.'})),storage_estimate:'Fixture',verification:'selected_chain'})}
   if(url.pathname==='/api/v1/index/build'){const queued={id:'blocks-queued',index:body.index,queue_managed:true,outputs:body.outputs,state:'queued',queue_position:1,waiting_reason:'Waiting for Bitmap fixture',from:body.from,to:body.to,height:body.from-1,retention:body.retention};jobs=[active,queued];return reply(queued)}
   if(url.pathname==='/api/v1/index/queue')return reply(jobs.find(j=>j.id===body.id));
   if(url.pathname==='/api/v1/index/query')return reply({checkpoint:{height:767431},rows:url.searchParams.get('index')==='inscriptions'?rows.map(r=>r.id==='unknowni0'&&discovered?{...r,sat_number:42}:r):url.searchParams.get('index')==='sat-state'?[{block_height:10,block_hash:'fixture-sat-snapshot',data:{outputs:12,retain_history:false}}]:[],total:rows.length,total_known:true,inspected_blocks:2,chain_state:'selected_chain'});
   if(url.pathname==='/api/v1/index/discover-sat'){assert.equal(body.id,'unknowni0');discovered=true;return reply({known:true,sat_number:42,state:'verified',note:'Fixture local evidence'})}
   if(url.pathname==='/api/v1/index/sat')return reply({known:true,sat_number:0,state:'located',satpoint:{txid:'fixture',vout:0,offset:0},snapshot:{height:10},origin:{height:0},chain_state:'selected_chain',history_retained:false,history_recording:true,history_coverage:[{from:7,to:10}],note:'Known at snapshot 10.'});
   throw Error('Unexpected '+req.method()+' '+url.pathname);
  });
  await page.goto(origin+'/indexes');await page.locator('.timeline-track[data-index="inscriptions"]').waitFor();assert.equal(calls.filter(c=>c.body).length,0);
  const selectTrack=async(id,view='build')=>{if(!await page.locator('.timeline-track[data-index="'+id+'"]').count()){await page.locator('#timeline-add-index').click();await page.locator('#timeline-index-picker button[data-index="'+id+'"]').click()}await page.locator('.timeline-track[data-index="'+id+'"] .timeline-track-select').click();await page.locator('#timeline-details').evaluate(el=>el.open=true);if(view!== 'build')await page.locator('[data-view="'+view+'"]').click()};
  await selectTrack('blocks');assert.equal(await page.locator('#index').inputValue(),'blocks');assert.equal(await page.locator('#plan-form').isVisible(),true);assert.equal(calls.filter(c=>c.body).length,0,'inspection never enables or starts work');
  for(const id of ['sat-state','tx-locator','inscriptions','bitmap','txo-spender'])await page.locator('[data-output="'+id+'"]').check();
  await page.locator('#from').fill('0');await page.locator('#to').fill('792436');await page.locator('#advanced-retention summary').click();await page.locator('#retain-sat-history').check();
  await page.locator('#plan-button').click();await page.locator('#plan-notes').getByText(/sat-state: Compatible fixture coverage reused/).waitFor();assert.equal(await page.locator('#build').innerText(),'Queue reviewed plan');assert.equal(await page.locator('#build').isEnabled(),true);
  const plan=requests.at(-1);assert.deepEqual(plan.outputs,['sat-state','tx-locator','inscriptions','bitmap','txo-spender']);assert.equal(plan.retention,'ephemeral');assert.equal(plan.retain_sat_history,true);assert.equal(plan.sat_history_configured,true);
  await page.locator('#build').click();await page.locator('#job-summary').getByText(/Waiting for Bitmap fixture/).waitFor();assert.equal(await page.locator('#job-summary').isVisible(),true,'Selected track owns the queued job details');
  await page.locator('#job-queue').getByRole('button',{name:'Later',exact:true}).click();assert.deepEqual(calls.filter(c=>c.path.endsWith('/queue')).at(-1).body,{id:'blocks-queued',action:'down'});
  await selectTrack('inscriptions','results');await page.locator('#query-results tbody tr').nth(1).waitFor();assert.equal(await page.locator('#job-summary').innerText(),'Inscriptions · queued · queue position 1 · Waiting for Bitmap fixture · Shared with blocks, sat-state, tx-locator, inscriptions, bitmap, txo-spender. Pausing pauses this shared job.');
  const known=page.locator('#query-results tbody tr').filter({hasText:'knowni0'}).first();assert.equal(await known.locator('td').nth(3).innerText(),'0');assert.equal(await known.locator('td').nth(4).innerText(),'0');
  await page.locator('#query-results').getByRole('button',{name:'Discover sat number',exact:true}).click();await page.locator('#query-results tbody').getByText('42',{exact:true}).waitFor();assert.equal(calls.filter(c=>c.path.endsWith('/discover-sat')).length,1);
  rows.unshift({id:'newi0',reveal_height:767432,sat_number:43});await page.locator('#query-form button').click();await page.locator('#query-summary').getByText(/1 newly received record/).waitFor();assert.equal(await page.locator('.record-received').count(),1,'only newly received API records stream into the table');
  measurePolling=true;const pollDeadline=Date.now()+26000;while((pollTimes.status.length<4||pollTimes.records.length<4)&&Date.now()<pollDeadline)await page.waitForTimeout(100);measurePolling=false;assert.equal(pollTimes.status.length>=4&&pollTimes.records.length>=4,true,'four status and changed-checkpoint record polls observed');
  const pollIntervals=Object.fromEntries(Object.entries(pollTimes).map(([name,times])=>[name,times.slice(1,4).map((time,i)=>Math.round(time-times[i]))]));if(out)fs.writeFileSync(path.join(out,'poll-cadence.json'),JSON.stringify({scope:'Real Chromium request timestamps; synthetic committed checkpoint changes at each API fixture status response; no real indexing/block throughput measured',configured_delay_ms:5000,intervals_ms:pollIntervals},null,2));console.log('Index polling intervals ms: '+JSON.stringify(pollIntervals));
  if(out)await page.screenshot({path:path.join(out,'inscriptions-records-and-queue.png'),fullPage:true});
  await selectTrack('blocks');assert.equal(await page.locator('#to').inputValue(),'792436','track-specific draft preserved');assert.equal(await page.locator('[data-output="sat-state"]').isChecked(),true);assert.equal(await page.locator('#retain-sat-history').isChecked(),true);
  await selectTrack('sat-state');await page.locator('#job-records').click();await page.locator('#query-results').getByText('12 unspent outputs',{exact:true}).waitFor();await page.locator('#sat-lookup-number').fill('0');await page.locator('#sat-lookup-form button').click();await page.locator('#sat-lookup-summary').getByText('Known at snapshot 10.',{exact:true}).waitFor();await page.locator('#sat-lookup-meta').getByText('On for newly indexed blocks',{exact:true}).waitFor();assert((await page.locator('#sat-lookup-meta').innerText()).includes('7–10'),'partial movement-history coverage is visible');assert.equal(await page.locator('#lookup-form').isVisible(),false);
  await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);if(out)await page.screenshot({path:path.join(out,'sat-index-narrow.png'),fullPage:true});
  assert.equal(calls.filter(c=>/\/live$|\/publish$|\/peers$/.test(c.path)).length,0);assert.deepEqual(errors,[]);
  console.log('Index timeline workspace browser: PASS (track-selected details, concrete shared-output plan + independent history retention, queue acceptance/order, job ownership, retained per-track drafts, known sat zero/number zero, explicit discovery, real received-row animation, Sat snapshot lookup, narrow layout; isolated APIs).');
 }finally{await browser.close();await new Promise(r=>server.close(r))}
}
run().catch(e=>{console.error(e);process.exitCode=1});
