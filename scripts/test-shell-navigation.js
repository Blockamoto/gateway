'use strict';

// Real browser DOM regression for the shell router. All HTTP traffic is fulfilled
// in memory: this never opens or changes a running Gateway node. All referenced
// shell scripts are real; embedded module pages and APIs are test doubles.
// Run: node scripts/test-shell-navigation.js
// Reproduce a checked-in regression: node scripts/test-shell-navigation.js --revision HEAD
// Requires Playwright and an installed Chromium browser (bundled Chromium, Edge or
// Chrome), or GATEWAY_TEST_BROWSER pointing to a browser executable. No downloads.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const {execFileSync} = require('node:child_process');
const root = path.resolve(__dirname, '..');
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--revision')) {
  throw Error('Usage: node scripts/test-shell-navigation.js [--revision <git-ref>]');
}
const revision = args[1];
const read = name => revision
  ? execFileSync('git', ['show', `${revision}:${name}`], {cwd:root, encoding:'utf8', maxBuffer:2 << 20})
  : fs.readFileSync(path.join(root, name), 'utf8');
// Read once so concurrent source edits cannot mix versions within a test run.
const html = read('ui/shell/index.html');
const scripts = new Map([...html.matchAll(/<script\b[^>]*\bsrc="(\/shell\/[^"?]+\.js)"/g)]
  .map(match => [match[1], read('ui' + match[1])]));
assert.ok(scripts.has('/shell/app.js'), 'real shell app.js must be referenced by the HTML');

function playwright() {
  try { return require('playwright'); } catch (error) {
    const bundled = path.join(os.homedir(), '.cache', 'codex-runtimes', 'codex-primary-runtime', 'dependencies', 'node', 'node_modules', 'playwright');
    if (fs.existsSync(bundled)) return require(bundled);
    throw Error('Playwright is required. Make an existing installation available through NODE_PATH. ' + error.message);
  }
}

function browserPath(chromium) {
  const candidates = [process.env.GATEWAY_TEST_BROWSER, chromium.executablePath()];
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

let origin; // A fresh loopback fixture server owns this origin for each run.
const bitcoinAddress='1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa';
const addressCheckpoint={height:170,block_hash:'b'.repeat(64),commitment:'address-head'};
const fixtures = {
  '/api/v1/settings': {settings:{bitcoin_peers:[], satline_enabled:false, ord_enabled:true}, core:{connected:false}, core_store:{}},
  '/api/v1/browser/status': {browser:{native_active:false}},
  '/api/v1/system/status': {},
  '/api/v1/updates/status': {state:'not_configured',current_version:'test',configured:false,auto_check:false},
  '/api/v1/peers': {connections:[], network:{}, core_peers:[]},
  '/api/v1/migration': {job:{}, archive_root:'local fixture archive'},
  '/api/v1/graph/status': {},
  '/api/v1/jobs': {jobs:[]},
  '/api/v1/index/status': {job:{}, instances:[], live:[]},
  '/api/v1/network': {enabled:true, connected:0, gateway_connected:0, headers:{header_height:0, header_state:'waiting'}},
  '/api/v1/setup': {show_automatically:false, settings:{}, progress:{stage:0}},
  '/api/v1/data-coverage': {headers:{header_height:0}, cache:{}, transaction_locations:{}, spender_knowledge:{}, mounted:{}, mounted_preparation:{}, core:{connected:false}, serving:{}},
};

async function harness(browser, address = '.gateway', failStatus = false, addressLookupState='not_built', satlineAvailable=false, satState='ready') {
  const context = await browser.newContext({serviceWorkers:'block'});
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  const errors = [], unexpected = [], requests = [];let appearance={theme:'',default:'light'};
  page.on('pageerror', error => errors.push(error.message));
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url());
    requests.push({method:request.method(), path:url.pathname,...(request.method()==='POST'?{body:request.postDataJSON()}: {})});
    const fulfill = (body, contentType, status = 200) => route.fulfill({status, contentType, body});
    if (url.origin !== origin) {
      unexpected.push(request.url());
      return route.abort();
    }
    if (url.pathname === '/') return fulfill(html.replace('__GATEWAY_BOOTSTRAP__', JSON.stringify({version:'test', address, activation:'',release_availability:{'sat-state':true,satline:satlineAvailable}})), 'text/html');
    if (scripts.has(url.pathname)) return fulfill(scripts.get(url.pathname), 'application/javascript');
    if (url.pathname === '/shell/style.css') return fulfill(read('ui/shell/style.css'), 'text/css');
    if (url.pathname === '/shell/theme.css') return fulfill(read('ui/shell/theme.css'), 'text/css');
    if (url.pathname === '/shell/sparse.css') return fulfill(read('ui/shell/sparse.css'), 'text/css');
    if (url.pathname.endsWith('.css')) return fulfill('', 'text/css');
    if (url.pathname === '/shell/gateway.ico') return fulfill('', 'image/x-icon');
    if (url.pathname === '/shell/gateway-mark.svg') return fulfill('<svg xmlns="http://www.w3.org/2000/svg"/>', 'image/svg+xml');
    if (['/satline', '/indexes'].includes(url.pathname)) return fulfill('<!doctype html><title>Module fixture</title><h1>Embedded module fixture</h1><script>window.addEventListener("message",e=>{if(e.source===parent&&e.origin===location.origin&&e.data.type==="gateway:workspace-select-index"){document.body.dataset.index=e.data.index;document.body.dataset.rebuildSnapshot=JSON.stringify(e.data.rebuild_snapshot||null);}});</script>', 'text/html');
    if(url.pathname==='/api/v1/appearance'){if(request.method()==='POST')appearance={...appearance,...request.postDataJSON()};return fulfill(JSON.stringify(appearance),'application/json')}
    if(url.pathname==='/api/v1/navigate'&&request.method()==='POST'){
      const address=request.postDataJSON().address,txid='a'.repeat(64),block='b'.repeat(64);
      if(address===bitcoinAddress)return fulfill(JSON.stringify({resource:{kind:'address',query:address,address:{address},address_index:{address,state:'ready'}}}),'application/json');
      if(address===txid||address===txid+'.840000.bitcoin'){const located=address===txid;return fulfill(JSON.stringify({resource:{query:address,transaction:{txid,height:840000,block_hash:block,tx_index:7,locator_verified:true,transaction_verified:!located,source_network:located?'local_index_locator':'local_index',locator_peer:'committed transaction locator',verification_state:located?'header_anchored_locator':'header_anchored',resolution_state:located?'located_bytes_unavailable':'',note:'<img src="https://untrusted.invalid/pixel"> A provider must supply the containing block.',transaction:located?{index:0,inputs:null,outputs:null}:{txid,index:7,inputs:[],outputs:[{n:0,value_sats:123,type:'script'}]}}}}),'application/json')}
    }
    if(url.pathname==='/api/v1/address/view'){
      assert.equal(request.method(),'GET');assert.equal(url.searchParams.get('address'),bitcoinAddress);assert.equal(url.searchParams.get('mode'),'current');
      return fulfill(JSON.stringify({address:bitcoinAddress,mode:'current',state:addressLookupState,snapshot:addressLookupState==='rebuild_required'?addressCheckpoint:{},current_balance_sats:null,current_output_count:null,outputs:[],complete:false,note:'Current UTXOs require inspection or a reviewed build.'}),'application/json');
    }
    if(url.pathname==='/api/v1/sats/view')return fulfill(JSON.stringify({state:satState,sat_number:Number(url.searchParams.get('sat')),origin:{height:144,first_sat:50,subsidy_offset:1},properties:{rarity:'common'},location:{state:'unspent',output_coordinate:'0.1.144',offset:1,address:bitcoinAddress},snapshot:addressCheckpoint,note:'Saved Sat location.'}),'application/json');
    if(url.pathname==='/api/v1/settings'&&request.method()==='POST'){assert.equal(Object.hasOwn(request.postDataJSON(),'satline_enabled'),false,'Settings no longer sends the legacy Satline disable flag');return fulfill(JSON.stringify({ok:true}),'application/json');}
    if (request.method() === 'GET' && Object.hasOwn(fixtures, url.pathname)) {
      if (failStatus && url.pathname === '/api/v1/settings') return fulfill(JSON.stringify({error:'Fixture services unavailable'}), 'application/json', 503);
      return fulfill(JSON.stringify(fixtures[url.pathname]), 'application/json');
    }
    unexpected.push(`${request.method()} ${url.pathname}`);
    return fulfill(JSON.stringify({error:'Unexpected test request'}), 'application/json', 500);
  });
  await page.goto(origin, {waitUntil:'load'});
  await page.waitForFunction(() => document.querySelector('#content')?.children.length || !document.querySelector('#notice')?.hidden);
  return {context, page, errors, unexpected, requests};
}

async function healthy(page, label) {
  const state = await page.evaluate(() => ({
    children:document.querySelector('#content').children.length,
    text:document.querySelector('#content').textContent.trim(),
    frames:document.querySelector('#content').querySelectorAll('iframe').length,
    notice:document.querySelector('#notice').hidden ? '' : document.querySelector('#notice').textContent,
  }));
  assert.ok(state.children > 0 && (state.text.length > 15 || state.frames > 0), `${label}: content is blank; notice=${JSON.stringify(state.notice)}`);
  assert.equal(state.notice, '', `${label}: unexpected shell error`);
}

async function checkRoute(page, address, selector) {
  await page.locator(selector).first().waitFor();
  await healthy(page, address);
  assert.equal(await page.locator('#address').inputValue(), address);
  const selected = page.locator('.nav-item.active');
  const positionalInput=/^(?:i[0-9]+\.[0-9]+\.[0-9]+|[0-9]+(?:\.[0-9]+)*)(?:\.bitcoin)?$/.test(address),primary = ['.gateway','indexes.gateway','peers.gateway','settings.gateway'].includes(address)||positionalInput;
  assert.equal(await selected.count(), primary ? 1 : 0, `${address}: primary workspace selection`);
  if (primary) {
    assert.equal(await selected.getAttribute('data-route'), positionalInput?'indexes.gateway':address);
    assert.equal(await selected.getAttribute('aria-current'), 'page');
  }
  if (!['satline.gateway', 'indexes.gateway'].includes(address)&&!positionalInput) {
    assert.equal(await page.locator('#content').evaluate(node => node.classList.contains('embed-host')), false, `${address}: stale embedded layout`);
  }
}

async function run() {
  const {chromium} = playwright();
  const server = http.createServer((req, res) => { res.writeHead(500); res.end('Unmocked fixture request'); });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  origin = 'http://127.0.0.1:' + server.address().port;
  let browser;
  try { browser = await chromium.launch({headless:true, executablePath:browserPath(chromium)}); }
  catch (error) { server.close(); throw error; }
  try {
    const h = await harness(browser);
    try {
      await healthy(h.page, 'initial Home');
      await h.page.locator('#home-state').getByText('Nothing is being maintained yet.', {exact:true}).waitFor();
      await checkRoute(h.page, '.gateway', '#home-state');
      assert.deepEqual(await h.page.locator('.topbar .top-nav').evaluateAll(nodes => nodes.map(n => n.dataset.route)), ['indexes.gateway','peers.gateway'], 'only Indexes and Peers are primary navigation');
      assert.equal(await h.page.locator('.brand[data-route=".gateway"]').count(), 1, 'Home stays reachable through the Gateway mark');
      for (const [address, selector] of [['indexes.gateway','iframe.indexes-frame'],['peers.gateway','#peer-data .panel']]) {
        const nav = h.page.locator(`.nav-item[data-route="${address}"]`);
        await nav.locator('span').first().click();
        await checkRoute(h.page, address, selector);
        assert.equal(new URL(h.page.url()).searchParams.get('resolve'), address);
      }
      assert.equal(await h.page.locator('[data-route="discovery.gateway"]').count(),0,'Removed discovery has no navigation route');
      await h.page.locator('#settings-button').click();await checkRoute(h.page,'settings.gateway','#settings-form');
      assert.equal((await h.page.locator('#network-journey').textContent()).trim(), 'Bitcoin peers supply headers and requested blocks. Inspect individual peers and capabilities in Peers.');
      assert.equal((await h.page.locator('#network-technical').textContent()).includes('bitcoin_gateway_discovery'),false,'ordinary technical state excludes the experiment');
      await h.page.locator('#address').fill('satline.gateway');
      await h.page.locator('#address-form').evaluate(form=>form.requestSubmit());
      await checkRoute(h.page,'satline.gateway','#content h1');
      await h.page.getByRole('heading',{name:/Satline/}).waitFor();
      assert.equal(await h.page.evaluate(()=>sessionStorage.getItem('gateway.dev_mode')),null,'Locked Satline explanation needs no development mode');
      assert.equal(h.requests.filter(request=>request.path.startsWith('/api/v1/satline')&&request.method!=='GET').length,0,'Opening Satline does not start a trace');
      const routes = [
        ['bitcoin.gateway', '#content h1'], ['ord.gateway', '#content h1'],
        ['sync.gateway', '#content h1'],
        ['storage.gateway', '#storage-body .panel'], ['activity.gateway', '#index-job-summary'],
        ['settings.gateway', '#settings-form'], ['.gateway', '#home-state'],
      ];
      for (const [address, selector] of routes) {
        await h.page.locator('#address').fill(address);
        await h.page.locator('#address-form').evaluate(form => form.requestSubmit());
        await checkRoute(h.page, address, selector);
        assert.equal(new URL(h.page.url()).searchParams.get('resolve'), address);
      }
      await h.page.locator('#settings-button').click();
      await checkRoute(h.page, 'settings.gateway', '#settings-form');
      await h.page.locator('.brand span').last().click();
      await checkRoute(h.page, '.gateway', '#home-state');
      // Address-bar entry and browser back/forward are separate router entry points.
      await h.page.locator('#address').fill('indexes.gateway');
      await h.page.locator('#address-form').evaluate(form => form.requestSubmit());
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      await h.page.goBack();
      await checkRoute(h.page, '.gateway', '#home-state');
      await h.page.goForward();
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      // A plain external/legacy nav anchor must not crash active-state traversal.
      await h.page.evaluate(() => {
        const anchor = document.createElement('a');
        anchor.className = 'nav-item'; anchor.href = '/legacy'; anchor.textContent = 'Legacy fixture';
        document.querySelector('nav').append(anchor);
      });
      await h.page.locator('.brand span').last().click();
      await checkRoute(h.page, '.gateway', '#home-state');
      // An untrusted origin must not drive the management shell through postMessage.
      await h.page.evaluate(() => window.dispatchEvent(new MessageEvent('message', {origin:'https://untrusted.invalid', data:{type:'gateway:navigate', address:'indexes.gateway'}})));
      assert.equal(await h.page.locator('#address').inputValue(), '.gateway');
      assert.deepEqual(h.errors, [], 'uncaught browser exceptions');
      assert.deepEqual(h.unexpected, [], 'unexpected network/API requests');
      assert.ok(h.requests.every(request => request.method === 'GET'), 'navigation unexpectedly mutated state');
    } finally { await h.context.close(); }

    const deep = await harness(browser, 'indexes.gateway');
    try {
      await checkRoute(deep.page, 'indexes.gateway', 'iframe.indexes-frame');
      assert.deepEqual(deep.errors, []);
      assert.deepEqual(deep.unexpected, []);
    } finally { await deep.context.close(); }

    const located=await harness(browser,'a'.repeat(64));
    try{
      await located.page.locator('iframe.indexes-frame').waitFor();await healthy(located.page,'Transaction address in the shared workspace');
      assert.equal(await located.page.locator('#address').inputValue(),'a'.repeat(64));assert.equal(located.requests.some(request=>request.path==='/api/v1/navigate'),false,'Shell forwards resource entry to the workspace instead of resolving twice');assert.equal(await located.page.locator('#transaction-location').count(),0,'Shell does not duplicate transaction Details outside the workspace');assert.deepEqual(located.errors,[]);assert.deepEqual(located.unexpected,[]);
    }finally{await located.context.close()}
    if(scripts.has('/shell/address-view.js')){
      const addressView=await harness(browser,bitcoinAddress);
      try{
        await addressView.page.locator('#address-balance').waitFor();await checkRoute(addressView.page,bitcoinAddress,'#address-balance');assert.equal(await addressView.page.locator('#address-balance').innerText(),'Unavailable');assert.equal(await addressView.page.locator('#content').getByText('[object Object]').count(),0,'Legacy typed address envelope is not passed as the address string');
        await addressView.page.getByRole('button',{name:'Build Current UTXOs',exact:true}).click();await checkRoute(addressView.page,'indexes.gateway','iframe.indexes-frame');const frame=addressView.page.frameLocator('#indexes-module');await frame.locator('body[data-index="address-state"]').waitFor();
        await addressView.page.goBack();await addressView.page.locator('#address-balance').waitFor();await checkRoute(addressView.page,bitcoinAddress,'#address-balance');const resolutions=addressView.requests.filter(r=>r.path==='/api/v1/navigate').length;await addressView.page.locator('#address').fill('i0.3.150');await addressView.page.locator('#address-form').evaluate(form=>form.requestSubmit());await checkRoute(addressView.page,'i0.3.150','iframe.indexes-frame');assert.equal(addressView.requests.filter(r=>r.path==='/api/v1/navigate').length,resolutions,'Spending-input BPS navigation uses the shared workspace');assert.equal(addressView.requests.some(r=>r.path==='/api/v1/index/build'),false,'Address navigation never starts a build');assert.deepEqual(addressView.errors,[]);assert.deepEqual(addressView.unexpected,[]);
      }finally{await addressView.context.close()}
      const damagedAddress=await harness(browser,bitcoinAddress,false,'rebuild_required');
      try{
        await damagedAddress.page.getByRole('button',{name:'Review Current UTXOs rebuild'}).click();await checkRoute(damagedAddress.page,'indexes.gateway','iframe.indexes-frame');const frame=damagedAddress.page.frameLocator('#indexes-module');await frame.locator('body[data-index="address-state"]').waitFor();assert.deepEqual(JSON.parse(await frame.locator('body').getAttribute('data-rebuild-snapshot')),addressCheckpoint,'Outer shell passes the damaged lookup checkpoint to the existing same-origin workspace');assert.equal(damagedAddress.requests.some(r=>r.path==='/api/v1/index/build'||r.path==='/api/v1/index/plan'),false,'Rebuild navigation requires a later explicit review');assert.deepEqual(damagedAddress.errors,[]);assert.deepEqual(damagedAddress.unexpected,[]);
      }finally{await damagedAddress.context.close()}
    }
    if(scripts.has('/shell/sat-view.js')){
      const sat=await harness(browser,'51.sat',false,'not_built',true);
      try{
        await sat.page.getByRole('heading',{name:'Sat 51',exact:true}).waitFor();await checkRoute(sat.page,'51.sat','.sat-evidence');await sat.page.getByRole('button',{name:'Trace Satline',exact:true}).click();await sat.page.locator('#satline-module').waitFor();assert.equal(new URL(await sat.page.locator('#satline-module').getAttribute('src'),origin).searchParams.get('input'),'51','Explicit trace action opens the existing numeric Satline journey');assert.equal(sat.requests.some(r=>r.path.startsWith('/api/v1/satline')&&r.method==='POST'),false,'Opening a Sat view does not start a trace itself');await sat.page.locator('#address').fill('51.sat');await sat.page.locator('#address-form').evaluate(form=>form.requestSubmit());await sat.page.getByRole('heading',{name:'Sat 51',exact:true}).waitFor();assert.equal(sat.requests.filter(r=>r.path==='/api/v1/navigate').length,0,'Internal Sat entry uses the read-only Sat view route');await sat.page.getByRole('button',{name:bitcoinAddress,exact:true}).click();await sat.page.locator('#address-balance').waitFor();await checkRoute(sat.page,bitcoinAddress,'#address-balance');await sat.page.goBack();await sat.page.getByRole('heading',{name:'Sat 51',exact:true}).waitFor();await sat.page.locator('#sat-number').fill('0');await sat.page.getByRole('button',{name:'Open sat',exact:true}).click();await sat.page.getByRole('heading',{name:'Sat 0',exact:true}).waitFor();assert.equal(new URL(sat.page.url()).searchParams.get('resolve'),'0.sat');await sat.page.getByRole('button',{name:'0.1.144',exact:true}).click();await checkRoute(sat.page,'0.1.144','iframe.indexes-frame');assert.equal(sat.requests.some(r=>r.path==='/api/v1/index/build'),false,'Sat/address/output navigation does not start indexing');assert.deepEqual(sat.errors,[]);assert.deepEqual(sat.unexpected,[]);
      }finally{await sat.context.close()}
    }
    if(scripts.has('/shell/sat-view.js')){const unindexed=await harness(browser,'5.sat',false,'not_built',true,'not_built');try{await unindexed.page.getByRole('button',{name:'Trace Satline',exact:true}).click();await unindexed.page.locator('#satline-module').waitFor();assert.equal(new URL(await unindexed.page.locator('#satline-module').getAttribute('src'),origin).searchParams.get('input'),'5','Trace is available with a legacy false preference and no global Sat index');await unindexed.page.locator('#settings-button').click();await unindexed.page.locator('#settings-form').waitFor();assert.equal(await unindexed.page.locator('#satline-enabled').count(),0,'Settings offers no Satline disable switch');await unindexed.page.getByRole('button',{name:'Save settings',exact:true}).click();await unindexed.page.waitForFunction(()=>document.querySelector('#toast').textContent==='Settings saved.');assert.equal(Object.hasOwn(unindexed.requests.filter(r=>r.path==='/api/v1/settings'&&r.method==='POST').at(-1).body,'satline_enabled'),false);assert.equal(unindexed.requests.some(r=>r.path==='/api/v1/index/build'),false);assert.deepEqual(unindexed.errors,[]);assert.deepEqual(unindexed.unexpected,[]);}finally{await unindexed.context.close()}}
    const degraded = await harness(browser, '.gateway', true);
    try {
      await degraded.page.locator('#home-state').getByText('Status unavailable', {exact:true}).waitFor();
      await healthy(degraded.page, 'Home with unavailable services');
      await degraded.page.locator('#address').fill('bitcoin.gateway');
      await degraded.page.locator('#address-form').evaluate(form => form.requestSubmit());
      await checkRoute(degraded.page, 'bitcoin.gateway', '#content h1');
      assert.deepEqual(degraded.errors, []);
      assert.deepEqual(degraded.unexpected, []);
    } finally { await degraded.context.close(); }
    console.log(`Shell navigation: PASS (${revision || 'working tree'}; real DOM/all ${scripts.size} shell scripts, initial/degraded Home, two primary workspaces, resolver-only legacy views, Indexes deep link, nested clicks, address entry, history, plain anchor, origin guard; mocked embedded pages/API boundaries).`);
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
}

run().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
