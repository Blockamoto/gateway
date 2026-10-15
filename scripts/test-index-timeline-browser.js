'use strict';
// Browser acceptance against source-identical UI and deliberately sparse API
// fixtures. No indexing, block fetches, peers or outside services are contacted.
const assert = require('node:assert/strict');
const {selectHeight}=require('./timeline-browser-gestures');
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
      if(url.pathname==='/api/v1/navigate')return reply({error:'Offline block fixture'},'application/json',503);
      if (url.pathname === '/api/v1/index/status') return unavailable ? reply({error:'Fixture unavailable'}, 'application/json', 503) : reply(snapshot);
      if (url.pathname === '/api/v1/index/query') return reply({rows:[], total:0, total_known:true, inspected_blocks:0, chain_state:'selected_chain'});
      unexpected.push(call.method + ' ' + call.path);
      return reply({error:'Unexpected fixture action'}, 'application/json', 500);
    });

    const screenshot = async name => {if (out) await page.screenshot({path:path.join(out,name+'.png'),fullPage:true});};
    const refresh = async (preserveFocus=false) => {
      const response = page.waitForResponse(response => response.url() === origin + '/api/v1/index/status');
      if(preserveFocus)await page.locator('#timeline-refresh').evaluate(button=>button.click());
      else await page.locator('#timeline-refresh').click();
      await response;
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    };
    const noMutations = () => assert.equal(calls.filter(call => call.method !== 'GET' && call.path !== '/api/v1/navigate').length, 0, 'Exploration cannot alter indexing, Live, sharing or network settings');
    const viewport = () => page.locator('#index-timeline').evaluate(node => ({from:Number(node.dataset.from),to:Number(node.dataset.to),fit:node.dataset.fit==='true'}));
    const blockLane = page.locator('.timeline-lane[data-index="blocks"]');
    const clickBlock = async height => {
      const view = await viewport(), box = await blockLane.boundingBox();
      assert(height>=view.from&&height<=view.to,'Clicked block must lie inside the actual viewport');
      const x=box.x+(height-view.from+.5)/(view.to-view.from+1)*box.width,y=box.y+40;
      // A selection marker occupies this exact point after Ctrl-click. Real
      // pointer input reaches that marker for the next modifier and explore.
      await page.keyboard.down('Control');await page.mouse.click(x,y);await page.keyboard.up('Control');
      await page.keyboard.down('Shift');await page.mouse.click(x,y);await page.keyboard.up('Shift');
      await page.mouse.click(x,y);
    };
    await page.goto(origin+'/indexes', {waitUntil:'networkidle'});
    await page.getByRole('button',{name:'Expand index tracks',exact:true}).click();
    await page.locator('.timeline-track[data-index="blocks"]').waitFor();
    assert.deepEqual(await page.locator('.timeline-track').evaluateAll(nodes=>nodes.map(n=>n.dataset.index)),['headers','blocks'],'Every available built-in is present, locked recipes stay excluded');
    assert.equal(await page.locator('#timeline-add-index').isDisabled(),true);
    assert.equal(await page.locator('#timeline-expand').count(),0,'One stable disclosure owns both states');
    assert.deepEqual(await viewport(),{from:0,to:999999,fit:true});
    const physical=[{from:0,to:9},{from:499990,to:499999},{from:500003,to:500010},{from:999990,to:999999}];
    assert.deepEqual(await blockLane.evaluate(node=>JSON.parse(node.dataset.coverage)),physical,'Physical coverage excludes gaps and ignores provider/processing claims');
    assert(await page.locator('*').count()<1300,'Million-height chain uses bounded DOM');
    assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).count(),0,'Inspector has no duplicate execution switches');
    assert.equal(await page.locator('.timeline-track[data-index=blocks] [data-control=arm]').isChecked(),false,'Existing data does not automatically arm a consumer');
    noMutations();await screenshot('timeline-chain-light');
    await selectHeight(page,500000,'blocks');
    const focused=await viewport();assert.equal(focused.fit,false);assert(focused.from<=500000&&focused.to>=500000);
    snapshot.timeline.tip_height=1000009;snapshot.timeline.target_height=1000009;snapshot.timeline.tracks[0].coverage[0].to=1000009;
    await refresh();assert.deepEqual(await viewport(),focused,'Tip growth preserves deliberate focused viewport');
    await page.locator('#timeline-fit').click();assert.equal((await viewport()).to,1000009);
    snapshot.timeline.tip_height=1000010;snapshot.timeline.target_height=1000010;snapshot.timeline.tracks[0].coverage[0].to=1000010;
    await refresh();assert.deepEqual(await viewport(),{from:0,to:1000010,fit:true});
    await blockLane.focus();await page.keyboard.press('Home');assert.equal(await page.locator('#index-timeline').getAttribute('data-playhead'),'0','Home moves to first item rather than starting work');
    await page.keyboard.press('Shift+ArrowRight');assert.equal(await page.locator('#index-timeline').getAttribute('data-playhead'),'10');
    await page.keyboard.press('ArrowLeft');assert.equal(await page.locator('#index-timeline').getAttribute('data-playhead'),'9');
    await refresh(true);assert.equal(await blockLane.evaluate(n=>document.activeElement===n),true,'Status redraw preserves lane focus');
    await page.keyboard.press('+');const zoomed=await viewport();assert(zoomed.to-zoomed.from<1000010);
    await page.locator('#timeline-pan-right').click();assert((await viewport()).from>zoomed.from);
    await page.locator('#timeline-pan-left').click();
    for(const mode of ['light','dark']){theme=mode;await page.evaluate(mode=>document.documentElement.dataset.theme=mode,mode);for(const width of [1440,390]){await page.setViewportSize({width,height:width===390?844:1100});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await screenshot('timeline-'+mode+'-'+width);}}
    await page.setViewportSize({width:1440,height:1100});
    const saved=snapshot.timeline.tracks.splice(1,1)[0];await refresh();assert.deepEqual(await blockLane.evaluate(n=>JSON.parse(n.dataset.coverage)),[],'Missing physical projection cannot fall back to committed processing coverage');
    snapshot.timeline.tracks.splice(1,0,saved);await refresh();
    unavailable=true;await refresh();assert.equal(await page.locator('#timeline-run-start').isDisabled(),true,'Unavailable status cannot authorize Start');
    unavailable=false;await refresh();assert.equal(await page.locator('#timeline-run-start').isDisabled(),false);
    await page.getByRole('button',{name:'Collapse index tracks',exact:true}).click();assert.equal(await page.locator('#timeline-compact-scrubber').isVisible(),true);assert.equal(await page.locator('#timeline-run-start').isVisible(),true,'Collapsed workspace retains shared transport');
    await page.getByRole('button',{name:'Expand index tracks',exact:true}).click();
    noMutations();assert.equal(calls.filter(c=>c.path.endsWith('/query')).length,0,'Navigation is independent of index records');
    assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);
    console.log('Index timeline browser PASS: physical sparse/gapped million-height coverage, bounded default tracks, stable disclosure, navigation keys, focused/fit growth, theme/narrow layout, status recovery, no duplicate execution or implicit work.');
    if (out) fs.writeFileSync(path.join(out,'acceptance.json'), JSON.stringify({result:'PASS',scope:'Source-identical UI with synthetic sparse range/API fixtures; no real indexing or network peers',screenshots:true,calls},null,2)+'\n');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
}
run().catch(error => {console.error(error);process.exitCode=1;});
