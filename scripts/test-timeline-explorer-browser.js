'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const root=path.resolve(__dirname,'..');
const os=require('node:os');
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
const transactions=(from,to)=>Array.from({length:to-from},(_,n)=>({index:from+n,txid:(''+(from+n)).padStart(64,'0'),output_sats:50,coinbase:from+n===0}));
const block=height=>({height,hash:String(height).padStart(64,'a'),transaction_count:45,transactions:transactions(0,40),serialized_bytes:2000,time_iso:'2026-10-08',verification_state:'header_anchored',total_output_sats:2250,verification:{verifier_version:1},source_network:'cache',evidence:{bytes:'cache'}});
(async()=>{
  const server=http.createServer((q,r)=>{r.end();});await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const origin='http://127.0.0.1:'+server.address().port,browser=await pw.chromium.launch({headless:true,executablePath:browserPath});
  try{
    const page=await browser.newPage({viewport:{width:900,height:700}}),calls=[],errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.route('**/*',async route=>{
      const url=new URL(route.request().url()),body=route.request().postDataJSON();
      const reply=(value,type='application/json')=>route.fulfill({contentType:type,body:type==='application/json'?JSON.stringify(value):value});
      if(url.pathname==='/test')return reply('<!doctype html><html><head><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/timeline.css"></head><body class="indexes-workspace"><section id="timeline-inspector" class="timeline-inspector"><h2 id="retained-title">Inspector content</h2><input id="retained-input" value="kept"></section><script src="/helper.js"></script><script>window.helper=GatewayTimelineExplorer.mount({root:document.querySelector("#timeline-inspector"),document});</script></body></html>','text/html');
      if(url.pathname==='/helper.js')return reply(fs.readFileSync(path.join(root,'ui/shell/timeline-explorer.js'),'utf8'),'application/javascript');
      if(url.pathname==='/style.css')return reply(fs.readFileSync(path.join(root,'ui/shell/style.css'),'utf8'),'text/css');
      if(url.pathname==='/timeline.css')return reply(fs.readFileSync(path.join(root,'ui/shell/index-timeline.css'),'utf8'),'text/css');
      if(url.pathname==='/favicon.ico')return reply('','image/x-icon');
      calls.push({path:url.pathname,body,query:url.search});
      if(url.pathname==='/api/v1/navigate'){
        const parts=body.address.split('.'),height=Number(parts[parts.length-2]);
        if(height===60 || parts.length===3&&Number(parts[0])===1)await new Promise(r=>setTimeout(r,500));
        if(height===70)await new Promise(r=>setTimeout(r,250));
        if(parts.length===2){try{return await reply({resource:{block:block(height)},elapsed_ms:3});}catch{return;}}
        const index=Number(parts[0]),tx={...transactions(index,index+1)[0],inputs:[{n:0,prev_txid:'b'.repeat(64),prev_vout:2}],outputs:[{n:0,value_sats:50,address:'bc1fixture'}],version:2,lock_time:0};
        try{return await reply({resource:{transaction:{height,block_hash:block(height).hash,verification_state:'header_anchored',transaction:tx}}});}catch{return;}
      }
      if(url.pathname==='/api/v1/block/transactions')return reply({height:61,hash:block(61).hash,transactions:transactions(40,45),total:45,next:45});
      if(url.pathname==='/api/v1/flow')return reply({inputs:[{index:0,outpoint:'fixture',value:60}],outputs:[{n:0,value_sats:50}],fee:10,note:'Fixture value flow'});
      throw Error('Unexpected request '+url.pathname);
    });
    await page.goto(origin+'/test');
    assert.equal(await page.locator('#retained-input').inputValue(),'kept');
    await page.locator('#timeline-explorer-tab').click();assert.equal(calls.length,0,'Opening empty Explorer tab does not fetch');
    await page.evaluate(()=>helper.focus({index:'blocks'}));assert.equal(calls.length,0,'Track-only focus does not fetch');
    await page.evaluate(()=>{helper.focus({height:50,immediate:false});helper.focus({height:51,immediate:false});helper.focus({height:52,immediate:false});});
    await page.waitForTimeout(800);assert.equal(calls.length,0,'Scrub has a settling interval');
    await page.waitForFunction(()=>helper.state().loadedHeight===52);assert.deepEqual(calls.map(c=>c.body?.address),['52.bitcoin'],'Only final scrub focus loads');
    await page.locator('#timeline-inspector-tab').click();
    await page.evaluate(()=>helper.focus({height:60,immediate:true}));await page.waitForTimeout(80);
    await page.evaluate(()=>helper.focus({height:61,immediate:true}));await page.waitForFunction(()=>helper.state().loadedHeight===61);
    await page.waitForTimeout(550);assert.equal(await page.evaluate(()=>helper.state().loadedHeight),61,'Delayed stale block cannot replace newer focus');
    assert.equal(await page.evaluate(()=>helper.state().active),'inspector','Explicit tab choice stays through focus changes');
    const before=calls.length;await page.evaluate(()=>helper.focus({index:'headers',height:61,immediate:true}));assert.equal(calls.length,before,'Changing track preserves loaded block without fetching');
    await page.locator('#timeline-explorer-tab').click();
    await page.locator('#timeline-explorer-tx-next').click();await page.waitForFunction(()=>document.querySelector('#timeline-explorer-tx-page').textContent.startsWith('41'));
    assert.equal(await page.locator('#timeline-explorer-tx-table tbody tr').count(),5);
    await page.locator('[data-explorer-tx="40"]').click();await page.locator('#timeline-explorer-flow').waitFor();
    assert.equal(calls.at(-1).body.address,'40.61.bitcoin','Transaction resolves by block position, without locator');
    assert.match(await page.locator('#timeline-explorer-tx-detail').innerText(),/Input 0/);assert.match(await page.locator('#timeline-explorer-tx-detail').innerText(),/Output 0/);
    await page.locator('#timeline-explorer-flow').click();await page.waitForFunction(()=>!document.querySelector('#timeline-explorer-flow-result').hidden);
    assert.equal(await page.locator('#timeline-explorer-flow-result svg').count(),1);
    assert.equal(await page.locator('#timeline-explorer-tx-detail button').filter({hasText:'Satline'}).isDisabled(),true);
    assert.equal(calls.some(c=>c.path.includes('graph')||c.path.includes('satline')||c.path.includes('index/build')),false,'Locked indexes and jobs never requested');
    await page.evaluate(()=>{helper.focus({height:62,immediate:false});helper.focus({height:62,immediate:true});});await page.waitForFunction(()=>helper.state().loadedHeight===62);
    await page.waitForTimeout(1100);assert.equal(calls.filter(c=>c.body?.address==='62.bitcoin').length,1,'Immediate click accelerates pending scrub once');
    await page.evaluate(()=>{helper.focus({height:64,immediate:false});helper.health(false);});await page.waitForTimeout(1100);
    assert.equal(calls.some(c=>c.body?.address==='64.bitcoin'),false,'Status failure cancels a deferred scrub before a fetch starts');
    assert.equal(await page.evaluate(()=>helper.state().height),64);assert.equal(await page.locator('#timeline-explorer-retry').count(),0,'Paused status has no active retry action');
    assert.match(await page.locator('#timeline-explorer-panel').innerText(),/paused until status recovers/);
    await page.evaluate(()=>helper.focus({height:65,immediate:true}));await page.waitForTimeout(50);assert.equal(calls.some(c=>c.body?.address==='65.bitcoin'),false,'Paused status cannot fetch even explicit focus');
    await page.evaluate(()=>helper.health(true));await page.waitForFunction(()=>helper.state().loadedHeight===65);
    assert.equal(calls.filter(c=>c.body?.address==='65.bitcoin').length,1,'Recovery loads the latest intentional focus once');
    await page.evaluate(()=>helper.health(true));await page.waitForTimeout(50);assert.equal(calls.filter(c=>c.body?.address==='65.bitcoin').length,1,'Healthy refresh does not repeat requests');
    await page.evaluate(()=>helper.focus({height:60,immediate:true}));await page.waitForTimeout(60);await page.evaluate(()=>helper.health(false));await page.waitForTimeout(550);
    assert.equal(await page.evaluate(()=>helper.state().loading),'paused','Health failure cancels and rejects in-flight responses');
    await page.evaluate(()=>{helper.focus({height:66,immediate:true});helper.health(true);});await page.waitForFunction(()=>helper.state().loadedHeight===66);
    await page.evaluate(()=>{window.savedSetTimeout=window.setTimeout;window.setTimeout=(callback,delay,...args)=>savedSetTimeout(callback,delay===90000?50:delay,...args);helper.focus({height:70,immediate:true});});
    await page.waitForFunction(()=>helper.state().loading==='error');assert.match(await page.locator('#timeline-explorer-panel').innerText(),/took too long/);assert.equal(await page.locator('#timeline-explorer-retry').count(),1);
    await page.evaluate(()=>window.setTimeout=window.savedSetTimeout);
    await page.evaluate(()=>{helper.focus({height:63,immediate:false});helper.teardown();});await page.waitForTimeout(1100);
    assert.equal(calls.some(c=>c.body?.address==='63.bitcoin'),false,'Teardown cancels settling requests');
    assert.equal(await page.locator('#retained-input').inputValue(),'kept');assert.equal(await page.locator('#timeline-explorer-panel').count(),0);assert.deepEqual(errors,[]);
    console.log('Timeline Explorer helper browser acceptance passed: debounce, stale focus, tabs, pagination, transactions, flow, status loss/recovery, timeout and teardown.');
  }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
