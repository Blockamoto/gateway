'use strict';
// Real source UI with local read-only fixtures; no running profile is used.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const pw=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const anchor='a'.repeat(64),snapshot={height:1,block_hash:anchor,commitment:'b'.repeat(64)},origin='http://sat-point.test';
(async()=>{
 const browser=await pw.chromium.launch({headless:true,executablePath:browserPath});
 try{
  const page=await browser.newPage(),calls=[],errors=[],unexpected=[];let mode='ready',release;
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async route=>{
   const r=route.request(),u=new URL(r.url());const reply=(v,type='application/json')=>route.fulfill({contentType:type,body:type==='application/json'?JSON.stringify(v):v});
   if(u.origin!==origin){unexpected.push(u.href);return route.abort();}
   if(u.pathname==='/')return reply('<!doctype html><body><section id="pane">Inspector</section><script src="/shell/timeline-explorer.js"></script><script>window.opened=[];window.helper=GatewayTimelineExplorer.mount({root:document.getElementById("pane"),document,onNavigate:a=>opened.push(a)});</script>','text/html');
   if(u.pathname==='/shell/timeline-explorer.js')return reply(fs.readFileSync(path.join(root,'ui/shell/timeline-explorer.js'),'utf8'),'application/javascript');
   if(u.pathname==='/favicon.ico')return reply('','image/x-icon');
   calls.push({path:u.pathname,method:r.method(),query:u.search,body:r.postData()});
   if(u.pathname==='/api/v1/navigate')return reply({resource:{kind:'block',block:{height:1,hash:anchor,transaction_count:1,transactions:[]}}});
   if(u.pathname==='/api/v1/sats/point'){
    assert.equal(r.method(),'GET');assert.equal(u.searchParams.get('output'),'0.0.1');assert.equal(u.searchParams.get('block_hash'),anchor);
    const offset=Number(u.searchParams.get('offset')),selected=mode;
    if(selected==='defer')await new Promise(resolve=>release=resolve);
    return reply({state:selected==='missing'?'unavailable':'ready',output_coordinate:selected==='mismatch'?'1.0.1':'0.0.1',block_hash:anchor,offset,sat_number:100+offset,snapshot:selected==='bad-checkpoint'?{...snapshot,commitment:''}:snapshot,note:'Saved ranges unavailable; no history scan was started.'});
   }
   unexpected.push(u.pathname);return route.abort();
  });
  await page.route('**/api/v1/index/layout',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({schema:1,order:{},revision:0})}));
  await page.goto(origin);await page.evaluate(()=>helper.loadHierarchy({kind:'block',height:1}));
  const choose=offset=>page.evaluate(offset=>helper.entity({kind:'satpoint',height:1,txIndex:0,outputIndex:0,satOffset:offset}),offset);
  await choose(9);await page.waitForFunction(()=>opened.at(-1)==='109.sat');assert.deepEqual(await page.evaluate(()=>opened),['109.sat'],'Output-local9 resolves to numbered109; never treat offset as identity');
  const before=calls.length;mode='missing';await choose(8);await page.getByText('Saved ranges unavailable; no history scan was started.',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>opened.length),1);assert.equal(calls.slice(before).filter(c=>c.path==='/api/v1/navigate').length,0,'Missing ranges do not fall through to transaction fetch');
  mode='mismatch';await choose(7);await page.getByText('The Sat identity response does not match this output and offset.',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>opened.length),1);
  mode='bad-checkpoint';await choose(6);await page.getByText('The saved Sat identity is missing its authenticated checkpoint.',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>opened.length),1);
  mode='defer';await choose(5);let deadline=Date.now()+5000;while(!release&&Date.now()<deadline)await new Promise(r=>setTimeout(r,10));assert(release,'Delayed point fixture was requested');mode='ready';await choose(4);await page.waitForFunction(()=>opened.at(-1)==='104.sat');release();await page.waitForTimeout(50);assert.deepEqual(await page.evaluate(()=>opened),['109.sat','104.sat'],'An old response cannot navigate over the newer selection');
  release=null;mode='defer';await choose(3);deadline=Date.now()+5000;while(!release&&Date.now()<deadline)await new Promise(r=>setTimeout(r,10));assert(release);const beforeHealth=calls.length;await page.evaluate(()=>{helper.health(false);helper.health(true)});await page.getByText('Select the Sat again to retry its saved identity. No history scan was started.',{exact:true}).waitFor();release();await page.waitForTimeout(50);assert.equal(calls.length,beforeHealth,'Status recovery cannot fall through into raw-block loading');assert.deepEqual(await page.evaluate(()=>opened),['109.sat','104.sat'],'Cancelled Sat identity cannot navigate after status recovery');
  assert.equal(calls.some(c=>c.method!=='GET'&&c.path!=='/api/v1/navigate'||c.path.includes('satline')||c.path.includes('/index/build')),false,'Sat selection starts no build, trace or scan');assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);
  console.log('PASS: retained-range Sat selection, exact output/offset/checkpoint, unavailable evidence, no transaction fallback, stale response protection and no trace/build.');
 }finally{await browser.close();}
})().catch(error=>{console.error(error.stack||error);process.exitCode=1});
