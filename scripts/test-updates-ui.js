'use strict';
// Real Chromium DOM tests plus controllable promise tests. The fixture publisher
// is never contacted: only the same-origin local updater API is represented here.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const os=require('node:os');
const vm=require('node:vm');
const http=require('node:http');
const root=path.resolve(__dirname,'..');
const source=fs.readFileSync(path.join(root,'ui/shell/updates.js'),'utf8');
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}};
async function concurrency(){
 const sandbox={window:{}};vm.runInNewContext(source,sandbox);
 const {createController,view}=sandbox.window.GatewayUpdates;
 const requests=[],changes=[];
 const controller=createController((url,data)=>{const request={url,data,...deferred()};requests.push(request);return request.promise},v=>changes.push(v),'0.6.4');
 const first=controller.refresh();
 const checking=controller.act('check');
 assert.equal(requests.length,2);
 assert.equal(controller.value().pending,'check');
 assert.equal(await controller.act('download'),false,'a second mutation cannot start while a request is pending');
 requests[1].resolve({state:'available',configured:true,available_version:'0.6.5'});
 await Promise.resolve();await Promise.resolve();
 assert.equal(requests.length,3,'action refreshes local status');
 requests[0].resolve({state:'current',configured:true,current_version:'old'});await first;
 assert.equal(controller.value().state,'available','a late pre-action GET cannot erase the available package');
 requests[2].resolve({state:'available',configured:true,available_version:'0.6.5'});await checking;
 const download=controller.act('download');requests[3].reject(Error('Connection interrupted'));assert.equal(await download,false);
 assert.equal(controller.value().state,'available','download failure retains retry context');
 assert.equal(view(controller.value()).error,'Connection interrupted');
 const older=controller.refresh(),newer=controller.refresh();
 requests[5].resolve({state:'staged',configured:true,available_version:'0.6.5',can_apply:true});await newer;
 requests[4].reject(Error('old request failed'));await older;
 assert.equal(controller.value().state,'staged');assert.equal(controller.value().client_error,'','an older failure cannot replace a newer success');
 const abandoned=controller.refresh();controller.dispose();requests[6].resolve({state:'current'});await abandoned;
 assert.equal(controller.value().state,'staged','disposing invalidates pending responses');
 assert.equal(view({state:'disabled_offline'}).state,'disabled_offline');
 assert.equal(view({state:'made-up'}).title,'Reading update status','unrecognized state cannot report up to date');
 const current=view({state:'current',current_version:'0.6.7',publisher_version:'0.6.7',platform:'windows-amd64',last_checked:'2026-10-07T12:00:00Z'});
 assert.match(current.description,/This Gateway is v0\.6\.7; the publisher offers v0\.6\.7 for windows-amd64/,'matching versions are explicit');
 assert.match(view({state:'available',current_version:'0.6.6',available_version:'0.6.7',platform:'windows-amd64'}).description,/This Gateway is v0\.6\.6/,'available update keeps the running version visible');
 assert.match(view({state:'checking'}).description,/minute to start/,'cold publisher wait is explained');
 assert.ok(changes.some(v=>v.pending==='download'));
}
function playwright(){try{return require('playwright')}catch(error){const bundled=path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright');if(fs.existsSync(bundled))return require(bundled);throw Error('Playwright is required through NODE_PATH or the bundled runtime. '+error.message)}}
function browserPath(chromium){const candidates=[process.env.GATEWAY_TEST_BROWSER,chromium.executablePath()];if(process.platform==='win32')for(const base of [process.env['ProgramFiles(x86)'],process.env.ProgramFiles,process.env.LOCALAPPDATA].filter(Boolean)){candidates.push(path.join(base,'Microsoft','Edge','Application','msedge.exe'));candidates.push(path.join(base,'Google','Chrome','Application','chrome.exe'))}const found=candidates.find(p=>p&&fs.existsSync(p));if(!found)throw Error('No installed Chromium browser found; set GATEWAY_TEST_BROWSER.');return found}
async function browserChecks(){
 let status={state:'not_configured',current_version:'0.6.4',platform:'windows-amd64',configured:false,auto_check:false};
 const requests=[],unexpected=[],publicKey=Buffer.alloc(32,7).toString('base64');let downloadFails=true,checkGate=null;
 const html='<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/sparse.css"><link rel="stylesheet" href="/shell/updates.css"><script src="/shell/updates.js" defer></script><script src="/fixture.js" defer></script></head><body><button id="updates-status" data-updates-open hidden></button><main><section id="settings-body"><nav class="settings-sections"><button data-settings-section="updates">Updates</button></nav><section id="updates-settings" class="panel"></section></section></main></body></html>';
 const server=http.createServer(async(req,res)=>{
  const reply=(body,type='application/json',code=200)=>{res.writeHead(code,{'Content-Type':type});res.end(typeof body==='string'?body:JSON.stringify(body))};
  if(req.url==='/')return reply(html,'text/html');
  if(req.url==='/fixture.js')return reply("window.GatewayUpdates.start({currentVersion:'0.6.4',api:async(path,data)=>{const response=await fetch(path,data===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});const value=await response.json();if(!response.ok)throw Error(value.error);return value}});window.GatewayUpdates.mount(document.getElementById('updates-settings'));",'application/javascript');
  if(req.url?.startsWith('/shell/')){const name=req.url.slice(7);if(['updates.js','updates.css','style.css','sparse.css'].includes(name))return reply(fs.readFileSync(path.join(root,'ui/shell',name),'utf8'),name.endsWith('.js')?'application/javascript':'text/css')}
  if(req.url==='/favicon.ico')return reply('','image/x-icon');
  if(req.url?.startsWith('/api/v1/updates/')){
   let raw='';for await(const chunk of req)raw+=chunk;const body=raw?JSON.parse(raw):undefined;requests.push({method:req.method,path:req.url,body});
   if(req.method==='GET'&&req.url.endsWith('/status'))return reply(status);
   if(req.method==='POST'&&req.url.endsWith('/config')){status={...status,...body,configured:true,state:'current',key_fingerprint:'test-key-fingerprint',access_token_configured:body.clear_access_token?false:body.access_token?true:!!status.access_token_configured};delete status.access_token;delete status.clear_access_token;return reply(status)}
   if(req.method==='POST'&&req.url.endsWith('/check')){if(checkGate)await checkGate.promise;status={...status,state:'available',available_version:'0.6.5',publisher_version:'0.6.5',last_checked:'2026-10-01T14:00:00Z',release_notes:'<img src="https://untrusted.invalid/tracker" onerror="alert(1)"> Signed test release'};return reply(status)}
   if(req.method==='POST'&&req.url.endsWith('/download')){if(downloadFails)return reply({error:'Download interrupted. Retry safely.'},'application/json',503);status={...status,state:'downloading',downloaded_bytes:512,total_bytes:1024};return reply(status)}
   if(req.method==='POST'&&req.url.endsWith('/apply')){status={...status,state:'applying'};return reply(status)}
  }
  unexpected.push(req.url);reply({error:'Unexpected fixture request'},'application/json',500);
 });
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve)});
 const origin='http://127.0.0.1:'+server.address().port,{chromium}=playwright();let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:browserPath(chromium)});
  const page=await browser.newPage();page.setDefaultTimeout(7000);const errors=[],external=[];let navigations=0;page.on('framenavigated',frame=>{if(frame===page.mainFrame())navigations++});
  page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(new URL(request.url()).origin!==origin)external.push(request.url())});
  await page.goto(origin);await page.locator('#update-title').getByText('Choose a trusted publisher',{exact:true}).waitFor();
  assert.equal(await page.locator('#update-check').isDisabled(),true);
  await page.locator('#update-publisher').fill('https://publisher.example.test');await page.locator('#update-key').fill(publicKey);await page.locator('#update-mode').selectOption('notify');
  await page.locator('#update-save').click();await page.locator('#update-config-error').getByText(/Verify the public key/).waitFor();
  assert.equal(requests.filter(r=>r.path.endsWith('/config')).length,0,'trust changes require deliberate confirmation');
  await page.locator('#update-key-confirm').check();await page.locator('#update-save').click();
  await page.locator('#update-title').getByText('Ready to check for updates',{exact:true}).waitFor();
  assert.equal(requests.filter(r=>r.path.endsWith('/check')).length,0,'browser status refresh never starts background work');
  assert.deepEqual(requests.find(r=>r.path.endsWith('/config')).body,{publisher_url:'https://publisher.example.test',trusted_key:publicKey,auto_check:true,auto_install:false});
  for(const [mode,check,install] of [['automatic',true,true],['manual',false,false],['notify',true,false]]){
   await page.locator('#update-mode').selectOption(mode);await page.locator('#update-save').click();await page.waitForFunction(()=>!document.getElementById('update-config').dataset.dirty);
   const choice=requests.filter(r=>r.path.endsWith('/config')).at(-1).body;assert.equal(choice.auto_check,check);assert.equal(choice.auto_install,install);assert.equal(await page.locator('#update-mode').inputValue(),mode,'saved choice remains visible');
  }
  status={...status,channel:'manual',channels:[{id:'preview',label:'Preview channel',publisher_url:'https://preview.example.test',trusted_key:publicKey,key_fingerprint:'fixture-preview-key'}]};
  await page.locator('#update-channel-field').waitFor();await page.locator('#update-channel').selectOption('preview');
  assert.equal(await page.locator('#update-publisher').inputValue(),'https://preview.example.test');assert.equal(await page.locator('#update-key-confirm').isChecked(),false);
  const configs=requests.filter(r=>r.path.endsWith('/config')).length;await page.locator('#update-save').click();assert.equal(requests.filter(r=>r.path.endsWith('/config')).length,configs,'channel selection still requires trust confirmation');
  await page.locator('#update-key-confirm').check();await page.locator('#update-access-token').fill('fixture-delivery-credential');await page.locator('#update-save').click();await page.locator('#update-access-state').getByText(/A publisher access credential is stored/).waitFor();
  const channelRequest=requests.filter(r=>r.path.endsWith('/config')).at(-1).body;assert.equal(channelRequest.channel,'preview');assert.equal(channelRequest.access_token,'fixture-delivery-credential');assert.equal(await page.locator('#update-access-token').inputValue(),'','saved credential is not returned to the field');
  assert.equal(await page.locator('#update-access-token').getAttribute('type'),'password');
  await page.locator('#update-clear-access').check();await page.locator('#update-save').click();await page.locator('#update-access-state').getByText(/No publisher access credential is stored/).waitFor();assert.equal(requests.filter(r=>r.path.endsWith('/config')).at(-1).body.clear_access_token,true);
  checkGate=deferred();await page.locator('#update-check').click();await page.locator('#update-title').getByText('Checking for updates',{exact:true}).waitFor();assert.equal(await page.locator('#update-check').isDisabled(),true);
  checkGate.resolve();checkGate=null;await page.locator('#update-title').getByText('v0.6.5 is available',{exact:true}).waitFor();
  assert.equal(await page.locator('#update-publisher-version').textContent(),'v0.6.5','verified publisher release is shown beside current version');
  assert.equal(await page.locator('#update-apply').isVisible(),false,'availability does not authorize installation');
  assert.equal(await page.locator('#updates-status').textContent(),'Update 0.6.5 available');
  await page.locator('#update-notes summary').click();assert.equal(await page.locator('#update-notes img').count(),0,'release notes remain text');
  await page.locator('#update-publisher').fill('https://editing.example.test');const reads=requests.filter(r=>r.method==='GET').length;
  await page.waitForFunction(()=>document.getElementById('update-title').textContent==='v0.6.5 is available');
  await new Promise(resolve=>{const check=()=>requests.filter(r=>r.method==='GET').length>reads?resolve():setTimeout(check,100);check()});
  assert.equal(await page.locator('#update-publisher').inputValue(),'https://editing.example.test','polling preserves unsaved publisher edits');
  await page.locator('#update-download').click();await page.locator('#update-error').getByText('Download interrupted. Retry safely.',{exact:true}).waitFor();assert.equal(await page.locator('#update-download').textContent(),'Retry download');
  downloadFails=false;await page.locator('#update-download').click();await page.waitForFunction(()=>document.getElementById('update-progress').getAttribute('value')==='512');assert.equal(await page.locator('#update-progress-label').textContent(),'512 B of 1.0 KiB · 50%');
  assert.equal(await page.locator('#update-check').isDisabled(),true);assert.equal(await page.locator('#update-progress').getAttribute('value'),'512');
  assert.equal(await page.locator('#update-publisher').isDisabled(),true,'Trust cannot change during a transfer');
  assert.equal(await page.locator('#update-mode').isDisabled(),false,'Update preference can be changed during a transfer');
  await page.locator('#update-mode').selectOption('manual');await page.locator('#update-save').click();await page.waitForFunction(()=>!document.getElementById('update-config').dataset.dirty);
  const withdrawn=requests.filter(r=>r.path.endsWith('/config')).at(-1).body;assert.equal(withdrawn.auto_check,false);assert.equal(withdrawn.auto_install,false);assert.equal(withdrawn.publisher_url,'https://preview.example.test','Mid-transfer opt-out cannot also submit an unsaved trust change');
  status={...status,state:'staged',can_apply:false,apply_unavailable_reason:'Fixture unsupported executable layout'};
  await page.locator('#update-title').getByText('v0.6.5 is ready to install',{exact:true}).waitFor();assert.equal(await page.locator('#update-apply').isDisabled(),true);await page.locator('#update-apply-help').getByText(status.apply_unavailable_reason,{exact:true}).waitFor();
  status={...status,can_apply:true};await page.waitForFunction(()=>!document.getElementById('update-apply').disabled);
  assert.equal(requests.filter(r=>r.path.endsWith('/apply')).length,0,'staging never automatically restarts');
  await page.locator('#update-apply').click();await page.locator('#update-title').getByText('Restarting to install v0.6.5',{exact:true}).waitFor();
  assert.equal(requests.filter(r=>r.path.endsWith('/apply')).length,1);assert.equal(await page.locator('#update-apply').isDisabled(),true);
  status={...status,current_version:'0.6.5'};await page.waitForFunction(()=>document.getElementById('update-current')?.textContent==='v0.6.5');assert.equal(navigations,1,'new process must finish installation before the UI reloads into its ordinary API admission barrier');
  const reloaded=page.waitForEvent('framenavigated',{predicate:frame=>frame===page.mainFrame()});status={...status,state:'current',current_version:'0.6.5',available_version:'',can_apply:false};await reloaded;await page.waitForLoadState('load');await page.waitForFunction(()=>document.getElementById('update-current')?.textContent==='v0.6.5');assert.equal(navigations,2,'same-origin status recovery reloads the new bundled client UI exactly once');assert.equal(new URL(page.url()).origin,origin);
  status={...status,state:'disabled_offline'};await page.locator('#update-title').getByText('Updates paused while offline',{exact:true}).waitFor();assert.equal(await page.locator('#update-check').isDisabled(),true);
  assert.deepEqual(errors,[],'uncaught UI exceptions');assert.deepEqual(external,[],'ordinary client never contacts GitHub or a publisher directly');assert.deepEqual(unexpected,[]);
 }finally{if(browser)await browser.close();await new Promise(resolve=>server.close(resolve))}
}
async function run(){await concurrency();await browserChecks();console.log('Updates UI: PASS (real Chromium configuration/trust confirmation, manual check, busy controls, signed update availability, text-only notes, polling edit preservation, failed download/retry/progress, explicit staged install, unsupported layout/offline states; stale-success/failure and pending-action guards).')}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1});
