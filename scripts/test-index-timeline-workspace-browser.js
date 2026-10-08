'use strict';
// Pointer, keyboard and layout acceptance against the actual source UI and
// isolated API fixtures. No user profile, peer or external service is contacted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const root = path.resolve(__dirname, '..');
const outputArg = process.argv.indexOf('--out');
const out = outputArg < 0 ? null : path.resolve(process.argv[outputArg + 1]);
if (out) fs.mkdirSync(out, {recursive:true});
const pw = (() => {try {return require('playwright');} catch {return require(path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const browserPath = [process.env.GATEWAY_TEST_BROWSER, pw.chromium.executablePath(), ...['ProgramFiles(x86)', 'ProgramFiles', 'LOCALAPPDATA'].flatMap(key => process.env[key] ? [path.join(process.env[key], 'Microsoft/Edge/Application/msedge.exe'), path.join(process.env[key], 'Google/Chrome/Application/chrome.exe')] : [])].find(file => file && fs.existsSync(file));
const base = {version:1, network:'bitcoin-mainnet', start_height:0, arbitrary_start:true, rules:[], dependencies:['headers'], buildable:true};
function fixture() {
  return {
    definitions:[{...base,id:'blocks',name:'Bitcoin blocks',theory:'Stored raw blocks.'}, {...base,id:'headers',name:'Bitcoin headers',buildable:false,dependencies:[]}, {...base,id:'inscriptions',name:'Inscriptions',locked:true,lock_reason:'Locked in this testing build.'}],
    instances:[], providers:[], live:[], jobs:[], job:{}, errors:[],
    timeline:{tip_height:999999,target_height:999999,tracks:[{id:'headers',coverage:[{from:0,to:999999}],gaps:[]},{id:'blocks',coverage:[],gaps:[],coverage_complete:true}]}
  };
}
async function run() {
  const server = http.createServer((req,res) => {res.writeHead(500);res.end('Unexpected unmocked request');});
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const browser = await pw.chromium.launch({headless:true,executablePath:browserPath});
  try {
    const context = await browser.newContext({viewport:{width:1440,height:1000}, reducedMotion:'reduce',hasTouch:true});
    const page = await context.newPage();
    const errors=[], calls=[], unexpected=[];
    let snapshot=fixture();
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/*', async route => {
      const req=route.request(), url=new URL(req.url());
      const reply=(body,type='application/json',status=200) => route.fulfill({status,contentType:type,body:type==='application/json'?JSON.stringify(body):body});
      if(url.origin!==origin){unexpected.push(req.method()+' '+url.origin+url.pathname);return route.abort();}
      if(url.pathname==='/indexes')return reply(fs.readFileSync(path.join(root,'ui/shell/indexes.html'),'utf8').replaceAll('__INDEX_NONCE__','fixture'),'text/html');
      if(url.pathname==='/embedded-fixture')return reply('<!doctype html><html><body style="margin:0;height:100dvh;display:grid;grid-template-rows:60px minmax(0,1fr);overflow:hidden"><header>Disposable host layout</header><iframe title="Indexes" src="/indexes?embedded=1" style="width:100%;height:100%;min-height:0;border:0;display:block"></iframe></body></html>','text/html');
      if(url.pathname.startsWith('/shell/')){
        const file=path.resolve(root,'ui','.'+url.pathname);
        assert(file.startsWith(path.resolve(root,'ui/shell')+path.sep));
        return reply(fs.readFileSync(file),url.pathname.endsWith('.js')?'application/javascript':'text/css');
      }
      if(url.pathname==='/favicon.ico')return reply('','image/x-icon');
      if(url.pathname==='/api/v1/appearance')return reply({theme:'light',default:'light'});
      if(url.pathname==='/api/v1/navigate')return reply({error:'Offline block fixture'},'application/json',503);
      const body=req.method()==='POST'?req.postDataJSON():null;
      calls.push({method:req.method(),path:url.pathname,body});
      if(url.pathname==='/api/v1/index/status')return reply(snapshot);
      if(url.pathname==='/api/v1/index/query')return reply({rows:[],total:0,total_known:true,inspected_blocks:0,chain_state:'selected_chain'});
      if(url.pathname==='/api/v1/index/plan')return reply({definition:snapshot.definitions.find(d=>d.id===body.index),from:body.from,to:body.to??snapshot.timeline.tip_height,retention:body.retention,dependencies:[],notes:['Disposable plan only.'],storage_estimate:'Fixture',verification:'selected_chain'});
      unexpected.push(req.method()+' '+url.pathname);
      return reply({error:'Unexpected fixture action'},'application/json',500);
    });
    const settle=() => page.evaluate(() => new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const view=() => page.locator('#index-timeline').evaluate(el=>({from:Number(el.dataset.from),to:Number(el.dataset.to),fit:el.dataset.fit==='true'}));
    const span=value=>value.to-value.from+1;
    const refresh=async()=>{const response=page.waitForResponse(r=>r.url()===origin+'/api/v1/index/status');await page.locator('#timeline-refresh').click();await response;await settle();};
    const shot=async name=>{if(out)await page.screenshot({path:path.join(out,name+'.png'),fullPage:true});};
    const drag=async(locator,dx,dy=0)=>{const box=await locator.boundingBox();assert(box,'Drag target is visible');const x=box.x+box.width/2,y=box.y+box.height/2;await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+dx,y+dy,{steps:8});await page.mouse.up();await settle();};
    const onlyPlans=()=>assert.deepEqual(calls.filter(c=>c.method==='POST'&&!c.path.endsWith('/plan')),[],'UI navigation, adding tracks and preparing ranges never start indexing');
    const rootBox=()=>page.locator('#index-timeline').boundingBox();
    const editorBox=()=>page.locator('.timeline-editor').boundingBox();
    const horizontal='#timeline-scrollbar-horizontal';
    await page.goto(origin+'/indexes',{waitUntil:'networkidle'});
    await page.locator('#timeline-divider').waitFor();
    assert.deepEqual(await page.locator('.timeline-track').evaluateAll(nodes=>nodes.map(n=>n.dataset.index)),['headers','blocks'],'Headers and Blocks are default tracks even without stored blocks');
    await page.locator('#timeline-add-index').click();
    assert.equal(await page.locator('#timeline-index-picker button[data-index="inscriptions"]').isDisabled(),true);
    await page.locator('#timeline-add-index').click();
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await page.locator('#timeline-details').evaluate(el=>el.open=true);
    assert.deepEqual(await page.locator('.timeline-track').evaluateAll(nodes=>nodes.map(n=>n.dataset.index)),['headers','blocks']);
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-index'),'blocks');
    assert.equal(await page.locator('#timeline-details').evaluate(el=>el.open),true,'Adding an index opens its settings without starting work');
    assert.equal(calls.some(c=>c.method==='POST'),false);
    await refresh();assert.equal(await page.locator('.timeline-track[data-index="blocks"]').count(),1);
    await page.reload({waitUntil:'networkidle'});assert.equal(await page.locator('.timeline-track[data-index="blocks"]').count(),1,'An added index with empty coverage survives reload');
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();

    // Expanding settings scrolls inside the inspector; workspace stays docked.
    const docked=await editorBox();
    await page.locator('#timeline-details').evaluate(el=>el.open=true);await settle();
    assert.deepEqual(await editorBox(),docked,'Inspector expansion cannot move or resize the timeline');
    const geometry=await page.evaluate(()=>({height:innerHeight,scrollHeight:document.documentElement.scrollHeight,scrollY,inspectorScrollable:document.querySelector('#timeline-inspector-panel').scrollHeight>document.querySelector('#timeline-inspector-panel').clientHeight}));
    assert(geometry.scrollHeight<=geometry.height+1&&geometry.scrollY===0,'The document is a fixed workspace');
    assert(geometry.inspectorScrollable,'Long inspector content has its own scroll area');
    assert(docked.y+docked.height<=geometry.height&&docked.y+docked.height>geometry.height-40,'Timeline fills the lower workspace');
    const divider=page.locator('#timeline-divider');
    assert.equal(await divider.getAttribute('aria-orientation'),'horizontal');
    await drag(divider,0,-70);const grown=await editorBox();assert(grown.height>docked.height+50);
    await divider.focus();await page.keyboard.press('ArrowDown');assert((await editorBox()).height<grown.height);
    const resized=await editorBox();
    await page.reload({waitUntil:'networkidle'});assert(Math.abs((await editorBox()).height-resized.height)<2,'Divider ratio persists across reload');
    await divider.focus();await page.keyboard.press('Home');assert.equal(Number(await divider.getAttribute('aria-valuenow')),Number(await divider.getAttribute('aria-valuemin')));
    await page.keyboard.press('End');assert.equal(Number(await divider.getAttribute('aria-valuenow')),Number(await divider.getAttribute('aria-valuemax')));
    await divider.dblclick();await settle();

    // The two horizontal edges independently anchor the opposite boundary.
    await page.locator('#timeline-fit').click();
    const barBox=await page.locator(horizontal).boundingBox();
    await drag(page.locator(horizontal+' .end'),-barBox.width*.2);
    const endZoom=await view();assert.equal(endZoom.from,0);assert(endZoom.to>700000&&endZoom.to<900000);
    await drag(page.locator(horizontal+' .start'),barBox.width*.15);
    const startZoom=await view();assert.equal(startZoom.to,endZoom.to,'Left handle preserves right endpoint');assert(startZoom.from>100000);
    await drag(page.locator(horizontal+' .timeline-scroll-thumb'),barBox.width*.05);
    const thumbPan=await view();assert.equal(span(thumbPan),span(startZoom));assert(thumbPan.from>startZoom.from);
    assert.equal(await page.locator(horizontal+' .timeline-scroll-thumb').getAttribute('aria-orientation'),'horizontal');
    await page.locator(horizontal+' .start').focus();await page.keyboard.press('ArrowRight');assert((await view()).from>thumbPan.from,'Zoom handle is keyboard operable');

    const lane=page.locator('.timeline-lane[data-index="blocks"]');
    const pointer=async fraction=>{const box=await lane.boundingBox();const p={x:box.x+box.width*fraction,y:box.y+Math.min(30,box.height/2)};await page.mouse.move(p.x,p.y);return p;};
    await lane.evaluate(el=>el.addEventListener('wheel',event=>{window.acceptanceWheelX=event.clientX;},{once:true}));
    await pointer(.73);const beforeWheel=await view();
    await page.keyboard.down('Control');await page.mouse.wheel(0,-100);await page.keyboard.up('Control');await settle();
    const afterWheel=await view();assert(span(afterWheel)<span(beforeWheel));
    const wheelAnchor=await lane.evaluate(el=>(window.acceptanceWheelX-el.getBoundingClientRect().left)/el.getBoundingClientRect().width);
    assert(Math.abs((beforeWheel.from+span(beforeWheel)*wheelAnchor)-(afterWheel.from+span(afterWheel)*wheelAnchor))<=3,'Wheel zoom preserves the block beneath the pointer');
    await pointer(.5);const beforePan=await view();await page.keyboard.down('Shift');await page.mouse.wheel(0,120);await page.keyboard.up('Shift');await settle();
    assert.equal(span(await view()),span(beforePan));assert((await view()).from>beforePan.from,'Shift-wheel pans horizontally');
    const beforeTrackpad=await view();await page.mouse.wheel(-80,0);await settle();assert((await view()).from<beforeTrackpad.from,'Horizontal trackpad delta pans');
    const thumb=await page.locator(horizontal+' .timeline-scroll-thumb').boundingBox();await page.mouse.move(thumb.x+thumb.width/2,thumb.y+thumb.height/2);
    const beforeBarWheel=await view();await page.keyboard.down('Control');await page.mouse.wheel(0,-60);await page.keyboard.up('Control');await settle();
    assert(span(await view())<span(beforeBarWheel),'Ctrl-wheel on the custom scrollbar zooms the timeline');

    // Space grab must not inspect a block, and must release on cancel/blur.
    await page.locator('.timeline-track[data-index="headers"] .timeline-track-select').click();
    const selection=await page.locator('#timeline-inspector').evaluate(el=>({id:el.dataset.index,kind:el.dataset.kind}));
    await lane.focus();const beforeGrab=await view();await page.keyboard.down('Space');await drag(lane,80);await page.keyboard.up('Space');
    assert((await view()).from<beforeGrab.from);assert.deepEqual(await page.locator('#timeline-inspector').evaluate(el=>({id:el.dataset.index,kind:el.dataset.kind})),selection,'A pan release does not also select the dragged track');
    const delayed=await lane.boundingBox();await lane.focus();await page.keyboard.down('Space');await page.mouse.move(delayed.x+delayed.width*.5,delayed.y+30);await page.mouse.down();await page.mouse.move(delayed.x+delayed.width*.6,delayed.y+30,{steps:4});
    await page.keyboard.up('Space');await page.waitForTimeout(650);await page.mouse.up();await settle();
    assert.deepEqual(await page.locator('#timeline-inspector').evaluate(el=>({id:el.dataset.index,kind:el.dataset.kind})),selection,'Releasing Space before a delayed mouseup cannot produce a selection');
    assert.equal(await page.locator('#index-timeline').getAttribute('data-hand'),null);
    await page.locator('#timeline-height').fill('123');await page.locator('#timeline-height').press('Space');assert.equal(await page.locator('#timeline-height').inputValue(),'123 ','Typing into inputs never activates grab mode');
    await page.locator('#timeline-height').fill('');
    await lane.focus();await page.keyboard.down('Space');await page.evaluate(()=>window.dispatchEvent(new Event('blur')));await page.keyboard.up('Space');
    assert.equal(await page.locator('#index-timeline').getAttribute('data-hand'),null);
    assert.equal(await page.locator('#index-timeline').getAttribute('data-dragging'),null);

    // Actual Chromium touch events exercise midpoint anchoring, distinct from
    // the Ctrl-wheel events a desktop precision trackpad normally produces.
    await page.locator('#timeline-fit').click();await pointer(.5);
    await page.keyboard.down('Control');await page.mouse.wheel(0,-100);await page.keyboard.up('Control');await settle();
    const touchBox=await lane.boundingBox(), mid=touchBox.x+touchBox.width*.6, y=touchBox.y+touchBox.height/2;
    const cdp=await context.newCDPSession(page), beforePinch=await view();
    const touch=(a,b)=>[{x:mid-a,y,id:1},{x:mid+b,y,id:2}];
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:touch(35,35)});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:touch(65,65)});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await settle();
    const afterPinch=await view();assert(span(afterPinch)<span(beforePinch),'Two-finger touch pinch zooms the chain');
    assert(Math.abs(beforePinch.from+span(beforePinch)*.6-afterPinch.from-span(afterPinch)*.6)<=3,'Touch zoom anchors at finger midpoint');
    await cdp.detach();

    // Bitcoin landmarks are independent scales, not falsely nested epochs.
    await page.locator('#timeline-fit').click();
    const fitMarks=await page.locator('.timeline-ruler-tick').evaluateAll(nodes=>nodes.map(n=>({height:Number(n.dataset.height),kind:n.classList.contains('halving')?'halving':n.classList.contains('difficulty')?'difficulty':'regular'})));
    assert(fitMarks.some(m=>m.kind==='halving'&&m.height===210000));
    await page.locator('#timeline-height').fill('210000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.equal(await page.locator('.timeline-ruler-tick.difficulty[data-height="210000"]').count(),0,'Halving boundaries are not invented difficulty boundaries');
    await page.locator('#timeline-height').fill('201600');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());
    assert.equal(await page.locator('.timeline-ruler-tick.difficulty[data-height="201600"]').count(),1);
    await shot('workspace-landmarks');

    // Enough established rows to exercise ordinary scrolling and both vertical
    // handles without enabling additional real application features.
    for(let i=0;i<8;i++){
      const id='fixture-index-'+i;snapshot.definitions.push({...base,id,name:'Fixture index '+i});
      snapshot.instances.push({definition:id,checkpoint:{from:0,height:10},coverage:[{from:0,to:10}],verification:'selected_chain'});
    }
    snapshot.timeline.tracks[1]={id:'blocks',coverage:[{from:499990,to:500010}],gaps:[{from:500000,to:500000}],coverage_complete:false};
    await refresh();
    const trackViewport=page.locator('#timeline-track-viewport');
    await lane.scrollIntoViewIfNeeded();await pointer(.5);await page.mouse.wheel(0,220);await settle();assert((await trackViewport.evaluate(el=>el.scrollTop))>0,'Plain wheel scrolls tracks vertically');
    const height=()=>page.locator('#index-timeline').evaluate(el=>Number.parseFloat(el.style.getPropertyValue('--timeline-track-height')));
    await trackViewport.evaluate(el=>el.scrollTop=0);await pointer(.5);const beforeHeight=await height();
    await page.keyboard.down('Alt');await page.mouse.wheel(0,-100);await page.keyboard.up('Alt');await settle();assert.notEqual(await height(),beforeHeight,'Alt-wheel changes track height');
    const vertical='#timeline-scrollbar-vertical';
    const beforeEnd=await height();await drag(page.locator(vertical+' .end'),0,18);assert.notEqual(await height(),beforeEnd,'Bottom vertical handle changes track height');
    await trackViewport.evaluate(el=>el.scrollTop=120);await settle();const beforeStart=await height();await drag(page.locator(vertical+' .start'),0,12);assert.notEqual(await height(),beforeStart,'Top vertical handle changes track height');
    const storedHeight=await height();await page.reload({waitUntil:'networkidle'});assert.equal(await height(),storedHeight,'Track height persists');
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    assert.match(await page.locator('#timeline-selection-note').innerText(),/partial|not been mapped/i);
    assert.deepEqual(await lane.evaluate(el=>JSON.parse(el.dataset.coverage)),[{from:499990,to:499999},{from:500001,to:500010}],'A one-block gap survives partial coverage mapping');
    await page.locator('#timeline-height').fill('500000');await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());assert.match(await page.locator('#timeline-selection-meta').innerText(),/0 \/ 1/);

    // Range handles and numeric fields share one draft; changes invalidate a
    // reviewed plan, but never submit a build or silently fix invalid text.
    await page.locator('#timeline-inspector-tab').click();
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await page.locator('#timeline-details').evaluate(el=>el.open=true);
    await page.locator('#from').fill('200000');await page.locator('#to').fill('700000');
    await page.locator('.build-range-rail').scrollIntoViewIfNeeded();
    for(const theme of ['light','dark']){
      await page.evaluate(mode=>document.documentElement.dataset.theme=mode,theme);
      assert.deepEqual(await page.locator('.build-range-rail input').evaluateAll(nodes=>nodes.map(node=>getComputedStyle(node).backgroundColor)),['rgba(0, 0, 0, 0)','rgba(0, 0, 0, 0)'],'Overlapping range inputs cannot obscure the other handle or rail in '+theme);
    }
    await page.evaluate(()=>document.documentElement.dataset.theme='light');
    const dragRange=async(id,pixels)=>{
      const handle=page.locator(id),value=Number(await handle.inputValue()),min=Number(await handle.getAttribute('min')),max=Number(await handle.getAttribute('max')),box=await handle.boundingBox();
      const x=box.x+8+(box.width-16)*(value-min)/(max-min),y=box.y+box.height/2;
      await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+pixels,y,{steps:6});await page.mouse.up();await settle();
    };
    await dragRange('#range-start',35);assert(Number(await page.locator('#from').inputValue())>200000,'Dragging the first range handle updates the number field');
    assert.equal(await page.locator('#to').inputValue(),'700000');
    await dragRange('#range-end',-35);assert(Number(await page.locator('#to').inputValue())<700000,'Dragging the last range handle updates its number field');
    await shot('workspace-range-handles-light');
    await page.locator('#from').fill('100');await page.locator('#to').fill('200');
    assert.equal(await page.locator('#range-start').inputValue(),'100');assert.equal(await page.locator('#range-end').inputValue(),'200');
    await page.locator('#plan-button').click();await page.waitForFunction(()=>!document.querySelector('#build').disabled);
    await page.locator('#range-start').focus();await page.keyboard.press('ArrowRight');assert.equal(await page.locator('#from').inputValue(),'101');assert.equal(await page.locator('#build').isDisabled(),true,'Moving a range endpoint invalidates reviewed plan');
    await page.locator('#range-end').focus();await page.keyboard.press('ArrowLeft');assert.equal(await page.locator('#to').inputValue(),'199');
    const plans=calls.filter(c=>c.path.endsWith('/plan')).length;
    await page.locator('#to').fill('99');assert.equal(await page.locator('#to').inputValue(),'99');assert.equal(await page.locator('#to').evaluate(el=>el.checkValidity()),false);
    await page.locator('#plan-button').click();assert.equal(calls.filter(c=>c.path.endsWith('/plan')).length,plans,'Invalid reversed input cannot request a plan');
    await page.locator('#to').fill('200');await page.locator('#from').fill('-1');assert.equal(await page.locator('#from').evaluate(el=>el.checkValidity()),false);
    await page.locator('#from').fill('1.5');assert.equal(await page.locator('#from').evaluate(el=>el.checkValidity()),false);
    await page.locator('#from').fill('100');await page.locator('#to').fill('');assert.match(await page.locator('#range-summary').innerText(),/tip/i);
    await page.locator('#initial-live').check();assert.equal(await page.locator('#range-end').isDisabled(),true);await page.locator('#initial-live').uncheck();
    onlyPlans();

    // Recipes whose first block is fixed retain that origin; slider gestures
    // and selecting a timeline range cannot replace it with an arbitrary start.
    snapshot.definitions.push({...base,id:'fixture-fixed',name:'Fixed-start fixture',arbitrary_start:false,start_height:500});
    await refresh();await page.locator('#timeline-add-index').click();await page.locator('#timeline-index-picker button[data-index="fixture-fixed"]').click();
    assert.equal(await page.locator('#from').inputValue(),'500');assert.equal(await page.locator('#from').evaluate(el=>el.readOnly),true);assert.equal(await page.locator('#range-start').isDisabled(),true);
    await page.locator('#to').fill('499');assert.equal(await page.locator('#to').evaluate(el=>el.checkValidity()),false);await page.locator('#to').fill('600');
    await page.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();assert.equal(await page.locator('#from').inputValue(),'100','Switching tracks preserves each draft');
    await page.locator('#timeline-details').evaluate(el=>el.open=true);
    await page.locator('#to').fill('200');await page.locator('#plan-button').click();await page.waitForFunction(()=>!document.querySelector('#build').disabled);
    snapshot.instances.push({definition:'blocks',checkpoint:{from:120,height:150,commitment:'new-local-instance'},coverage:[{from:120,to:150}],verification:'selected_chain'});
    await refresh();
    assert.equal(await page.locator('#from').inputValue(),'120','A newly established checkpoint constrains the currently selected draft on refresh');
    assert.equal(await page.locator('#from').evaluate(el=>el.readOnly),true);assert.equal(await page.locator('#range-start').isDisabled(),true);assert.equal(await page.locator('#range-start').inputValue(),'120');
    assert.equal(await page.locator('#to').inputValue(),'200','New checkpoint preserves the chosen last block');assert.equal(await page.locator('#build').isDisabled(),true,'Changed retained origin invalidates an old reviewed plan');
    await page.locator('#plan-button').click();await page.waitForFunction(()=>!document.querySelector('#build').disabled);assert.equal(calls.filter(c=>c.path.endsWith('/plan')).at(-1).body.from,120);

    // Each theme and small viewport keeps both panes inside the application.
    for(const theme of ['light','dark'])for(const width of [1440,390]){
      await page.evaluate(mode=>document.documentElement.dataset.theme=mode,theme);await page.setViewportSize({width,height:width===390?844:1000});await settle();
      const box=await editorBox(), bounds=await rootBox();
      assert(box.y>=bounds.y&&box.y+box.height<=bounds.y+bounds.height+1);
      assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth&&document.documentElement.scrollHeight<=innerHeight+1),'Fixed workspace remains within '+theme+' '+width);
      await shot('workspace-'+theme+'-'+width);
    }
    await page.setViewportSize({width:1280,height:900});
    await page.goto(origin+'/embedded-fixture',{waitUntil:'networkidle'});
    const frame=page.frameLocator('iframe');await frame.locator('#timeline-divider').waitFor();
    const embeddedBefore=await frame.locator('.timeline-editor').boundingBox();
    await frame.locator('.timeline-track[data-index="blocks"] .timeline-track-select').click();
    await frame.locator('#timeline-details').evaluate(el=>el.open=true);await settle();
    assert.deepEqual(await frame.locator('.timeline-editor').boundingBox(),embeddedBefore,'Fixed-height embedding preserves timeline docking');
    assert(embeddedBefore.y+embeddedBefore.height<=900&&embeddedBefore.y>=60);
    await shot('workspace-embedded');
    onlyPlans();assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);
    console.log('Index timeline workspace gestures PASS: docked layout, persistent splitter, add-index locks, two-edge scrollbar zoom/pan, anchored wheel and touch pinch, Space-grab, vertical scrolling/height, Bitcoin landmarks, range draft validation, themes and embedding.');
    if(out)fs.writeFileSync(path.join(out,'acceptance.json'),JSON.stringify({result:'PASS',scope:'Actual UI, synthetic sparse and empty APIs, disposable browser profile; no external network or real indexing',calls},null,2)+'\n');
  } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
}
run().catch(error=>{console.error(error);process.exitCode=1;});
