'use strict';
// Actual production timeline pointer/keyboard code, served with isolated local data.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..');
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const html=`<!doctype html><html data-theme="light"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/index-timeline.css"><link rel="stylesheet" href="/shell/bitmap-workspace.css"></head><body class="indexes-workspace embedded"><main><section id="index-timeline"></section></main><script src="/shell/index-cards.js"></script><script src="/shell/index-scrollbar-geometry.js"></script><script src="/shell/index-timeline.js"></script><script src="/shell/index-workspace.js"></script><script>
window.calls=[];const ids=['headers','blocks'];window.snapshot={definitions:ids.map(id=>({id,name:id,buildable:id!=='headers',start_height:0,version:1,rules:[],dependencies:[]})),instances:[],providers:[],jobs:[],job:{},live:[],timeline:{tip_height:999999,tracks:ids.map(id=>({id,coverage:id==='headers'?[{from:0,to:999999}]:[],coverage_complete:true}))}};window.timeline=GatewayIndexTimeline.mount({document,root:document.querySelector('#index-timeline'),select:id=>timeline.select(id),api:async(action,body)=>{calls.push({action,body});if(action==='layout')return {schema:2,revision:0,order:{}};throw Error('Unexpected action '+action);},refresh:async()=>timeline.render(snapshot)});timeline.render(snapshot);GatewayIndexWorkspace.mountLayout(document.querySelector('#index-timeline'));
</script></body></html>`;
const span=v=>v.to-v.from+1;
(async()=>{
  const server=http.createServer((req,res)=>{const u=new URL(req.url,'http://fixture');if(u.pathname==='/'){res.setHeader('Content-Type','text/html');return res.end(html);}if(u.pathname==='/favicon.ico'){res.writeHead(204);return res.end();}const file=path.resolve(root,'ui','.'+u.pathname);if(!file.startsWith(path.resolve(root,'ui/shell')+path.sep)||!fs.existsSync(file)){res.writeHead(404);return res.end();}res.setHeader('Content-Type',file.endsWith('.js')?'application/javascript':'text/css');res.end(fs.readFileSync(file));});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await pw.chromium.launch({headless:true,executablePath});
  try {
    const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],results=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto('http://127.0.0.1:'+server.address().port);
    if(await page.locator('#timeline-collapse:not([aria-expanded=true])').isVisible())await page.locator('#timeline-collapse').click();
    const view=()=>page.evaluate(()=>{const v=timeline.getView();return {from:v.from,to:v.to};});
    const settle=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const target=edge=>page.locator('#timeline-scrollbar-horizontal .timeline-scroll-handle.'+edge);
    async function prepare(){
      await page.locator('#timeline-fit').click();await page.evaluate(()=>{timeline.focus(400000);timeline.zoomIntoFocus();});
      await target('end').focus();
      for(let i=0;i<100&&span(await view())<100;i++)await page.keyboard.press('ArrowRight');
      await settle();const exact=await view();assert.equal(exact.from,400000);assert(span(exact)>=100&&span(exact)<=106,'Keyboard establishes a known fine actual range');
    }
    async function drag(locator,delta){const box=await locator.boundingBox();assert(box);const x=box.x+box.width/2,y=box.y+box.height/2;assert(await locator.evaluate((el,p)=>el.contains(document.elementFromPoint(p.x,p.y)),{x,y}),'Pointer hits the intended actual control');await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x+delta,y);await page.mouse.up();await settle();return box;}
    for(const width of [1440,390]){
      await page.setViewportSize({width,height:width===390?844:1000});
      for(const edge of ['start','end']){
        await prepare();
        const before=await view(),pick=target(edge),box=await pick.boundingBox();
        assert(box.width>=24&&box.height>=24,'Both resize hit targets remain usable on desktop and narrow layouts');
        const other=await target(edge==='start'?'end':'start').boundingBox();
        assert(box.x+box.width<=other.x||other.x+other.width<=box.x,'Resize target interiors remain separate at high zoom');
        const direction=edge==='start'?-1:1;
        await drag(pick,direction);
        const after=await view();
        assert(span(after)>span(before)&&span(after)<200,'One physical pixel gives a fine real-range zoom change at high zoom');
        assert.equal(edge==='start'?after.to:after.from,edge==='start'?before.to:before.from,'Opposite actual endpoint is exact');
        results.push({width,edge,delta:direction,before,after,hit:{width:box.width,height:box.height}});
        await prepare();const keysBefore=await view(),step=Math.max(1,Math.round(span(keysBefore)/20));await pick.focus();await page.keyboard.press(edge==='start'?'ArrowRight':'ArrowLeft');
        const keyboard=await view();assert.deepEqual(keyboard,edge==='start'?{from:keysBefore.from+step,to:keysBefore.to}:{from:keysBefore.from,to:keysBefore.to-step},'Existing keyboard step and endpoint semantics remain exact');
      }
      await prepare();const before=await view();
      await drag(page.locator('#timeline-scrollbar-horizontal .timeline-scroll-thumb'),3);
      assert.equal(span(await view()),span(before),'Thumb body panning preserves the exact actual zoom');
      await prepare();const wheelBefore=await view(),box=await page.locator('#timeline-ruler').boundingBox();
      await page.mouse.move(box.x+box.width*.6,box.y+box.height/2);await page.keyboard.down('Control');await page.mouse.wheel(0,-40);await page.keyboard.up('Control');await settle();
      assert(span(await view())<span(wheelBefore),'Pointer wheel zoom continues to use the real viewport');
    }
    assert.deepEqual(errors,[]);assert.equal(await page.evaluate(()=>calls.some(c=>c.body)),false,'Navigation and zoom send no mutation requests');
    console.log('Index scrollbar browser: PASS '+JSON.stringify(results));
  }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;});
