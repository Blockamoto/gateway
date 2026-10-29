'use strict';
// Actual shell HTML, app.js and CSS; only unrelated modules and the embedded
// workspace are isolated fixtures. No Gateway runtime or user profile is used.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..'),outAt=process.argv.indexOf('--out'),out=outAt<0?null:path.resolve(process.argv[outAt+1]);
if(out)fs.mkdirSync(out,{recursive:true});
const read=name=>fs.readFileSync(path.join(root,'ui/shell',name),'utf8'),html=read('index.html'),app=read('app.js'),css=read('style.css'),theme=read('theme.css');
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
assert(executablePath,'An existing Chromium browser is required; this test never downloads one.');
const key='gateway.arrival.shown.v1',checks=[];
let origin,browser;
async function fixture(options={}){
 const context=await browser.newContext({viewport:{width:1280,height:850},reducedMotion:options.reduced?'reduce':'no-preference',serviceWorkers:'block'}),page=await context.newPage(),errors=[],requests=[];
 page.setDefaultTimeout(4000);page.on('pageerror',error=>errors.push(error.message));
 let releaseFrame;const frameGate=options.holdFrame?new Promise(resolve=>releaseFrame=resolve):Promise.resolve();
 await page.addInitScript(options=>{
  window.arrivalEvents=[];window.moduleStarts=[];
  new MutationObserver(records=>{for(const record of records){const node=record.target;if(node.id==='gateway-arrival'&&record.attributeName==='data-active')arrivalEvents.push({active:node.dataset.active==='true',at:performance.now()});}}).observe(document,{subtree:true,attributes:true,attributeFilter:['data-active']});
  if(options.hidden)Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>window.fixtureVisible?'visible':'hidden'});
  if(options.storageFailure){const original=Storage.prototype.getItem;Storage.prototype.getItem=function(key){if(key==='gateway.arrival.shown.v1')throw Error('Isolated storage failure');return original.call(this,key);};}
  if(options.noAnimationFrame)window.requestAnimationFrame=()=>{throw Error('Isolated animation scheduler failure');};
 },options);
 await context.route('**/*',async route=>{
  const request=route.request(),url=new URL(request.url());requests.push({path:url.pathname,method:request.method(),at:Date.now()});assert.equal(url.origin,origin,'Entrance requests no external assets');
  const fulfill=(body,type='text/html')=>route.fulfill({status:200,contentType:type,body});
  if(url.pathname==='/'){
   let markup=html.replace('__GATEWAY_BOOTSTRAP__',JSON.stringify({version:'fixture',address:options.address||'.gateway',activation:options.activation||'',release_availability:{}}));
   if(options.dialog)markup=markup.replace('id="gateway-setup"','id="gateway-setup" open');
   if(options.setupEntering)markup=markup.replace('<body>','<body class="gateway-entering">');
   if(options.noAnimations)markup=markup.replace('</head>','<style>.gateway-arrival,.gateway-arrival *,.gateway-arrival *:before,.gateway-arrival *:after{animation:none!important}</style></head>');
   return fulfill(markup);
  }
  if(url.pathname==='/other')return fulfill('<!doctype html><title>History fixture</title><p>History fixture</p>');
  if(url.pathname==='/indexes'){
   await frameGate;
   return fulfill('<!doctype html><html><head><style>body{margin:0;background:#fffdf8;color:#252f2c;font:16px system-ui}main{padding:70px}button,input{padding:12px}</style></head><body><main><h1>Workspace fixture</h1><p>Independent workspace content is usable during the optional arrival accent.</p><button id="action" onclick="document.body.dataset.clicked=\'true\'">Existing action</button><input id="entry" aria-label="Workspace input"></main><script>document.addEventListener("keydown",event=>{if(event.code!=="Backquote"&&event.key!=="Escape")return;event.preventDefault();event.stopImmediatePropagation();document.body.dataset.shortcutCount=String(Number(document.body.dataset.shortcutCount||0)+1);document.body.dataset.shortcut=event.code;},true);parent.postMessage({type:"gateway:workspace-focused",address:new URLSearchParams(location.search).get("focus")||".gateway",navigationId:Number(new URLSearchParams(location.search).get("navigationId"))},location.origin);</script></body></html>');
  }
  if(url.pathname==='/shell/app.js')return fulfill(app,'application/javascript');
  if(url.pathname==='/shell/updates.js')return fulfill('window.GatewayUpdates={start(){moduleStarts.push({name:"updates",at:performance.now()});},unmount(){}};','application/javascript');
  if(url.pathname==='/shell/sat-view.js')return fulfill('window.GatewaySatView={nameNumber:()=>null};','application/javascript');
  if(url.pathname==='/shell/address-view.js')return fulfill('window.GatewayAddressView={destination:()=>({isPublicKey:false})};','application/javascript');
  if(url.pathname==='/shell/network.js')return fulfill('moduleStarts.push({name:"network",at:performance.now()});','application/javascript');
  if(url.pathname==='/shell/style.css')return fulfill(options.noCSS?'':css,'text/css');
  if(url.pathname==='/shell/theme.css')return fulfill(theme,'text/css');
  if(url.pathname.endsWith('.css'))return fulfill('','text/css');
  if(url.pathname==='/shell/theme.js')return fulfill('document.documentElement.dataset.theme='+JSON.stringify(options.theme||'light')+';','application/javascript');
  if(url.pathname.endsWith('.js'))return fulfill('','application/javascript');
  if(url.pathname==='/shell/gateway.ico')return fulfill('','image/x-icon');
  if(url.pathname==='/api/v1/activation')return fulfill('{}','application/json');
  throw Error('Unexpected entrance request: '+request.method()+' '+url.pathname);
 });
 await page.goto(origin+(options.query||'/'),{waitUntil:'domcontentloaded'});
 const node=page.locator('#gateway-arrival'),active=()=>page.waitForFunction(()=>document.getElementById('gateway-arrival').dataset.active==='true'),hidden=()=>page.waitForFunction(()=>document.getElementById('gateway-arrival').hidden);
 const close=async()=>{releaseFrame?.();assert.deepEqual(errors,[]);assert(requests.every(row=>row.method==='GET'||row.path==='/api/v1/activation'),'Entrance has no state-changing request');await context.close();};
 return {context,page,node,errors,requests,releaseFrame,active,hidden,close};
}
async function skipped(options,label){const f=await fixture(options);try{await f.page.waitForTimeout(80);assert.equal(await f.node.evaluate(node=>node.hidden),true,label);assert.deepEqual(await f.page.evaluate(()=>arrivalEvents),[],label+' never starts later');assert.equal(await f.page.locator('#indexes-module').count(),1,label+' still starts normal navigation');checks.push(label);}finally{await f.close();}}
async function immediate(label,act){const f=await fixture();try{await f.active();await act(f);assert.equal(await f.node.evaluate(node=>node.hidden),true,label+' dismisses without a fade or timer');checks.push(label);}finally{await f.close();}}
async function run(){
 const server=http.createServer((request,response)=>{response.writeHead(404);response.end();});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));origin='http://127.0.0.1:'+server.address().port;
 browser=await pw.chromium.launch({headless:true,executablePath});
 try{
  const f=await fixture({holdFrame:true});try{
   await f.active();const state=await f.node.evaluate(node=>({aria:node.getAttribute('aria-hidden'),outside:!document.getElementById('content').contains(node)&&node.parentElement===document.getElementById('content').parentElement,pointer:getComputedStyle(node).pointerEvents,focusable:node.querySelectorAll('button,input,a[href],[tabindex]').length,contentTransform:getComputedStyle(document.getElementById('content')).transform,contentOpacity:getComputedStyle(document.getElementById('content')).opacity,active:document.activeElement.tagName,events:arrivalEvents,starts:moduleStarts,storage:sessionStorage.getItem('gateway.arrival.shown.v1')}));
   assert.equal(state.aria,'true');assert.equal(state.outside,true);assert.equal(state.pointer,'none');assert.equal(state.focusable,0);assert.equal(state.contentTransform,'none');assert.equal(state.contentOpacity,'1');assert.equal(state.active,'BODY');assert.equal(state.storage,'1');assert.deepEqual(state.starts.map(row=>row.name),['updates','network']);assert(f.requests.some(row=>row.path==='/indexes'),'Workspace navigation begins while its response is held');assert(state.starts.every(row=>row.at<=state.events[0].at),'Normal startup modules run before animation begins');
   if(out)await f.page.screenshot({path:path.join(out,'arrival-light.png')});
   await f.hidden();const timing=await f.page.evaluate(()=>arrivalEvents);assert(timing.length>=2);const elapsed=timing.at(-1).at-timing[0].at;assert(elapsed>=850&&elapsed<1400,'Actual one-second CSS/timer cleanup: '+elapsed+'ms');f.releaseFrame();await f.page.frameLocator('#indexes-module').getByRole('heading',{name:'Workspace fixture'}).waitFor();
   await f.page.locator('[data-route=".gateway"]').first().click();assert.equal(await f.node.evaluate(node=>node.hidden),true,'Later Home navigation never repeats the arrival');assert.equal((await f.page.evaluate(()=>arrivalEvents.filter(event=>event.active))).length,1);checks.push('first-visible-home-once-and-one-second-cleanup','top-level-accessible-pointer-transparent','startup-independent-of-held-workspace');
  }finally{await f.close();}
  for(const [options,label]of [[{address:'0.bitcoin'},'resource-deep-link'],[{activation:'fixture-activation'},'activation-deep-link'],[{query:'/?setup=1'},'explicit-setup'],[{dialog:true},'active-dialog'],[{setupEntering:true},'existing-setup-intro'],[{reduced:true},'reduced-motion-instant'],[{hidden:true},'hidden-initial-load'],[{noCSS:true},'missing-css-fails-open'],[{storageFailure:true},'storage-failure-fails-open'],[{noAnimationFrame:true},'scheduler-failure-fails-open']])await skipped(options,label);
  await immediate('parent-pointer',async f=>{await f.page.mouse.move(20,20);});
  await immediate('parent-keyboard-input',async f=>{await f.page.locator('#address').focus();await f.page.keyboard.press('x');assert.equal(await f.page.locator('#address').inputValue(),'.gatewayx','Input remains usable');});
  await immediate('parent-input-event',async f=>{await f.page.locator('#address').fill('0.bitcoin');assert.equal(await f.page.locator('#address').inputValue(),'0.bitcoin');});
  await immediate('workspace-pointer',async f=>{await f.page.frameLocator('#indexes-module').getByRole('button',{name:'Existing action'}).click();assert.equal(await f.page.frameLocator('#indexes-module').locator('body').getAttribute('data-clicked'),'true');});
  await immediate('workspace-keyboard-input',async f=>{const input=f.page.frameLocator('#indexes-module').getByRole('textbox',{name:'Workspace input'});await input.focus();await f.page.keyboard.press('x');assert.equal(await input.inputValue(),'x');});
  await immediate('workspace-first-captured-shortcut',async f=>{const frame=f.page.frameLocator('#indexes-module');await frame.getByRole('button',{name:'Existing action'}).focus();assert.equal(await f.node.evaluate(node=>node.hidden),false,'Programmatic iframe focus leaves the entrance active before the first key');await f.page.keyboard.press('Backquote');assert.equal(await frame.locator('body').getAttribute('data-shortcut-count'),'1','The real document capture shortcut still runs exactly once');assert.equal(await frame.locator('body').getAttribute('data-shortcut'),'Backquote','The first real key reaches the existing shortcut despite dismissing the entrance');});
  await immediate('workspace-navigation',async f=>{await f.page.frameLocator('#indexes-module').locator('body').evaluate(()=>parent.postMessage({type:'gateway:workspace-focused',address:'1.bitcoin',historyMode:'push'},location.origin));await f.page.waitForFunction(()=>document.getElementById('address').value==='1.bitcoin');});
  await immediate('parent-navigation',async f=>{await f.page.evaluate(()=>{document.getElementById('address').value='indexes.gateway';document.getElementById('address-form').requestSubmit();});});
  await immediate('dialog-opening',async f=>{await f.page.evaluate(()=>document.getElementById('gateway-setup').showModal());await f.page.evaluate(()=>Promise.resolve());});
  await immediate('setup-completion-class',async f=>{await f.page.evaluate(()=>document.body.classList.add('gateway-entering'));await f.page.evaluate(()=>Promise.resolve());});
  await immediate('document-hidden',async f=>{await f.page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{configurable:true,get:()=> 'hidden'});document.dispatchEvent(new Event('visibilitychange'));});});
  const hidden=await fixture({hidden:true});try{await hidden.page.evaluate(()=>{window.fixtureVisible=true;document.dispatchEvent(new Event('visibilitychange'));});await hidden.page.waitForTimeout(80);assert.deepEqual(await hidden.page.evaluate(()=>arrivalEvents),[],'A background load is never deferred into a surprise entrance');checks.push('hidden-load-never-replays-on-visible');}finally{await hidden.close();}
  const reload=await fixture();try{await reload.active();await reload.page.evaluate(()=>{document.dispatchEvent(new Event('input'));sessionStorage.removeItem('gateway.arrival.shown.v1');});await reload.page.reload({waitUntil:'domcontentloaded'});assert.equal(await reload.page.evaluate(()=>performance.getEntriesByType('navigation')[0].type),'reload');await reload.page.waitForTimeout(80);assert.deepEqual(await reload.page.evaluate(()=>arrivalEvents),[],'Actual reload skips even with no seen marker');checks.push('actual-reload');}finally{await reload.close();}
  const history=await fixture();try{await history.active();await history.page.evaluate(()=>{document.dispatchEvent(new Event('input'));sessionStorage.removeItem('gateway.arrival.shown.v1');});await history.page.goto(origin+'/other');await history.page.goBack({waitUntil:'domcontentloaded'});await history.page.waitForTimeout(80);assert.equal(await history.node.evaluate(node=>node.hidden),true);assert.equal(await history.page.evaluate(()=>performance.getEntriesByType('navigation')[0].type),'back_forward');assert.deepEqual(await history.page.evaluate(()=>arrivalEvents),[],'Actual history traversal skips even with no seen marker');checks.push('actual-back-forward');}finally{await history.close();}
  const fallback=await fixture({noAnimations:true});try{await fallback.active();await fallback.hidden();const events=await fallback.page.evaluate(()=>arrivalEvents),elapsed=events.at(-1).at-events[0].at;assert(elapsed>=1000&&elapsed<1450,'Missing animationend uses a bounded cleanup timer: '+elapsed+'ms');checks.push('missing-animationend-cleanup-fallback');}finally{await fallback.close();}
  const dark=await fixture({theme:'dark'});try{await dark.active();if(out)await dark.page.screenshot({path:path.join(out,'arrival-dark.png')});checks.push('dark-theme-decoration');}finally{await dark.close();}
  if(out)fs.writeFileSync(path.join(out,'summary.json'),JSON.stringify({result:'PASS',checks,limitations:['Actual source shell/app/CSS in Chromium; unrelated modules and embedded workspace use isolated fixtures. Compiled runtime integration is separate.','Initial/changed visibility state is simulated because headless Chromium pages remain visible. Reload, history, pointer, keyboard, input, dialogs and animation timing use real browser behavior.']},null,2)+'\n');
  console.log('Shell arrival browser PASS: '+checks.length+' focused checks, actual timing and interaction, no startup or workspace readiness gate.');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
