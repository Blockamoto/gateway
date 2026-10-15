'use strict';
// Shipped timeline assets with isolated scheduling and parent-count fixtures.
// No user profile, Bitcoin provider, peer or index store is contacted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const root = path.resolve(__dirname, '..');
const outputArg = process.argv.indexOf('--out'), output = outputArg < 0 ? null : path.resolve(process.argv[outputArg+1]);
if (output) fs.mkdirSync(output, {recursive:true});
const pw = (() => { try { return require('playwright'); } catch { return require(path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright')); } })();
const browserPath = [process.env.GATEWAY_TEST_BROWSER, pw.chromium.executablePath(), ...['ProgramFiles(x86)', 'ProgramFiles', 'LOCALAPPDATA'].flatMap(key => process.env[key] ? [path.join(process.env[key], 'Microsoft/Edge/Application/msedge.exe'), path.join(process.env[key], 'Google/Chrome/Application/chrome.exe')] : [])].find(file => file && fs.existsSync(file));
if (!browserPath) throw Error('An existing Chromium browser is required; this test never downloads one.');
const html = `<!doctype html><html data-theme="light"><head><meta charset="utf-8"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/index-timeline.css"></head><body class="indexes-workspace embedded"><main><section id="index-timeline"></section></main><script src="/shell/index-cards.js"></script><script src="/shell/index-transport.js"></script><script src="/shell/index-timeline.js"></script><script src="/shell/index-workspace.js"></script><script>
window.fixtureCalls=[];window.fixtureEntities=[];window.fixtureNotifications=[];
const defs=[{id:'headers',name:'Bitcoin headers',buildable:false},{id:'blocks',name:'Raw blocks',buildable:true},{id:'tx-locator',name:'Transaction Index',buildable:true},{id:'txo-spender',name:'TXIO',buildable:true},{id:'sat-state',name:'Sat ranges',buildable:true},{id:'inscriptions',name:'Inscriptions',buildable:true}].map(d=>({...d,start_height:0,dependencies:[],rules:[],version:1,network:'bitcoin-mainnet'}));
const fixtureState={definitions:defs,instances:[],providers:[],live:[],jobs:[],job:{},run:{schema:1,configured:false,prepared:{consumers:[]},run:null},timeline:{tip_height:999999,target_height:999999,tracks:[{id:'headers',coverage:[{from:0,to:999999}]},{id:'blocks',coverage:[]}]}};
window.fixtureState=fixtureState;
function metadata(value){return {...value,block_hash:'a'.repeat(64),count:value.kind==='block'?20:value.kind==='transaction'?3:[5000000000,0,7][value.outputIndex],entries:[]};}
window.timeline=GatewayIndexTimeline.mount({document,root:document.getElementById('index-timeline'),select:id=>timeline.select(id),notify:(text,error)=>fixtureNotifications.push({text,error}),onFocus:()=>{},onEntity:value=>{fixtureEntities.push(value);timeline.acceptResource(value,metadata(value.kind==='satpoint'?{...value,kind:'output'}:value));},loadHierarchy:async value=>metadata(value),api:async(action,body)=>{
 fixtureCalls.push({action,body:JSON.parse(JSON.stringify(body??null))});
 if(action==='run/prepare'){fixtureState.run.prepared=body;fixtureState.run.configured=true;return fixtureState.run;}
 if(action==='run/plan')return {ready:true,plan_id:'fixture-reviewed-plan',target_height:body.out??999999,request:body,consumers:body.consumers.filter(c=>c.armed).map(c=>({index:c.index,state:'ready',reason:'Verified fixture source',dependencies:[]})),notes:['No live provider is contacted by this browser fixture.']};
 if(action==='run/start'){fixtureState.run.run={id:'fixture-run',state:'running',request:body.request,consumers:body.request.consumers.filter(c=>c.armed).map(c=>({index:c.index,state:'running',height:0}))};return fixtureState.run;}
 if(action==='run/control'){fixtureState.run.run.state=body.action==='pause'?'paused':body.action==='cancel'?'cancelled':'running';return fixtureState.run;}
 throw Error('Unexpected execution boundary '+action);
},refresh:async()=>timeline.render(fixtureState)});
timeline.render(fixtureState);timeline.health(true);timeline.select('blocks');
const inspector=document.getElementById('timeline-inspector'),panel=document.createElement('div');panel.className='timeline-inspector-panel';const text=document.createElement('p');text.textContent='Read-only selection details remain available above the compact timeline.';panel.append(text);while(inspector.firstChild)panel.append(inspector.firstChild);inspector.append(panel);GatewayIndexWorkspace.mountLayout(document.getElementById('index-timeline'));
</script></body></html>`;

async function main() {
  const server = http.createServer((req, res) => {
    if (req.url === '/') { res.setHeader('Content-Type', 'text/html'); res.end(html); return; }
    if (req.url === '/favicon.ico') { res.writeHead(204); res.end(); return; }
    const file = path.resolve(root, 'ui', '.' + new URL(req.url, 'http://fixture').pathname);
    if (!file.startsWith(path.resolve(root, 'ui/shell') + path.sep) || !fs.existsSync(file)) { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', file.endsWith('.js') ? 'application/javascript' : 'text/css'); res.end(fs.readFileSync(file));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await pw.chromium.launch({headless:true, executablePath:browserPath});
    const page = await browser.newPage({viewport:{width:1440, height:1000}, reducedMotion:'reduce'});
    const errors = []; page.on('pageerror', error => errors.push(error.message));
    await page.goto('http://127.0.0.1:' + server.address().port);
    const capture = name => output ? page.screenshot({path:path.join(output,name+'.png')}) : Promise.resolve();
    const geometries=[];
    await page.locator('#timeline-compact-scrubber').waitFor({state:'visible'});
    for (const id of ['timeline-pan-left', 'timeline-pan-right', 'timeline-refresh', 'timeline-expand']) assert.equal(await page.locator('#' + id).count(), 0, id + ' is not a second navigation control');
    assert.equal(await page.locator('#timeline-control-bar,.timeline-control-bar').count(), 1, 'One compact control bar');
    assert.equal(await page.locator('#timeline-transport-slot #timeline-transport').count(), 1, 'Shared transport occupies its intended slot');
    for (const id of ['timeline-run-start','timeline-run-record','timeline-mark-in','timeline-mark-out','timeline-range-handles','timeline-clear-range']) {
      const button = page.locator('#'+id);
      assert((await button.getAttribute('aria-label'))?.length > 3, id+' has a descriptive accessible label');
      assert((await button.getAttribute('title'))?.length > 3, id+' explains its action on hover');
      assert.equal((await button.innerText()).trim(), '', id+' uses a compact symbol instead of a text-heavy label');
    }
    const pull = page.locator('#timeline-collapse'), pullIdentity = await pull.elementHandle();
    async function layout(label) {
      const geometry = await page.evaluate(() => {
        const editor = document.querySelector('.timeline-editor'), e = editor.getBoundingClientRect(), button = document.querySelector('#timeline-collapse').getBoundingClientRect();
        const rows = [...editor.children].map(node => ({node, rect:node.getBoundingClientRect(), style:getComputedStyle(node)})).filter(({rect, style}) => rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden' && !['absolute', 'fixed'].includes(style.position));
        const ordered=rows.map(({rect})=>rect).sort((a,b)=>a.top-b.top);
        return {viewport:innerWidth, pageWidth:document.documentElement.scrollWidth, editorWidth:editor.clientWidth, scrollWidth:editor.scrollWidth, height:e.height, bottomGap:e.bottom-Math.max(...rows.map(({rect}) => rect.bottom)), internalGap:Math.max(0,...ordered.slice(1).map((r,i)=>r.top-ordered[i].bottom)), centerError:Math.abs((button.left+button.right-e.left-e.right)/2), upperHeight:document.querySelector('.timeline-upper-workspace').getBoundingClientRect().height};
      });
      geometries.push({label,...geometry});
      assert(geometry.pageWidth <= geometry.viewport + 1, label + ' page has no horizontal overflow: ' + JSON.stringify(geometry));
      assert(geometry.scrollWidth <= geometry.editorWidth + 1, label + ' timeline has no horizontal overflow: ' + JSON.stringify(geometry));
      assert(geometry.bottomGap <= 12, label + ' compact controls do not reserve an empty lower region: ' + JSON.stringify(geometry));
      assert(geometry.centerError <= 2, label + ' shared pull-tab is centered: ' + JSON.stringify(geometry));
      assert(geometry.upperHeight >= 120, label + ' resource details retain usable space');
      if (await pull.getAttribute('aria-expanded') === 'false') { assert(geometry.height <= 240, label + ' collapsed editor fits its visible controls: ' + JSON.stringify(geometry)); assert(geometry.internalGap <= 12, label+' collapsed controls have no unexplained internal spacer: '+JSON.stringify(geometry)); }
    }
    await layout('Desktop collapsed');
    await capture('desktop-collapsed');
    await pull.click(); assert.equal(await pull.getAttribute('aria-expanded'), 'true');
    assert.equal(await pull.evaluate((node, original) => node === original, pullIdentity), true, 'The same control expands and collapses');
    const txio = page.locator('#timeline-group-txio'), labels = page.locator('.timeline-group[data-group=txio] .timeline-group-labels');
    assert.equal(await labels.isVisible(), true, 'Collapsed TXIO summarizes its child layers');
    await txio.click(); assert.equal(await labels.isVisible(), false, 'Expanded TXIO does not repeat the child-name summary');
    assert.deepEqual(await page.locator('.timeline-track[data-group=txio]').evaluateAll(nodes => nodes.map(n => n.dataset.index)), ['txio-links','txio-values','txio-scripts','txio-sat-ranges'], 'Sat ranges stays adjacent to the other TXIO children');
    await capture('desktop-expanded');
    await txio.click(); assert.equal(await labels.isVisible(), true, 'Collapsing TXIO restores its child summary');
    await page.locator('.timeline-track[data-index=blocks] label[data-switch=arm]').click();
    await page.locator('.timeline-track[data-index=tx-locator] label[data-switch=arm]').click();
    await pull.click(); assert.equal(await pull.getAttribute('aria-expanded'), 'false');
    await page.evaluate(()=>timeline.setGlobalRange(20,80));
    await page.getByRole('button',{name:'Play: Review finite indexing',exact:true}).click();
    assert.equal((await page.evaluate(() => fixtureCalls)).some(c => c.action === 'run/start'), false, 'Play first reviews, rather than starting a build');
    const playPlan = await page.evaluate(() => fixtureCalls.filter(c=>c.action==='run/plan').at(-1).body);
    assert.deepEqual(playPlan.consumers.filter(c=>c.armed).map(c=>[c.index,c.live]), [['blocks',false],['tx-locator',false]], 'Play prepares a finite pass for armed consumers');
    await page.getByRole('button',{name:'Record: Review Live indexing',exact:true}).click();
    assert.equal((await page.evaluate(() => fixtureCalls)).some(c => c.action === 'run/start'), false, 'Switching to Record requires its own review');
    const recordPlan = await page.evaluate(() => fixtureCalls.filter(c=>c.action==='run/plan').at(-1).body);
    assert.deepEqual(recordPlan.consumers.filter(c=>c.armed).map(c=>[c.index,c.live]), [['blocks',true],['tx-locator',true]], 'Record prepares Live on armed eligible consumers');
    assert.equal(recordPlan.in,20); assert.equal(recordPlan.out,80,'Record preserves the user’s hard Out point');
    await page.getByRole('button',{name:'Record: Start reviewed indexing',exact:true}).click();
    assert.equal((await page.evaluate(() => fixtureCalls)).filter(c=>c.action==='run/start').length, 1, 'Matching reviewed Record starts exactly one coordinated run');
    await page.getByRole('button',{name:'Stop indexing',exact:true}).click();
    const startsBeforeNavigation = 1;

    // Read the actual visible ruler ticks. The expected destination does not
    // recompute or import the implementation's spacing algorithm.
    async function visibleNotches() { return page.locator('.timeline-ruler-tick').evaluateAll(nodes => [...new Set(nodes.filter(n => n.getBoundingClientRect().width > 0).map(n => Number(n.dataset.height)))].sort((a,b) => a-b)); }
    const notches = await visibleNotches(); assert(notches.length >= 4, 'A useful rendered ruler is available');
    const middle = Math.floor(notches.length / 2), left = notches[middle-1], right = notches[middle];
    await page.evaluate(position => timeline.focus(position, true), left+1);
    await page.getByRole('button',{name:'Next ruler notch',exact:true}).click();
    assert.equal(await page.evaluate(() => timeline.getNavigationState().position), right, 'Skip forward lands on the next visible ruler notch');
    await page.getByRole('button',{name:'Previous ruler notch',exact:true}).click();
    assert.equal(await page.evaluate(() => timeline.getNavigationState().position), left, 'Skip backward lands on the previous visible ruler notch');
    await page.locator('#timeline-compact-scrubber').focus(); await page.keyboard.press('Shift+ArrowRight');
    assert.equal(await page.evaluate(() => timeline.getNavigationState().position), right, 'Shift+Arrow uses the same ruler skip');
    const special = await page.locator('.timeline-ruler-tick.halving,.timeline-ruler-tick.difficulty').evaluateAll(nodes => nodes.map(n => Number(n.dataset.height)).filter(n => n > 0));
    if (special.length) { await page.evaluate(position => { timeline.focus(position-1, true); timeline.skipNotch(1); }, special[0]); assert.equal(await page.evaluate(() => timeline.getNavigationState().position), special[0], 'Special Bitcoin boundary ticks are navigation notches too'); }

    // Handles must create useful typed marks without requiring an existing
    // selection. Their exact values vary with the visible parent and zoom.
    async function createMarks(kind) {
      if (!await page.locator('#timeline-clear-range').isDisabled()) await page.locator('#timeline-clear-range').click();
      assert.deepEqual(await page.evaluate(() => timeline.getGlobalMarks()), {in:null, out:null});
      await page.locator('#timeline-range-handles').click();
      const value = await page.evaluate(() => ({marks:timeline.getGlobalMarks(), view:timeline.getView(), nav:timeline.getNavigationState()}));
      const position = point => kind === 'block' ? point.height : kind === 'transaction' ? point.txIndex : kind === 'output' ? point.outputIndex : point.satOffset;
      assert(value.marks.in && value.marks.out, kind + ' Handles creates both global points');
      assert(position(value.marks.in) >= value.view.from && position(value.marks.out) <= value.view.to, kind + ' marks are inside the visible bounds');
      assert(position(value.marks.in) < position(value.marks.out), kind + ' handles are separated and ordered');
      const span=value.view.to-value.view.from,tolerance=1/span+.02;
      assert(Math.abs((position(value.marks.in)-value.view.from)/span-.2)<=tolerance&&Math.abs((position(value.marks.out)-value.view.from)/span-.8)<=tolerance,kind+' handles leave useful room near each end of the visible range');
      assert.equal(value.marks.in.kind, kind); assert.equal(value.marks.out.kind, kind);
      if (kind !== 'block') { assert.equal(value.marks.in.height, 42); assert.equal(value.marks.out.height, 42); }
      assert.equal(await page.locator('.timeline-compact .timeline-global-handle').count(), 2, kind + ' exposes two draggable handles');
      assert.equal((await page.evaluate(() => fixtureCalls)).filter(c => c.action === 'run/start').length, startsBeforeNavigation, 'Creating handles starts no indexing');
      return value.marks;
    }
    await createMarks('block');
    await page.evaluate(() => { timeline.returnParent(-1); timeline.focus(42,true); timeline.zoomIntoFocus(); });
    await page.locator('#timeline-clear-range').click(); await page.locator('#timeline-range-handles').click();
    assert.deepEqual(await page.evaluate(()=>timeline.getGlobalMarks()),{in:null,out:null},'A single visible position does not fabricate two distinct handles');
    assert.match((await page.evaluate(()=>fixtureNotifications)).at(-1).text,/two separate positions|zoom out/i,'An unavailable handle range is explained');
    await page.evaluate(() => { timeline.returnParent(-1); timeline.focus(42,true); timeline.zoomIntoFocus(); timeline.zoomIntoFocus(); });
    await page.waitForFunction(() => timeline.getHierarchy().length === 1); await createMarks('transaction');
    await page.locator('#timeline-compact-scrubber').focus(); await page.keyboard.press('Home'); await page.keyboard.press('Enter'); await page.keyboard.press('Enter');
    await page.waitForFunction(() => timeline.getHierarchy().length === 2); await createMarks('output');
    await page.locator('#timeline-compact-scrubber').focus(); await page.keyboard.press('Home'); await page.keyboard.press('Enter'); await page.keyboard.press('Enter');
    await page.waitForFunction(() => timeline.getHierarchy().length === 3); const satMarks = await createMarks('satpoint');
    assert.equal(satMarks.in.txIndex, 0); assert.equal(satMarks.in.outputIndex, 0); assert(satMarks.out.satOffset > 1000000, 'Five-billion-sat parent uses exact output-relative offsets');
    const satNotches = await visibleNotches(); assert(satNotches.length >= 3);
    await page.evaluate(position => { timeline.step(position-timeline.getNavigationState().position); timeline.skipNotch(1); }, satNotches[1]-1);
    assert.equal(await page.evaluate(() => timeline.getNavigationState().position), satNotches[1], 'Deep skip uses Sat ruler units');
    await page.evaluate(() => { timeline.step(4999999999-timeline.getNavigationState().position); timeline.skipNotch(1); });
    assert.equal(await page.evaluate(() => timeline.getNavigationState().position), 4999999999, 'Skip clamps to the exact final Sat');
    assert.equal(await page.getByRole('button',{name:'Next ruler notch',exact:true}).isDisabled(),true,'Next notch is unavailable at the parent bound');

    const help = page.getByLabel('Timeline navigation help', {exact:true});
    await help.click(); assert.equal(await page.locator('.timeline-help').evaluate(n => n.open), true);
    await page.locator('.timeline-inspector-panel').click({position:{x:5,y:5}}); assert.equal(await page.locator('.timeline-help').evaluate(n => n.open), false, 'Clicking outside closes help');
    const hierarchyBeforeHelpEscape=await page.evaluate(()=>timeline.getHierarchy());
    await help.click(); await page.keyboard.press('Escape'); assert.equal(await page.locator('.timeline-help').evaluate(n => n.open), false, 'Escape closes help without changing navigation');
    assert.deepEqual(await page.evaluate(()=>timeline.getHierarchy()),hierarchyBeforeHelpEscape,'Dismissing help does not also ascend the hierarchy');
    await page.setViewportSize({width:390,height:844}); await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))); await layout('Narrow collapsed');
    await capture('narrow-collapsed');
    await help.click();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const helpGeometry = await page.locator('.timeline-instructions').evaluate(n => { const r=n.getBoundingClientRect(); return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:innerWidth,height:innerHeight,client:n.clientHeight,scroll:n.scrollHeight,overflow:getComputedStyle(n).overflowY}; });
    assert(helpGeometry.left >= 0 && helpGeometry.right <= helpGeometry.width+1 && helpGeometry.top >= 0 && helpGeometry.bottom <= helpGeometry.height+1, 'Help stays inside the narrow viewport: '+JSON.stringify(helpGeometry));
    assert(['auto','scroll'].includes(helpGeometry.overflow), 'Long help can scroll');
    await capture('narrow-help');
    // Enlarged text exercises the actual overflow path, rather than merely
    // inspecting an overflow:auto declaration while short content happens to fit.
    const beforeHelpScroll=await page.evaluate(()=>timeline.getView());
    await page.locator('.timeline-instructions').evaluate(n=>{n.style.fontSize='22px';n.style.lineHeight='1.6';});
    const longHelp=await page.locator('.timeline-instructions').evaluate(n=>({client:n.clientHeight,scroll:n.scrollHeight}));
    assert(longHelp.scroll>longHelp.client,'Enlarged long help has a bounded scrollable content region');
    await page.locator('.timeline-instructions').hover();await page.mouse.wheel(0,350);await page.waitForTimeout(80);
    assert(await page.locator('.timeline-instructions').evaluate(n=>n.scrollTop>0),'Wheel input scrolls the help content');
    assert.deepEqual(await page.evaluate(()=>timeline.getView()),beforeHelpScroll,'Scrolling help does not zoom or pan the timeline');
    await capture('narrow-help-long');
    await page.keyboard.press('Escape'); await pull.click(); await layout('Narrow expanded'); await capture('narrow-expanded'); await pull.click();
    const damagedReason='Saved run controls are damaged; review recovery without changing committed indexes.';
    await page.evaluate(reason=>{fixtureState.run.error=reason;timeline.render(fixtureState);},damagedReason);
    for(const id of ['timeline-run-start','timeline-run-record']){
      const control=page.locator('#'+id);assert.equal(await control.isDisabled(),true,id+' refuses execution with damaged run controls');
      assert((await control.getAttribute('title')).includes(damagedReason),id+' exposes the specific unavailable reason');
    }
    const statusIcon=page.locator('#timeline-run-status-icon');assert.equal(await statusIcon.isVisible(),true,'Damaged controls have a visible status indicator');
    assert((await statusIcon.getAttribute('title')).includes(damagedReason));assert((await statusIcon.getAttribute('aria-label')).includes(damagedReason));
    await statusIcon.focus();assert.equal(await statusIcon.evaluate(n=>n===n.ownerDocument.activeElement),true,'The status explanation is reachable without a pointer');
    assert(await page.locator('#timeline-run-state').evaluate(n=>n.getBoundingClientRect().height<=2),'The accessible status does not duplicate a visible explanatory row');
    await capture('narrow-damaged-status');
    assert.equal((await page.evaluate(()=>fixtureCalls)).filter(c=>c.action==='run/start').length,startsBeforeNavigation,'Rendering damaged state starts no work');
    assert.deepEqual(errors, [], 'No runtime page errors');
    console.log('Timeline review browser PASS: compact controls, one centered expander, responsive geometry, visible-notch skipping, deep typed Handles and bounded dismissible help.');
    if(output)console.log(JSON.stringify({geometries,helpGeometry,output}));
  } finally { await browser?.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
}
main().catch(error => { console.error(error); process.exitCode=1; });
