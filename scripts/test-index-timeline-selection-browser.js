'use strict';
// Real Indexes page and real pointer/keyboard events with disposable API data.
// No client profile, Bitcoin peer or externally hosted service is contacted.
const assert=require('node:assert/strict');
const {selectHeight}=require('./timeline-browser-gestures');
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
      if(url.pathname==='/api/v1/index/run/prepare')return reply({schema:1,prepared:body});
      if(['/api/v1/index/status','/api/v1/index/progress'].includes(url.pathname))return unavailable?reply({error:'Fixture status unavailable'},'application/json',503):reply(snapshot);
      if(url.pathname==='/api/v1/index/query')return reply({rows:[],total:0,total_known:true,inspected_blocks:0,chain_state:'selected_chain'});
      if(url.pathname==='/api/v1/bitmap/view')return reply({state:'not_built',note:'No saved canonical Bitmap mapping.'});
      if(url.pathname==='/api/v1/index/block-map'){const height=parseInt(url.searchParams.get('block'),16);return reply({height,hash:height.toString(16).padStart(64,'0'),total:1,transaction_count:1,transactions:[{index:0,txid:height.toString(16).padStart(64,'0'),output_sats:5000000000,vsize:100,weight:400,coinbase:true}]});}
      if(url.pathname==='/api/v1/navigate'){
        const height=Number(body.address.replace('.bitcoin',''));
        assert(Number.isSafeInteger(height)&&height>=0,'Only single focused block requests are expected');
        return reply({elapsed_ms:1,resource:{block:{height,hash:height.toString(16).padStart(64,'0'),previous_block_hash:'fixture-previous',merkle_root:'fixture-merkle',transaction_count:1,serialized_bytes:200,total_output_sats:5000000000,time_iso:'Fixture block time',verification_state:'selected_chain',evidence:{bytes:'cache',chain_authority:'selected_chain'},transactions:[{index:0,txid:height.toString(16).padStart(64,'0'),coinbase:true,output_sats:5000000000}]}}});
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
    const jump=async height=>{const focused=()=>navigations().filter(c=>c.body.address===height+'.bitcoin').length,before=focused();await selectHeight(page,height,'blocks');await page.waitForFunction(height=>document.querySelector('#timeline-explorer-panel').dataset.state==='ready'&&explorer.state().loadedHeight===height,height);assert(focused()-before<=1,'A deliberate jump reuses known evidence or loads its focused block only once: '+JSON.stringify({height,before,requests:navigations().map(c=>c.body)}));};
    const refresh=async preserveFocus=>{const response=page.waitForResponse(r=>r.url()===origin+'/api/v1/index/status');await page.evaluate(()=>refresh());await response;await settle();};
    const drag=async(locator,dx)=>{await locator.waitFor({state:'visible'});const box=await locator.boundingBox();const x=box.x+box.width/2,y=box.y+box.height/2;await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+dx,y,{steps:5});await page.mouse.up();await settle();};

    await page.route('**/api/v1/index/layout',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({schema:1,order:{},revision:0})}));
  await page.goto(origin+'/indexes',{waitUntil:'networkidle'});await page.getByRole('button',{name:'Expand index tracks',exact:true}).click();
    const range=()=>page.locator('#timeline-global-range').innerText(),handle=edge=>page.locator('#timeline-ruler .timeline-global-handle.'+edge);
    await jump(100);await page.locator('#timeline-ruler').focus();await page.keyboard.press('i');await jump(112);await page.locator('#timeline-ruler').focus();await page.keyboard.press('o');
    assert.match(await range(),/100.*112/);assert.deepEqual(await picks('blocks'),[{from:100,to:112}]);assert.deepEqual(await picks('headers'),[{from:100,to:112}],'Every track reads the same interval');
    await handle('out').focus();await page.evaluate(()=>window.savedOutHandle=document.activeElement);await page.keyboard.press('ArrowLeft');await page.keyboard.press('ArrowLeft');assert.deepEqual(await picks('blocks'),[{from:100,to:110}]);assert.equal(await page.evaluate(()=>document.activeElement===window.savedOutHandle&&document.querySelector('#timeline-ruler .timeline-global-handle.out')===window.savedOutHandle),true,'Endpoint redraw preserves the focused handle element for repeated keys');
    await refresh(true);assert.equal(await page.evaluate(()=>document.activeElement===window.savedOutHandle&&document.querySelector('#timeline-ruler .timeline-global-handle.out')===window.savedOutHandle),true,'Status refresh preserves focused marker identity');
    await drag(handle('out'),24);const moved=await picks('blocks');assert.equal(moved[0].from,100);assert(moved[0].to>110,'Captured pointer moves only the global Out');
    await handle('in').click({button:'right'});assert.equal(await page.locator('#timeline-clear-in').isDisabled(),true);assert.equal(await page.locator('#timeline-clear-out').isDisabled(),false,'Clear one edge leaves the other');
    await page.locator('#timeline-clear-out').click();assert.deepEqual(await picks('blocks'),[]);assert.equal(await page.evaluate(()=>window.savedOutHandle.isConnected),false,'Clearing the mark removes the retained handle');
    await page.locator('#timeline-mark-out').click();await handle('out').focus();await page.evaluate(()=>window.outFirst=document.activeElement);await page.keyboard.press('i');assert.deepEqual(await page.locator('#timeline-ruler .timeline-global-handle').evaluateAll(nodes=>nodes.map(n=>n.dataset.edge)),['in','out'],'Out-first equal endpoints retain In-then-Out stacking and tab order');assert.equal(await page.evaluate(()=>document.activeElement===window.outFirst&&document.querySelector('#timeline-ruler .timeline-global-handle.out')===window.outFirst),true,'Creating In does not detach the focused Out');assert.equal(await handle('in').getAttribute('aria-valuenow'),await handle('out').getAttribute('aria-valuenow'));await page.locator('#timeline-collapse').click();assert.deepEqual(await page.locator('.timeline-compact .timeline-global-handle').evaluateAll(nodes=>nodes.map(n=>n.dataset.edge)),['in','out'],'Compact host retains equal-endpoint ordering');await page.locator('#timeline-collapse').click();await page.locator('#timeline-clear-range').click();
    await jump(120);await page.locator('#timeline-mark-in').click();await jump(100);await page.locator('#timeline-mark-out').click();assert.match(await page.locator('#message').innerText(),/In must precede Out/);assert.equal(await page.locator('#timeline-clear-out').isDisabled(),true,'Rejected reversed endpoint does not replace the valid range');
    await page.locator('#timeline-clear-range').click();await jump(100);await page.locator('#timeline-mark-in').click();await clickHeight(112,['Shift']);assert.deepEqual(await picks('blocks'),[{from:100,to:112}],'Shift selects one global interval');
    const prior=await picks('blocks');await jump(130);await page.locator('#timeline-ruler').focus();await page.keyboard.press('ArrowLeft');assert.deepEqual(await picks('blocks'),prior,'Stepping moves focus without rewriting marks');
    await page.reload({waitUntil:'networkidle'});assert.match(await range(),/100.*112/,'Marked interval persists through reload');
    unavailable=true;await refresh();assert.equal(await page.locator('#timeline-run-start').isDisabled(),true);unavailable=false;await refresh();
    const mutations=calls.filter(c=>c.method==='POST'&&!['/api/v1/navigate','/api/v1/index/run/prepare'].includes(c.path));assert.deepEqual(mutations,[],'Marking, navigating and staging never executes indexing');
    assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);console.log('Global timeline selection browser PASS: one shared In/Out, repeated keyboard focus, captured drag, individual clearing, reversed rejection, shared Shift interval, navigation isolation, persistence and unavailable status.');
  }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
}
run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});
