'use strict';
// Real Chromium checks for profile appearance, same-origin embedded views and
// changed management ports. Both servers represent one isolated fixture profile.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..');
function playwright(){try{return require('playwright')}catch(error){const bundled=path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright');if(fs.existsSync(bundled))return require(bundled);throw error}}
function browserPath(chromium){const candidates=[process.env.GATEWAY_TEST_BROWSER,chromium.executablePath()];for(const base of [process.env['ProgramFiles(x86)'],process.env.ProgramFiles,process.env.LOCALAPPDATA].filter(Boolean)){candidates.push(path.join(base,'Microsoft','Edge','Application','msedge.exe'));candidates.push(path.join(base,'Google','Chrome','Application','chrome.exe'))}const found=candidates.find(p=>p&&fs.existsSync(p));if(!found)throw Error('No installed Chromium browser');return found}
async function run(){
 let choice='',rejectSave=false,gate=null,browser;const calls=[],errors=[],external=[];
 const html=embedded=>'<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/updates.css"><link rel="stylesheet" href="/shell/theme.css"><script src="/shell/theme.js"></script></head><body><main><section id="settings-body"><form id="settings-form"><section class="panel"><h1>Fixture settings</h1></section></form></section>'+(embedded?'':'<iframe src="/embedded" title="Embedded indexes"></iframe>')+'</main></body></html>';
 const servers=[0,1].map(()=>http.createServer(async(req,res)=>{
  const reply=(value,type='application/json',status=200)=>{res.writeHead(status,{'Content-Type':type,'Content-Security-Policy':"default-src 'self'; script-src 'self'; style-src 'self'; frame-src 'self'; object-src 'none'"});res.end(typeof value==='string'?value:JSON.stringify(value))};
  if(req.url==='/'||req.url==='/embedded')return reply(html(req.url==='/embedded'),'text/html');
  if(req.url?.startsWith('/shell/')){const name=req.url.slice(7);if(['theme.js','theme.css','style.css','updates.css'].includes(name))return reply(fs.readFileSync(path.join(root,'ui/shell',name),'utf8'),name.endsWith('.js')?'application/javascript':'text/css')}
  if(req.url==='/favicon.ico')return reply('','image/x-icon');
  if(req.url==='/api/v1/appearance'){let raw='';for await(const chunk of req)raw+=chunk;const body=raw?JSON.parse(raw):null;calls.push({method:req.method,body});if(req.method==='POST'){if(gate)await gate.promise;if(rejectSave)return reply({error:'Fixture disk unavailable'},'application/json',503);assert(['dark','light'].includes(body.theme));choice=body.theme}return reply({theme:choice,default:'light'})}
  return reply({error:'Unexpected fixture route'},'application/json',404);
 }));
 try{
  for(const server of servers)await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const origins=servers.map(server=>'http://127.0.0.1:'+server.address().port),allowed=new Set(origins);
  const {chromium}=playwright();browser=await chromium.launch({headless:true,executablePath:browserPath(chromium)});
  const context=await browser.newContext();const page=await context.newPage();page.setDefaultTimeout(7000);page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(!allowed.has(new URL(r.url()).origin))external.push(r.url())});
  await page.goto(origins[0]);await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'light');assert.equal(calls.filter(x=>x.method==='POST').length,0,'Default light must not fabricate an explicit saved choice');
  await page.locator('#theme-select').selectOption('dark');await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');assert.equal(choice,'dark');await page.frameLocator('iframe').locator('html[data-theme="dark"]').waitFor();
  await page.goto(origins[1]);await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','A new port reads the same saved profile rather than its empty localStorage');
  await page.evaluate(()=>localStorage.clear());await page.reload();await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Profile survives cleared browser storage');
  await page.evaluate(()=>localStorage.setItem('gateway-theme','light'));await page.reload();await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Profile choice overrides stale local preference');
  choice='';await page.reload();await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(choice,'dark','An existing explicit local preference migrates into an unconfigured profile');
  let release;gate={promise:new Promise(resolve=>{release=resolve}),resolve:()=>release()};await page.locator('#theme-select').selectOption('light');await page.waitForFunction(()=>document.getElementById('theme-select').disabled);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Pending save does not pretend a new choice is persisted');gate.resolve();gate=null;await page.waitForFunction(()=>document.documentElement.dataset.theme==='light');await page.frameLocator('iframe').locator('html[data-theme="light"]').waitFor();
  rejectSave=true;await page.locator('#theme-select').selectOption('dark');await page.locator('#theme-error').getByText('Fixture disk unavailable',{exact:true}).waitFor();assert.equal(await page.locator('#theme-select').inputValue(),'light');assert.equal(await page.locator('html').getAttribute('data-theme'),'light');
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('Appearance UI: PASS (warm default, explicit profile save, embedded synchronization, changed-port and cleared-storage persistence, legacy migration, profile authority, pending and failed-save behavior; real Chromium/CSP).');
 }finally{if(gate)gate.resolve();if(browser)await browser.close();await Promise.all(servers.map(server=>new Promise(resolve=>server.close(resolve))))}
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1});
