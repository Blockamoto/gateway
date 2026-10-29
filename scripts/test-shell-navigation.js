'use strict';

// Real browser DOM regression for the shell router. All HTTP traffic is fulfilled
// in memory: this never opens or changes a running Gateway node. All referenced
// shell and embedded workspace scripts are real; API responses are isolated test doubles.
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
const {ECDH} = require('node:crypto');
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
const workspaceHTML=read('ui/shell/indexes.html');
const scripts = new Map([...html.matchAll(/<script\b[^>]*\bsrc="(\/shell\/[^"?]+\.js)"/g)]
  .map(match => [match[1], read('ui' + match[1])]));
for(const match of workspaceHTML.matchAll(/<script\b[^>]*\bsrc="(\/shell\/[^"?]+\.js)"/g))scripts.set(match[1],read('ui'+match[1]));
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
const numericPublicKey='02'+'0'.repeat(63)+'1';
assert.equal(ECDH.convertKey(Buffer.from(numericPublicKey,'hex'),'secp256k1',undefined,undefined,'compressed').toString('hex'),numericPublicKey,'Numeric destination fixture is a valid secp256k1 public key');
const addressCheckpoint={height:170,block_hash:'b'.repeat(64),commitment:'address-head'};
const definitions=['headers','blocks','tx-locator','txo-spender','utxo-state','sat-state','address-state','address-history','inscriptions'].map(id=>({id,name:id,locked:false,buildable:id!=='headers',dependencies:[],rules:[],version:1,network:'bitcoin-mainnet'}));
const runState={schema:1,configured:false,prepared:{in:null,out:null,consumers:[]}};
const fixtures = {
  '/api/v1/settings': {settings:{bitcoin_peers:[], satline_enabled:false, ord_enabled:true}, core:{connected:false}, core_store:{}},
  '/api/v1/browser/status': {browser:{native_active:false}},
  '/api/v1/system/status': {},
  '/api/v1/updates/status': {state:'not_configured',current_version:'test',configured:false,auto_check:false},
  '/api/v1/peers': {connections:[], network:{}, core_peers:[]},
  '/api/v1/migration': {job:{}, archive_root:'local fixture archive'},
  '/api/v1/graph/status': {},
  '/api/v1/jobs': {jobs:[]},
  '/api/v1/index/status': {definitions,job:{},jobs:[],instances:[],live:[],providers:[],browse:[],run:runState,timeline:{tip_height:170,tip_hash:'b'.repeat(64),tracks:[]},utxo:{state:'current',processed_height:170,processed_hash:'b'.repeat(64),target_height:170},addresses:{current:{state:'ready',p2pk_supported:true,snapshot:addressCheckpoint,utxo_snapshot:addressCheckpoint}}},
  '/api/v1/index/run':runState,
  '/api/v1/network': {enabled:true, connected:0, gateway_connected:0, headers:{header_height:0, header_state:'waiting'}},
  '/api/v1/setup': {show_automatically:false, settings:{}, progress:{stage:0}},
  '/api/v1/data-coverage': {headers:{header_height:0}, cache:{}, transaction_locations:{}, spender_knowledge:{}, mounted:{}, mounted_preparation:{}, core:{connected:false}, serving:{}},
};

async function harness(browser, address = '.gateway', failStatus = false, addressLookupState='not_built', satlineAvailable=false, satState='ready', numberOptions={}) {
  const context = await browser.newContext({serviceWorkers:'block'});
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  const errors = [], unexpected = [], requests = [];let appearance={theme:'',default:'light'};
  const numbers={tipHeight:170,tipHash:'b'.repeat(64),failure:null,...numberOptions,holds:new Map()},satHolds=new Map();
  function holdNumber(number, response={}){
    let release,started;const wait=new Promise(resolve=>release=resolve),began=new Promise(resolve=>started=resolve);
    numbers.holds.set(number,{wait,started,response});return {release,started:began};
  }
  function holdSat(number){
    let release,started;const wait=new Promise(resolve=>release=resolve),began=new Promise(resolve=>started=resolve);
    satHolds.set(String(number),{wait,started});return {release,started:began};
  }
  page.on('pageerror', error => errors.push(error.message));
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url());
    requests.push({method:request.method(), path:url.pathname,search:url.search,...(request.method()==='POST'?{body:request.postDataJSON()}: {})});
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
    if(url.pathname==='/indexes')return fulfill(workspaceHTML.replaceAll('__INDEX_NONCE__','fixture').replace('__INDEX_BOOTSTRAP__','{}'),'text/html');
    if(url.pathname==='/api/v1/index/layout')return fulfill(JSON.stringify({schema:1,order:{},revision:0}),'application/json');
    if(url.pathname==='/api/v1/index/run/prepare')return fulfill(JSON.stringify({...runState,prepared:request.postDataJSON()}),'application/json');
    if(url.pathname==='/api/v1/index/block-map')return fulfill(JSON.stringify({height:Number(url.searchParams.get('height')||0),hash:'b'.repeat(64),transactions:[],indexing:[],total:0}),'application/json');
    if(url.pathname==='/api/v1/flow')return fulfill(JSON.stringify({state:'unavailable',nodes:[],edges:[]}), 'application/json');
    if(url.pathname==='/satline') return fulfill('<!doctype html><title>Module fixture</title><h1>Embedded module fixture</h1><script>window.addEventListener("message",e=>{if(e.source===parent&&e.origin===location.origin&&e.data.type==="gateway:workspace-select-index"){document.body.dataset.index=e.data.index;document.body.dataset.rebuildSnapshot=JSON.stringify(e.data.rebuild_snapshot||null);}});</script>', 'text/html');
    if(url.pathname==='/api/v1/appearance'){if(request.method()==='POST')appearance={...appearance,...request.postDataJSON()};return fulfill(JSON.stringify(appearance),'application/json')}
    if(url.pathname==='/api/v1/navigate/number'){
      assert.equal(request.method(),'GET');const raw=url.searchParams.get('number'),number=Number(raw),held=numbers.holds.get(raw);
      assert(/^[0-9]+$/.test(raw)&&Number.isSafeInteger(number),'Unsafe/malformed numeric input never reaches classification');
      const tipHeight=numbers.tipHeight,tipHash=numbers.tipHash,failure=held?.response.failure||numbers.failure;
      const kind=number<=tipHeight?'block':'sat',answer=held?.response.answer||{kind,address:number+(kind==='block'?'.bitcoin':'.sat'),tip_height:tipHeight,tip_hash:tipHash};
      if(held){held.started();await held.wait;numbers.holds.delete(raw);}
      return fulfill(JSON.stringify(failure?{error:failure.error}:answer),'application/json',failure?.status||200);
    }
    if(url.pathname==='/api/v1/navigate'&&request.method()==='POST'){
      const address=request.postDataJSON().address,txid='a'.repeat(64),block='b'.repeat(64);
      if([bitcoinAddress,numericPublicKey].includes(address))return fulfill(JSON.stringify({resource:{kind:'address',query:address,address:{address},address_index:{address,state:'ready'}}}),'application/json');
      if(address===txid||address===txid+'.840000.bitcoin'){const located=address===txid;return fulfill(JSON.stringify({resource:{query:address,transaction:{txid,height:840000,block_hash:block,tx_index:7,locator_verified:true,transaction_verified:!located,source_network:located?'local_index_locator':'local_index',locator_peer:'committed transaction locator',verification_state:located?'header_anchored_locator':'header_anchored',resolution_state:located?'located_bytes_unavailable':'',note:'<img src="https://untrusted.invalid/pixel"> A provider must supply the containing block.',transaction:located?{index:0,inputs:null,outputs:null}:{txid,index:7,inputs:[],outputs:[{n:0,value_sats:123,type:'script'}]}}}}),'application/json')}
    }
    if(url.pathname==='/api/v1/navigate'&&request.method()==='POST'){const address=request.postDataJSON().address,height=Number(address.replace(/\.bitcoin$/,'').split('.').at(-1));return fulfill(JSON.stringify({resource:{kind:'block',query:address,block:{height:Number.isFinite(height)?height:0,hash:'b'.repeat(64),transaction_count:0,transactions:[]}}}),'application/json');}
    if(url.pathname==='/api/v1/bitmap/view')return fulfill(JSON.stringify({state:'not_built',note:'No saved canonical Bitmap mapping.'}),'application/json');
    if(url.pathname==='/api/v1/txio/readiness')return fulfill(JSON.stringify({inputs:[],outputs:[],sources_checked:[]}),'application/json');
    if(url.pathname==='/api/v1/address/view'){
      const destination=url.searchParams.get('address');assert.equal(request.method(),'GET');assert([bitcoinAddress,numericPublicKey].includes(destination));assert.equal(url.searchParams.get('mode'),'current');
      return fulfill(JSON.stringify({address:destination,destination_type:destination===numericPublicKey?'p2pk':'p2pkh',mode:'current',state:addressLookupState,snapshot:addressLookupState==='rebuild_required'?addressCheckpoint:{},current_balance_sats:null,current_output_count:null,outputs:[],complete:false,note:'Current UTXOs require inspection or a reviewed build.'}),'application/json');
    }
    if(url.pathname==='/api/v1/sats/view'){const held=satHolds.get(url.searchParams.get('sat'));if(held){satHolds.delete(url.searchParams.get('sat'));held.started();await held.wait;}return fulfill(JSON.stringify({state:satState,sat_number:Number(url.searchParams.get('sat')),origin:{height:144,first_sat:50,subsidy_offset:1},properties:{rarity:'common'},location:{state:'unspent',output_coordinate:'0.1.144',offset:1,address:bitcoinAddress},snapshot:addressCheckpoint,note:'Saved Sat location.'}),'application/json');}
    if(url.pathname==='/api/v1/settings'&&request.method()==='POST'){assert.equal(Object.hasOwn(request.postDataJSON(),'satline_enabled'),false,'Settings no longer sends the legacy Satline disable flag');return fulfill(JSON.stringify({ok:true}),'application/json');}
    if (request.method() === 'GET' && Object.hasOwn(fixtures, url.pathname==='/api/v1/index/progress'?'/api/v1/index/status':url.pathname)) {
      if (failStatus && url.pathname === '/api/v1/settings') return fulfill(JSON.stringify({error:'Fixture services unavailable'}), 'application/json', 503);
      return fulfill(JSON.stringify(fixtures[url.pathname==='/api/v1/index/progress'?'/api/v1/index/status':url.pathname]), 'application/json');
    }
    unexpected.push(`${request.method()} ${url.pathname}`);
    return fulfill(JSON.stringify({error:'Unexpected test request'}), 'application/json', 500);
  });
  await page.goto(origin, {waitUntil:'load'});
  await page.waitForFunction(() => document.querySelector('#content')?.children.length || !document.querySelector('#notice')?.hidden);
  return {context, page, errors, unexpected, requests, numbers, holdNumber, holdSat};
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

const workspace=page=>page.frameLocator('#indexes-module');
async function checkRoute(page,address,selector){
 const exception=['peers.gateway','settings.gateway'].includes(address);
 if(selector&&selector!=='iframe.indexes-frame')await (exception?page:workspace(page)).locator(selector+':visible').first().waitFor();
 else await page.locator('#indexes-module').waitFor({state:'visible'});
 await page.waitForFunction(expected=>document.querySelector('#address')?.value===expected,address);await healthy(page,address);assert.equal(await page.locator('#address').inputValue(),address);
 const active=page.locator('.nav-item.active');assert.equal(await active.count(),1);
 assert.equal(await active.getAttribute('data-route'),exception?address:'indexes.gateway');
 assert.equal(await active.getAttribute('aria-current'),'page');
 assert.equal(await page.locator('#indexes-module').isVisible(),!exception,'Only Settings and Peers leave the workspace surface');
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
      await workspace(h.page).getByRole('heading',{name:'Gateway workspace',exact:true}).waitFor();
      await checkRoute(h.page, '.gateway', '#workspace-resource-overview');
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
      await checkRoute(h.page,'satline.gateway','h1');
      await workspace(h.page).getByRole('heading',{name:/Satline/}).waitFor();
      assert.equal(await h.page.evaluate(()=>sessionStorage.getItem('gateway.dev_mode')),null,'Locked Satline explanation needs no development mode');
      assert.equal(h.requests.filter(request=>request.path.startsWith('/api/v1/satline')&&request.method!=='GET').length,0,'Opening Satline does not start a trace');
      const routes = [
        ['bitcoin.gateway', 'h1'], ['ord.gateway', 'h1'],
        ['sync.gateway', 'h1'],
        ['storage.gateway', '#workspace-resource-overview'], ['activity.gateway', '#workspace-resource-overview'],
        ['settings.gateway', '#settings-form'], ['.gateway', '#workspace-resource-overview'],
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
      await checkRoute(h.page, '.gateway', '#workspace-resource-overview');
      // Address-bar entry and browser back/forward are separate router entry points.
      await h.page.locator('#address').fill('indexes.gateway');
      await h.page.locator('#address-form').evaluate(form => form.requestSubmit());
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      await h.page.goBack();
      await checkRoute(h.page, '.gateway', '#workspace-resource-overview');
      await h.page.goForward();
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      // A plain external/legacy nav anchor must not crash active-state traversal.
      await h.page.evaluate(() => {
        const anchor = document.createElement('a');
        anchor.className = 'nav-item'; anchor.href = '/legacy'; anchor.textContent = 'Legacy fixture';
        document.querySelector('nav').append(anchor);
      });
      await h.page.locator('.brand span').last().click();
      await checkRoute(h.page, '.gateway', '#workspace-resource-overview');
      // An untrusted origin must not drive the management shell through postMessage.
      await h.page.evaluate(() => window.dispatchEvent(new MessageEvent('message', {origin:'https://untrusted.invalid', data:{type:'gateway:navigate', address:'indexes.gateway'}})));
      assert.equal(await h.page.locator('#address').inputValue(), '.gateway');
      assert.deepEqual(h.errors, [], 'uncaught browser exceptions');
      assert.deepEqual(h.unexpected, [], 'unexpected network/API requests: '+JSON.stringify(h.unexpected));
      assert.ok(h.requests.every(request => request.method === 'GET'||request.path==='/api/v1/navigate'), 'navigation unexpectedly mutated state');
    } finally { await h.context.close(); }

    const deep = await harness(browser, 'indexes.gateway');
    try {
      await checkRoute(deep.page, 'indexes.gateway', 'iframe.indexes-frame');
      assert.deepEqual(deep.errors, []);
      assert.deepEqual(deep.unexpected, []);
    } finally { await deep.context.close(); }

    const numeric=await harness(browser,'.gateway',false,'not_built',true);
    try{
      // The host updates its address before the embedded resource resolves.
      // Track the frame's actual resolved echo so the next synthetic entry
      // cannot race the previous resource's asynchronous focus message.
      await numeric.page.evaluate(()=>{window.testResolvedAddresses=[];window.addEventListener('message',event=>{if(event.source===document.querySelector('#indexes-module')?.contentWindow&&event.origin===location.origin&&event.data?.type==='gateway:workspace-focused'&&event.data.resolved===true)window.testResolvedAddresses.push(event.data.address);});});
      const numberResponses=[];numeric.page.on('response',response=>{if(new URL(response.url()).pathname==='/api/v1/navigate/number')numberResponses.push({url:response.url(),status:response.status()});});
      const enter=async raw=>{await numeric.page.locator('#address').fill(raw);await numeric.page.locator('#address-form').evaluate(form=>form.requestSubmit());};
      const classified=()=>numeric.requests.filter(r=>r.path==='/api/v1/navigate/number');
      const route=async(raw,canonical)=>{const previous=await numeric.page.evaluate(()=>window.testResolvedAddresses.length);await enter(raw);await checkRoute(numeric.page,canonical,canonical.endsWith('.sat')?'.sat-evidence':'iframe.indexes-frame');if(canonical.endsWith('.sat'))await numeric.page.waitForFunction(({previous,canonical})=>window.testResolvedAddresses.slice(previous).includes(canonical),{previous,canonical});assert.equal(new URL(numeric.page.url()).searchParams.get('resolve'),canonical);assert.equal(await numeric.page.evaluate(()=>history.state.address),canonical,'History stores explicit canonical meaning');};
      const delayedSat=numeric.holdSat(52);
      try{const previous=await numeric.page.evaluate(()=>window.testResolvedAddresses.length);await enter('52.sat');await delayedSat.started;await numeric.page.locator('#address').fill('99');delayedSat.release();await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.slice(previous).includes('52.sat'),previous);assert.equal(await numeric.page.locator('#address').inputValue(),'99','A delayed resolved frame echo cannot replace an edited address');assert.equal(await numeric.page.evaluate(()=>history.state.address),'52.sat','The resolved resource still updates canonical history');}finally{delayedSat.release();}
      const child=await (await numeric.page.locator('#indexes-module').elementHandle()).contentFrame(),delayedChild=numeric.holdSat(53);
      try{const previous=await numeric.page.evaluate(()=>window.testResolvedAddresses.length);await numeric.page.locator('#indexes-module').focus();await child.evaluate(()=>navigateWorkspace('53.sat'));await delayedChild.started;await numeric.page.locator('#address').fill('99');delayedChild.release();await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.slice(previous).includes('53.sat'),previous);assert.equal(await numeric.page.locator('#address').inputValue(),'99','A delayed untagged frame echo cannot replace a later edit');assert.equal(await numeric.page.evaluate(()=>history.state.address),'53.sat');}finally{delayedChild.release();}
      const previousIntent=await numeric.page.evaluate(()=>window.testResolvedAddresses.length);await numeric.page.locator('#indexes-module').focus();await child.evaluate(()=>navigateWorkspace('54.sat'));await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.slice(previous).includes('54.sat'),previousIntent);await numeric.page.waitForFunction(()=>document.querySelector('#address').value==='54.sat');assert.equal(await numeric.page.evaluate(()=>history.state.address),'54.sat','A new intentional iframe navigation still updates the address');
      const delayedNumber=numeric.holdNumber('60');
      try{await enter('60');await delayedNumber.started;await numeric.page.locator('#address').fill('61');const response=numeric.page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/navigate/number'&&new URL(r.url()).searchParams.get('number')==='60');delayedNumber.release();await response;await numeric.page.waitForTimeout(20);assert.equal(await numeric.page.locator('#address').inputValue(),'61','An older numeric classification cannot replace a newer edit');assert.equal(await numeric.page.evaluate(()=>history.state.address),'54.sat','An abandoned classification adds no history');}finally{delayedNumber.release();}
      const supersededNumber=numeric.holdNumber('62');
      try{await enter('62');await supersededNumber.started;const previous=await numeric.page.evaluate(()=>window.testResolvedAddresses.length);await numeric.page.locator('#indexes-module').focus();await child.evaluate(()=>navigateWorkspace('55.sat'));await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.slice(previous).includes('55.sat'),previous);const response=numeric.page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/navigate/number'&&new URL(r.url()).searchParams.get('number')==='62');supersededNumber.release();await response;await numeric.page.waitForTimeout(20);assert.equal(await numeric.page.locator('#address').inputValue(),'55.sat','A pending numeric request cannot override a newer iframe navigation');assert.equal(await numeric.page.evaluate(()=>history.state.address),'55.sat');}finally{supersededNumber.release();}
      const oldFrameID=await child.evaluate(()=>navigationContext.id);await route('56.sat','56.sat');assert.notEqual(await child.evaluate(()=>navigationContext.id),oldFrameID,'New host navigation uses a different frame generation');const newerRoute=await numeric.page.evaluate(()=>({url:location.href,previous:window.testResolvedAddresses.length}));await child.evaluate(id=>{parent.postMessage({type:'gateway:workspace-focused',address:'55.sat',resolved:true,navigationId:id},location.origin);parent.postMessage({type:'gateway:workspace-focused',address:'54.sat',resolved:true},location.origin);},oldFrameID);await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.length>=previous+2,newerRoute.previous);assert.equal(await numeric.page.locator('#address').inputValue(),'56.sat','Old and untagged iframe echoes cannot override newer host navigation');assert.equal(await numeric.page.evaluate(()=>history.state.address),'56.sat');assert.equal(numeric.page.url(),newerRoute.url);
      const workspaceBefore63=await child.evaluate(()=>workspaceHostNavigationId),staleIntent=numeric.holdNumber('63');
      try{await numeric.page.evaluate(()=>{window.testOldIntentSeen=false;window.addEventListener('message',e=>{if(e.data?.type==='gateway:workspace-intent'&&e.data.navigationId===window.testOldFrameID)window.testOldIntentSeen=true;});});await numeric.page.evaluate(id=>window.testOldFrameID=id,oldFrameID);await enter('63');await staleIntent.started;await numeric.page.locator('#indexes-module').focus();await child.evaluate(id=>parent.postMessage({type:'gateway:workspace-intent',navigationId:id},location.origin),oldFrameID);await numeric.page.waitForFunction(()=>window.testOldIntentSeen);staleIntent.release();await checkRoute(numeric.page,'63.bitcoin','iframe.indexes-frame');await child.waitForFunction(previous=>workspaceHostNavigationId!==previous,workspaceBefore63);assert.equal(await numeric.page.evaluate(()=>history.state.address),'63.bitcoin','A stale iframe intent cannot cancel a newer host classification');}finally{staleIntent.release();}
      await numeric.page.evaluate(()=>{window.testOldNavigateSeen=false;window.addEventListener('message',e=>{if(e.data?.type==='gateway:navigate'&&e.data.navigationId===window.testOldFrameID)window.testOldNavigateSeen=true;});});const beforeStaleNavigate=numeric.page.url();await child.evaluate(id=>parent.postMessage({type:'gateway:navigate',address:'settings.gateway',navigationId:id},location.origin),oldFrameID);await numeric.page.waitForFunction(()=>window.testOldNavigateSeen);assert.equal(numeric.page.url(),beforeStaleNavigate,'An old iframe navigation cannot replace a newer host route');
      const currentFrameID=await child.evaluate(()=>workspaceHostNavigationId),beforeQueuedNavigate=await numeric.page.evaluate(()=>({url:location.href,address:history.state.address}));await numeric.page.evaluate(id=>{window.testQueuedIntentSeen=false;window.testQueuedNavigateSeen=false;window.addEventListener('message',e=>{if(e.data?.navigationId!==id)return;if(e.data.type==='gateway:workspace-intent')window.testQueuedIntentSeen=true;if(e.data.type==='gateway:navigate'&&e.data.address==='settings.gateway')window.testQueuedNavigateSeen=true;});},currentFrameID);await numeric.page.locator('#indexes-module').focus();await child.evaluate(id=>parent.postMessage({type:'gateway:workspace-intent',navigationId:id},location.origin),currentFrameID);await numeric.page.waitForFunction(()=>window.testQueuedIntentSeen);await numeric.page.locator('#address').fill('99');await child.evaluate(id=>parent.postMessage({type:'gateway:navigate',address:'settings.gateway',navigationId:id},location.origin),currentFrameID);await numeric.page.waitForFunction(()=>window.testQueuedNavigateSeen);assert.equal(await numeric.page.locator('#address').inputValue(),'99','A queued iframe navigation cannot erase a newer host edit');assert.equal(numeric.page.url(),beforeQueuedNavigate.url);assert.equal(await numeric.page.evaluate(()=>history.state.address),beforeQueuedNavigate.address);
      await numeric.page.locator('#indexes-module').focus();await child.evaluate(()=>navigateWorkspace('settings.gateway'));await numeric.page.locator('#settings-form').waitFor();const hiddenBefore=await numeric.page.evaluate(()=>({url:location.href,address:history.state.address,previous:window.testResolvedAddresses.length}));await child.evaluate(()=>parent.postMessage({type:'gateway:workspace-focused',address:'99.sat',resolved:true},location.origin));await numeric.page.waitForFunction(previous=>window.testResolvedAddresses.length>previous,hiddenBefore.previous);assert.equal(await numeric.page.locator('#address').inputValue(),'settings.gateway','A hidden iframe cannot replace the active Settings route');assert.equal(numeric.page.url(),hiddenBefore.url);assert.equal(await numeric.page.evaluate(()=>history.state.address),hiddenBefore.address);
      for(const [raw,canonical] of [['169','169.bitcoin'],['170','170.bitcoin'],['171','171.sat'],['0','0.bitcoin'],['000170','170.bitcoin'],[' 000171 ','171.sat'],['5000000000','5000000000.sat']]){
        const before=classified().length;await route(raw,canonical);assert.equal(classified().length,before+1,'Each bare entry consults the selected tip once');
      }
      const explicitBefore=classified().length;
      for(const address of ['0.sat','51.sat','171.bitcoin'])await route(address,address);
      assert.equal(classified().length,explicitBefore,'Explicit Sat and block addresses bypass numeric classification on either side of the tip');

      // Meaning follows the selected tip when entered afresh; canonical history
      // keeps its original explicit meaning through advances and shorter forks.
      await route('171','171.sat');
      numeric.numbers.tipHeight=200;numeric.numbers.tipHash='c'.repeat(64);await route('171','171.bitcoin');
      numeric.numbers.tipHeight=100;numeric.numbers.tipHash='d'.repeat(64);await route('171','171.sat');
      const historyClassifications=classified().length;
      await numeric.page.goBack();await checkRoute(numeric.page,'171.bitcoin','iframe.indexes-frame');
      await numeric.page.goBack();await checkRoute(numeric.page,'171.sat','.sat-evidence');
      await numeric.page.goForward();await checkRoute(numeric.page,'171.bitcoin','iframe.indexes-frame');
      assert.equal(classified().length,historyClassifications,'Back/forward never reinterprets a saved Sat or block after the tip changes');

      await route('51.sat','51.sat');
      for(const failure of [{status:409,error:'Selected chain tip is unavailable; use an explicit N.bitcoin or N.sat address.'},{status:503,error:'Selected tip probe failed; use an explicit N.bitcoin or N.sat address.'}]){
        numeric.numbers.failure=failure;
        const before=await numeric.page.evaluate(()=>{window.numberStableNode=document.querySelector('#content').firstElementChild;return{url:location.href,length:history.length,address:history.state.address};});
        const resolutions=numeric.requests.filter(r=>r.path==='/api/v1/navigate'||r.path==='/api/v1/sats/view').length;
        await enter('99');try{await numeric.page.locator('#notice').filter({hasText:failure.error}).waitFor();}catch(error){const details=await numeric.page.evaluate(()=>({address:document.querySelector('#address')?.value,notice:document.querySelector('#notice')?.textContent,noticeHidden:document.querySelector('#notice')?.hidden,history:history.state,url:location.href,resolved:window.testResolvedAddresses.slice(-5)}));throw Error('Number classification notice missing: '+JSON.stringify({details,requests:classified().slice(-3),responses:numberResponses.slice(-3),cause:String(error)}));}
        assert.deepEqual(await numeric.page.evaluate(()=>({url:location.href,length:history.length,address:history.state.address})),before,'Missing/failed authority cannot commit a guessed route or history entry');
        assert.equal(await numeric.page.evaluate(()=>window.numberStableNode===document.querySelector('#content').firstElementChild),true,'Classification failure preserves the current view');
        assert.equal(numeric.requests.filter(r=>r.path==='/api/v1/navigate'||r.path==='/api/v1/sats/view').length,resolutions,'Classification failure performs no resource lookup');
        await route('99.bitcoin','99.bitcoin');await route('0.sat','0.sat');
      }
      numeric.numbers.failure=null;
      const unsafeBefore=classified().length,urlBeforeUnsafe=numeric.page.url(),historyBeforeUnsafe=await numeric.page.evaluate(()=>history.length);
      await enter('9007199254740992');await numeric.page.locator('#notice').filter({hasText:'supported exact range'}).waitFor();
      assert.equal(classified().length,unsafeBefore,'Unsafe integer is rejected before any classification request');assert.equal(numeric.page.url(),urlBeforeUnsafe);assert.equal(await numeric.page.evaluate(()=>history.length),historyBeforeUnsafe);
      await route('0.sat','0.sat');
      const typedBefore=classified().length;
      await enter('0'.repeat(64));await checkRoute(numeric.page,'0'.repeat(64),'iframe.indexes-frame');
      await enter(numericPublicKey);await checkRoute(numeric.page,numericPublicKey,'#address-balance');
      assert.equal(classified().length,typedBefore,'Valid numeric-only hashes and SEC destinations retain their typed meaning');
      assert.equal(numeric.requests.some(r=>r.path.startsWith('/api/v1/index/')&&r.method==='POST'||r.path.startsWith('/api/v1/satline')&&r.method==='POST'),false,'Number classification never starts indexing or tracing');
      assert.deepEqual(numeric.errors,[]);assert.deepEqual(numeric.unexpected,[]);
    }finally{await numeric.context.close();}

    const staleNumber=await harness(browser,'51.sat',false,'not_built',true),heldNumbers=[];
    try{
      const enter=async raw=>{await staleNumber.page.locator('#address').fill(raw);await staleNumber.page.locator('#address-form').evaluate(form=>form.requestSubmit());};
      for(const [raw,response] of [['60',{}],['61',{failure:{status:409,error:'Obsolete classification error'}}]]){
        const held=staleNumber.holdNumber(raw,response);heldNumbers.push(held);
        const old=await staleNumber.page.evaluate(()=>{window.numberStableNode=document.querySelector('#content').firstElementChild;return{url:location.href,length:history.length};});
        await enter(raw);await held.started;
        assert.equal(staleNumber.page.url(),old.url);assert.equal(await staleNumber.page.evaluate(()=>history.length),old.length,'A pending classification cannot push raw or speculative history');
        assert.equal(await staleNumber.page.evaluate(()=>window.numberStableNode===document.querySelector('#content').firstElementChild),true,'Pending classification leaves current content mounted');
        await enter('42.sat');await checkRoute(staleNumber.page,'42.sat','.sat-evidence');
        const latest={url:staleNumber.page.url(),length:await staleNumber.page.evaluate(()=>history.length)},finished=staleNumber.page.waitForResponse(r=>r.url()===origin+'/api/v1/navigate/number?number='+raw);
        held.release();await finished;await staleNumber.page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
        await checkRoute(staleNumber.page,'42.sat','.sat-evidence');assert.equal(staleNumber.page.url(),latest.url);assert.equal(await staleNumber.page.evaluate(()=>history.length),latest.length,'An obsolete classification result/error cannot rewrite current history');
        assert.equal(await staleNumber.page.locator('#notice').isVisible(),false,'An obsolete failure cannot overwrite the latest page with an error');
      }
      assert.equal(staleNumber.requests.some(r=>r.path==='/api/v1/navigate'||r.path.startsWith('/api/v1/index/')&&r.method==='POST'||r.path.startsWith('/api/v1/satline')&&r.method==='POST'),false,'Obsolete replies cannot fetch a block or start work');
      assert.deepEqual(staleNumber.errors,[]);assert.deepEqual(staleNumber.unexpected,[]);
    }finally{for(const held of heldNumbers)held.release();await staleNumber.context.close();}

    const located=await harness(browser,'a'.repeat(64));
    try{
      await located.page.locator('iframe.indexes-frame').waitFor();await healthy(located.page,'Transaction address in the shared workspace');
      assert.equal(await located.page.locator('#address').inputValue(),'a'.repeat(64));await workspace(located.page).locator('#timeline-explorer-panel[data-state="located_bytes_unavailable"]').waitFor();assert.equal(located.requests.filter(request=>request.path==='/api/v1/navigate').length,1,'The workspace resolves a transaction once');assert.equal(await workspace(located.page).locator('img[src*=untrusted]').count(),0,'Provider notes remain escaped');assert.equal(await located.page.locator('#transaction-location').count(),0,'Shell does not duplicate transaction Details outside the workspace');assert.deepEqual(located.errors,[]);assert.deepEqual(located.unexpected,[]);
    }finally{await located.context.close()}
    if(scripts.has('/shell/address-view.js')){
      const addressView=await harness(browser,bitcoinAddress);
      try{
        await workspace(addressView.page).locator('#address-balance').waitFor();await checkRoute(addressView.page,bitcoinAddress,'#address-balance');assert.equal(await workspace(addressView.page).locator('#address-balance').innerText(),'Unavailable');assert.equal(await addressView.page.locator('#content').getByText('[object Object]').count(),0,'Legacy typed address envelope is not passed as the address string');
        await workspace(addressView.page).getByRole('button',{name:'Inspect Current UTXOs',exact:true}).first().click();const frame=workspace(addressView.page);await frame.locator('#address-state-build-panel').waitFor({state:'visible'});assert.equal((await frame.locator('.timeline-track[data-index="address-state"] [data-control="arm"]').getAttribute('aria-pressed')==='true'),false,'Inspecting does not arm');
        await addressView.page.locator('#address').fill(bitcoinAddress);await addressView.page.locator('#address-form').evaluate(form=>form.requestSubmit());await workspace(addressView.page).locator('#address-balance').waitFor();await checkRoute(addressView.page,bitcoinAddress,'#address-balance');const resolutions=addressView.requests.filter(r=>r.path==='/api/v1/navigate').length;await addressView.page.locator('#address').fill('i0.3.150');await addressView.page.locator('#address-form').evaluate(form=>form.requestSubmit());await checkRoute(addressView.page,'i0.3.150','iframe.indexes-frame');assert.equal(addressView.requests.filter(r=>r.path==='/api/v1/navigate').length,resolutions+1,'Spending-input BPS navigation resolves once in the shared workspace');assert.equal(addressView.requests.some(r=>r.path==='/api/v1/index/build'),false,'Address navigation never starts a build');assert.deepEqual(addressView.errors,[]);assert.deepEqual(addressView.unexpected,[]);
      }finally{await addressView.context.close()}
      const damagedAddress=await harness(browser,bitcoinAddress,false,'rebuild_required');
      try{
        await workspace(damagedAddress.page).getByRole('button',{name:'Review Current UTXOs recovery',exact:true}).click();const frame=workspace(damagedAddress.page);await frame.locator('#address-state-build-panel').waitFor({state:'visible'});assert.deepEqual(await frame.locator('#index-timeline').evaluate(()=>timeline.getRunDraft().consumers.find(c=>c.index==='address-state').rebuild_snapshot),addressCheckpoint,'Recovery stages the exact damaged lookup checkpoint');assert.equal(damagedAddress.requests.some(r=>r.path==='/api/v1/index/build'||r.path==='/api/v1/index/plan'),false,'Rebuild navigation requires a later explicit review');assert.deepEqual(damagedAddress.errors,[]);assert.deepEqual(damagedAddress.unexpected,[]);
      }finally{await damagedAddress.context.close()}
    }
    if(scripts.has('/shell/sat-view.js')){
      const sat=await harness(browser,'51.sat',false,'not_built',true);
      try{
        await workspace(sat.page).getByRole('heading',{name:'Sat 51',exact:true}).waitFor();await checkRoute(sat.page,'51.sat','.sat-evidence');assert.equal(await workspace(sat.page).getByRole('button',{name:'Trace Satline',exact:true}).count(),0,'Sat views retain read-only navigation without deferred trace actions');assert.match(await workspace(sat.page).locator('.sat-evidence').innerText(),/Satline.*deferred|history.*deferred/i);assert.equal(sat.requests.some(r=>r.path.startsWith('/api/v1/satline')&&r.method==='POST'),false,'Opening a Sat view does not start a trace itself');await sat.page.locator('#address').fill('51.sat');await sat.page.locator('#address-form').evaluate(form=>form.requestSubmit());await workspace(sat.page).getByRole('heading',{name:'Sat 51',exact:true}).waitFor();assert.equal(sat.requests.filter(r=>r.path==='/api/v1/navigate').length,0,'Internal Sat entry uses the read-only Sat view route');await workspace(sat.page).getByRole('button',{name:bitcoinAddress,exact:true}).click();await workspace(sat.page).locator('#address-balance').waitFor();await checkRoute(sat.page,bitcoinAddress,'#address-balance');await sat.page.goBack();await workspace(sat.page).getByRole('heading',{name:'Sat 51',exact:true}).waitFor();assert.equal(await workspace(sat.page).locator('#sat-number').count(),0,'Individual Sat pages omit the duplicate address form');const numericClassifications=sat.requests.filter(r=>r.path==='/api/v1/navigate/number').length;await sat.page.locator('#address').fill('nvtdijuwxlp');await sat.page.locator('#address-form').evaluate(form=>form.requestSubmit());await workspace(sat.page).getByRole('heading',{name:'Sat 0',exact:true}).waitFor();assert.equal(new URL(sat.page.url()).searchParams.get('resolve'),'nvtdijuwxlp.sat','Bare ordinal names preserve canonical name history');assert.equal(sat.requests.filter(r=>r.path==='/api/v1/navigate/number').length,numericClassifications,'Names need no selected-tip classification');await sat.page.locator('#address').fill('nvtdijuwxlp.sat');await sat.page.locator('#address-form').evaluate(form=>form.requestSubmit());await workspace(sat.page).getByRole('heading',{name:'Sat 0',exact:true}).waitFor();assert.equal(await sat.page.locator('#address').inputValue(),'nvtdijuwxlp.sat','Explicit name.sat opens the same read-only Sat view');await workspace(sat.page).getByRole('button',{name:'0.1.144',exact:true}).click();await checkRoute(sat.page,'0.1.144','iframe.indexes-frame');assert.equal(sat.requests.some(r=>r.path==='/api/v1/index/build'),false,'Sat/address/output navigation does not start indexing');assert.deepEqual(sat.errors,[]);assert.deepEqual(sat.unexpected,[]);
      }finally{await sat.context.close()}
    }
    if(scripts.has('/shell/sat-view.js')){const unindexed=await harness(browser,'5.sat',false,'not_built',true,'not_built');try{await workspace(unindexed.page).getByRole('heading',{name:'Sat 5',exact:true}).waitFor();assert.equal(await workspace(unindexed.page).getByRole('button',{name:'Trace Satline',exact:true}).count(),0,'Deferred tracing stays absent even with stale available metadata');await unindexed.page.locator('#settings-button').click();await unindexed.page.locator('#settings-form').waitFor();assert.equal(await unindexed.page.locator('#satline-enabled').count(),0,'Settings offers no Satline disable switch');await unindexed.page.getByRole('button',{name:'Save settings',exact:true}).click();await unindexed.page.waitForFunction(()=>document.querySelector('#toast').textContent==='Settings saved.');assert.equal(Object.hasOwn(unindexed.requests.filter(r=>r.path==='/api/v1/settings'&&r.method==='POST').at(-1).body,'satline_enabled'),false);assert.equal(unindexed.requests.some(r=>r.path==='/api/v1/index/build'),false);await unindexed.page.locator('#address').fill('satline.gateway');await unindexed.page.locator('#address-form').evaluate(form=>form.requestSubmit());await workspace(unindexed.page).getByRole('heading',{name:'Satline is deferred',exact:true}).waitFor();assert.equal(await unindexed.page.locator('#satline-module').count(),0,'A stale availability flag cannot mount the tracing module');assert.match(await workspace(unindexed.page).locator('#workspace-resource-overview').innerText(),/History tracing remains disabled/);assert.equal(unindexed.requests.some(r=>r.path.startsWith('/api/v1/satline')||r.path==='/satline'),false,'Deferred direct navigation requests no module or trace');assert.deepEqual(unindexed.errors,[]);assert.deepEqual(unindexed.unexpected,[]);}finally{await unindexed.context.close()}}
    const degraded = await harness(browser, '.gateway', true);
    try {
      await workspace(degraded.page).getByRole('heading',{name:'Gateway workspace',exact:true}).waitFor();
      await healthy(degraded.page, 'Home with unavailable services');
      await degraded.page.locator('#address').fill('bitcoin.gateway');
      await degraded.page.locator('#address-form').evaluate(form => form.requestSubmit());
      await checkRoute(degraded.page, 'bitcoin.gateway', 'h1');
      assert.deepEqual(degraded.errors, []);
      assert.deepEqual(degraded.unexpected, []);
    } finally { await degraded.context.close(); }
    console.log(`Shell navigation: PASS (${revision || 'working tree'}; real DOM/all ${scripts.size} shell scripts, dynamic bare-number selected-tip boundaries and canonical history, explicit forms and numeric-only typed identities, unavailable/failed authority and stale-result guards, initial/degraded Home, primary workspaces, resolver-only legacy views, deep links, nested clicks, origin guard; real persistent workspace and isolated API boundaries).`);
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
}

run().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
