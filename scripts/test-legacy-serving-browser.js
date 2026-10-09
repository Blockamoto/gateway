'use strict';
// Render the actual Advanced controls page with read-only API fixtures. No
// real listener, installed profile, stored data or network settings are changed.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const playwright=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright'))}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,playwright.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft','Edge','Application','msedge.exe'),path.join(process.env[k],'Google','Chrome','Application','chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const html=fs.readFileSync(path.join(root,'web.go'),'utf8').match(/const indexHTML = \x60([\s\S]*?)\x60/);
assert(html,'The actual legacy page must be embedded in web.go');
async function run(){
 const browser=await playwright.chromium.launch({headless:true,executablePath:browserPath});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[],mutations=[],unexpected=[];
  const fixture={version:'0.7.1',settings:{onboarded:true,serve_data:true,cache_blocks:true,share_cache:true,storage_cap_mb:1024,rpc_auth_mode:'auto'},core:{connected:false},core_store:{},discovered_peers:[],compatibility:{},bitcoin_p2p:{requested:true,enabled:true,listen_port:48444,automatic_port:true,node_witness:true,blocks_served:2,not_found:3}};
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url());
   if(request.method()!=='GET'){mutations.push({path:url.pathname,method:request.method()});return route.abort()}
   if(url.origin!=='http://gateway-legacy.test'){unexpected.push(request.url());return route.abort()}
   if(url.pathname==='/legacy')return route.fulfill({contentType:'text/html; charset=utf-8',body:html[1]});
   const responses={
    '/api/v1/p2p/status':fixture,
    '/api/v1/status':{header_height:840000,ready:true},
    '/api/v1/system/status':{supported:false},
    '/api/v1/browser/status':{namespaces:[]},
    '/api/v1/graph/status':{native_complete_through:-1},
    '/api/v1/satline/status':{enabled:false},
    '/api/v1/sync/status':{mode:'on-demand',running:false}
   };
   if(!Object.hasOwn(responses,url.pathname)){unexpected.push(url.pathname);return route.abort()}
   return route.fulfill({contentType:'application/json',body:JSON.stringify(responses[url.pathname])});
  });
  await page.goto('http://gateway-legacy.test/legacy?settings=1');
  await page.waitForFunction(()=>document.querySelector('#drawer.open'));
  const notice=page.locator('[data-bitcoin-serving-status]');
  assert.equal(await notice.count(),1);
  assert.equal(await notice.locator('b').innerText(),'ON');
  assert.match(await notice.innerText(),/port 48,?444/,'Show the actual automatically selected port');
  assert.match(await notice.innerText(),/default port is busy/);
  assert(!((await notice.innerText()).includes('48333')),'Do not substitute the default port');
  assert.match(await notice.innerText(),/served 2 blocks.*3 notfound/,'Keep serving counters visible');
  assert.equal(await page.locator('#serve').isChecked(),true);
  async function render(listener){
   fixture.bitcoin_p2p=listener;fixture.settings.serve_data=listener.requested;
   const before=structuredClone(fixture);
   await page.evaluate(()=>refreshStatus());
   assert.deepEqual(fixture,before,'Rendering must not rewrite saved preferences');
   assert.equal(await page.locator('#serve').isChecked(),listener.requested);
   assert.equal(await page.locator('#cache').isChecked(),true);
   assert.equal(await page.locator('#shareCache').isChecked(),true);
   assert.equal(await page.locator('#cap').inputValue(),'1024');
   return notice.innerText();
  }
  let text=await render({requested:true,enabled:true,listen_port:48335,automatic_port:false});
  assert.equal(await notice.locator('b').innerText(),'ON');assert.match(text,/port 48,?335/);assert(!text.includes('default port is busy'));
  const hostile='bind failed: <img src=x onerror="window.injected=true"> & "occupied"';
  text=await render({requested:true,enabled:false,listen_port:0,automatic_port:false,error:hostile});
  assert.equal(await notice.locator('b').innerText(),'UNAVAILABLE');assert(text.includes(hostile),'Show the listener error literally');
  assert(!/\bport \d/.test(text),'A failed listener must not advertise a port');
  assert.equal(await notice.locator('img').count(),0,'Error text must not become HTML');
  assert.equal(await page.evaluate(()=>window.injected),undefined);
  text=await render({requested:true,enabled:false,listen_port:0,automatic_port:false});
  assert.equal(await notice.locator('b').innerText(),'UNAVAILABLE','Requested serving is not off while its listener is unavailable');assert(!/\bport \d/.test(text));
  text=await render({requested:false,enabled:false,listen_port:0,automatic_port:false});
  assert.equal(await notice.locator('b').innerText(),'OFF');assert(!/\bport \d/.test(text));assert(!text.includes(hostile),'Stopping clears stale error text');
  assert.deepEqual(mutations,[],'Reading Advanced controls must not mutate settings or stored data');
  assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);
  console.log('PASS: Advanced controls shows actual serving ports, automatic fallback, unavailable errors and off state; errors are escaped and settings remain unchanged.');
 }finally{await browser.close()}
}
run().catch(error=>{console.error(error);process.exitCode=1});
