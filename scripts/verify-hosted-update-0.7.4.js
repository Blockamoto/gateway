'use strict';
// Prepared public-release acceptance harness. No network or processes until
// --run-hosted is supplied after final artifacts and live delivery are ready.
// Uses the genuine published 0.7.3 client, isolated disposable profiles only.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {spawn, execFileSync} = require('node:child_process');
const {createHash, createPublicKey, verify} = require('node:crypto');
const root = path.resolve(__dirname, '..');
const opts = {
  source: root,
  newBuild: path.join(root, 'build/local-0.7.4-release/package'),
  artifacts: path.join(root, 'build/local-0.7.4-release/artifacts'),
  publisher: 'https://gateway-updates.onrender.com',
  python: 'python',
  out: path.join(root, 'build/hosted-public-acceptance-0.7.4'),
  sequence: '7',
  headerSequence: '6',
  oldBuild: path.join(root, 'build/local-0.7.4-release/previous-release'),
  oldArtifacts: path.join(root, 'build/review-0.7.3'),
};
const flags = {'--source':'source','--new-build':'newBuild','--artifacts':'artifacts','--publisher':'publisher','--python':'python','--go':'go','--out':'out','--browser':'browser','--sequence':'sequence','--header-sequence':'headerSequence','--old-build':'oldBuild','--old-artifacts':'oldArtifacts'};
let runHosted = false;
for (let i=2;i<process.argv.length;i++) {
  if (process.argv[i] === '--run-hosted') { runHosted = true; continue; }
  if (process.argv[i] === '--inspect') continue;
  const key = flags[process.argv[i]];
  assert(key && process.argv[i+1], 'Unknown or incomplete option: '+process.argv[i]);
  opts[key] = process.argv[++i];
}
const binaries = ['GatewayClient.exe','GatewayUpdateHelper.exe','GatewayOnDemand.exe','GatewayNativeHost.exe'];
const hash = value => createHash('sha256').update(value).digest('hex');
const digest = file => hash(fs.readFileSync(file));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const readJSON = file => JSON.parse(fs.readFileSync(file,'utf8'));
const within = (base,file) => {const rel=path.relative(path.resolve(base),path.resolve(file));return rel!==''&&!path.isAbsolute(rel)&&rel!=='..'&&!rel.startsWith('..'+path.sep);};
const out = path.resolve(opts.out);
assert(out !== path.parse(out).root && path.basename(out).startsWith('hosted-public-acceptance'), 'Use a dedicated hosted-public-acceptance evidence directory');
assert.equal(process.platform,'win32','Native replacement acceptance requires Windows');
assert.equal(opts.publisher,'https://gateway-updates.onrender.com','Use the approved public publisher');
const trusted = readJSON(path.join(opts.source,'assets/bootstrap/update-channels.json')).channels.find(channel=>channel.id==='stable').trusted_key;
assert.deepEqual(Object.keys(trusted).sort(),['key_id','public_key'],'Input must be a public-only trust document');
const publicBytes=Buffer.from(trusted.public_key,'base64');
assert.equal(publicBytes.length,32);
assert.equal(hash(publicBytes).slice(0,32),trusted.key_id);
const publicKey=createPublicKey({key:Buffer.concat([Buffer.from('302a300506032b6570032100','hex'),publicBytes]),format:'der',type:'spki'});
const expected = {};
for (const name of binaries) expected[name]=digest(path.join(opts.newBuild,name));
const candidateAudit=readJSON(path.join(opts.newBuild,'binary-audit.json'));
assert.equal(candidateAudit.status,'PASS');
for (const name of binaries) assert.equal(candidateAudit.windows.find(item=>item.file===name)?.sha256,expected[name],name+' differs from candidate audit');
const targetRecord=readJSON(path.join(opts.artifacts,'artifact-manifest.json'));
assert.equal(targetRecord.version,'0.7.4');
const targetPackage=path.join(opts.artifacts,'gateway-client-v0.7.4-windows-amd64.zip');
const expectedArtifact=targetRecord.artifacts.find(a=>a.file===path.basename(targetPackage));
assert.equal(digest(targetPackage),expectedArtifact.sha256);
const sourceRevision=targetRecord.source_revision;
assert.match(sourceRevision,/^[a-f0-9]{40}$/);
assert.equal(execFileSync('git',['rev-parse',sourceRevision+'^{commit}'],{cwd:opts.source,encoding:'utf8',windowsHide:true}).trim(),sourceRevision,'Reviewed artifact revision is absent or is not a commit');
assert.equal(trusted.key_id,'c5f20c46d5daf2aa7f21c1602efd3a0b');
assert(Number.isSafeInteger(Number(opts.sequence))&&Number(opts.sequence)>0);
assert(Number.isSafeInteger(Number(opts.headerSequence))&&Number(opts.headerSequence)>0);
const prepared = {scope:'Public 0.7.4 acceptance: genuine published 0.7.3 client upgrading to exact approved 0.7.4 release; separate fresh 0.7.4 profile',source_revision:sourceRevision,mode:runHosted?'hosted-run':'inspection-only',source_version:'0.7.3',target_version:'0.7.4',publisher:opts.publisher,public_key_id:trusted.key_id,expected_binary_sha256:expected,expected_manifest_sequence:Number(opts.sequence),expected_header_sequence:Number(opts.headerSequence),network_enabled:runHosted};
if (!runHosted) { console.log(JSON.stringify(prepared,null,2)); process.exit(0); }

function clientEnvironment() {
  // Build a small environment rather than inheriting publisher/GitHub credentials.
  const allow = new Set(['systemroot','windir','path','pathext','comspec','temp','tmp','userprofile','homedrive','homepath','localappdata','appdata','programfiles','programfiles(x86)','programw6432','programdata','number_of_processors','processor_architecture','os']);
  const env={};
  for (const [key,value] of Object.entries(process.env)) if (allow.has(key.toLowerCase())) env[key]=value;
  assert(!Object.keys(env).some(key=>/token|secret|github|^gh_|publisher|private/i.test(key)));
  return env;
}
function playwright() {
  try {return require('playwright');}
  catch {return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}
}
function browserPath(chromium) {
  const choices=[opts.browser,chromium.executablePath()];
  for (const base of [process.env['ProgramFiles(x86)'],process.env.ProgramFiles,process.env.LOCALAPPDATA].filter(Boolean)) {
    choices.push(path.join(base,'Microsoft/Edge/Application/msedge.exe'),path.join(base,'Google/Chrome/Application/chrome.exe'));
  }
  const found=choices.find(file=>file&&fs.existsSync(file)); assert(found,'Installed Chromium not found'); return found;
}
async function until(fn,label,timeout=90000) {
  const end=Date.now()+timeout;
  while (Date.now()<end) {const value=await fn();if(value)return value;await delay(150);}
  throw Error('Timed out: '+label);
}
async function runtimeFor(profile,version,previousPID) {
  return until(async()=>{try {
    const record=readJSON(path.join(profile,'runtime.json'));
    if (record.pid===previousPID) return false;
    const url=new URL(record.url); assert.equal(url.hostname,'127.0.0.1');
    const response=await fetch(record.url+'/api/v1/runtime/ping',{signal:AbortSignal.timeout(1000)});
    if (!response.ok) return false;
    const ping=await response.json();
    return ping.version===version&&ping.pid===record.pid?{record,ping}:false;
  } catch {return false;}},'runtime '+version);
}
async function statusFor(record) {
  const response=await fetch(record.url+'/api/v1/updates/status',{headers:{'X-Gateway-Token':record.token},signal:AbortSignal.timeout(3000)});
  assert(response.ok,'Local update status failed');
  const value=await response.json();return value.update||value;
}
async function stopProfile(profile) {
  if (!fs.existsSync(path.join(profile,'runtime.json'))) return;
  const record=readJSON(path.join(profile,'runtime.json'));
  assert.equal(new URL(record.url).hostname,'127.0.0.1');
  try {process.kill(record.pid,0);} catch {return;}
  const response=await fetch(record.url+'/api/v1/runtime/quit',{method:'POST',headers:{'Content-Type':'application/json','X-Gateway-Token':record.token},body:'{}',signal:AbortSignal.timeout(5000)});
  assert(response.ok,'Disposable runtime refused graceful exit');
  await until(()=>{try{process.kill(record.pid,0);return false;}catch{return true;}},'disposable runtime exit',30000);
}
function verifyEnvelope(bytes) {
  assert(bytes.length>0&&bytes.length<=65536);
  const envelope=JSON.parse(bytes.toString('utf8'));
  assert.equal(envelope.schema,1);assert.equal(envelope.key_id,trusted.key_id);
  const payload=Buffer.from(envelope.payload,'base64'),signature=Buffer.from(envelope.signature,'base64');
  assert(verify(null,Buffer.concat([Buffer.from('Gateway desktop update manifest v1\0'),payload]),publicKey,signature),'Independent hosted signature verification failed');
  const manifest=JSON.parse(payload.toString('utf8'));
  assert.equal(manifest.schema,1);assert.equal(manifest.version,'0.7.4');assert.equal(manifest.sequence,Number(opts.sequence));assert.equal(manifest.source_revision,targetRecord.source_revision);
  assert.equal(manifest.release_url,'https://github.com/Blockamoto/gateway/releases/tag/v0.7.4');
  assert(Date.parse(manifest.issued_at)<=Date.now()+300000&&Date.parse(manifest.expires_at)>Date.now());
  assert(Date.parse(manifest.expires_at)-Date.parse(manifest.issued_at)<=31*86400000);
  const artifact=manifest.artifacts.find(item=>item.platform==='windows-amd64');
  assert(artifact);assert.equal(artifact.file,'gateway-client-v0.7.4-windows-amd64.zip');assert.equal(artifact.format,'zip');
  assert.match(artifact.sha256,/^[a-f0-9]{64}$/);assert(artifact.bytes>0&&artifact.bytes<=256*1024*1024);assert.equal(artifact.sha256,expectedArtifact.sha256);assert.equal(artifact.bytes,expectedArtifact.bytes);
  return {manifest,artifact};
}
function makeInstallation(temp,name,build) {
  const install=path.join(temp,name);fs.mkdirSync(install);
  for (const file of binaries) fs.copyFileSync(path.join(build,file),path.join(install,file));
  fs.mkdirSync(path.join(install,'bootstrap'));
  for (const file of ['headers-mainnet.bin','headers-mainnet.json']) fs.copyFileSync(path.join(build,'bootstrap',file),path.join(install,'bootstrap',file));
  fs.writeFileSync(path.join(install,'portable.marker'),'Disposable real hosted-update acceptance.\n');
  return install;
}
function packageMetadata(install,archive,version) {
  const script=`import pathlib,sys,zipfile
archive,dest,version=sys.argv[1],pathlib.Path(sys.argv[2]),sys.argv[3]
prefix='gateway-client-v'+version+'-windows-amd64/'
with zipfile.ZipFile(archive) as z:
 for item in z.infolist():
  if not item.filename.startswith(prefix): raise ValueError('unexpected package root')
  relative=pathlib.PurePosixPath(item.filename[len(prefix):])
  if relative.is_absolute() or '..' in relative.parts: raise ValueError('unsafe metadata path')
  if str(relative)=='COMPATIBILITY.json' or (len(relative.parts)==2 and relative.parts[0]=='browser-companion'):
   if item.is_dir(): continue
   p=dest.joinpath(*relative.parts);p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(z.read(item))
`;
  execFileSync(opts.python,['-c',script,archive,install,version],{windowsHide:true,env:clientEnvironment(),stdio:'pipe',timeout:30000});
  assert.equal(readJSON(path.join(install,'COMPATIBILITY.json')).app_version,version);
}
function makeProfile(temp,name,install,online) {
  const profile=path.join(temp,name);fs.mkdirSync(profile);
  const settings={network_disabled:!online,headers_paused:true,core_disabled:true,core_mount_disabled:true,serve_data:false,share_cache:false,onboarded:true,bitcoin_peers:['127.0.0.1:1'],satline_enabled:false,ord_enabled:false};
  fs.writeFileSync(path.join(profile,'settings.json'),JSON.stringify(settings));
  fs.writeFileSync(path.join(profile,'appearance.json'),JSON.stringify({theme:'dark',default:'light'}));
  fs.writeFileSync(path.join(profile,'keep-user-data.txt'),'hosted-profile-preservation-sentinel\n');
  fs.mkdirSync(path.join(profile,'indexes'));fs.writeFileSync(path.join(profile,'indexes/live.json'),JSON.stringify({schema:1,policies:{bitmap:{index:'bitmap',enabled:false,paused:false,stopped:true,retention:'ephemeral',updated:'2026-10-01T00:00:00Z'}}}));
  fs.mkdirSync(path.join(profile,'headers'));fs.writeFileSync(path.join(profile,'headers/headers.bin'),fs.readFileSync(path.join(install,'bootstrap/headers-mainnet.bin')));
  return profile;
}
async function openUpdates(page,record) {
  await page.goto(record.url+'/?resolve=settings.gateway',{waitUntil:'networkidle'});
  await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();
  await page.waitForFunction(()=>document.getElementById('update-current')?.textContent?.startsWith('v'));
}
async function capture(page,name) {await page.screenshot({path:path.join(out,name),fullPage:true});}
function assertNoCredential(profile,status,autoCheck=false,autoInstall=false) {
  assert.equal(!!status.access_token_configured,false);
  const config=readJSON(path.join(profile,'updates/config.json'));
  assert(!config.access_token,'A delivery credential was stored unexpectedly');
  assert.equal(config.trusted_key.public_key,trusted.public_key);
  assert.equal(config.publisher_url,opts.publisher);
  assert.equal(config.auto_check,autoCheck);assert.equal(!!config.auto_install,autoInstall);
  return {publisher:config.publisher_url,public_key_id:config.trusted_key.key_id,access_token_configured:false};
}
async function run() {
  fs.mkdirSync(out,{recursive:true});assert(!fs.existsSync(path.join(out,'result.json')),'Choose a new evidence output directory; previous receipt exists');
  fs.writeFileSync(path.join(out,'prepared-inputs.json'),JSON.stringify(prepared,null,2));
  const temp=fs.mkdtempSync(path.join(out,'disposable-public-update-'));assert(within(out,temp));
  const profiles=[],children=[],fds=[],pageErrors=[],externalRequests=[],allowedOrigins=new Set();
  let browser,page,result,cleanupComplete=false;
  function launch(install,profile,label) {
    const fd=fs.openSync(path.join(out,label+'-runtime.txt'),'w');fds.push(fd);
    const child=spawn(path.join(install,'GatewayClient.exe'),['-data',profile,'-background','-no-tray','-http','127.0.0.1:0'],{windowsHide:true,env:clientEnvironment(),stdio:['ignore',fd,fd]});
    children.push(child);profiles.push(profile);
  }
  try {
    const response=await fetch(opts.publisher+'/manifest.json',{redirect:'error',headers:{Accept:'application/octet-stream'},signal:AbortSignal.timeout(120000)});
    assert.equal(response.status,200,'Hosted manifest unavailable');
    const manifestBytes=Buffer.from(await response.arrayBuffer()),hosted=verifyEnvelope(manifestBytes);
    fs.writeFileSync(path.join(out,'hosted-manifest.json'),manifestBytes);
    const oldRecord=readJSON(path.join(opts.oldArtifacts,'artifact-manifest.json'));
    assert.equal(oldRecord.version,'0.7.3');
    const oldPackage=path.join(opts.oldArtifacts,'gateway-client-v0.7.3-windows-amd64.zip');
    const oldArtifact=oldRecord.artifacts.find(a=>a.file===path.basename(oldPackage));
    assert.equal(digest(oldPackage),oldArtifact.sha256,'Previous released package differs from its recorded artifact');
    const oldAudit=readJSON(path.join(opts.oldBuild,'binary-audit.json'));assert.equal(oldAudit.status,'PASS');
    for(const file of binaries)assert.equal(digest(path.join(opts.oldBuild,file)),oldAudit.windows.find(x=>x.file===file)?.sha256,'Previous released binary differs from final audit');
    const install=makeInstallation(temp,'released-0.7.3-install',opts.oldBuild);packageMetadata(install,oldPackage,'0.7.3');
    const fixture={runtime_version:'0.7.3',synthetic:false,published:true,source_revision:oldRecord.source_revision,archive_sha256:oldArtifact.sha256,client_sha256:digest(path.join(install,'GatewayClient.exe')),private_signing_key_used:false};
    const profile=makeProfile(temp,'update-profile',install,true);
    const sidecars=new Map(['headers-mainnet.bin','headers-mainnet.json'].map(name=>[name,digest(path.join(install,'bootstrap',name))]));
    launch(install,profile,'upgrade');const initial=await runtimeFor(profile,'0.7.3');allowedOrigins.add(new URL(initial.record.url).origin);
    const preserve=['settings.json','appearance.json','keep-user-data.txt','indexes/live.json','headers/headers.bin'];
    const before=new Map(preserve.map(name=>[name,digest(path.join(profile,name))]));
    const {chromium}=playwright();browser=await chromium.launch({headless:true,executablePath:browserPath(chromium)});
    page=await browser.newPage({viewport:{width:1280,height:900}});page.setDefaultTimeout(20000);
    page.on('pageerror',error=>pageErrors.push(error.message));
    page.on('request',request=>{const url=new URL(request.url());if(['http:','https:'].includes(url.protocol)&&!allowedOrigins.has(url.origin))externalRequests.push(request.url());});
    await openUpdates(page,initial.record);
    const oldStatus=await statusFor(initial.record);assert.equal(oldStatus.configured,true);assert.equal(oldStatus.current_version,'0.7.3');
    assert.equal(oldStatus.channel,'stable');assert.equal(oldStatus.auto_check,true);assert.equal(oldStatus.auto_install,false);
    assertNoCredential(profile,oldStatus,true,false);
    assert.equal(await page.locator('#update-publisher').inputValue(),opts.publisher);assert.equal(await page.locator('#update-key').inputValue(),trusted.public_key);
    // The previous released client checks the same bundled public feed without setup.
    await page.locator('#update-title').getByText('v0.7.4 is available',{exact:true}).waitFor({timeout:150000});
    assert.equal(await page.locator('#update-apply').isVisible(),false);await capture(page,'hosted-update-available.png');
    await page.locator('#update-mode').selectOption('automatic');await capture(page,'hosted-automatic-selected.png');
    await page.locator('#update-save').click();
    // Explicit automatic mode is the only installation consent. No Download or
    // Update and restart button is pressed, and no apply/config API is injected.
    const updated=await runtimeFor(profile,'0.7.4',initial.ping.pid);
    const stage=readJSON(path.join(profile,'updates/stage.json'));
    assert(within(path.join(profile,'updates'),stage.package_path));assert(within(path.join(profile,'updates'),stage.manifest_path));
    const stagedBytes=fs.readFileSync(stage.manifest_path),staged=verifyEnvelope(stagedBytes);
    assert.equal(staged.artifact.sha256,hosted.artifact.sha256,'Hosted artifact changed during acceptance');
    assert.equal(fs.statSync(stage.package_path).size,staged.artifact.bytes);assert.equal(digest(stage.package_path),staged.artifact.sha256);
    assert.equal(updated.record.url,initial.record.url,'Update changed management origin');
    const helper=await until(()=>{const dir=path.join(profile,'updates/apply');if(!fs.existsSync(dir))return false;for(const entry of fs.readdirSync(dir)){try{const value=readJSON(path.join(dir,entry,'result.json'));if(value.state==='installed')return value;}catch{}}return false;},'helper successful installation',30000);
    for (const file of binaries) assert.equal(digest(path.join(install,file)),expected[file],file+' differs from the actual candidate');
    for (const [file,sha] of sidecars) assert.equal(digest(path.join(install,'bootstrap',file)),sha,'Header sidecar changed: '+file);
    for (const [file,sha] of before) assert.equal(digest(path.join(profile,file)),sha,'Profile file changed: '+file);
    await page.waitForFunction(()=>document.getElementById('footer-status')?.textContent.includes('0.7.4'),null,{timeout:30000});
    await openUpdates(page,updated.record);await page.evaluate(()=>window.GatewayTheme.ready);
    assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
    const trust=assertNoCredential(profile,await statusFor(updated.record),true,true);
    assert.equal(await page.locator('#update-key').inputValue(),trusted.public_key);assert.equal(await page.locator('#update-publisher').inputValue(),opts.publisher);
    await capture(page,'hosted-update-installed.png');await stopProfile(profile);
    // A separate fresh 0.7.4 profile must configure and check the reviewed
    // Render channel automatically. No Settings save/check action is sent.
    const channelInstall=makeInstallation(temp,'channel-install',opts.newBuild);
    packageMetadata(channelInstall,targetPackage,'0.7.4');
    const channelProfile=makeProfile(temp,'channel-profile',channelInstall,true);const freshSettings=readJSON(path.join(channelProfile,'settings.json'));freshSettings.headers_paused=false;fs.writeFileSync(path.join(channelProfile,'settings.json'),JSON.stringify(freshSettings));
    launch(channelInstall,channelProfile,'bundled-channel');const channelRuntime=await runtimeFor(channelProfile,'0.7.4');allowedOrigins.add(new URL(channelRuntime.record.url).origin);
    const channelStatus=await statusFor(channelRuntime.record);
    const stable=channelStatus.channels.find(item=>item.id==='stable');assert(stable,'Bundled stable channel missing');
    assert.equal(stable.publisher_url,opts.publisher);assert.equal(stable.trusted_key,trusted.public_key);
    assert.equal(channelStatus.configured,true);assert.equal(channelStatus.channel,'stable');assert.equal(channelStatus.auto_check,true);assert.equal(channelStatus.auto_install,false);
    const finalChannel=await until(async()=>{const s=await statusFor(channelRuntime.record);if(s.state==='error')throw Error('Fresh automatic check failed: '+s.error);return s.state==='current'&&s.last_checked?s:false;},'fresh default automatic hosted check',150000);
    assertNoCredential(channelProfile,finalChannel,true);
    assert.equal(finalChannel.current_version,'0.7.4');assert.equal(finalChannel.publisher_version,'0.7.4');assert(!finalChannel.error);
    await openUpdates(page,channelRuntime.record);
    await page.locator('#update-title').getByText('Gateway is up to date',{exact:true}).waitFor();
    assert.equal(await page.locator('#update-channel').inputValue(),'stable');assert.equal(await page.locator('#update-mode').inputValue(),'notify');
    assert.equal(await page.locator('#update-publisher').inputValue(),opts.publisher);assert.equal(await page.locator('#update-key').inputValue(),trusted.public_key);
    assert.equal(await page.locator('#update-download').isVisible(),false,'Current client offered an unnecessary download');
    assert.equal(await page.locator('#update-key-confirm').isChecked(),false,'Embedded publisher never needed manual key confirmation');
    await capture(page,'bundled-stable-channel.png');
    assert.deepEqual(pageErrors,[],'Unexpected browser script errors');assert.deepEqual(externalRequests,[],'Browser contacted a nonlocal origin');
    const headerReceiptPath=path.join(channelProfile,'updates','headers-'+trusted.key_id+'.json');
    const headerReceipt=await until(()=>{try{const receipt=readJSON(headerReceiptPath);return receipt.sequence===Number(opts.headerSequence)?receipt:false;}catch{return false;}},'fresh signed public header feed',150000);
    const headerBytes=Buffer.from(await (await fetch(opts.publisher+'/headers/manifest.json',{redirect:'error',signal:AbortSignal.timeout(120000)})).arrayBuffer());
    const headerEnvelope=JSON.parse(headerBytes.toString('utf8'));assert.equal(headerEnvelope.key_id,trusted.key_id);
    const headerPayload=Buffer.from(headerEnvelope.payload,'base64');assert(verify(null,Buffer.concat([Buffer.from('Gateway Bitcoin header manifest v1\0'),headerPayload]),publicKey,Buffer.from(headerEnvelope.signature,'base64')),'Header feed signature invalid');
    const headerManifest=JSON.parse(headerPayload.toString('utf8'));assert.equal(headerManifest.sequence,Number(opts.headerSequence));assert.equal(headerManifest.source_release,'https://github.com/Blockamoto/gateway/releases/tag/v0.7.4');
    assert.equal(headerReceipt.digest,hash(headerBytes));
    const syncResponse=await fetch(channelRuntime.record.url+'/api/v1/status',{headers:{'X-Gateway-Token':channelRuntime.record.token},signal:AbortSignal.timeout(5000)});assert(syncResponse.ok);
    const syncStatus=await syncResponse.json();assert.equal(syncStatus.ready,true);assert(syncStatus.header_count>=headerManifest.count);assert.equal(syncStatus.header_height,syncStatus.header_count-1);assert(!syncStatus.header_bootstrap_error);
    // Unpaused fresh clients may legitimately append newer Bitcoin headers.
    // Freeze this disposable runtime, then prove the entire reviewed snapshot
    // is unchanged and the file consists of complete headers at/after its tip.
    await stopProfile(channelProfile);
    const freshHeaders=fs.readFileSync(path.join(channelProfile,'headers/headers.bin')),baselineHeaders=fs.readFileSync(path.join(channelInstall,'bootstrap/headers-mainnet.bin'));
    assert(freshHeaders.length>=baselineHeaders.length);assert.equal(freshHeaders.length%80,0);assert(freshHeaders.subarray(0,baselineHeaders.length).equals(baselineHeaders),'Fresh synchronized headers changed the validated snapshot prefix');assert(freshHeaders.length/80>=syncStatus.header_count);
    const freshHeaderEvidence={sequence:headerReceipt.sequence,digest:headerReceipt.digest,signed_count:headerManifest.count,validated_snapshot_prefix_sha256:hash(baselineHeaders),complete_file_sha256:hash(freshHeaders),complete_file_count:freshHeaders.length/80,ordinary_peer_extension_headers:(freshHeaders.length-baselineHeaders.length)/80,synchronization:{ready:syncStatus.ready,state:syncStatus.header_state,count:syncStatus.header_count,height:syncStatus.header_height},existing_snapshot_prefix_preserved:true};
    fs.writeFileSync(path.join(out,'hosted-headers-manifest.json'),headerBytes);
    result={result:'PASS',update_mode:'automatic',download_button_pressed:false,restart_button_pressed:false,scope:'Public-release hosted acceptance: genuine published 0.7.3 client automatically installs exact public 0.7.4 and restarts with settings and full headers preserved. Separate fresh 0.7.4 automatically checks both app and header feeds without Settings setup; its validated snapshot prefix remains unchanged while ordinary peers may extend the chain.',publisher:opts.publisher,source_version:'0.7.3',source_is_synthetic_fixture:false,fixture,installed_version:'0.7.4',source_revision:staged.manifest.source_revision,manifest_sequence:staged.manifest.sequence,manifest_sha256:hash(stagedBytes),archive_sha256:staged.artifact.sha256,archive_bytes:staged.artifact.bytes,installed_binary_sha256:expected,preserved_profile_files:Object.fromEntries(before),preserved_bootstrap_files:Object.fromEntries(sidecars),initial_pid:initial.ping.pid,updated_pid:updated.ping.pid,management_origin_preserved:true,helper_state:helper.state,trust,bundled_channel:{id:stable.id,label:stable.label,visible:true,configured_before_ui:true,automatic_check:true,automatic_install:false,manual_configuration_actions:0,live_check:true,up_to_date:true,last_checked:finalChannel.last_checked},fresh_header_feed:freshHeaderEvidence,github_credentials_in_client:false,private_signing_key_read:false,fixture_release_created:false,system_install_performed:false,production_profile_touched:false,preflight_warmed_publisher:true,cold_start_measured:false,full_profile_header_bytes:fs.statSync(path.join(profile,'headers/headers.bin')).size,page_errors:pageErrors,external_browser_requests:externalRequests,browser:browser.version()};

  } catch (error) {
    try {if(page)await capture(page,'failure.png');} catch {}
    result={result:'NOT_PASSED',error:String(error.stack||error),temporary_root:temp,page_errors:pageErrors,external_browser_requests:externalRequests};
    throw error;
  } finally {
    try {
      if (browser) await browser.close();
      for (const profile of profiles) await stopProfile(profile);
      for (const child of children) if (child.exitCode===null&&child.signalCode===null) await until(()=>child.exitCode!==null||child.signalCode!==null,'tracked child exit',10000);
      for (const fd of fds) fs.closeSync(fd);
      assert.equal(path.dirname(temp),out);assert(within(out,temp));assert(path.basename(temp).startsWith('disposable-public-update-'));assert.equal(fs.lstatSync(temp).isSymbolicLink(),false);
      fs.rmSync(temp,{recursive:true,force:true});cleanupComplete=true;
    } catch (error) {if(result?.result==='PASS')result={...result,result:'NOT_PASSED',cleanup_error:String(error.stack||error),temporary_root:temp};throw error;}
    finally {if(result)fs.writeFileSync(path.join(out,'result.json'),JSON.stringify({...result,disposable_profiles_removed:cleanupComplete},null,2));}
  }
  console.log('Hosted update acceptance PASS. Evidence: '+out);
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
