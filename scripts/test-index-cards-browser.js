'use strict';
// Real browser acceptance against a fresh, isolated offline Gateway process.
// Uses an existing Playwright installation and browser; no downloads or policy changes.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {createHash} = require('node:crypto');
const options = {};
for (let i = 2; i < process.argv.length; i++) {
  const key = process.argv[i];
  const value = process.argv[++i];
  if (!['--binary', '--out', '--browser', '--source'].includes(key) || !value) throw Error('Usage: node scripts/test-index-cards-browser.js --binary PATH --out DIR [--browser PATH] [--source FROZEN_SOURCE]');
  options[key.slice(2)] = value;
}
if (!options.binary || !options.out) throw Error('--binary and --out are required');
// Operational verifier fixes can check unchanged published executables while
// version and channel expectations still come from their exact frozen source.
const source = path.resolve(options.source || path.join(__dirname, '..'));
const expectedVersion = fs.readFileSync(path.join(source, 'VERSION.txt'), 'utf8').trim();
const bundledChannels = JSON.parse(fs.readFileSync(path.join(source, 'assets/bootstrap/update-channels.json'), 'utf8')).channels;
const expectedChannel = bundledChannels.find(channel=>channel.id==='stable') || bundledChannels[0];
assert.ok(expectedChannel, 'This release acceptance requires a reviewed bundled update channel');
const out = path.resolve(options.out);
fs.mkdirSync(out, {recursive:true});

function playwright() {
  try { return require('playwright'); } catch (error) {
    const bundled = path.join(os.homedir(), '.cache', 'codex-runtimes', 'codex-primary-runtime', 'dependencies', 'node', 'node_modules', 'playwright');
    if (fs.existsSync(bundled)) return require(bundled);
    throw Error('Playwright is required. Make an existing installation available through NODE_PATH. ' + error.message);
  }
}
function browserPath(chromium) {
  const candidates = [options.browser, process.env.GATEWAY_TEST_BROWSER, chromium.executablePath()];
  if (process.platform === 'win32') {
    for (const base of [process.env['ProgramFiles(x86)'], process.env.ProgramFiles, process.env.LOCALAPPDATA].filter(Boolean)) {
      candidates.push(path.join(base, 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
      candidates.push(path.join(base, 'Google', 'Chrome', 'Application', 'chrome.exe'));
    }
  }
  const found = candidates.find(candidate => candidate && fs.existsSync(candidate));
  if (!found) throw Error('No installed Chromium browser found. Set GATEWAY_TEST_BROWSER; this test does not download browsers.');
  return found;
}
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function endpointFor(profile, runtime) {
  for (let i = 0; i < 100; i++) {
    if (runtime.exitCode !== null) throw Error('Fixture runtime exited before startup');
    try {
      const endpoint = JSON.parse(fs.readFileSync(path.join(profile, 'runtime.json'), 'utf8')).url;
      const response = await fetch(endpoint + '/api/v1/runtime/ping', {signal:AbortSignal.timeout(1000)});
      if (response.ok) return endpoint;
    } catch (_) {}
    await delay(100);
  }
  throw Error('No fixture endpoint');
}

async function run() {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'gateway-cards-'));
  fs.writeFileSync(path.join(profile, 'settings.json'), JSON.stringify({network_disabled:true, headers_paused:true, core_disabled:true, core_mount_disabled:true, serve_data:false, share_cache:false, onboarded:true, lan_discovery:false}));
  const log = fs.openSync(path.join(out, 'runtime.txt'), 'w');
  const runtime = spawn(path.resolve(options.binary), ['-data', profile, '-background', '-no-tray', '-http', '127.0.0.1:0'], {stdio:['ignore', log, log], windowsHide:true});
  let browser;
  const errors = [], external = [];
  try {
    const endpoint = await endpointFor(profile, runtime);
    const ping = await (await fetch(endpoint + '/api/v1/runtime/ping')).json();
    assert.equal(ping.version, expectedVersion, 'Runtime must match the intended validation version');
    // Trust and the default notification preference must already exist before
    // visiting Settings. Networking stays disabled throughout this fixture.
    const firstUpdate = await (await fetch(endpoint+'/api/v1/updates/status')).json();
    assert.equal(firstUpdate.configured,true,'Fresh startup provisions the reviewed publisher');
    assert.equal(firstUpdate.channel,expectedChannel.id);
    assert.equal(firstUpdate.publisher_url,expectedChannel.publisher_url);
    assert.equal(firstUpdate.trusted_key,expectedChannel.trusted_key.public_key);
    assert.equal(firstUpdate.key_fingerprint,expectedChannel.trusted_key.key_id);
    assert.equal(firstUpdate.auto_check,true,'Fresh startup defaults to automatic notifications');
    assert.equal(firstUpdate.auto_install,false,'Unattended installation needs an explicit choice');
    assert.equal(firstUpdate.state,'disabled_offline','Provisioning does not bypass the offline setting');
    assert.equal(firstUpdate.last_checked,undefined,'Fresh offline startup did not check the publisher');
    const savedUpdate = JSON.parse(fs.readFileSync(path.join(profile,'updates','config.json'),'utf8'));
    assert.equal(savedUpdate.publisher_url,expectedChannel.publisher_url,'Startup provisioning persists before UI interaction');
    assert.equal(savedUpdate.trusted_key.key_id,expectedChannel.trusted_key.key_id);
    assert.equal(savedUpdate.auto_check,true);assert.equal(savedUpdate.auto_install,false);
    const {chromium} = playwright(), executablePath = browserPath(chromium);
    browser = await chromium.launch({headless:true, executablePath});
    const page = await browser.newPage({viewport:{width:1280, height:900}});
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => { if (!request.url().startsWith(endpoint + '/')) external.push(request.url()); });
    await page.goto(endpoint + '/', {waitUntil:'networkidle'});
    const indexes=page.frameLocator('#indexes-module');await indexes.locator('#timeline-collapse').waitFor();
    if(await indexes.locator('#timeline-collapse:not([aria-expanded=true])').isVisible())await indexes.locator('#timeline-collapse').click();
    const status=await(await fetch(endpoint+'/api/v1/index/status')).json();
    const available=status.definitions.filter(d=>!d.locked&&d.id!=='satline'&&!/^bitmap-chain(?:-|$)/.test(d.id)).flatMap(d=>d.id==='txo-spender'?['txio-links','txio-values','txio-scripts']:d.id==='sat-state'?['txio-sat-ranges']:[d.id]);
    assert.deepEqual((await indexes.locator('.timeline-track').evaluateAll(nodes=>nodes.map(n=>n.dataset.index))).sort(),available.sort(),'All available built-ins appear on a fresh actual app');
    for(const chain of status.definitions.filter(d=>!d.locked&&/^bitmap-chain(?:-|$)/.test(d.id))){const parent=chain.district_instance||'bitmap-districts';assert.equal(await indexes.locator('.timeline-track[data-index="'+parent+'"] [data-control=chain]').count(),1,'Each available Chain has its sole Arm control on its District');assert.equal(await indexes.locator('.timeline-track[data-index="'+chain.id+'"]').count(),0,'Chain does not duplicate a named row');}assert.equal(await indexes.locator('#timeline-add-index').isDisabled(),false);assert.equal(await indexes.locator('#timeline-expand').count(),0);
    for(const id of ['txio','addresses','bitmap'])if(await indexes.locator('#timeline-group-'+id).count())if(await indexes.locator('#timeline-group-'+id).getAttribute('aria-expanded')==='false')await indexes.locator('#timeline-group-'+id).click();
    await indexes.locator('.timeline-track[data-index=address-state] .timeline-track-select').click();await indexes.getByRole('heading',{name:'Requirements',exact:true}).first().waitFor();
    assert.match(await indexes.locator('#index-requirements').innerText(),/UTXO/);assert.equal(await indexes.locator('#build-range-picker').isVisible(),false);
    const arm=indexes.locator('.timeline-track[data-index=blocks] [data-control=arm]'),live=indexes.locator('.timeline-track[data-index=blocks] [data-control=live]');
    const stage=page.waitForResponse(r=>r.url()===endpoint+'/api/v1/index/run/prepare'&&r.request().method()==='POST');await arm.click();await stage;
    assert.equal(fs.existsSync(path.join(profile,'indexes','job.json')),false,'Arming creates no index job');
    let prepared=await(await fetch(endpoint+'/api/v1/index/run')).json();assert.equal(prepared.prepared.consumers.find(c=>c.index==='blocks').armed,true);
    const liveSaved=page.waitForResponse(r=>r.url()===endpoint+'/api/v1/index/run/prepare');await live.click();await liveSaved;
    prepared=await(await fetch(endpoint+'/api/v1/index/run')).json();assert.equal(prepared.prepared.consumers.find(c=>c.index==='blocks').live,true);assert.equal(prepared.run,undefined,'Live is staged, not dispatched');
    await page.reload({waitUntil:'networkidle'});await indexes.locator('.timeline-track[data-index=blocks] [data-control=arm]').waitFor();assert.equal((await arm.getAttribute('aria-pressed')==='true'),true);assert.equal((await live.getAttribute('aria-pressed')==='true'),true);
    const planReply=page.waitForResponse(r=>r.url()===endpoint+'/api/v1/index/run/plan');await indexes.locator('#timeline-run-start').click();const planned=await planReply;if(planned.ok())await indexes.locator('#timeline-run-review').waitFor({state:'visible'});assert.equal(fs.existsSync(path.join(profile,'indexes','job.json')),false,'Review cannot start a hidden scan');assert.equal((await(await fetch(endpoint+'/api/v1/index/run')).json()).run,undefined,'Plan is read only even if genesis authority is present');
    const stopped=page.waitForResponse(r=>r.url()===endpoint+'/api/v1/index/run/prepare');await arm.click();await stopped;
    await page.screenshot({path:path.join(out,'timeline-desktop.png'),fullPage:true});
    const frameNode=await page.locator('#indexes-module').elementHandle();await indexes.locator('#index-timeline').evaluate(n=>n.dataset.nativeIdentity='kept');
    await page.locator('#settings-button').click();await page.locator('#theme-select').waitFor();
    await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();await page.locator('#update-config').waitFor();
    assert.equal(await page.locator('#update-check').isDisabled(),true);assert.equal(await page.locator('#update-publisher').inputValue(),expectedChannel.publisher_url);assert.equal(await page.locator('#update-key').inputValue(),expectedChannel.trusted_key.public_key);assert.equal(await page.locator('#update-mode').inputValue(),'notify');
    await page.locator('.nav-item[data-route="indexes.gateway"]').click();await indexes.locator('#timeline-run-start').waitFor();assert.equal(await indexes.locator('#index-timeline').getAttribute('data-native-identity'),'kept');assert.equal(await page.locator('#indexes-module').evaluate((n,old)=>n===old,frameNode),true,'Settings return preserves actual frame document');
    for(const width of [1280,390]){await page.setViewportSize({width,height:width===390?844:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.documentElement.scrollHeight<=innerHeight+1),true);await page.screenshot({path:path.join(out,'workspace-'+width+'.png'),fullPage:true});}
    await page.locator('.nav-item[data-route="peers.gateway"]').click();await page.locator('#peer-data .panel').waitFor();await page.locator('.nav-item[data-route="indexes.gateway"]').click();await indexes.locator('#timeline-run-start').waitFor();assert.equal(await indexes.locator('#index-timeline').getAttribute('data-native-identity'),'kept','Peers preserves timeline state');
    await page.locator('#settings-button').click();await page.locator('#theme-select').selectOption('dark');await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
    await page.reload({waitUntil:'networkidle'});await page.locator('#theme-select').waitFor();assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
    await page.evaluate(()=>localStorage.clear());await page.reload({waitUntil:'networkidle'});await page.locator('#theme-select').waitFor();assert.equal(await page.locator('html').getAttribute('data-theme'),'dark','Theme is preserved in actual profile, independently of browser storage');
    const setupTitles=['Your Bitcoin connection','Browser access','Network & local data'];
    const setupCalls=[];page.on('request',request=>{if(request.method()==='POST')setupCalls.push(new URL(request.url()).pathname)});
    for(const theme of ['light','dark']){
      await page.locator('#settings-button').click();await page.locator('#theme-select').selectOption(theme);await page.waitForFunction(theme=>document.documentElement.dataset.theme===theme,theme);
      const satline=await browser.newPage();satline.on('pageerror',error=>errors.push(error.message));satline.on('request',request=>{if(!request.url().startsWith(endpoint+'/'))external.push(request.url())});await satline.goto(endpoint+'/satline?embedded=1',{waitUntil:'networkidle'});await satline.getByRole('heading',{name:/Satline/}).waitFor();for(const width of [1280,390]){await satline.setViewportSize({width,height:width===390?844:900});assert.equal(await satline.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Embedded Satline theme overflow');await satline.screenshot({path:path.join(out,'satline-'+theme+'-'+width+'.png'),fullPage:true})}await satline.close();
      await page.evaluate(async()=>{const response=await fetch('/api/v1/setup',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'save',stage:0})});if(!response.ok)throw Error('Fixture setup stage reset failed')});
      await page.locator('#content [data-gateway-setup]').first().click();await page.locator('#gateway-setup[open]').waitFor();
      for(let stage=0;stage<setupTitles.length;stage++){
        await page.locator('#setup-title').getByText(setupTitles[stage],{exact:true}).waitFor();assert.equal(await page.locator('#setup-step').innerText(),'STEP '+(stage+1)+' OF 3');
        if(stage===0){assert.equal(await page.locator('[name="connection-mode"]').count(),2);assert.equal(await page.locator('#setup-core-options').isVisible(),false);assert.equal(await page.locator('#setup-explore').count(),0)}
        if(stage===1){assert.equal(await page.locator('#setup-open-browser').innerText(),'Copy extensions address & open browser');assert.equal(await page.locator('#setup-open-folder').innerText(),'Open companion folder');await page.locator('#setup-browser').selectOption('edge');await page.locator('#setup-profile').fill('Profile 2');await page.locator('#setup-body').getByText(/Checks never change registrations/).waitFor();assert.equal(await page.locator('#setup-routing').innerText(),'Enable Gateway browser routing');assert.equal(await page.locator('#setup-register').count(),0)}
        if(stage===2){assert.equal(await page.locator('#setup-network').isChecked(),false);assert.equal(await page.locator('#setup-serve').isChecked(),false)}
        for(const width of [1280,390]){await page.setViewportSize({width,height:width===390?844:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Setup page overflow');assert.equal(await page.locator('#gateway-setup').evaluate(dialog=>dialog.scrollWidth<=dialog.clientWidth+2),true,'Setup dialog overflow');await page.screenshot({path:path.join(out,'setup-'+(stage+1)+'-'+theme+'-'+width+'.png'),fullPage:true})}
        await page.locator('#setup-next').click();
      }
      await page.waitForFunction(()=>!document.getElementById('gateway-setup').open);const setup=await(await fetch(endpoint+'/api/v1/setup')).json();assert.equal(setup.progress.completed,true);assert.equal(setup.progress.profile,'Profile 2');assert.equal(setup.progress.browser,'edge');assert.equal(setup.browser.native_active,false,'Completing setup must not mark an unobserved native exchange ready');assert.equal(setup.settings.network_disabled,true);assert.equal(setup.settings.serve_data,false);
    }
    assert.equal(setupCalls.some(path=>path.startsWith('/api/v1/system/')||path==='/api/v1/browser/open-setup'),false,'Rendering/completing setup did not silently register integrations or launch another browser');
    assert.deepEqual(errors, [], 'Uncaught browser errors');
    assert.deepEqual(external, [], 'Fresh offline workspace requested an external origin');
    const finalUpdate = await (await fetch(endpoint+'/api/v1/updates/status')).json();assert.equal(finalUpdate.state,'disabled_offline');assert.equal(finalUpdate.last_checked,undefined,'UI traversal did not start a hosted update check');
    const result = {result:'PASS', scope:'Native offline runtime; actual Chromium DOM/CSP, default available tracks, no duplicate controls, staged Arm/Live and reload persistence, read-only shared review without a hidden job, persistent frame through Settings/Peers, desktop/narrow layout, reviewed updater trust/offline behavior, profile theme persistence and setup stages; no external-origin requests', version:ping.version, binary:path.resolve(options.binary), binary_sha256:createHash('sha256').update(fs.readFileSync(path.resolve(options.binary))).digest('hex'), update_channel:expectedChannel.id, update_publisher:expectedChannel.publisher_url, browser:executablePath, browser_version:browser.version(), platform:process.platform, page_errors:errors, external_requests:external};
    fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify(result, null, 2));
    console.log('Index timeline runtime browser: PASS (' + result.scope + ').');
  } catch (error) {
    fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({result:'NOT_PASSED', error:String(error.stack || error), page_errors:errors, external_requests:external}, null, 2));
    throw error;
  } finally {
    if (browser) await browser.close();
    if (runtime.exitCode === null) { runtime.kill(); await once(runtime, 'exit'); }
    fs.closeSync(log);
    // Delete only the fixture directory created above, never a redirected path.
    assert.equal(path.dirname(path.resolve(profile)), path.resolve(os.tmpdir()));
    assert(path.basename(profile).startsWith('gateway-cards-'));
    assert.equal(fs.lstatSync(profile).isSymbolicLink(), false);
    fs.rmSync(profile, {recursive:true, force:true});
  }
}
run().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
