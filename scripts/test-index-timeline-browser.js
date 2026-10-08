'use strict';
// Browser acceptance against source-identical UI and deliberately sparse API
// fixtures. No indexing, block fetches, peers or outside services are contacted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');

const root = path.resolve(__dirname, '..');
const options = {};
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i], value = process.argv[i + 1];
  if (!['--out', '--browser'].includes(key) || !value) throw Error('Usage: node scripts/test-index-timeline-browser.js [--out DIR] [--browser PATH]');
  options[key.slice(2)] = value;
}
const out = options.out ? path.resolve(options.out) : null;
if (out) fs.mkdirSync(out, {recursive:true});

function playwright() {
  try { return require('playwright'); }
  catch (error) {
    const bundled = path.join(os.homedir(), '.cache', 'codex-runtimes', 'codex-primary-runtime', 'dependencies', 'node', 'node_modules', 'playwright');
    if (fs.existsSync(bundled)) return require(bundled);
    throw Error('An existing Playwright installation is required. Set NODE_PATH. ' + error.message);
  }
}
function browserPath(chromium) {
  const candidates = [options.browser, process.env.GATEWAY_TEST_BROWSER, chromium.executablePath()];
  if (process.platform === 'win32') {
    for (const key of ['ProgramFiles(x86)', 'ProgramFiles', 'LOCALAPPDATA']) {
      if (!process.env[key]) continue;
      candidates.push(path.join(process.env[key], 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
      candidates.push(path.join(process.env[key], 'Google', 'Chrome', 'Application', 'chrome.exe'));
    }
  }
  const found = candidates.find(candidate => candidate && fs.existsSync(candidate));
  if (!found) throw Error('No installed Chromium browser found; this test never downloads one.');
  return found;
}

const base = {version:1, network:'bitcoin-mainnet', start_height:0, rules:['Only retained selected-chain evidence establishes coverage.'], dependencies:['headers'], arbitrary_start:true};
const definitions = [
  {...base, id:'blocks', name:'Bitcoin blocks', buildable:true, theory:'Raw block data with exact local coverage.'},
  {...base, id:'headers', name:'Bitcoin headers', buildable:false, dependencies:[], theory:'The foundational selected-chain index.'},
  {...base, id:'inscriptions', name:'Inscriptions', buildable:true, locked:true, lock_reason:'Locked in this testing build. Saved data is retained.'},
  {...base, id:'bitmap-compatibility', name:'Bitmap: Terrain Claim Chains', buildable:false, locked:true, lock_reason:'Locked in this testing build.'}
];
function fixture() {
  return {
    definitions,
    // These intentionally disagree with physical block availability. Neither
    // committed processing nor a configured provider may fill timeline gaps.
    instances:[{definition:'blocks', checkpoint:{from:0, height:999999, commitment:'fixture-committed'}, coverage:[{from:0,to:999999}], retention:'ephemeral', verification:'selected_chain'}],
    providers:[{id:'blocks', provider:'core', queryable:true, coverage:[{from:0,to:999999}], note:'External provider claim only.'}],
    live:[], jobs:[], job:{}, errors:[],
    timeline:{tip_height:999999, target_height:999999, errors:[], tracks:[
      {id:'headers', coverage:[{from:0,to:999999}], gaps:[], sources:[], note:'Selected headers.'},
      {id:'blocks', coverage:[{from:0,to:9},{from:499990,to:500010},{from:999990,to:999999}], gaps:[{from:500000,to:500002}], sources:[], note:'Raw blocks currently held locally.'},
      {id:'inscriptions', coverage:[], gaps:[], sources:[], note:'Locked.'}
    ]}
  };
}

async function run() {
  const server = http.createServer((request, response) => {response.writeHead(500); response.end('Unexpected unmocked request');});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const {chromium} = playwright();
  let browser;
  try {
    browser = await chromium.launch({headless:true, executablePath:browserPath(chromium)});
    const page = await browser.newPage({viewport:{width:1440,height:1100}, reducedMotion:'reduce'});
    const errors = [], calls = [], unexpected = [];
    page.on('pageerror', error => errors.push(error.message));
    let snapshot = fixture(), unavailable = false, theme = 'light';
    await page.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      const reply = (body, type = 'application/json', status = 200) => route.fulfill({status, contentType:type, body:type === 'application/json' ? JSON.stringify(body) : body});
      if (url.origin !== origin) {unexpected.push(request.method() + ' ' + url.origin + url.pathname); return route.abort();}
      if (url.pathname === '/indexes') return reply(fs.readFileSync(path.join(root, 'ui/shell/indexes.html'), 'utf8').replaceAll('__INDEX_NONCE__', 'fixture'), 'text/html');
      if (url.pathname.startsWith('/shell/')) {
        const file = path.resolve(root, 'ui', '.' + url.pathname);
        assert(file.startsWith(path.resolve(root, 'ui/shell') + path.sep), 'Fixture static path must stay in shell');
        return reply(fs.readFileSync(file), url.pathname.endsWith('.js') ? 'application/javascript' : 'text/css');
      }
      if (url.pathname === '/favicon.ico') return reply('', 'image/x-icon');
      if (url.pathname === '/api/v1/appearance') return reply({theme, default:'light'});
      const call = {method:request.method(), path:url.pathname, body:request.method() === 'POST' ? request.postDataJSON() : null};
      calls.push(call);
      if (url.pathname === '/api/v1/index/status') return unavailable ? reply({error:'Fixture unavailable'}, 'application/json', 503) : reply(snapshot);
      if (url.pathname === '/api/v1/index/query') return reply({rows:[], total:0, total_known:true, inspected_blocks:0, chain_state:'selected_chain'});
      unexpected.push(call.method + ' ' + call.path);
      return reply({error:'Unexpected fixture action'}, 'application/json', 500);
    });

    const screenshot = async name => {if (out) await page.screenshot({path:path.join(out,name+'.png'),fullPage:true});};
    const refresh = async (preserveFocus=false) => {
      const response = page.waitForResponse(response => response.url() === origin + '/api/v1/index/status');
      if(preserveFocus)await page.locator('#refresh').evaluate(button=>button.click());
      else await page.locator('#refresh').click();
      await response;
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    };
    const noMutations = () => assert.equal(calls.filter(call => call.method !== 'GET').length, 0, 'Navigation and inspection cannot alter indexing, Live, sharing or network settings');
    const viewport = () => page.locator('#index-timeline').evaluate(node => ({from:Number(node.dataset.from),to:Number(node.dataset.to),fit:node.dataset.fit==='true'}));
    const blockLane = page.locator('.timeline-lane[data-index="blocks"]');
    const clickBlock = async height => {
      const view = await viewport(), box = await blockLane.boundingBox();
      assert(height>=view.from&&height<=view.to,'Clicked block must lie inside the actual viewport');
      await blockLane.click({position:{x:(height-view.from+.5)/(view.to-view.from+1)*box.width,y:40}});
    };
    await page.goto(origin+'/indexes', {waitUntil:'networkidle'});
    await page.locator('.timeline-track[data-index="blocks"]').waitFor();
    assert.deepEqual(await page.locator('.timeline-track').evaluateAll(nodes => nodes.map(node => node.dataset.index)), ['headers','blocks'], 'Only added or established indexes have tracks, with foundational Headers first');
    assert.deepEqual(await viewport(),{from:0,to:999999,fit:true});
    assert.deepEqual(await blockLane.evaluate(node=>JSON.parse(node.dataset.coverage)),[{from:0,to:9},{from:499990,to:499999},{from:500003,to:500010},{from:999990,to:999999}], 'Rendered physical block coverage excludes gaps and disregards processing/provider claims');
    assert(await page.locator('*').count()<1200,'Million-height chain must use bounded DOM, not one node per block');
    assert.equal(await page.locator('.timeline-lane').count(),2);
    noMutations();
    await screenshot('timeline-chain-light');

    // The header itself is present at both heights. Explorer needs the body,
    // so its fetch disclosure must use Blocks availability independently.
    await page.locator('#timeline-height').fill('500000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-index'),'headers');
    assert.match(await page.locator('#timeline-selection-meta').innerText(),/1 \/ 1/);
    assert.equal(await page.locator('#timeline-open-entity').innerText(),'Fetch & open in Explorer','A local header does not imply a locally held block body');
    await page.locator('#timeline-height').fill('500003');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.equal(await page.locator('#timeline-open-entity').innerText(),'Open in Explorer','Header selection can use independent known local block availability');
    await page.locator('#timeline-fit').click();

    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await page.locator('#timeline-inspector').waitFor();
    assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).count(), 1, 'Controls belong to the selected track inspector');
    assert.equal(await page.locator('#timeline-details').evaluate(details => details.open), false, 'Detailed workspaces begin collapsed');
    assert.equal(calls.filter(call => call.path.endsWith('/query')).length, 0, 'Merely selecting a track does not request records');
    assert(await page.locator('#timeline-selection-title').innerText());
    const inspectorBox=await page.locator('#timeline-inspector').boundingBox(),editorBox=await page.locator('.timeline-editor').boundingBox();
    assert(inspectorBox.y+inspectorBox.height<=editorBox.y,'Selected track inspector appears above the aligned timeline');
    noMutations();

    await page.locator('.timeline-exact summary').click();
    await page.getByRole('button',{name:'Select blocks 499,990–499,999',exact:true}).click();
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-kind'),'range');
    assert.match(await page.locator('#timeline-selection-meta').innerText(),/10 \/ 10/);
    await page.locator('#timeline-focus').click();
    const focused=await viewport();assert.equal(focused.fit,false);assert(focused.from<=499990&&focused.to>=499999);
    assert(focused.to-focused.from<64,'Focusing a short range reaches individually inspectable blocks');
    await screenshot('timeline-range-light');
    snapshot.timeline.tip_height=1000009;snapshot.timeline.target_height=1000009;snapshot.timeline.tracks[0].coverage[0].to=1000009;
    await refresh();
    assert.deepEqual(await viewport(),focused,'Appending headers never moves a user-focused viewport');
    await page.locator('#timeline-fit').click();assert.equal((await viewport()).to,1000009);
    snapshot.timeline.tip_height=1000010;snapshot.timeline.target_height=1000010;snapshot.timeline.tracks[0].coverage[0].to=1000010;
    await refresh();assert.deepEqual(await viewport(),{from:0,to:1000010,fit:true},'Fit chain tracks an appended tip');

    // Read-only entity inspection must preserve exact absence in the middle of
    // a recorded range; only the distinct Explorer action may request bytes.
    await page.locator('#timeline-height').fill('500000');
    await page.locator('#timeline-jump').evaluate(form => form.requestSubmit());
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-kind'),'block');
    assert.match(await page.locator('#timeline-selection-meta').innerText(),/0 \/ 1/);
    assert.equal(await page.locator('#timeline-open-entity').innerText(),'Fetch & open in Explorer');
    await clickBlock(500003);
    assert.match(await page.locator('#timeline-selection-meta').innerText(),/1 \/ 1/);
    assert.equal(await page.locator('#timeline-open-entity').innerText(),'Open in Explorer');
    await blockLane.focus();await page.keyboard.press('ArrowLeft');
    assert.match(await page.locator('#timeline-selection-title').innerText(),/500,002/);
    assert.match(await page.locator('#timeline-selection-meta').innerText(),/0 \/ 1/);
    await refresh(true);assert.equal(await blockLane.evaluate(node=>document.activeElement===node),true,'Refresh retains keyboard focus on the same track node');
    await page.keyboard.press('+');const zoomed=await viewport();assert(zoomed.to-zoomed.from<32);
    await page.locator('#timeline-pan-right').click();const panned=await viewport();assert(panned.from>zoomed.from);
    await page.locator('#timeline-pan-left').click();assert((await viewport()).from<panned.from);
    await blockLane.focus();await page.keyboard.press('Home');assert.equal((await viewport()).fit,true,'Home returns the keyboard viewport to the full chain');
    await page.locator('#timeline-height').fill('500000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    await page.locator('#timeline-focus').click();
    await screenshot('timeline-focused-light');
    noMutations();

    await page.locator('#timeline-add-index').click();
    for (const id of ['inscriptions','bitmap-compatibility']) {
      const option = page.locator('#timeline-index-picker button[data-index="'+id+'"]');
      assert.equal(await option.isDisabled(), true, 'Locked indexes remain unavailable in the Add index chooser');
      await option.evaluate(node => node.click());
      assert.equal(await page.locator('.timeline-track[data-index="'+id+'"]').count(),0, 'A locked option cannot create a track');
    }
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-index'),'blocks');
    await screenshot('timeline-locked-light');
    await page.locator('#timeline-add-index').click();
    noMutations();

    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    for (const mode of ['light','dark']) {
      theme = mode;
      await page.evaluate(mode => {document.documentElement.dataset.theme=mode;}, mode);
      const switchColors=await page.locator('.timeline-switches .index-switch').evaluateAll(labels=>labels.map(label=>({on:label.querySelector('input').checked,color:getComputedStyle(label.querySelector('.switch-track')).backgroundColor})));
      assert.equal(switchColors[0].on,true);assert.equal(switchColors[1].on,false);
      assert.notEqual(switchColors[0].color,switchColors[1].color,'On and Off use distinguishable colours in '+mode+' theme');
      for (const width of [1440,390]) {
        await page.setViewportSize({width,height:width===390?844:1100});
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Timeline fits '+mode+' '+width+' without document overflow');
        await screenshot('timeline-'+mode+'-'+width);
      }
    }
    await page.setViewportSize({width:1440,height:1100});
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    const physical=snapshot.timeline.tracks.splice(1,1)[0];
    await refresh();assert.deepEqual(await blockLane.evaluate(node=>JSON.parse(node.dataset.coverage)),[],'Missing physical projection does not fall back to processing/provider coverage');
    await page.locator('.timeline-track[data-index="headers"] .timeline-track-select').click();
    await page.locator('#timeline-height').fill('500003');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.equal(await page.locator('#timeline-open-entity').innerText(),'Fetch & open in Explorer','An unavailable block source never gets filled in by selected-header coverage');
    snapshot.timeline.tracks.splice(1,0,physical);await refresh();
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    unavailable = true;
    await refresh();
    await page.getByText(/status unavailable/i).first().waitFor();
    assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).isDisabled(), true, 'Unavailable control status cannot authorize changes');
    assert.equal(await page.getByRole('switch',{name:'blocks Live',exact:true}).isDisabled(), true);
    await screenshot('timeline-unavailable');
    unavailable = false;
    await refresh();
    assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).isDisabled(), false, 'A successful refresh recovers controls');
    assert.equal(calls.filter(call=>call.path.endsWith('/query')).length,0,'All timeline inspection remains independent of record queries');
    await page.locator('#timeline-height').fill('500000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    await page.locator('#timeline-build-range').click();
    assert.equal(await page.locator('#from').inputValue(),'0','Preparing a range keeps the retained instance origin fixed');assert.equal(await page.locator('#from').getAttribute('readonly'),'');assert.equal(await page.locator('#to').inputValue(),'500000');
    assert.equal(await page.locator('#initial-live').isChecked(),false,'Preparing a selected range does not implicitly follow the chain');
    assert.equal(await page.locator('#build').isDisabled(),true,'A selected range still needs a reviewed plan');
    assert.equal(await page.locator('#timeline-details').evaluate(details=>details.open),true);
    noMutations();

    // Embedded Explorer navigation is explicit and emits only the selected
    // address; the fixture never supplies a real resolver or peer connection.
    await page.goto(origin+'/indexes?embedded=1',{waitUntil:'networkidle'});
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await page.evaluate(()=>{window.timelineNavigations=[];window.addEventListener('message',event=>{if(event.origin===location.origin&&event.data?.type==='gateway:navigate')window.timelineNavigations.push(event.data.address);});});
    await page.locator('#timeline-height').fill('500000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.deepEqual(await page.evaluate(()=>window.timelineNavigations),[]);
    await page.locator('#timeline-open-entity').click();
    await page.waitForFunction(()=>window.timelineNavigations.length===1);
    assert.deepEqual(await page.evaluate(()=>window.timelineNavigations),['500000.bitcoin']);
    noMutations();
    assert.deepEqual(unexpected, [], 'All browser traffic stays in the isolated read-only fixture');
    assert.deepEqual(errors, [], 'No uncaught browser errors');
    console.log('Index timeline browser PASS: source-identical sparse/gapped million-height fixture, bounded DOM, Headers-first tracks, exact read-only range/block/keyboard navigation, focused-vs-Fit tip growth, locked controls, theme/narrow layout, status recovery, explicit range planning and Explorer navigation.');
    if (out) fs.writeFileSync(path.join(out,'acceptance.json'), JSON.stringify({result:'PASS',scope:'Source-identical UI with synthetic sparse range/API fixtures; no real indexing or network peers',screenshots:true,calls},null,2)+'\n');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
}
run().catch(error => {console.error(error);process.exitCode=1;});
