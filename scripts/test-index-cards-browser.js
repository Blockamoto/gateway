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
const expectedVersion = fs.readFileSync(path.join(__dirname, '../VERSION.txt'), 'utf8').trim();
const bundledChannels = JSON.parse(fs.readFileSync(path.join(__dirname, '../assets/bootstrap/update-channels.json'), 'utf8')).channels;
const expectedChannel = bundledChannels.find(channel=>channel.id==='stable') || bundledChannels[0];
assert.ok(expectedChannel, 'This release acceptance requires a reviewed bundled update channel');

const options = {};
for (let i = 2; i < process.argv.length; i++) {
  const key = process.argv[i];
  const value = process.argv[++i];
  if (!['--binary', '--out', '--browser'].includes(key) || !value) throw Error('Usage: node scripts/test-index-cards-browser.js --binary PATH --out DIR [--browser PATH]');
  options[key.slice(2)] = value;
}
if (!options.binary || !options.out) throw Error('--binary and --out are required');
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
    await page.goto(endpoint + '/indexes', {waitUntil:'networkidle'});
    await page.locator('.timeline-track[data-index="headers"]').waitFor();
    assert.equal(await page.locator('.timeline-track').count(),2,'A fresh profile starts with foundational Headers and Blocks tracks');
    assert.equal(await page.locator('select#index, select#query-index, select#peer-index').count(), 0);
    await page.screenshot({path:path.join(out, 'timeline-desktop.png'), fullPage:true});

    await page.locator('#timeline-add-index').click();
    assert.equal(await page.locator('#timeline-index-picker [data-index="inscriptions"]').isDisabled(),false,'The inscription stage permits adding its track');
    assert.equal(await page.locator('#timeline-index-picker [data-index="tx-locator"]').isDisabled(),true,'The later transaction stage remains locked');
    assert.equal(await page.locator('.timeline-track[data-index="inscriptions"]').count(),0,'An available index does not occupy a track until added');
    await page.screenshot({path:path.join(out,'locked-features.png'),fullPage:true});
    await page.locator('#timeline-add-index').click();
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await page.locator('.timeline-track[data-index="blocks"]').waitFor();
    assert.equal(await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').getAttribute('aria-pressed'), 'true', 'Choosing default Blocks opens its track inspector');
    assert.equal(fs.existsSync(path.join(profile,'indexes','job.json')),false,'Choosing a track never starts indexing');
    await page.locator('#timeline-details').evaluate(el=>el.open=false);
    const control = page.getByRole('switch', {name:'blocks On', exact:true});
    const live = page.getByRole('switch', {name:'blocks Live', exact:true});
    await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.evaluate(()=>document.documentElement.dataset.theme),'light');
    for(const id of ['blocks']){
      const on=page.getByRole('switch',{name:id+' On',exact:true}),follow=page.getByRole('switch',{name:id+' Live',exact:true});
      assert.equal(await follow.isDisabled(),false);await on.click();await page.waitForFunction(id=>document.querySelector('[aria-label="'+id+' On"]').checked,id);
      assert.equal(fs.existsSync(path.join(profile,'indexes','job.json')),false,'Fresh On does not scan '+id);await on.click();await page.waitForFunction(id=>!document.querySelector('[aria-label="'+id+' On"]').checked,id);
    }
    assert.equal(await live.isDisabled(),false,'Live is selectable before an initial checkpoint');
    await page.locator('#timeline-inspector').getByText(/all applicable history from block 0/).waitFor();
    await control.focus();
    await page.keyboard.press('Space');
    await page.waitForFunction(()=>document.querySelector('[aria-label="blocks On"]').checked);
    assert.equal(await page.locator('#timeline-details').evaluate(el=>el.open),false,'On persists permission without opening advanced settings');
    assert.equal(fs.existsSync(path.join(profile,'indexes','live.json')),true,'Fresh On must persist before any index exists');
    assert.equal(fs.existsSync(path.join(profile, 'indexes', 'job.json')), false, 'On alone began unbounded work');
    await control.focus();await page.keyboard.press('Space');await page.waitForFunction(()=>!document.querySelector('[aria-label="blocks On"]').checked);
    await live.focus();await page.keyboard.press('Space');await page.waitForFunction(()=>document.querySelector('[aria-label="blocks Live"]').checked&&document.querySelector('[aria-label="blocks On"]').checked);
    assert.equal((await (await fetch(endpoint+'/api/v1/index/status')).json()).live.find(x=>x.index==='blocks').enabled,true,'Live persisted through the real backend');
    await page.reload({waitUntil:'networkidle'});await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();await page.waitForFunction(()=>document.querySelector('[aria-label="blocks Live"]')?.checked&&document.querySelector('[aria-label="blocks On"]')?.checked);
    await control.focus();await page.keyboard.press('Space');await page.waitForFunction(()=>!document.querySelector('[aria-label="blocks On"]').checked&&document.querySelector('[aria-label="blocks Live"]').checked);
    await live.focus();await page.keyboard.press('Space');await page.waitForFunction(()=>!document.querySelector('[aria-label="blocks Live"]').checked);
    await page.locator('#timeline-details').evaluate(el=>el.open=true);
    assert.equal(await page.locator('#plan-form').isVisible(), true);assert.equal(await page.locator('#index').inputValue(),'blocks','Expanded settings belong to the selected Blocks track');
    assert.equal(await page.locator('#selected-index-label').innerText(), 'Bitcoin blocks');
    assert.equal(await page.locator('#build').isDisabled(), true);
    assert.equal(fs.existsSync(path.join(profile, 'indexes', 'job.json')), false, 'Offline Live attempted a network scan');
    await page.setViewportSize({width:390, height:844});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Narrow Indexes overflow');
    await page.screenshot({path:path.join(out, 'timeline-narrow.png'), fullPage:true});
    // Exercise the real shell, embedded workspace, address entry and history.
    await page.goto(endpoint + '/', {waitUntil:'networkidle'});
    assert.equal(await page.locator('.sidebar').count(),0);
    assert.deepEqual(await page.locator('.topbar .nav-item').evaluateAll(nodes=>nodes.map(x=>x.dataset.route)),['.gateway','indexes.gateway','peers.gateway','settings.gateway']);
    assert.equal(await page.locator('#footer-status').innerText(), 'Gateway ' + expectedVersion + ' · early access');
    await page.locator('#home-state').getByText('Nothing is being maintained yet.', {exact:true}).waitFor();
    await page.setViewportSize({width:1280,height:900});await page.screenshot({path:path.join(out,'home-desktop.png'),fullPage:true});
    await page.locator('#settings-button').click();await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();
    await page.locator('#update-config').waitFor();await page.locator('#update-title').getByText('Updates paused while offline',{exact:true}).waitFor();assert.equal(await page.locator('#update-check').isDisabled(),true,'Configured updater still respects offline networking');
    assert.equal(await page.locator('#update-key-confirm').isChecked(),false);
    assert.equal(await page.locator('#update-publisher').inputValue(),expectedChannel.publisher_url);assert.equal(await page.locator('#update-key').inputValue(),expectedChannel.trusted_key.public_key);
    assert.equal(await page.locator('#update-channel').inputValue(),expectedChannel.id);assert.equal(await page.locator('#update-mode').inputValue(),'notify');
    assert.equal(await page.locator('#update-trust').innerText(),'Trusted public key configured');
    await page.screenshot({path:path.join(out,'updates-settings.png'),fullPage:true});
    await page.locator('.brand').click();await page.locator('#home-state').waitFor();
    await page.locator('.nav-item[data-route="indexes.gateway"]').click();
    assert.equal(await page.locator('.topbar [data-route="indexes.gateway"]').getAttribute('aria-current'),'page');
    const indexes = page.frameLocator('#indexes-module');
    await indexes.locator('.timeline-track').nth(1).waitFor();
    assert.equal(await page.locator('#address').inputValue(), 'indexes.gateway');
    async function assertDocked() {
      const geometry = await page.locator('#indexes-module').evaluate(frame=>{
        const r=frame.getBoundingClientRect();return {top:r.top,bottom:r.bottom,height:r.height,pageHeight:document.documentElement.scrollHeight,viewport:innerHeight};
      });
      assert(geometry.top>=0&&geometry.bottom<=geometry.viewport+1&&geometry.height>100,'Embedded workspace fits below app navigation');
      assert(geometry.pageHeight<=geometry.viewport+1,'Timeline cannot require outer page scrolling');
      const editor = await indexes.locator('.timeline-editor').boundingBox();
      assert(editor.y>=geometry.top&&editor.y+editor.height<=geometry.viewport+1,'Timeline remains visible within the window');
      const before=editor.y;
      await indexes.locator('#timeline-details').evaluate(el=>el.open=!el.open);
      const after=await indexes.locator('.timeline-editor').boundingBox();
      assert(Math.abs(before-after.y)<=1,'Expanding inspector details cannot displace the timeline');
    }
    await assertDocked();
    await page.screenshot({path:path.join(out,'shell-indexes-desktop.png'),fullPage:true});
    await page.setViewportSize({width:390,height:844});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Narrow shell overflow');
    await assertDocked();
    await page.screenshot({path:path.join(out, 'shell-indexes-narrow.png'), fullPage:true});
    await page.locator('.nav-item[data-route="peers.gateway"]').click();
    await page.locator('#peer-data .panel').waitFor();
    await page.locator('.brand').click();
    await page.locator('#home-state').waitFor();
    await page.locator('#address').fill('indexes.gateway');
    await page.locator('#address-form').evaluate(form => form.requestSubmit());
    await indexes.locator('.timeline-track').nth(1).waitFor();
    await page.goBack();
    await page.locator('#home-state').waitFor();
    assert.equal(await page.locator('#address').inputValue(), '.gateway');
    await page.goForward();
    await indexes.locator('.timeline-track').nth(1).waitFor();
    assert.equal(await page.locator('#address').inputValue(), 'indexes.gateway');

    await page.goto(endpoint+'/?resolve=settings.gateway',{waitUntil:'networkidle'});
    await page.locator('#theme-select').selectOption('dark');
    await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
    await page.reload({waitUntil:'networkidle'});
    await page.evaluate(()=>window.GatewayTheme.ready);
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.theme),'dark');
    await page.screenshot({path:path.join(out,'settings-dark.png'),fullPage:true});
    await page.locator('.settings-sections').getByRole('button',{name:'Updates',exact:true}).click();await page.screenshot({path:path.join(out,'updates-settings-dark.png'),fullPage:true});
    await page.evaluate(()=>localStorage.clear());await page.reload({waitUntil:'networkidle'});await page.evaluate(()=>window.GatewayTheme.ready);assert.equal(await page.evaluate(()=>document.documentElement.dataset.theme),'dark','Profile choice survives loss of per-origin browser storage');
    await page.locator('.nav-item[data-route="indexes.gateway"]').click();await indexes.locator('.timeline-track[data-index="blocks"]').waitFor();assert.equal(await indexes.locator('html').getAttribute('data-theme'),'dark');await page.screenshot({path:path.join(out,'indexes-dark.png'),fullPage:true});
    await page.locator('.nav-item[data-route="peers.gateway"]').click();await page.locator('#peer-data .panel').waitFor();await page.screenshot({path:path.join(out,'peers-dark.png'),fullPage:true});
    await page.locator('#settings-button').click();await page.locator('#theme-select').waitFor();
    await page.locator('#theme-select').selectOption('light');
    await page.waitForFunction(()=>document.documentElement.dataset.theme==='light');
    await page.reload({waitUntil:'networkidle'});
    await page.evaluate(()=>window.GatewayTheme.ready);
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.theme),'light');
    await page.screenshot({path:path.join(out,'settings-light.png'),fullPage:true});
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
    const result = {result:'PASS', scope:'Native offline runtime; actual Chromium DOM/CSP, all available fresh persisted On controls, initial Live enabling On/reload/stop, honest offline no-scan, reviewed bundled updater trust and notification defaults persisted before UI interaction with offline checks paused, top-bar Home/Indexes/Peers/Settings, profile-saved light/dark theme and embedded theme, all three setup stages in both themes at desktop/narrow with pending browser/native readiness preserved, address entry and history; no external-origin requests', version:ping.version, binary:path.resolve(options.binary), binary_sha256:createHash('sha256').update(fs.readFileSync(path.resolve(options.binary))).digest('hex'), update_channel:expectedChannel.id, update_publisher:expectedChannel.publisher_url, browser:executablePath, browser_version:browser.version(), platform:process.platform, page_errors:errors, external_requests:external};
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
