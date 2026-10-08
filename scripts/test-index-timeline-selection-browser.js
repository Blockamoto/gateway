'use strict';
// Real Indexes page and real pointer/keyboard events with disposable API data.
// No client profile, Bitcoin peer or externally hosted service is contacted.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const os=require('node:os');
const http=require('node:http');
const root=path.resolve(__dirname,'..');
const outIndex=process.argv.indexOf('--out'),out=outIndex<0?null:path.resolve(process.argv[outIndex+1]);
if(out)fs.mkdirSync(out,{recursive:true});
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const browserPath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
if(!browserPath)throw Error('This test requires an existing Chromium browser and never downloads one.');
const base={version:1,network:'bitcoin-mainnet',start_height:0,arbitrary_start:true,rules:[],dependencies:['headers'],buildable:true};
const snapshot={definitions:[{...base,id:'headers',name:'Bitcoin headers',buildable:false,dependencies:[]},{...base,id:'blocks',name:'Bitcoin blocks'},{...base,id:'inscriptions',name:'Inscriptions',locked:true,lock_reason:'Locked in this build.'}],instances:[],providers:[],live:[],jobs:[],job:{},errors:[],timeline:{tip_height:400,target_height:400,tracks:[{id:'headers',coverage:[{from:0,to:400}],gaps:[]},{id:'blocks',coverage:[],gaps:[],coverage_complete:true}]}};
// The production catalog is much taller than the original three-entry fixture.
for(let i=0;i<18;i++)snapshot.definitions.push({...base,id:'locked-fixture-'+i,name:'Locked future index '+i,locked:true,lock_reason:'Available in a future build after its index has been individually validated.'});

async function run(){
  const server=http.createServer((req,res)=>{res.writeHead(500);res.end('Unexpected unmocked request');});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const origin='http://127.0.0.1:'+server.address().port;
  const browser=await pw.chromium.launch({headless:true,executablePath:browserPath});
  try{
    const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),calls=[],errors=[],unexpected=[];
    let unavailable=false;
    page.on('pageerror',error=>errors.push(error.message));
    await page.route('**/*',async route=>{
      const req=route.request(),url=new URL(req.url()),reply=(body,type='application/json',status=200)=>route.fulfill({status,contentType:type,body:type==='application/json'?JSON.stringify(body):body});
      if(url.origin!==origin){unexpected.push(req.method()+' '+url.origin+url.pathname);return route.abort();}
      if(url.pathname==='/indexes')return reply(fs.readFileSync(path.join(root,'ui/shell/indexes.html'),'utf8').replaceAll('__INDEX_NONCE__','fixture'),'text/html');
      if(url.pathname.startsWith('/shell/')){const file=path.resolve(root,'ui','.'+url.pathname);assert(file.startsWith(path.resolve(root,'ui/shell')+path.sep));return reply(fs.readFileSync(file),url.pathname.endsWith('.js')?'application/javascript':'text/css');}
      if(url.pathname==='/favicon.ico')return reply('','image/x-icon');
      if(url.pathname==='/api/v1/appearance')return reply({theme:'light',default:'light'});
      const body=req.method()==='POST'?req.postDataJSON():null;calls.push({method:req.method(),path:url.pathname,body,time:Date.now()});
      if(url.pathname==='/api/v1/index/status')return unavailable?reply({error:'Fixture status unavailable'},'application/json',503):reply(snapshot);
      if(url.pathname==='/api/v1/index/query')return reply({rows:[],total:0,total_known:true,inspected_blocks:0,chain_state:'selected_chain'});
      if(url.pathname==='/api/v1/navigate'){
        const height=Number(body.address.replace('.bitcoin',''));
        assert(Number.isSafeInteger(height)&&height>=0,'Only single focused block requests are expected');
        return reply({elapsed_ms:1,resource:{block:{height,hash:'fixture-block-'+height,previous_block_hash:'fixture-previous',merkle_root:'fixture-merkle',transaction_count:1,serialized_bytes:200,total_output_sats:5000000000,time_iso:'Fixture block time',verification_state:'selected_chain',evidence:{bytes:'cache',chain_authority:'selected_chain'},transactions:[{index:0,txid:'fixture-transaction-'+height,coinbase:true,output_sats:5000000000}]}}});
      }
      unexpected.push(req.method()+' '+url.pathname);return reply({error:'Unexpected action'},'application/json',500);
    });
    const settle=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const view=()=>page.locator('#index-timeline').evaluate(el=>({from:Number(el.dataset.from),to:Number(el.dataset.to)}));
    const picks=id=>page.locator('.timeline-lane[data-index="'+id+'"]').evaluate(el=>JSON.parse(el.dataset.selection));
    const playhead=()=>page.locator('#index-timeline').evaluate(el=>Number(el.dataset.playhead));
    const navigations=()=>calls.filter(c=>c.path==='/api/v1/navigate');
    const track=id=>page.locator('.timeline-track[data-index="'+id+'"] .timeline-track-select');
    const marker=(height,edge='single')=>page.locator('.timeline-track[data-index="blocks"] .timeline-selection-marker.'+edge+'[data-height="'+height+'"]');
    const activeMarker=()=>page.evaluate(()=>({height:Number(document.activeElement.dataset.height),edge:document.activeElement.dataset.edge,index:document.activeElement.closest('.timeline-track')?.dataset.index}));
    const heightPoint=async(height,target='.timeline-lane[data-index="blocks"]')=>{const current=await view(),box=await page.locator(target).boundingBox();assert(height>=current.from&&height<=current.to);return{x:box.x+(height-current.from+.5)/(current.to-current.from+1)*box.width,y:box.y+box.height/2};};
    const clickHeight=async(height,modifiers=[])=>{const point=await heightPoint(height);for(const key of modifiers)await page.keyboard.down(key);await page.mouse.click(point.x,point.y);for(const key of modifiers.reverse())await page.keyboard.up(key);await settle();};
    const jump=async height=>{const before=navigations().length;await page.locator('#timeline-height').fill(String(height));await page.locator('#timeline-jump').evaluate(form=>form.requestSubmit());await page.waitForFunction(()=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready');assert.equal(navigations().length,before+1,'A deliberate jump loads exactly one block immediately');};
    const refresh=async preserveFocus=>{const response=page.waitForResponse(r=>r.url()===origin+'/api/v1/index/status');if(preserveFocus)await page.locator('#timeline-refresh').evaluate(button=>button.click());else await page.locator('#timeline-refresh').click();await response;await settle();};
    const drag=async(locator,dx)=>{const box=await locator.boundingBox();const x=box.x+box.width/2,y=box.y+box.height/2;await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+dx,y,{steps:5});await page.mouse.up();await settle();};

    await page.goto(origin+'/indexes',{waitUntil:'networkidle'});
    assert.deepEqual(await page.locator('.timeline-track').evaluateAll(nodes=>nodes.map(n=>n.dataset.index)),['headers','blocks'],'Both foundational tracks exist without raw blocks');
    assert.equal(await page.locator('.timeline-add-track-row').evaluate(el=>el.parentElement.lastElementChild===el),true,'Plus row is after all tracks');
    assert.equal(await page.locator('.timeline-track[data-index="blocks"] .timeline-track-placeholder').isVisible(),false,'Empty coverage does not obscure selectable positions');
    const plus=page.locator('#timeline-add-index'),picker=page.locator('#timeline-index-picker');
    await plus.click();assert.equal(await picker.locator('[data-index="inscriptions"]').isDisabled(),true);
    const pickerBounds=await picker.boundingBox(),plusBounds=await plus.boundingBox();
    assert(pickerBounds.y>=0&&pickerBounds.y+pickerBounds.height<=1000,'Tall catalog popup stays inside the viewport');
    assert(pickerBounds.y+pickerBounds.height<=plusBounds.y-7||pickerBounds.y>=plusBounds.y+plusBounds.height+7,'Catalog popup preserves a clear gap around its plus trigger');
    assert.equal(await picker.evaluate(el=>el.scrollHeight>el.clientHeight),true,'Tall catalog has its own scroll area');
    const trackScroll=await page.locator('#timeline-track-viewport').evaluate(el=>el.scrollTop);
    await page.mouse.move(pickerBounds.x+pickerBounds.width/2,pickerBounds.y+pickerBounds.height/2);await page.mouse.wheel(0,300);await page.waitForTimeout(100);
    assert((await picker.evaluate(el=>el.scrollTop))>0,'Wheel scrolls the catalog rather than being intercepted by timeline navigation');
    assert.equal(await page.locator('#timeline-track-viewport').evaluate(el=>el.scrollTop),trackScroll);
    await plus.click();assert.equal(await picker.isVisible(),false,'Plus remains clickable and closes a full catalog');
    await plus.click();await page.keyboard.press('Escape');assert.equal(await picker.isVisible(),false,'Escape closes even when all catalog entries are disabled');
    await plus.click();await page.locator('#timeline-height').click();assert.equal(await picker.isVisible(),false,'An outside click closes the catalog');
    await page.setViewportSize({width:390,height:844});await plus.click();
    const narrowPicker=await picker.boundingBox(),narrowPlus=await plus.boundingBox();
    assert(narrowPicker.x>=0&&narrowPicker.x+narrowPicker.width<=390&&narrowPicker.y>=0&&narrowPicker.y+narrowPicker.height<=844,'The full catalog fits a narrow viewport');
    assert(narrowPicker.y+narrowPicker.height<=narrowPlus.y-7||narrowPicker.y>=narrowPlus.y+narrowPlus.height+7,'Narrow catalog retains a clear plus-trigger gap');
    await plus.click();assert.equal(await picker.isVisible(),false,'Plus also closes the full catalog in narrow windows');await page.setViewportSize({width:1440,height:1000});
    await track('blocks').click();await page.locator('#timeline-inspector-tab').click();
    assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).isVisible(),true);
    await jump(100);const first=navigations().length;
    await clickHeight(102,['Shift']);assert.deepEqual(await picks('blocks'),[{from:100,to:102}],'Shift click forms an inclusive interval');assert.equal(await playhead(),100,'Range operations leave the explored position unchanged');
    await clickHeight(101,['Control']);assert.deepEqual(await picks('blocks'),[{from:100,to:100},{from:102,to:102}],'Ctrl deselects an interior height without filling its hole');
    await clickHeight(112,['Control']);assert.deepEqual(await picks('blocks'),[{from:100,to:100},{from:102,to:102},{from:112,to:112}],'Ctrl adds a distant single block');
    assert.equal(await page.locator('#timeline-build-range').isDisabled(),true,'Separated selections cannot silently become one indexing plan');
    await marker(102).click({modifiers:['Control']});assert.deepEqual(await picks('blocks'),[{from:100,to:100},{from:112,to:112}],'Ctrl click at a full-height marker centre deselects its exact block');
    await marker(112).click({modifiers:['Meta']});assert.deepEqual(await picks('blocks'),[{from:100,to:100}],'Command click shares individual toggle semantics');
    await marker(100).click({modifiers:['Shift']});assert.deepEqual(await picks('blocks'),[{from:100,to:112}],'Shift click on a marker uses the saved anchor rather than being swallowed');
    assert.equal(navigations().length,first,'Modifier selections never request block data');

    // Repeated keys must continue reaching the replacement marker after redraw.
    await marker(112,'out').focus();await page.keyboard.press('ArrowLeft');await page.keyboard.press('ArrowLeft');
    assert.deepEqual(await picks('blocks'),[{from:100,to:110}]);assert.deepEqual(await activeMarker(),{height:110,edge:'out',index:'blocks'});
    await refresh(true);assert.deepEqual(await activeMarker(),{height:110,edge:'out',index:'blocks'},'Status redraw preserves focused endpoint identity');
    await marker(110,'out').click({modifiers:['Control']});assert.deepEqual(await picks('blocks'),[{from:100,to:109}],'Out marker toggles its inclusive endpoint, not the next block at the visual boundary');
    await clickHeight(114,['Control']);await marker(114).focus();await page.keyboard.press('ArrowLeft');await page.keyboard.press('ArrowLeft');
    assert.deepEqual(await picks('blocks'),[{from:100,to:109},{from:112,to:112}]);assert.deepEqual(await activeMarker(),{height:112,edge:'single',index:'blocks'});
    await page.keyboard.press('ArrowLeft');await page.keyboard.press('ArrowLeft');
    assert.deepEqual(await picks('blocks'),[{from:100,to:110}],'Moving a single marker into adjacency correctly normalizes the interval');
    assert.deepEqual(await activeMarker(),{height:110,edge:'out',index:'blocks'},'Merged selections retain a usable corresponding endpoint');
    const lane=await page.locator('.timeline-lane[data-index="blocks"]').boundingBox(),rangeSpan=(await view()).to-(await view()).from+1;
    await drag(marker(110,'out'),-lane.width/rangeSpan*2);assert.deepEqual(await picks('blocks'),[{from:100,to:108}],'Pointer endpoint dragging changes only its exact indexing range');
    assert.equal(await playhead(),100);assert.equal(navigations().length,first,'Marker editing does not explore or fetch');
    await track('headers').click();assert.equal(await playhead(),100);await track('blocks').click();
    assert.deepEqual(await picks('blocks'),[{from:100,to:108}],'Track changes retain selected indexing ranges');
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-kind'),'track');assert.equal(await page.getByRole('switch',{name:'blocks On',exact:true}).isVisible(),true,'Clicking the track name returns to its settings');
    assert.equal(await page.locator('#timeline-inspector').getAttribute('data-active-pane'),'inspector','Track switches respect the chosen upper tab');

    // Scrubbing emits no request until the final pointer position settles.
    await jump(200);const beforeScrub=navigations().length,point=await heightPoint(205,'#timeline-ruler');
    await page.mouse.move(point.x,point.y);await page.mouse.down();await page.waitForTimeout(350);
    assert.equal(navigations().length,beforeScrub,'The first scrub position is not loaded immediately');
    const moved=await heightPoint(207,'#timeline-ruler');await page.mouse.move(moved.x,moved.y,{steps:2});await page.waitForTimeout(350);
    const final=await heightPoint(209,'#timeline-ruler');await page.mouse.move(final.x,final.y,{steps:2});await page.mouse.up();await page.waitForTimeout(450);
    assert.equal(navigations().length,beforeScrub,'Intermediate blocks are skipped while scrubbing');
    await page.waitForTimeout(700);assert.equal(navigations().length,beforeScrub+1);assert.equal(navigations().at(-1).body.address,'209.bitcoin','Only the settled playhead block loads');
    assert.deepEqual(await picks('blocks'),[{from:200,to:200}],'Ruler scrubbing leaves indexing selections intact');
    assert.equal(await playhead(),209);
    const beforeMarkerClick=navigations().length;await marker(200).click();await page.waitForFunction(()=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready');
    assert.equal(await playhead(),200);assert.equal(navigations().length,beforeMarkerClick+1);assert.equal(navigations().at(-1).body.address,'200.bitcoin','Plain click on a selected marker explores its block after the playhead moved elsewhere');
    assert.deepEqual(await picks('blocks'),[{from:200,to:200}],'Exploring a selected marker does not change indexing selections');
    await page.locator('#timeline-ruler').focus();await page.keyboard.press('ArrowRight');await page.waitForFunction(()=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready');
    assert.equal(await playhead(),201);assert.equal(await page.locator('#timeline-ruler').evaluate(el=>document.activeElement===el),true,'Ruler keyboard focus survives its child redraw');
    const beforeDirect=navigations().length;await clickHeight(211);await page.waitForFunction(()=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready');
    assert.equal(navigations().length,beforeDirect+1);assert.equal(navigations().at(-1).body.address,'211.bitcoin','Direct block clicks load immediately');
    unavailable=true;await refresh(false);const beforeOffline=navigations().length;await clickHeight(212);await page.waitForTimeout(1100);
    assert.equal(await playhead(),212);assert.equal(navigations().length,beforeOffline,'Unavailable status pauses Explorer loading even though local focus can change');
    unavailable=false;await refresh(false);await page.waitForFunction(()=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready');
    assert.equal(navigations().length,beforeOffline+1);assert.equal(navigations().at(-1).body.address,'212.bitcoin','Status recovery loads the latest playhead rather than its previous stale position');

    assert.deepEqual(errors,[],'No browser script errors');assert.deepEqual(unexpected,[],'No unexpected or external request');
    assert.equal(calls.filter(c=>c.method==='POST'&&c.path!=='/api/v1/navigate').length,0,'Selections and navigation never create indexing, live or sharing work');
    if(out){await page.screenshot({path:path.join(out,'timeline-selections.png'),fullPage:true});fs.writeFileSync(path.join(out,'summary.json'),JSON.stringify({result:'PASS',blockRequests:navigations().map(c=>c.body.address),indexMutationRequests:0},null,2)+'\n');}
    console.log('Index timeline selection browser PASS: default tracks; inclusive Shift and Ctrl/Command gaps; exact endpoint toggles; marker keyboard/pointer focus and merge; retained track selections; independent playhead; settled and immediate Explorer loading; unavailable-status pause; zero indexing mutations.');
    await page.close();
  }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
