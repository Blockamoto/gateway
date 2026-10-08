'use strict';
// Native Windows updater acceptance. All keys, signed packages, processes and
// profiles are local fixtures; no GitHub origin, release or system install is used.
// Requires a complete preview build, Go, Python, Playwright and installed Chromium.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const {spawn,execFileSync}=require('node:child_process');
const {createHash}=require('node:crypto');
const root=path.resolve(__dirname,'..'),opts={};
for(let i=2;i<process.argv.length;i++){if(process.argv[i]==='--automatic'){opts.automatic=true;continue}if(!['--build','--publisher','--go','--python','--out','--browser'].includes(process.argv[i])||!process.argv[i+1])throw Error('Usage: node scripts/test-desktop-updates-browser.js --build DIR --publisher EXE --go EXE --out DIR [--python EXE] [--browser EXE] [--automatic]');opts[process.argv[i].slice(2)]=process.argv[++i]}
for(const name of ['build','publisher','go','out'])if(!opts[name])throw Error('--'+name+' is required');
assert.equal(process.platform,'win32','This script exercises native Windows replacement');
const build=path.resolve(opts.build),out=path.resolve(opts.out),publisherExe=path.resolve(opts.publisher),go=path.resolve(opts.go);
fs.mkdirSync(out,{recursive:true});
const digest=p=>createHash('sha256').update(fs.readFileSync(p)).digest('hex');
async function capture(page,name){await page.evaluate(()=>window.scrollTo(0,0));await page.screenshot({path:path.join(out,name),fullPage:true})}
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
function playwright(){try{return require('playwright')}catch(error){const bundled=path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright');if(fs.existsSync(bundled))return require(bundled);throw error}}
function browserPath(chromium){const choices=[opts.browser,process.env.GATEWAY_TEST_BROWSER,chromium.executablePath()];for(const base of [process.env['ProgramFiles(x86)'],process.env.ProgramFiles,process.env.LOCALAPPDATA].filter(Boolean)){choices.push(path.join(base,'Microsoft','Edge','Application','msedge.exe'));choices.push(path.join(base,'Google','Chrome','Application','chrome.exe'))}const found=choices.find(p=>p&&fs.existsSync(p));if(!found)throw Error('No installed Chromium browser');return found}
function clientEnvironment(){const env={...process.env};for(const name of Object.keys(env))if(/^(?:GATEWAY_PUBLISHER_GITHUB_TOKEN|GH_TOKEN|GITHUB_TOKEN|GATEWAY_UPDATE_ACK_PATH|GATEWAY_UPDATE_ACK_TOKEN)$/i.test(name))delete env[name];return env}
async function until(fn,label,timeout=60000){const end=Date.now()+timeout;while(Date.now()<end){const value=await fn();if(value)return value;await delay(100)}throw Error('Timed out: '+label)}
async function runtimeFor(profile,version,pid){return until(async()=>{try{const record=JSON.parse(fs.readFileSync(path.join(profile,'runtime.json'),'utf8'));if(pid&&record.pid===pid)return false;const response=await fetch(record.url+'/api/v1/runtime/ping',{signal:AbortSignal.timeout(500)});if(!response.ok)return false;const value=await response.json();return(!version||value.version===version)&&value.pid===record.pid?{record,ping:value}:false}catch{return false}},'runtime '+(version||'startup'),90000)}
async function stopProfile(profile){try{const record=JSON.parse(fs.readFileSync(path.join(profile,'runtime.json'),'utf8'));await fetch(record.url+'/api/v1/runtime/quit',{method:'POST',headers:{'Content-Type':'application/json','X-Gateway-Token':record.token},body:'{}',signal:AbortSignal.timeout(2000)});await until(()=>{try{process.kill(record.pid,0);return false}catch{return true}},'fixture runtime exit',20000)}catch(error){if(fs.existsSync(path.join(profile,'runtime.json')))throw error}}
function fixtureSource(directory,version){
 fs.mkdirSync(directory,{recursive:true});
 // Copy only compiler inputs; exclude profiles, release artifacts and secrets.
 for(const entry of fs.readdirSync(root,{withFileTypes:true}))if(entry.isFile()&&(entry.name.endsWith('.go')||entry.name.endsWith('.syso')))fs.copyFileSync(path.join(root,entry.name),path.join(directory,entry.name));
 for(const name of ['internal','ui','assets/bootstrap','packaging/update-helper'])fs.cpSync(path.join(root,name),path.join(directory,name),{recursive:true,dereference:false});
 const sourcePath=path.join(directory,'compatibility.go'),source=fs.readFileSync(sourcePath,'utf8');
 assert.match(source,/appVersion\s*=\s*"[0-9.]+"/);fs.writeFileSync(sourcePath,source.replace(/(appVersion\s*=\s*")[0-9.]+(")/,'$1'+version+'$2'));
 fs.writeFileSync(path.join(directory,'VERSION.txt'),version+'\n');
 const compatibility=JSON.parse(fs.readFileSync(path.join(root,'COMPATIBILITY.json'),'utf8'));compatibility.app_version=version;fs.writeFileSync(path.join(directory,'COMPATIBILITY.json'),JSON.stringify(compatibility,null,2)+'\n');
}
function compileFixture(source,candidate){const env={...clientEnvironment(),GO111MODULE:'off',CGO_ENABLED:'0',GOOS:'windows',GOARCH:'amd64'};fs.mkdirSync(candidate,{recursive:true});execFileSync(go,['build','-trimpath','-ldflags','-s -w','-o',path.join(candidate,'GatewayClient.exe'),'.'],{cwd:source,env,windowsHide:true,stdio:'pipe',timeout:180000});execFileSync(go,['build','-trimpath','-ldflags','-s -w -H windowsgui','-o',path.join(candidate,'GatewayUpdateHelper.exe'),'.'],{cwd:path.join(source,'packaging/update-helper'),env,windowsHide:true,stdio:'pipe',timeout:180000})}
function packageFixture(candidate,source,fixture,version,revision){
 const packageName='gateway-client-v'+version+'-windows-amd64',folder=path.join(fixture,packageName);fs.mkdirSync(folder,{recursive:true});
 for(const name of ['GatewayClient.exe','GatewayUpdateHelper.exe'])fs.copyFileSync(path.join(candidate,name),path.join(folder,name));
 for(const name of ['GatewayOnDemand.exe','GatewayNativeHost.exe'])fs.copyFileSync(path.join(build,name),path.join(folder,name));
 for(const name of ['LICENSE','THIRD-PARTY-NOTICES.txt','GO-LICENSE.txt'])fs.copyFileSync(path.join(root,name),path.join(folder,name));
 fs.cpSync(path.join(root,'browser-companion'),path.join(folder,'browser-companion'),{recursive:true});fs.copyFileSync(path.join(source,'COMPATIBILITY.json'),path.join(folder,'COMPATIBILITY.json'));
 fs.writeFileSync(path.join(folder,'SOURCE-REVISION.txt'),'Commit: '+revision+'\nVersion: '+version+'\nLocal fixture compiled from current worktree; not a published release.\n');
 const archive=path.join(fixture,packageName+'.zip');
 execFileSync(opts.python||'python',['-c',"import pathlib,sys,zipfile; root=pathlib.Path(sys.argv[1]); out=pathlib.Path(sys.argv[2]); z=zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED); [z.write(p,p.relative_to(root.parent).as_posix()) for p in sorted(root.rglob('*')) if p.is_file()]; z.close()",folder,archive],{windowsHide:true,stdio:'pipe',timeout:60000});
 fs.writeFileSync(path.join(fixture,'artifact-manifest.json'),JSON.stringify({version,source_revision:revision,verification:'local_fixture_zip_and_hash',artifacts:[{file:path.basename(archive),bytes:fs.statSync(archive).size,sha256:digest(archive)}]},null,2));return {archive,folder};
}
async function freePort(){const server=http.createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const port=server.address().port;await new Promise(resolve=>server.close(resolve));return port}
async function run(){
 const temp=fs.mkdtempSync(path.join(os.tmpdir(),'gateway-desktop-update-ui-')),install=path.join(temp,'install'),profile=path.join(temp,'profile'),fixture=path.join(temp,'fixture'),distribution=path.join(temp,'distribution'),keys=path.join(temp,'publisher-keys');
 for(const p of [install,profile,fixture,keys])fs.mkdirSync(p,{recursive:true});
 const sourceVersion=fs.readFileSync(path.join(root,'VERSION.txt'),'utf8').trim(),parts=sourceVersion.split('.').map(Number),futureVersion=[parts[0],parts[1],parts[2]+1].join('.'),revision=execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8',windowsHide:true}).trim();
 for(const name of ['GatewayClient.exe','GatewayUpdateHelper.exe','GatewayOnDemand.exe','GatewayNativeHost.exe'])fs.copyFileSync(path.join(build,name),path.join(install,name));
 for(const name of ['LICENSE','THIRD-PARTY-NOTICES.txt','GO-LICENSE.txt'])fs.copyFileSync(path.join(root,name),path.join(install,name));
 fs.cpSync(path.join(root,'browser-companion'),path.join(install,'browser-companion'),{recursive:true});fs.writeFileSync(path.join(install,'portable.marker'),'Isolated updater acceptance installation.\n');fs.copyFileSync(path.join(root,'COMPATIBILITY.json'),path.join(install,'COMPATIBILITY.json'));
 fs.mkdirSync(path.join(install,'bootstrap'));
 for(const name of ['headers-mainnet.bin','headers-mainnet.json'])fs.copyFileSync(path.join(build,'bootstrap',name),path.join(install,'bootstrap',name));
 const installedHeaders=new Map(['headers-mainnet.bin','headers-mainnet.json'].map(name=>[name,digest(path.join(install,'bootstrap',name))]));
 const settings={network_disabled:false,headers_paused:true,core_disabled:true,core_mount_disabled:true,serve_data:false,share_cache:false,onboarded:true,bitcoin_peers:['127.0.0.1:1'],satline_enabled:false,ord_enabled:false};
 fs.writeFileSync(path.join(profile,'settings.json'),JSON.stringify(settings));fs.writeFileSync(path.join(profile,'keep-user-data.txt'),'profile-preservation-sentinel\n');fs.mkdirSync(path.join(profile,'indexes'));fs.writeFileSync(path.join(profile,'indexes','live.json'),JSON.stringify({schema:1,policies:{bitmap:{index:'bitmap',enabled:false,paused:false,stopped:true,retention:'ephemeral',updated:'2026-10-01T00:00:00Z'}}}));
 fs.writeFileSync(path.join(profile,'appearance.json'),JSON.stringify({theme:'dark',default:'light'}));
 fs.mkdirSync(path.join(profile,'headers'));fs.writeFileSync(path.join(profile,'headers','headers.bin'),fs.readFileSync(path.join(install,'bootstrap','headers-mainnet.bin')).subarray(0,80));
 let browser,page,client,publisher,relay,logFD,publisherLogFD,tamper=true;const errors=[],external=[],relayRequests=[];
 try{
  // Seed a local-only manual publisher before startup. A fresh real release
  // enables its bundled hosted checks, which must never run in this fixture.
  const privateKey=path.join(keys,'private.json'),publicKey=path.join(keys,'trusted.json');
  execFileSync(publisherExe,['keygen','-private',privateKey,'-public',publicKey],{windowsHide:true,env:clientEnvironment(),stdio:'pipe'});
  const trusted=JSON.parse(fs.readFileSync(publicKey,'utf8'));
  fs.mkdirSync(path.join(profile,'updates'));fs.writeFileSync(path.join(profile,'updates','config.json'),JSON.stringify({publisher_url:'http://127.0.0.1:1',trusted_key:trusted,channel:'manual',auto_check:false,auto_install:false,minimum_sequence:0,accepted_manifest_sha256:''}));
  logFD=fs.openSync(path.join(out,'runtime.txt'),'w');client=spawn(path.join(install,'GatewayClient.exe'),['-data',profile,'-background','-no-tray','-http','127.0.0.1:0'],{windowsHide:true,env:clientEnvironment(),stdio:['ignore',logFD,logFD]});
  const initial=await runtimeFor(profile);const initialVersion=initial.ping.version,originalPID=initial.ping.pid;
  const preserved=new Map(['settings.json','appearance.json','keep-user-data.txt','indexes/live.json','headers/headers.bin'].map(name=>[name,digest(path.join(profile,name))]));
  const source=path.join(temp,'future-source'),candidate=path.join(temp,'future-build');fixtureSource(source,futureVersion);console.log('Captured isolated future source fixture '+futureVersion+'; repository version unchanged.');compileFixture(source,candidate);
  const packaged=packageFixture(candidate,source,fixture,futureVersion,revision);
  execFileSync(publisherExe,['publish','-version',futureVersion,'-approve-release',futureVersion,'-sequence','1','-private',privateKey,'-out',distribution,'-local-fixture',fixture],{windowsHide:true,env:clientEnvironment(),stdio:'pipe',timeout:60000});
  const port=await freePort(),publisherURL='http://127.0.0.1:'+port;
  publisherLogFD=fs.openSync(path.join(out,'publisher.txt'),'w');publisher=spawn(publisherExe,['serve','-dir',distribution,'-public',publicKey,'-listen','127.0.0.1:'+port],{windowsHide:true,env:clientEnvironment(),stdio:['ignore',publisherLogFD,publisherLogFD]});
  await until(async()=>{try{return(await fetch(publisherURL+'/manifest.json',{signal:AbortSignal.timeout(500)})).ok}catch{return false}},'local signed publisher');
  relay=http.createServer(async(req,res)=>{try{if(req.method!=='GET'||!(req.url==='/manifest.json'||/^\/artifacts\/[a-zA-Z0-9.-]+\.zip$/.test(req.url))){res.writeHead(404);res.end();return}relayRequests.push(req.url);const response=await fetch(publisherURL+req.url);const body=Buffer.from(await response.arrayBuffer());if(tamper&&req.url.startsWith('/artifacts/')&&body.length)body[0]^=1;res.writeHead(response.status,{'Content-Type':response.headers.get('Content-Type')||'application/octet-stream','Content-Length':body.length});res.end(body)}catch{res.writeHead(502);res.end('Fixture transport unavailable')}});
  await new Promise(resolve=>relay.listen(0,'127.0.0.1',resolve));const feedURL='http://127.0.0.1:'+relay.address().port;
  const {chromium}=playwright();browser=await chromium.launch({headless:true,executablePath:browserPath(chromium)});page=await browser.newPage({viewport:{width:1280,height:900}});page.setDefaultTimeout(15000);
  let activeOrigin=initial.record.url;const allowedOrigins=new Set([new URL(activeOrigin).origin]);page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(!allowedOrigins.has(new URL(request.url()).origin))external.push(request.url())});
  await page.goto(activeOrigin+'/?resolve=settings.gateway',{waitUntil:'networkidle'});await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();
  await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Existing profile appearance selected before update');
  await page.locator('#update-publisher').fill(feedURL);await page.locator('#update-key').fill(trusted.public_key);await page.locator('#update-key-confirm').check();await page.locator('#update-mode').selectOption('manual');await page.locator('#update-save').click();
  await page.waitForFunction(()=>!document.getElementById('update-check').disabled);await page.locator('#update-check').click();await page.locator('#update-title').getByText('v'+futureVersion+' is available',{exact:true}).waitFor();
  assert.equal(await page.locator('#update-apply').isVisible(),false,'Check did not silently install');await capture(page,'signed-update-available.png');
  await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Narrow topbar/update badge has no horizontal overflow');assert.equal(await page.locator('#updates-status').isVisible(),true);assert.deepEqual(await page.locator('.topbar .top-nav').evaluateAll(nodes=>nodes.map(x=>x.dataset.route)),['indexes.gateway','peers.gateway']);assert.equal(await page.locator('.topbar .top-nav').last().evaluate(node=>node.nextElementSibling.id),'settings-button');await capture(page,'signed-update-narrow.png');await page.setViewportSize({width:1280,height:900});
  await page.locator('#update-download').click();await page.waitForFunction(()=>!document.getElementById('update-error').hidden&&/digest mismatch/.test(document.getElementById('update-error').textContent),null,{timeout:30000});
  assert.equal(fs.existsSync(path.join(profile,'updates','stage.json')),false,'Tampered download was staged');await capture(page,'tampered-download-rejected.png');
  tamper=false;
  if(opts.automatic){
   await page.locator('#update-mode').selectOption('automatic');await capture(page,'automatic-update-selected.png');await page.locator('#update-save').click();
   // The explicit preference authorizes the full pipeline. No download or
   // restart button is clicked after this point.
  }else{
   await page.locator('#update-download').click();await page.locator('#update-title').getByText('v'+futureVersion+' is ready to install',{exact:true}).waitFor({timeout:30000});
   assert.equal(await page.locator('#update-apply').isDisabled(),false,'Complete native preview layout supports applying');await capture(page,'signed-update-staged.png');
   assert.equal(JSON.parse(fs.readFileSync(path.join(profile,'runtime.json'),'utf8')).pid,originalPID,'Download did not silently restart');
   await page.locator('#update-apply').click();
  }
  const replacement=await runtimeFor(profile,futureVersion,originalPID);assert.notEqual(replacement.ping.pid,originalPID,'New executable must be a new process');
  await until(()=>{const apply=path.join(profile,'updates','apply');if(!fs.existsSync(apply))return false;for(const entry of fs.readdirSync(apply)){try{const result=JSON.parse(fs.readFileSync(path.join(apply,entry,'result.json'),'utf8'));if(result.state==='installed')return result}catch{}}return false},'helper installed outcome',30000);
  for(const name of ['GatewayClient.exe','GatewayUpdateHelper.exe'])assert.equal(digest(path.join(install,name)),digest(path.join(candidate,name)),name+' replaced with exact signed package payload');
  for(const [name,hash] of installedHeaders)assert.equal(digest(path.join(install,'bootstrap',name)),hash,'App-only update preserves installed header sidecar '+name);
  for(const [name,hash]of preserved)assert.equal(digest(path.join(profile,name)),hash,name+' preserved across restart');
  assert.equal(fs.existsSync(path.join(profile,'private.json')),false);assert.equal(fs.existsSync(path.join(install,'private.json')),false);
  assert.equal(replacement.record.url,initial.record.url,'Approved update preserves the running local management origin');
  await page.waitForFunction(version=>document.getElementById('footer-status')?.textContent.includes(version),futureVersion,{timeout:15000});
  assert.equal(new URL(page.url()).origin,new URL(initial.record.url).origin,'Original browser window reconnected and loaded new bundled UI');await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();await page.locator('#update-current').getByText('v'+futureVersion,{exact:true}).waitFor();await capture(page,'updated-client.png');
  await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Profile-saved appearance survives binary update and original window reload');
  assert.equal(await page.locator('#update-key').inputValue(),trusted.public_key,'Trust preserved across restart');assert.equal(await page.locator('#update-publisher').inputValue(),feedURL);
  assert.equal(await page.locator('#update-mode').inputValue(),opts.automatic?'automatic':'manual','Update preference survives replacement and restart');
  assert.deepEqual(errors,[],'Uncaught browser errors');assert.deepEqual(external,[],'Browser only contacted local client management origin');assert.ok(relayRequests.some(x=>x==='/manifest.json')&&relayRequests.filter(x=>x.startsWith('/artifacts/')).length>=2);
  const result={result:'PASS',mode:opts.automatic?'automatic':'manual',scope:'Native Windows real publisher local-fixture signing/distribution and client UI configuration/check/download, same-size tampered payload rejection and retry, '+(opts.automatic?'explicitly selected automatic download and restart without further button presses':'explicit Update and restart')+', same management origin/original browser automatic reload, helper installed acknowledgement, new PID/version/exact binary hashes, byte-preserved appearance.json/profile settings/index policy/user sentinel and profile-saved dark palette after restart, retained public trust; no GitHub origin or release',current_version:initialVersion,fixture_version:futureVersion,initial_pid:originalPID,updated_pid:replacement.ping.pid,publisher_key_id:trusted.key_id,package_sha256:digest(packaged.archive),installed_client_sha256:digest(path.join(install,'GatewayClient.exe')),installed_helper_sha256:digest(path.join(install,'GatewayUpdateHelper.exe')),preserved_files:[...preserved.keys()],page_errors:errors,external_browser_requests:external,browser:browser.version(),production_trust_configured:false,published:false};fs.writeFileSync(path.join(out,'result.json'),JSON.stringify(result,null,2));console.log('Desktop updates browser: PASS ('+result.scope+').');
 }catch(error){let browserState;try{if(page){await capture(page,'failure.png');browserState=await page.evaluate(()=>({url:location.href,notice:document.getElementById('notice')?.textContent,footer:document.getElementById('footer-status')?.textContent,settings_sections:document.querySelector('.settings-sections')?.textContent,update_state:document.getElementById('update-state')?.textContent}))}}catch{}fs.writeFileSync(path.join(out,'result.json'),JSON.stringify({result:'NOT_PASSED',error:String(error.stack||error),page_errors:errors,external_browser_requests:external,browser_state:browserState,fixture_profile:profile},null,2));throw error}
 finally{
  if(browser)await browser.close();await stopProfile(profile);if(client&&client.exitCode===null&&client.signalCode===null)client.kill();if(publisher&&publisher.exitCode===null&&publisher.signalCode===null){publisher.kill();await until(()=>publisher.exitCode!==null||publisher.signalCode!==null,'publisher exit',5000)}if(relay)await new Promise(resolve=>relay.close(resolve));if(logFD!==undefined)fs.closeSync(logFD);if(publisherLogFD!==undefined)fs.closeSync(publisherLogFD);
  // Only remove the exact fresh fixture root after every runtime has stopped.
  assert.equal(path.dirname(temp),path.resolve(os.tmpdir()));assert(path.basename(temp).startsWith('gateway-desktop-update-ui-'));assert.equal(fs.lstatSync(temp).isSymbolicLink(),false);fs.rmSync(temp,{recursive:true,force:true});
 }
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1});
