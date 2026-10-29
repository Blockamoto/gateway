'use strict';
// Actual browser DOM and keyboard behavior with the real workspace view and
// in-memory API fixtures. No node, real peer or installed profile is changed.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const playwright=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright'))}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,playwright.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft','Edge','Application','msedge.exe'),path.join(process.env[k],'Google','Chrome','Application','chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const outIndex=process.argv.indexOf('--out'),out=outIndex>=0?path.resolve(process.argv[outIndex+1]):null;if(out)fs.mkdirSync(out,{recursive:true});
async function run(){
 const browser=await playwright.chromium.launch({headless:true,executablePath:browserPath});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[],requests=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',route=>{requests.push(route.request().url());return route.abort()});
  await page.setContent('<!doctype html><html lang="en" data-theme="light"><title>Gateway peer aliases test</title><main id="content"></main></html>');
  for(const name of ['style.css','theme.css'])await page.addStyleTag({content:fs.readFileSync(path.join(root,'ui/shell',name),'utf8')});
  await page.addScriptTag({content:fs.readFileSync(path.join(root,'ui/shell/workspace.js'),'utf8')});
  const ipv4='192.0.2.11:8333',ipv6='[2001:db8::12]:8333',failed='192.0.2.13:8333';
  await page.evaluate(async ({ipv4,ipv6,failed})=>{
   window.fixture={connections:[
    {address:ipv4,state:'connected',protocols:['Bitcoin P2P'],discovery_source:'manual_bitcoin'},
    {address:ipv6,state:'connected',protocols:['Bitcoin P2P'],direction:'inbound'}
   ],network:{connected:2,bootstrap_error:'Failed to dial '+failed,recent:[{address:failed}]},core_peers:[{addr:'192.0.2.14:8333'}]};
   window.mutations=[];window.notices=[];window.current=true;
   window.renderPeers=()=>window.GatewayWorkspace.connections({
    root:document.querySelector('#content'),heading:()=>'<h1>Bitcoin connections</h1>',isCurrent:()=>window.current,
    schedule:fn=>{window.nextPeers=fn},notice:message=>window.notices.push(message),
    api:async(url,data)=>{
     if(!url.startsWith('/api/v1/peers'))throw Error('Unexpected API '+url);
     if(data){window.mutations.push(data);if(data.action==='disconnect')window.fixture.connections=window.fixture.connections.filter(x=>x.address!==data.address);return {ok:true}}
     return structuredClone(window.fixture);
    }
   });
   await window.renderPeers();
  },{ipv4,ipv6,failed});
  const aliases=page.locator('[data-peer-alias]');
  const labels=await aliases.allTextContents();
  assert.equal(labels.length,2);assert.equal(new Set(labels).size,2);
  const ids=await aliases.evaluateAll(buttons=>buttons.map(b=>b.id));
  const visible=await page.locator('body').innerText();
  for(const address of [ipv4,ipv6,failed,'192.0.2.14:8333'])assert(!visible.includes(address),'Default view must conceal '+address);
  assert(!((await page.locator('#content').ariaSnapshot()).includes(ipv4)),'Hidden address must not be read by assistive technology');
  assert.equal(await page.getByRole('button',{name:'Connect to a Gateway peer'}).isDisabled(),true);
  if(out)await page.screenshot({path:path.join(out,'peer-aliases.png'),fullPage:true});
  const first=page.locator('#'+ids[0]);await first.focus();await page.keyboard.press('Enter');
  assert.equal(await first.getAttribute('aria-expanded'),'true');
  assert.equal(await first.getAttribute('aria-label'),'Hide address for '+labels[0]);
  assert((await page.locator('body').innerText()).includes(ipv4));
  assert(!(await page.locator('body').innerText()).includes(ipv6));
  await page.keyboard.press('Space');assert.equal(await first.getAttribute('aria-expanded'),'false');
  assert.equal(await first.getAttribute('aria-label'),'Show address for '+labels[0]);
  assert(!(await page.locator('body').innerText()).includes(ipv4));
  const second=page.locator('#'+ids[1]);await second.click();
  await page.evaluate(async()=>{window.fixture.connections.reverse();await window.nextPeers()});
  assert.deepEqual(await aliases.allTextContents(),[labels[1],labels[0]],'A changed API order must not reassign aliases');
  assert.equal(await second.getAttribute('aria-expanded'),'true','Automatic refresh preserves deliberate reveal');
  assert.equal(await page.evaluate(()=>document.activeElement.id),ids[1],'Automatic refresh preserves keyboard focus');
  assert((await page.locator('body').innerText()).includes(ipv6));
  if(out)await page.screenshot({path:path.join(out,'peer-address-revealed.png'),fullPage:true});
  await page.getByRole('button',{name:'Disconnect '+labels[1],exact:true}).click();
  await page.waitForFunction(()=>window.mutations.length===1&&document.querySelectorAll('[data-peer-alias]').length===1);
  assert.deepEqual(await page.evaluate(()=>window.mutations),[{action:'disconnect',address:ipv6}],'Alias actions must retain the correct actual endpoint');
  await first.click();await page.evaluate(()=>window.renderPeers());
  assert.equal(await first.getAttribute('aria-expanded'),'false','Returning to Peers conceals addresses again');
  assert.equal(await first.innerText(),labels[0],'Aliases survive leaving and returning to the route');
  await page.locator('#peer-diagnostics summary').click();
  assert((await page.locator('body').innerText()).includes(failed),'Explicit diagnostics can reveal failed connection addresses');
  await page.evaluate(()=>window.nextPeers());assert.equal(await page.locator('#peer-diagnostics').getAttribute('open'),'','Refresh preserves open diagnostics');
  await page.locator('#peer-diagnostics summary').click();
  await page.getByRole('button',{name:'Retry Bitcoin connections'}).click();
  assert.deepEqual(await page.evaluate(()=>window.mutations),[{action:'disconnect',address:ipv6},{action:'refresh'}]);
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Peer table must scroll inside its wrapper on narrow screens');
  if(out)await page.screenshot({path:path.join(out,'peer-aliases-narrow.png'),fullPage:true});
  const hostile='peer"><img src=x onerror="window.injected=true">';
  await page.evaluate(async address=>{window.fixture.connections=[{address,state:'connected'}];await window.nextPeers()},hostile);
  await aliases.first().click();assert((await page.locator('body').innerText()).includes(hostile));
  assert.equal(await page.locator('#peer-data img').count(),0);assert.equal(await page.evaluate(()=>window.injected),undefined);
  await page.evaluate(async()=>{window.fixture.connections=Array.from({length:70},(_,i)=>({address:'192.0.2.'+i+':8333',state:'connected'}));await window.nextPeers()});
  const many=await aliases.allTextContents();assert.equal(many.length,new Set(many).size,'Aliases stay distinct when friendly words repeat');
  await page.evaluate(async()=>{window.fixture.connections=[];await window.nextPeers()});
  await page.getByRole('heading',{name:'No active connections'}).waitFor();
  await page.evaluate(async()=>{window.fixture.listener={enabled:true,requested:true,listen_port:48444,automatic_port:true};await window.nextPeers()});
  const serving=page.locator('[data-bitcoin-serving-status]');
  assert.match(await serving.innerText(),/Bitcoin serving is on.*48444/);
  assert.match(await serving.innerText(),/default port is busy/);
  assert(!(await serving.innerText()).includes('48333'),'Status must show the actual listening port');
  await page.evaluate(async()=>{window.fixture.listener={enabled:false,requested:true,listen_port:0,error:'Cannot listen <img src=x onerror="window.injected=true">'};await window.nextPeers()});
  assert.match(await serving.innerText(),/Bitcoin serving is unavailable/);
  assert.match(await serving.innerText(),/Cannot listen <img/);
  assert.equal(await page.locator('#connection-summary img').count(),0,'Listener errors must remain escaped text');
  assert.equal(await page.evaluate(()=>window.injected),undefined);
  await page.evaluate(async()=>{window.fixture.listener={enabled:false,requested:false,listen_port:0};await window.nextPeers()});
  assert.equal(await serving.innerText(),'Bitcoin serving is off');
  assert.deepEqual(await page.evaluate(()=>window.mutations),[{action:'disconnect',address:ipv6},{action:'refresh'}],'Reading listener status must not change preferences');
  const before=await page.locator('#content').innerHTML();
  await page.evaluate(async()=>{window.current=false;const next=window.nextPeers;window.nextPeers=null;await next()});
  assert.equal(await page.evaluate(()=>window.nextPeers),null,'A departed view does not keep polling');
  assert.equal(await page.locator('#content').innerHTML(),before);
  assert.deepEqual(await page.evaluate(()=>window.notices),[]);assert.deepEqual(errors,[]);assert.deepEqual(requests,[]);
  console.log('Peer aliases browser: PASS (default concealment, keyboard reveal/hide, stable distinct labels, refresh/focus, correct disconnect endpoint, diagnostic disclosure, narrow viewport, actual serving port/failure/off state, escaping, route cancellation; isolated API fixtures).');
 }finally{await browser.close()}
}
run().catch(e=>{console.error(e);process.exitCode=1});
