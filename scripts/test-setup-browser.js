'use strict';
// Isolated browser acceptance. Every integration response is a fixture; no
// registration, external request or installed application is changed.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..');
const playwright=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright'))}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,playwright.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft','Edge','Application','msedge.exe'),path.join(process.env[k],'Google','Chrome','Application','chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const outIndex=process.argv.indexOf('--out'),out=outIndex>=0?path.resolve(process.argv[outIndex+1]):null;if(out)fs.mkdirSync(out,{recursive:true});
async function run(){
 const server=http.createServer((req,res)=>{res.writeHead(500);res.end('Unexpected fixture request')});await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port;
 const browser=await playwright.chromium.launch({headless:true,executablePath:browserPath});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900},reducedMotion:'reduce'}),errors=[],mutations=[];let settingsFailure=false,routingAttempt=0,launchFailure=true;
  let settings={core_disabled:true,core_mount_disabled:true,bitcoin_data_dir:'C:\\fixture-core',rpc_port:8332,network_disabled:true,cache_blocks:true,privacy_mode:true,share_cache:false,serve_data:false,onboarded:false,bitcoin_peers:[]};
  let progress={schema:2,stage:0,flow_stage:0,completed:false,integration_reviewed:false,browser:'edge',profile:'Default'},system={supported:true,run_on_startup:false,god_protocol:false,bitcoin_domain_rule:false};
  let native=false,headerHeight=1,measurePolling=false;const pollTimes={network:[],readiness:[]};
  const browserState=()=>({native_active:native,companion_path:'C:\\fixture\\browser-companion',namespaces:[{suffix:'.bitcoin'},{suffix:'.gateway'},{suffix:'.bitmap'}],browsers:[{id:'edge',name:'Edge',installed:true,native_registered:routingAttempt>0},{id:'chrome',name:'Chrome',installed:false,native_registered:false}]});
  const snapshot=()=>({mode:'installed',show_automatically:!progress.completed,settings,progress,system,browser:browserState(),core:{connected:false},core_store:{},mounted_preparation:{},data_directory:'C:\\fixture-profile'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url()),method=request.method(),body=method==='POST'?request.postDataJSON():null;
   if(url.origin!==origin)throw Error('External request '+url);
   const reply=(value,status=200,type='application/json')=>route.fulfill({status,contentType:type,body:type==='application/json'?JSON.stringify(value):value});
   if(url.pathname==='/')return reply(fs.readFileSync(path.join(root,'ui/shell/index.html'),'utf8').replace('__GATEWAY_BOOTSTRAP__',JSON.stringify({version:'0.6.5-test',address:'.gateway'})),200,'text/html');
   if(url.pathname.startsWith('/shell/')){const file=path.join(root,'ui',url.pathname);return route.fulfill({status:200,contentType:url.pathname.endsWith('.js')?'application/javascript':url.pathname.endsWith('.css')?'text/css':'image/svg+xml',body:fs.readFileSync(file)})}
   if(method==='POST')mutations.push({path:url.pathname,body});
   if(measurePolling&&method==='GET'){if(url.pathname==='/api/v1/network')pollTimes.network.push(performance.now());if(url.pathname==='/api/v1/setup')pollTimes.readiness.push(performance.now())}
   if(url.pathname==='/api/v1/setup'){
    if(body?.action==='browser-routing'){routingAttempt++;system={...system,god_protocol:true,bitcoin_domain_rule:routingAttempt>1};return reply({...snapshot(),integration_result:{complete:routingAttempt>1,steps:[{id:'edge',name:'Edge native host',success:true},{id:'namespaces',name:'Gateway browser namespaces',success:routingAttempt>1,error:routingAttempt===1?'Administrator cancelled':''}]}})}
    if(body?.action==='save'){progress={...progress,...body};delete progress.action}
    if(body?.action==='complete'){progress.completed=true;settings.onboarded=true}
    return reply(snapshot());
   }
   if(url.pathname==='/api/v1/settings'){if(body){if(settingsFailure)return reply({error:'Settings could not be saved'},500);settings={...settings,...body}}return reply({settings,core:{connected:false},core_store:{}})}
   if(url.pathname==='/api/v1/system/status')return reply(system);
   if(url.pathname==='/api/v1/browser/status')return reply(browserState());
   if(url.pathname==='/api/v1/browser/open-setup')return launchFailure?reply({error:'Selected browser launch unavailable'},404):reply({state:'manual_navigation_required',manual_url:'edge://extensions/'});
   if(url.pathname==='/api/v1/network')return reply({enabled:false,connected:0,gateway_connected:0,headers:{header_height:headerHeight,header_target_height:2,header_state:headerHeight===2?'current':'catching_up'}});
   if(url.pathname==='/api/v1/index/status')return reply({definitions:[],live:[],instances:[],job:{}});
   if(url.pathname==='/api/v1/updates/status')return reply({state:'not_configured',configured:false,channels:[]});
   if(url.pathname==='/api/v1/appearance')return reply({theme:'light',default:'light'});
   return reply({error:'Unexpected request '+method+' '+url.pathname},500);
  });
  await page.goto(origin);await page.locator('#gateway-setup[open]').waitFor();
  assert.equal(await page.locator('#setup-title').innerText(),'Your Bitcoin connection');assert.equal(await page.locator('#setup-explore').count(),0);assert.equal(await page.locator('#setup-core-options').isVisible(),false);assert.equal(mutations.length,0);
  assert.equal(await page.locator('#header-warning').isVisible(),true);headerHeight=2;await page.locator('#header-warning').waitFor({state:'hidden'});
  await page.locator('[name="connection-mode"][value="core"]').check();assert.equal(await page.locator('#setup-core-options').isVisible(),true);assert.equal(await page.locator('#setup-mount').isChecked(),false);
  await page.locator('[name="connection-mode"][value="gateway"]').check();
  await page.locator('#setup-close').click();assert.equal(progress.completed,false);assert.equal(mutations.length,0,'cancel does not save or complete');await page.locator('#network-details summary').first().click();await page.locator('[data-gateway-setup]').first().click();await page.locator('#gateway-setup[open]').waitFor();
  if(out)await page.screenshot({path:path.join(out,'setup-connection.png'),fullPage:true});
  await page.locator('#setup-next').click();await page.getByRole('heading',{name:'Browser access',exact:true}).waitFor();assert.equal(await page.locator('#setup-startup').isChecked(),false,'fresh installed mode must not imply startup consent');
  const mutationCount=mutations.length;measurePolling=true;const pollDeadline=Date.now()+20000;while((pollTimes.network.length<4||pollTimes.readiness.length<4)&&Date.now()<pollDeadline)await page.waitForTimeout(100);measurePolling=false;assert.equal(pollTimes.network.length>=4&&pollTimes.readiness.length>=4,true,'four consecutive readiness polls observed');assert.equal(mutations.length,mutationCount,'readiness polling is read-only');
  const pollIntervals=Object.fromEntries(Object.entries(pollTimes).map(([name,times])=>[name,times.slice(1,4).map((time,i)=>Math.round(time-times[i]))]));if(out)fs.writeFileSync(path.join(out,'poll-cadence.json'),JSON.stringify({scope:'Real Chromium request timestamps; isolated instantaneous API fixtures; no Windows integration or block throughput measured',configured_delay_ms:2500,intervals_ms:pollIntervals},null,2));console.log('Setup polling intervals ms: '+JSON.stringify(pollIntervals));
  await page.locator('#setup-routing').click();await page.getByText('Gateway browser namespaces: Administrator cancelled',{exact:true}).waitFor();assert.equal(await page.locator('#setup-next').isDisabled(),true,'partial failure requires retry or deliberate partial acceptance');
  await page.locator('#setup-routing').click();await page.getByText('Gateway browser namespaces: saved',{exact:true}).waitFor();assert.equal(await page.locator('#setup-next').isDisabled(),false);
  const routingCalls=mutations.filter(x=>x.body?.action==='browser-routing');assert.equal(routingCalls.length,2);assert(routingCalls.every(x=>x.body.consent===true&&x.body.enabled===true));
  await page.locator('#setup-browser').selectOption('edge');await page.locator('#setup-profile').fill('Profile 2');
  await page.locator('#setup-open-browser').click();await page.locator('#setup-error').getByText(/edge:\/\/extensions\/ manually/).waitFor();assert.equal(await page.locator('#gateway-setup').isVisible(),true);
  launchFailure=false;await page.evaluate(()=>{window.copiedExtensions='';Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.copiedExtensions=value}}})});
  await page.locator('#setup-open-browser').click();await page.locator('#setup-browser-launch').getByText(/Extensions address copied/).waitFor();assert.equal(await page.evaluate(()=>window.copiedExtensions),'edge://extensions/');
  await page.evaluate(()=>{navigator.clipboard.writeText=async()=>{throw Error('Clipboard denied')}});await page.locator('#setup-open-browser').click();await page.locator('#setup-browser-launch').getByText(/Copy the displayed extensions address/).waitFor();
  native=true;await page.locator('#setup-browser-state').getByText(/Authenticated native exchange observed/).waitFor();
  await page.setViewportSize({width:390,height:844});assert.equal(await page.locator('#gateway-setup').evaluate(el=>getComputedStyle(el).overflow),'hidden');assert.equal(await page.locator('#setup-body').evaluate(el=>getComputedStyle(el).overflowY),'auto');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);if(out)await page.screenshot({path:path.join(out,'setup-browser-narrow.png'),fullPage:true});
  await page.locator('#setup-next').click();await page.getByRole('heading',{name:'Network & local data',exact:true}).waitFor();assert.equal(await page.locator('#setup-private').isChecked(),true);assert.equal(await page.locator('#setup-network').isChecked(),false);assert.equal(await page.locator('#setup-gateway-serve').isDisabled(),true);assert.equal(await page.locator('#setup-serve').isChecked(),false);
  assert.equal(await page.locator('#setup-private').getAttribute('role'),'switch');assert.equal(await page.locator('#setup-normal, #setup-private-preset').count(),0);
  await page.locator('#setup-private').uncheck();assert.equal(await page.locator('#setup-private').isChecked(),false);assert.equal(await page.locator('#setup-network').isChecked(),false,'privacy does not change outbound connections');assert.equal(await page.locator('#setup-serve').isChecked(),false,'privacy does not opt into serving');
  await page.locator('#setup-private').check();await page.locator('#setup-serve').check();assert.equal(await page.locator('#setup-gateway-serve').isChecked(),false);if(out)await page.screenshot({path:path.join(out,'setup-serving-narrow.png'),fullPage:true});
  settingsFailure=true;await page.locator('#setup-next').click();await page.locator('#setup-error').getByText('Settings could not be saved',{exact:true}).waitFor();assert.equal(progress.completed,false);assert.equal(await page.locator('#gateway-setup').isVisible(),true);
  settingsFailure=false;await page.locator('#setup-next').click();await page.locator('#gateway-setup').waitFor({state:'hidden'});assert.equal(progress.completed,true);assert.equal(progress.profile,'Profile 2');assert.equal(settings.privacy_mode,true);assert.equal(settings.serve_data,true);assert.equal(system.run_on_startup,false);
  await page.locator('#settings-button').click();await page.locator('#settings-form').waitFor();assert.equal(await page.locator('#private-mode').getAttribute('role'),'switch');assert.equal(await page.locator('#serve-gateway-data').isDisabled(),true);assert.equal(await page.locator('#serve-data').isChecked(),true);
  await page.getByRole('button',{name:'Privacy & storage',exact:true}).click();if(out)await page.screenshot({path:path.join(out,'settings-privacy-narrow.png'),fullPage:true});await page.locator('#private-mode').uncheck();await page.locator('#serve-data').uncheck();await page.locator('#settings-form button[type="submit"]').click();await page.locator('#settings-form').waitFor();assert.equal(settings.privacy_mode,false);assert.equal(settings.serve_data,false);assert.equal(settings.share_cache,false,'privacy toggle preserves public-cache opt-out');
  assert.equal(mutations.filter(x=>x.path==='/api/v1/system/startup').length,0);assert.deepEqual(errors,[]);
  console.log('Gateway setup browser: PASS (3 steps, Core-only reuse, explicit routing consent, browser-specific partial failure/retry, read-only polling, launch copy/fallback, independent Bitcoin serving, private switches, preserved choices, cancellation/save failure, reduced motion, narrow scroll containment, direct entry).');
 }finally{await browser.close();await new Promise(r=>server.close(r))}
}
run().catch(e=>{console.error(e);process.exitCode=1});
