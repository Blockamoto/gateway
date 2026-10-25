'use strict';
// Independent production-asset probe. Every API response/profile is disposable.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..'),pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const html=`<!doctype html><html data-theme="light"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/index-timeline.css"><link rel="stylesheet" href="/shell/bitmap-workspace.css"><link rel="stylesheet" href="/shell/index-help.css"></head><body class="indexes-workspace embedded"><main><section id="index-timeline"></section></main><script src="/shell/index-cards.js"></script><script src="/shell/index-help-content.js"></script><script src="/shell/index-help.js"></script><script src="/shell/index-transport.js"></script><script src="/shell/index-scrollbar-geometry.js"></script><script src="/shell/index-timeline.js"></script><script src="/shell/index-workspace.js"></script><script>
window.GatewayNetworkSnapshot={observedAt:Date.now(),value:{enabled:true,headers_user_paused:false,headers:{header_state:'waiting',message:'Waiting for a Bitcoin peer.'}}};window.calls=[];const ids=['headers','blocks','tx-locator','txo-spender','utxo-state','sat-state','address-state','address-history','inscriptions','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'];window.snapshot={definitions:ids.map(id=>({id,name:({'headers':'Headers','blocks':'Raw blocks','tx-locator':'TX ID Locators','txo-spender':'TXIO index','utxo-state':'UTXO set','address-state':'Current Addresses','address-history':'Historical Outputs','inscriptions':'Inscriptions','bitmap-districts-standard-v1':'Bitmap Districts Standard V1','bitmap-parcels-standard-v1':'Bitmap Parcels Standard V1'})[id]||id,buildable:id!=='headers',start_height:id.startsWith('bitmap-')?792435:0,version:1,rules:[],dependencies:[]})),instances:[],providers:[],jobs:[],job:{},live:[],run:{schema:1,configured:true,prepared:{consumers:ids.map(index=>({index,armed:false,live:false}))},run:null},timeline:{tip_height:999999,tracks:ids.map(id=>({id,coverage:id==='headers'?[{from:0,to:999999}]:[],coverage_complete:true}))}};window.layout={schema:2,revision:0,order:{}};window.timeline=GatewayIndexTimeline.mount({document,root:document.querySelector('#index-timeline'),select:id=>timeline.select(id),loadHierarchy:async p=>({...p,count:p.kind==='block'?3:2,block_hash:'a'.repeat(64)}),api:async(action,body)=>{calls.push({action,body});if(action==='layout'){if(body)layout={schema:2,revision:layout.revision+1,order:body.order};return layout;}if(action==='run/prepare'){snapshot.run.prepared=body;return snapshot.run;}throw Error('Unexpected action '+action)},refresh:async()=>timeline.render(snapshot)});timeline.render(snapshot);GatewayIndexWorkspace.mountLayout(document.querySelector('#index-timeline'));
</script></body></html>`;
const transactions=['tx-locator','txio-links','txio-values','txio-scripts','utxo-state'];
const ordinals=['txio-sat-ranges','inscriptions'];
const allTracks=['headers','blocks',...transactions,...ordinals,'address-state','address-history','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'];
const groups=['blocks','txio','addresses','ordinals','bitmap'];
async function verifyNetworkCacheFreshness(){
  const vm=require('node:vm'),listeners=new Map(),timers=[],nodes=new Map();let now=1000,fail=false,events=0;
  const doc={getElementById:id=>{if(!nodes.has(id))nodes.set(id,{dataset:{},classList:{add(){},remove(){}},addEventListener(){}});return nodes.get(id);},addEventListener:(type,handler)=>listeners.set(type,handler)};
  const win={dispatchEvent(){events++;}},context={document:doc,window:win,sessionStorage:{getItem:()=>null},location:{search:''},URLSearchParams,AbortController,Event,Date:class extends Date{static now(){return now;}},setTimeout:(fn,ms)=>{timers.push({fn,ms});return timers.length;},clearTimeout(){},fetch:async path=>{if(path==='/api/v1/network'&&fail)throw Error('fixture status failure');return{ok:true,json:async()=>path==='/api/v1/network'?{enabled:true,headers_user_paused:false,headers:{header_state:'waiting'}}:{}};}};
  vm.runInNewContext(fs.readFileSync(path.join(root,'ui/shell/network.js'),'utf8'),context);await new Promise(resolve=>setImmediate(resolve));
  assert.equal(win.GatewayNetworkSnapshot.observedAt,1000);const beforeEvents=events;now=15000;fail=true;await timers.find(timer=>timer.ms===2500).fn();
  for(let n=0;n<4;n++)listeners.get('gateway:index-status')({detail:{localIndexes:n,following:0}});listeners.get('gateway:dev-mode')();
  assert.equal(win.GatewayNetworkSnapshot.observedAt,1000,'Failed network read plus continued index/dev repaints cannot refresh cached policy age');assert.equal(events,beforeEvents,'Repaints never publish a fresh network observation');
}
async function verifyLocatorConsent(browser,origin){
  const title='Also turn on TX ID Locators?',district='bitmap-districts-standard-v1',parcel='bitmap-parcels-standard-v1',outcomes=[];
  const cases=[
    {name:'Inscriptions individual Escape',id:'inscriptions',scope:['inscriptions'],answer:'escape'},
    {name:'District individual keyboard accept',id:district,scope:[district],answer:'keyboard',shot:'desktop'},
    {name:'Parcel individual pointer decline',id:parcel,scope:[parcel],answer:'decline',width:390,shot:'narrow'},
    {name:'Bitmap parent pointer accept',group:'bitmap',scope:[district,parcel],answer:'accept'},
    {name:'Ordinals parent keyboard accept',group:'ordinals',scope:['sat-state','inscriptions'],answer:'keyboard'},
    {name:'Mixed Bitmap newly joins Parcel',group:'bitmap',armed:[district],scope:[district,parcel],answer:'escape'},
    {name:'Public setter individual',id:'inscriptions',scope:['inscriptions'],answer:'decline',publicSetter:true},
    {name:'Inspector options event',id:district,scope:[district],answer:'accept',optionsEvent:true},
    {name:'Locator locks while question is open',id:district,scope:[district],answer:'locked'},
  ];
  async function fresh(options,run){
    const context=await browser.newContext({viewport:{width:options.width||1440,height:900}});try{
      const page=await context.newPage(),errors=[],requests=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>requests.push({method:r.method(),path:new URL(r.url()).pathname}));
      await page.addInitScript(()=>localStorage.setItem('gateway.timeline.workspace.v1',JSON.stringify({collapsed:false,categoryOpen:{blocks:true,txio:true,addresses:true,ordinals:true,bitmap:true}})));
      const query=new URLSearchParams({consent:'1',armed:(options.armed||[]).join(','),live:'sat-state,inscriptions'});await page.goto(origin+'/?'+query);
      await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
      const draft=()=>page.evaluate(()=>timeline.getRunDraft()),calls=()=>page.evaluate(()=>window.calls.filter(c=>c.body)),question=page.getByRole('dialog',{name:title,exact:true});
      assert.equal(await question.count(),0,'Saved-choice initialization never asks');await run({page,draft,calls,question,requests});assert.deepEqual(errors,[]);assert(requests.every(r=>r.method==='GET'),'No hidden HTTP writes outside the fixture shared prepare callback');
    }finally{await context.close();}
  }
  for(const item of cases)await fresh(item,async({page,draft,calls,question})=>{
    const before=await draft(),beforeCalls=(await calls()).length,selector=item.group?'.timeline-group[data-group="'+item.group+'"] .timeline-group-arm':'.timeline-track[data-index="'+item.id+'"] [data-control=arm]',trigger=page.locator(selector);
    await page.evaluate(()=>{window.locatorOpenings=0;new MutationObserver(records=>{for(const r of records)if(r.type==='attributes'&&r.attributeName==='open'&&r.target.id==='timeline-locator-companion'&&r.target.open)window.locatorOpenings++;}).observe(document,{subtree:true,attributes:true,attributeFilter:['open']});});
    if(item.optionsEvent){
      await page.evaluate(id=>{timeline.inspectIndex(id);const button=document.createElement('button');button.id='fixture-options-arm';button.textContent='Apply inspected Arm';button.onclick=()=>document.dispatchEvent(new CustomEvent('gateway:timeline-options',{detail:{index:id,options:{armed:true}}}));document.querySelector('#timeline-inspector').append(button);},item.id);assert.equal(await question.count(),0,'Inspection alone never asks');await page.locator('#fixture-options-arm').click();
    }else if(item.publicSetter){await trigger.focus();await page.evaluate(id=>timeline.setConsumerOptions(id,{armed:true}),item.id);}
    else await trigger.click();
    await question.waitFor({state:'visible'});assert.equal(await question.count(),1,item.name+' shows one accessible question');assert.equal(await page.locator('#timeline-locator-companion[open]').count(),1);assert.equal(await page.locator('.bitmap-companion-offer').count(),0,'No obsolete inline offer remains');
    assert.equal(await question.getAttribute('aria-labelledby'),'timeline-locator-companion-title');assert.equal(await question.getAttribute('aria-describedby'),'timeline-locator-companion-description');assert.equal(await question.getAttribute('aria-modal'),'true');assert.equal(await page.locator('#timeline-locator-companion-decline').evaluate(e=>e===document.activeElement),true,'Safe decline receives initial focus');
    await page.evaluate(()=>{timeline.render(snapshot);timeline.update(snapshot);timeline.redraw();});assert.equal(await question.count(),1,'Repeated status/render preserves one prompt');assert.equal(await page.evaluate(()=>locatorOpenings),1,'One staging action opens only one modal, including parent bulk');
    await page.waitForFunction(n=>window.calls.filter(c=>c.body).length>n,beforeCalls);const stagedCalls=await calls(),beforeAnswer=stagedCalls.length,staged=await draft(),expected=JSON.parse(JSON.stringify(before));for(const c of expected.consumers)if(item.scope.includes(c.index))c.armed=true;
    assert.deepEqual(staged,expected,item.name+' arms only the explicit source selection before answering');
    if(item.shot){const folder=path.join(root,'build/qa-078/locator-companion');fs.mkdirSync(folder,{recursive:true});await page.screenshot({path:path.join(folder,item.shot+'.png')});}
    if(item.answer==='locked')await page.evaluate(()=>{snapshot.definitions.find(d=>d.id==='tx-locator').locked=true;timeline.render(snapshot);});
    if(item.answer==='escape')await page.keyboard.press('Escape');
    else if(item.answer==='keyboard'){
      for(let n=0;n<4&&!await page.locator('#timeline-locator-companion-accept').evaluate(e=>e===document.activeElement);n++)await page.keyboard.press('Tab');assert.equal(await page.locator('#timeline-locator-companion-accept').evaluate(e=>e===document.activeElement),true,'Keyboard reaches accept within the modal');await page.keyboard.press('Enter');
    }else{
      const answer=page.locator(item.answer==='decline'?'#timeline-locator-companion-decline':'#timeline-locator-companion-accept');await answer.hover();const box=await answer.boundingBox();assert(await answer.evaluate((e,p)=>e.contains(document.elementFromPoint(p.x,p.y)),{x:box.x+box.width/2,y:box.y+box.height/2}),'Actual pointer hits the answer button');await answer.click();
    }
    await question.waitFor({state:'detached'});
    const accepted=['accept','keyboard'].includes(item.answer);if(accepted){for(const c of expected.consumers)if(c.index==='tx-locator')c.armed=true;await page.waitForFunction(n=>window.calls.filter(c=>c.body).length>n,beforeAnswer);}
    const focus=item.optionsEvent?page.locator('#fixture-options-arm'):trigger,restoredFocus=await focus.evaluate(e=>e===document.activeElement),focusAfterAnswer=await page.evaluate(()=>document.activeElement.outerHTML.slice(0,200));
    if(item.answer==='locked')await page.evaluate(()=>{snapshot.definitions.find(d=>d.id==='tx-locator').locked=false;timeline.render(snapshot);});
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.deepEqual(await draft(),expected,'Only accepting changes the existing locator Arm; Live/mode/history/Raw/range/consumer identities stay exact');
    const afterCalls=await calls();assert.equal(afterCalls.length,beforeAnswer+(accepted?1:0),'Decline/Escape/unavailable accept adds no save; valid accept adds one shared prepare');assert(afterCalls.every(c=>c.action==='run/prepare'),'Prompt starts no run, backfill, Live or storage API');if(accepted)assert.deepEqual(afterCalls.at(-1).body,expected,'Acceptance uses the existing normal prepare request');
    assert.equal(restoredFocus,true,item.name+' returns focus to its initiator before the independent test unlock/render; actual='+focusAfterAnswer);
    // No-op true and disarm through both public staging paths stay quiet.
    if(item.publicSetter||item.optionsEvent){await page.evaluate(id=>{timeline.setConsumerOptions(id,{armed:true});document.dispatchEvent(new CustomEvent('gateway:timeline-options',{detail:{index:id,options:{armed:true}}}));},item.id);assert.equal(await question.count(),0,'No-op armed:true remains quiet');await page.evaluate(id=>document.dispatchEvent(new CustomEvent('gateway:timeline-options',{detail:{index:id,options:{armed:false}}})),item.id);assert.equal(await question.count(),0,'Public disarm remains quiet');}
    outcomes.push(item.name);
  });
  await fresh({armed:['tx-locator']},async({page,draft,question})=>{const before=await draft();await page.locator('.timeline-track[data-index=inscriptions] [data-control=arm]').click();assert.equal(await question.count(),0,'Already armed locator does not ask');before.consumers.find(c=>c.index==='inscriptions').armed=true;assert.deepEqual(await draft(),before);await page.locator('.timeline-track[data-index=inscriptions] [data-control=arm]').click();assert.equal(await question.count(),0,'Individual disarm never asks');outcomes.push('Already armed / individual Off quiet');});
  await fresh({armed:['inscriptions',district,parcel]},async({page,draft,calls,question})=>{const before=await draft(),count=(await calls()).length;await page.evaluate(()=>{for(let n=0;n<4;n++){timeline.render(snapshot);timeline.update(snapshot);timeline.redraw();}});assert.equal(await question.count(),0,'Restored Inscriptions/Bitmap Arm state never asks during rendering');assert.deepEqual(await draft(),before);assert.equal((await calls()).length,count);outcomes.push('Saved choices and repeated rendering quiet');});
  await fresh({armed:['inscriptions']},async({page,draft,question})=>{const before=await draft();await page.locator('.timeline-group[data-group=ordinals] .timeline-group-arm').click();assert.equal(await question.count(),0,'Mixed Ordinals only newly arming Sat Ranges never asks');before.consumers.find(c=>c.index==='sat-state').armed=true;assert.deepEqual(await draft(),before);outcomes.push('Mixed Ordinals Sat-only join quiet');});
  await fresh({},async({page,draft,question})=>{const before=await draft();await page.locator('.timeline-track[data-index=inscriptions] [data-control=live]').click();assert.equal(await question.count(),0,'Live alone never asks');before.consumers.find(c=>c.index==='inscriptions').live=false;assert.deepEqual(await draft(),before);outcomes.push('Live independent / no prompt');});
  await fresh({},async({page,question})=>{const state=await page.evaluate(()=>{document.querySelector('.timeline-track[data-index=inscriptions] .index-help-purpose').focus();timeline.setConsumerOptions('inscriptions',{armed:true});return{help:!document.querySelector('.index-help-popover').hidden,prompt:document.querySelector('#timeline-locator-companion')?.open};});assert.deepEqual(state,{help:true,prompt:true},'Help is open when the public Arm question opens');await page.keyboard.press('Escape');await question.waitFor({state:'detached'});assert.equal((await page.evaluate(()=>timeline.getRunDraft())).consumers.find(c=>c.index==='tx-locator').armed,false,'First real Escape declines before the background help handler');outcomes.push('Help-open Escape precedence');});
  await fresh({},async({page,draft,question})=>{const before=await draft(),help=page.locator('.timeline-help'),arm=page.locator('.timeline-track[data-index=inscriptions] [data-control=arm]');await page.getByLabel('Timeline navigation help',{exact:true}).click();assert.equal(await help.evaluate(e=>e.open),true);await arm.focus();await page.keyboard.press('Space');await question.waitFor({state:'visible'});assert.equal(await help.evaluate(e=>e.open),true,'Navigation help remains open behind the native question');await page.keyboard.press('Escape');await question.waitFor({state:'detached'});assert.equal(await help.evaluate(e=>e.open),true,'First Escape belongs to the question before Navigation help');assert.equal(await arm.evaluate(e=>e===document.activeElement),true);before.consumers.find(c=>c.index==='inscriptions').armed=true;assert.deepEqual(await draft(),before,'Escape rejects locators while preserving explicit source Arm');await page.keyboard.press('Escape');assert.equal(await help.evaluate(e=>e.open),false,'After question closes the normal Navigation help Escape handler works');outcomes.push('Navigation help open / first Escape precedence');});
  console.log('Locator companion consent browser PASS: '+outcomes.length+' cases; '+outcomes.join('; '));
}
(async()=>{
  await verifyNetworkCacheFreshness();
  const headerRequests=[];let headerNetwork={enabled:true,headers_user_paused:false,headers:{header_state:'waiting',message:'Waiting for a Bitcoin peer.'}};
  const server=http.createServer((req,res)=>{
    const u=new URL(req.url,'http://fixture');
    if(u.pathname==='/'){res.setHeader('Content-Type','text/html');let markup=html;if(u.searchParams.has('consent')){const armed=(u.searchParams.get('armed')||'').split(',').filter(Boolean),live=(u.searchParams.get('live')||'').split(',').filter(Boolean),seed='for(const c of snapshot.run.prepared.consumers){c.armed='+JSON.stringify(armed)+'.includes(c.index);c.live='+JSON.stringify(live)+'.includes(c.index);if(c.index==="inscriptions"){c.mode="full";c.conventional_ids=false;}if(c.index==="sat-state")c.retain_sat_history=true;}snapshot.run.prepared.in=792435;snapshot.run.prepared.out=792442;';markup=markup.replace('window.layout=',seed+'window.layout=');}return res.end(markup);}
    if(u.pathname==='/api/v1/network'){headerRequests.push({method:req.method,path:u.pathname});res.setHeader('Content-Type','application/json');return res.end(JSON.stringify(headerNetwork));}
    if(u.pathname==='/api/v1/headers'&&req.method==='POST'){let body='';req.on('data',part=>body+=part);req.on('end',()=>{const input=JSON.parse(body);headerRequests.push({method:req.method,path:u.pathname,...input});headerNetwork={enabled:true,headers_user_paused:input.action==='pause',headers:{header_state:input.action==='pause'?'paused':'connecting',message:input.action==='pause'?'Paused by you.':'Connecting to Bitcoin peers.'}};res.setHeader('Content-Type','application/json');res.end(JSON.stringify({ok:true,status:headerNetwork.headers}));});return;}
    if(u.pathname==='/api/v1/bitmap/target-coverage'){
      const from=Number(u.searchParams.get('from')),to=Number(u.searchParams.get('to'));
      res.setHeader('Content-Type','application/json');
      return res.end(JSON.stringify({state:'ready',instance:u.searchParams.get('axis')==='blocks'?'bitmap-districts-standard-v1':'bitmap-parcels-standard-v1',pixels:Number(u.searchParams.get('pixels')),snapshot:{height:792435,block_hash:'b'.repeat(64),commitment:'c'.repeat(64),profile_hash:'d'.repeat(64)},chain:{source:'bod_headers',height:999999,hash:'a'.repeat(64)},cache_key:'e'.repeat(64),total_winners:1,total_winners_known:true,query_complete:true,truncated:false,axis:u.searchParams.get('axis'),...(u.searchParams.has('district')?{district:Number(u.searchParams.get('district'))}:{}),from,to,bins:[{from,to:from,targets:1,winners:1,unknown:0}],note:'Disposable target fixture'}));
    }
    if(u.pathname==='/favicon.ico'){res.writeHead(204);return res.end();}
    const file=path.resolve(root,'ui','.'+u.pathname);
    if(!file.startsWith(path.resolve(root,'ui/shell')+path.sep)||!fs.existsSync(file)){res.writeHead(404);return res.end();}
    res.setHeader('Content-Type',file.endsWith('.js')?'application/javascript':'text/css');res.end(fs.readFileSync(file));
  });
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const origin='http://127.0.0.1:'+server.address().port,browser=await pw.chromium.launch({headless:true,executablePath});
  if(process.env.GATEWAY_LOCATOR_ONLY==='1'){try{await verifyLocatorConsent(browser,origin);}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}return;}
  try{
    const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],measurements=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.addInitScript(()=>{
      if(!localStorage.getItem('independent-parent-probe')){
        localStorage.setItem('independent-parent-probe','1');
        localStorage.setItem('gateway.timeline.workspace.v1',JSON.stringify({collapsed:false,groups:{blocks:true,txio:true,addresses:true,ordinals:true,bitmap:true},expandedTracks:{'tx-locator':true}}));
      }
    });
    await page.goto(origin);
    const settle=()=>page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    const track=id=>page.locator('.timeline-track[data-index="'+id+'"]');
    const group=id=>page.locator('.timeline-group[data-group="'+id+'"]');
    const disclosure=id=>group(id).locator(':scope > .timeline-group-heading .timeline-group-toggle');
    const minis=id=>group(id).locator(':scope > .timeline-child-minis > .timeline-track').evaluateAll(es=>es.map(e=>e.dataset.index));
    const draft=()=>page.evaluate(()=>timeline.getRunDraft());
    const mutations=()=>page.evaluate(()=>calls.filter(c=>c.body).length);
    async function declineLocatorOffer(){const question=page.locator('#timeline-locator-companion[open]');if(await question.count())await page.locator('#timeline-locator-companion-decline').click();}
    const closeHelp=async()=>{await page.mouse.move(3,3);await page.keyboard.press('Escape');};
    async function screenshot(name,locator){
      if(!process.env.GATEWAY_COMBINED_PROBE_DIR)return;
      fs.mkdirSync(process.env.GATEWAY_COMBINED_PROBE_DIR,{recursive:true});await closeHelp();
      await locator.screenshot({path:path.join(process.env.GATEWAY_COMBINED_PROBE_DIR,name+'.png')});
    }
    async function parentOpen(id,open){if((await disclosure(id).getAttribute('aria-expanded'))!==String(open))await disclosure(id).click();await settle();}
    async function hit(control,description){
      await closeHelp();await control.hover();const box=await control.boundingBox();assert(box&&box.width>=16&&box.height>=20,'Usable hit box: '+description);
      assert(await control.evaluate((el,p)=>el===document.elementFromPoint(p.x,p.y)||el.contains(document.elementFromPoint(p.x,p.y)),{x:box.x+box.width/2,y:box.y+box.height/2}),'Real pointer center: '+description);if(await control.evaluate(el=>el.classList.contains('timeline-reorder-handle')))assert.match(await control.evaluate(el=>getComputedStyle(el).backgroundColor),/rgba\([^)]*, 0\)|transparent/,'Grip stays transparent on real hover');
    }
    async function rightBounds(width){
      const data=await page.evaluate(()=>[...document.querySelectorAll('.timeline-track')].filter(el=>el.getClientRects().length&&getComputedStyle(el).display!=='none').map(el=>{
        const heading=el.querySelector('.timeline-track-heading'),row=el.getBoundingClientRect(),h=heading.getBoundingClientRect(),name=heading.querySelector('.timeline-track-select strong'),n=name.getBoundingClientRect(),lane=el.querySelector('.timeline-lane').getBoundingClientRect(),actions=heading.querySelector('.timeline-track-actions'),a=actions?.getBoundingClientRect();
        return{id:el.dataset.index,rowHeight:row.height,headingHeight:h.height,headingWidth:h.width,laneHeight:lane.height,label:name.textContent,labelClipped:name.scrollWidth>name.clientWidth+1,labelRight:n.right,title:name.title,aria:heading.querySelector('.timeline-track-select').getAttribute('aria-label'),actions:a&&{left:a.left,right:a.right,top:a.top,bottom:a.bottom,width:a.width},heading:{left:h.left,right:h.right,top:h.top,bottom:h.bottom},buttons:[...actions?.querySelectorAll('button')||[]].map(button=>{const b=button.getBoundingClientRect();return{control:button.dataset.control||button.className,slot:button.classList.contains('index-help-purpose')?0:button.classList.contains('index-help-dependencies')?1:button.dataset.control==='live'?2:button.dataset.control==='arm'?3:null,width:b.width,height:b.height,left:b.left,right:b.right,top:b.top,bottom:b.bottom}}).filter(b=>b.width&&b.height).sort((a,b)=>a.left-b.left)};
      }));
      for(const item of data){
        assert.equal(item.headingWidth,width<=700?244:260,'Protected label column '+item.id+' at'+width);
        assert(item.actions,'Stable right action region '+item.id);assert.equal(item.actions.width,94,'Four reserved action stations '+item.id);
        assert(item.actions.right<=item.heading.right+.01&&item.actions.bottom<=item.heading.bottom+1,'Actions inside header '+item.id);
        assert(item.labelRight<=item.actions.left+1&&!item.labelClipped,'Label does not overlap actions or clip '+item.id+' at'+width+' '+JSON.stringify(item));
        assert(Math.abs(item.laneHeight-item.headingHeight)<=1,'Label and lane heights align '+item.id);
        assert(item.aria?.startsWith('Select '),'Full accessible name preserved '+item.id);
        for(let i=0;i<item.buttons.length;i++){
          const button=item.buttons[i];if(button.slot!==null)assert.equal(button.left-item.actions.left,button.slot*24,'Fixed help/deps/beacon/Arm station '+item.id+' '+button.control);assert(button.width>=16&&button.height>=20,'Right action usable '+item.id+' '+button.control);
          assert(button.left>=item.actions.left-1&&button.right<=item.actions.right+1&&button.top>=item.heading.top-1&&button.bottom<=item.heading.bottom+1,'Whole action stays in header '+item.id+' '+button.control);
          if(i)assert(item.buttons[i-1].right<=button.left+1,'Right action boxes do not overlap '+item.id);
        }
      }
      for(const id of groups){
        const heading=group(id).locator(':scope > .timeline-group-heading'),actions=heading.locator('.timeline-group-actions');await heading.scrollIntoViewIfNeeded();
        const h=await heading.boundingBox(),a=await actions.boundingBox(),name=await heading.locator('.timeline-group-name strong').boundingBox();
        assert(a&&a.x+a.width<=h.x+h.width+1&&name.x+name.width<=a.x+1,'Parent actions stay right of title '+id+' at'+width);
        const arm=heading.locator('.timeline-group-arm');if(await arm.count()){const b=await arm.boundingBox();assert.equal(b.x-a.x,72,'Parent ArmAll occupies the far-right station');await hit(arm,'parent '+id+' ArmAll at'+width);}
        await hit(disclosure(id),'parent '+id+' disclosure at'+width);
        await hit(heading.locator('.timeline-reorder-handle'),'parent '+id+' grip at'+width);
      }
      for(const id of ['headers','tx-locator','txio-values','bitmap-parcels-standard-v1']){
        const heading=track(id).locator('.timeline-track-heading');
        for(const button of await heading.locator('.timeline-track-actions button').all())await hit(button,id+' right action at'+width);
        await hit(heading.locator('.timeline-reorder-handle'),id+' transparent grip at'+width);
      }
      await closeHelp();measurements.push({width,tracks:data.map(({id,rowHeight,label})=>({id,rowHeight,label}))});
    }
    const layout=()=>page.evaluate(()=>[...document.querySelector('.timeline-tracks').children].filter(el=>!el.hidden&&(el.classList.contains('timeline-group')||el.classList.contains('timeline-track'))).map(el=>el.dataset.group?'group:'+el.dataset.group:el.dataset.index));
    await settle();const unchanged={draft:await draft(),mutations:await mutations()};
    for(const id of groups){assert.equal(await disclosure(id).getAttribute('aria-expanded'),'false','Category default collapsed despite prototype preferences: '+id);assert.equal(await group(id).locator(':scope > .timeline-child-minis').isVisible(),false,'No collapsed child labels/lanes: '+id);}
    for(const id of groups){await group(id).locator('.timeline-group-name').click();assert.equal(await disclosure(id).getAttribute('aria-expanded'),'false','Parent title does not act as another expansion control');}
    assert.equal(await page.locator('.timeline-track-expand').count(),0,'No child expansion disclosure');
    assert.equal(await page.locator('.timeline-group-labels:visible').count(),0,'No inline child legend');
    assert.equal(await page.locator('.timeline-tracks > .timeline-track[data-group]').count(),0,'Every grouped child stays inside parent');
    assert.equal(await track('inscriptions').isVisible(),false,'Inscriptions hides inside collapsed Ordinals');
    assert.equal(await track('inscriptions').locator('.timeline-group-toggle,.timeline-track-expand').count(),0,'Inscriptions has no expander/children');
    assert.deepEqual(await layout(),['group:blocks','group:txio','group:addresses','group:ordinals','group:bitmap'],'Root hierarchy order preserved');
    for(const id of allTracks)assert.equal(await track(id).count(),1,'Exactly one DOM owner '+id);
    const target=group('bitmap').locator('.timeline-bitmap-target-band');await target.scrollIntoViewIfNeeded();assert.equal(await target.isVisible(),true,'Collapsed Bitmap keeps parent target band');
    await screenshot('collapsed-defaults-1440',page.locator('.timeline-tracks'));
    for(const id of groups)await parentOpen(id,true);
    assert.deepEqual(await minis('txio'),transactions,'Transactions has five child tracks');assert.deepEqual(await minis('ordinals'),ordinals,'Ordinals contains separate Sat Ranges and Inscriptions');
    await rightBounds(1440);await screenshot('open-transactions-1440',group('txio'));
    await page.setViewportSize({width:390,height:844});await settle();await rightBounds(390);await screenshot('open-transactions-390',group('txio'));
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Narrow layout does not overflow document');
    await page.setViewportSize({width:1440,height:1000});
    const valueHeight=(await track('txio-values').boundingBox()).height;
    await track('txio-values').locator('.timeline-track-select').click();
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),['txio-values'],'Own label selects own track');
    assert.equal(await page.locator('#timeline-selection-title').textContent(),'Values','Own label selects upper details');
    assert.equal((await track('txio-values').boundingBox()).height,valueHeight,'Own label never expands child');
    assert.match(await track('txio-values').locator('.timeline-track-heading').evaluate(e=>getComputedStyle(e).boxShadow),/inset.*3px|3px.*inset/,'Selected child keeps green left accent');
    assert.equal(await track('txio-values').locator('.timeline-track-count').isVisible(),false,'No inline coverage count');
    assert.equal(await track('txio-values').locator('.timeline-consumer-state').isVisible(),false,'No inline Off/status');
    await parentOpen('txio',false);assert.equal(await track('txio-values').isVisible(),false,'Collapse hides selected child too');await parentOpen('txio',true);
    assert.deepEqual(await minis('txio'),transactions,'Reopen restores same original order');
    await track('headers').locator('.index-help-purpose').focus();assert.equal(await page.locator('.index-help-popover').isVisible(),true,'Headers help available');await closeHelp();
    const deps=track('address-state').locator('.index-help-dependencies');await deps.hover();assert.equal(await page.locator('.index-help-popover').isVisible(),true,'Dependency hover works at actual right icon');await closeHelp();
    const headerBeacon=track('headers').locator('[data-header-sync]'),masterGlyph=await page.locator('#timeline-run-start').innerHTML();
    assert.equal(await track('headers').locator('[data-control=arm]').count(),0,'Headers has a reserved blank Arm station');
    assert.equal(await headerBeacon.locator('svg').evaluate(e=>e.innerHTML),await track('tx-locator').locator('[data-control=live] svg').evaluate(e=>e.innerHTML),'Headers uses the exact same radiating Live glyph');
    const applyNetwork=network=>page.evaluate(network=>{window.GatewayNetworkSnapshot={value:network,observedAt:Date.now()+1};window.dispatchEvent(new Event('gateway:network-status'));},network);
    await applyNetwork({enabled:false,headers_user_paused:false,headers:{header_state:'paused',message:'Outbound networking is disabled.'}});assert.equal(await headerBeacon.isDisabled(),true);assert.equal(await headerBeacon.getAttribute('aria-pressed'),'false');assert.match(await headerBeacon.getAttribute('title'),/networking is disabled/i);
    await applyNetwork({enabled:true,headers_user_paused:false,headers:{header_state:'paused',message:'The healthy Core provider supplies this range; independent mirroring is optional.'}});assert.equal(await headerBeacon.isDisabled(),false);assert.equal(await headerBeacon.getAttribute('aria-label'),'Resume header synchronization');assert.match(await headerBeacon.getAttribute('title'),/optional.*Core|Core.*optional/i);assert.equal(await headerBeacon.getAttribute('aria-pressed'),'false');
    await applyNetwork({enabled:true,headers_user_paused:false,headers:{header_state:'waiting',message:'Waiting for a Bitcoin peer.',error:'Fixture connection timeout'}});assert.equal(await headerBeacon.getAttribute('aria-pressed'),'true','Enabled waiting/error remains a Live choice, not a caught-up claim');assert.match(await headerBeacon.getAttribute('title'),/Fixture connection timeout/);assert.match(await headerBeacon.getAttribute('title'),/freshness.*separately/);
    for(const theme of ['light','dark']){await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);assert.equal(await headerBeacon.evaluate(e=>getComputedStyle(e).backgroundColor),theme==='dark'?'rgb(0, 183, 255)':'rgb(0, 98, 255)','Header enabled beacon shares exact Live color');}
    await page.evaluate(()=>{window.GatewayNetworkSnapshot.observedAt=Date.now()-11000;window.dispatchEvent(new Event('gateway:network-status'));for(let n=0;n<4;n++){timeline.render(snapshot);document.dispatchEvent(new CustomEvent('gateway:index-status',{detail:{localIndexes:n,following:0}}));}});assert.equal(await headerBeacon.getAttribute('aria-pressed'),'false');assert.match(await headerBeacon.getAttribute('title'),/current Header Live policy is unavailable/i);
    assert.equal(await page.evaluate(()=>{window.GatewayNetworkSnapshot.observedAt=Date.now()-9900;window.dispatchEvent(new Event('gateway:network-status'));return document.querySelector('[data-header-sync]').getAttribute('aria-pressed');}),'true','A still-fresh cached policy remains enabled until its deadline');await page.waitForFunction(()=>document.querySelector('[data-header-sync]').getAttribute('aria-pressed')==='false');
    assert.equal(headerRequests.length,0,'Status reuse and automatic cache expiry introduce no background network polling');
    await applyNetwork({enabled:true,headers_user_paused:true,headers:{header_state:'paused',message:'Paused by you.'}});await headerBeacon.click();await page.waitForFunction(()=>document.querySelector('[data-header-sync]').getAttribute('aria-pressed')==='true');await headerBeacon.click();await page.waitForFunction(()=>document.querySelector('[data-header-sync]').getAttribute('aria-pressed')==='false');
    assert.deepEqual(headerRequests.filter(r=>r.method==='POST').map(r=>r.action),['resume','pause'],'Headers immediately uses only its direct pause/resume API');assert.equal(headerRequests.filter(r=>r.method==='GET').length,2,'One network status read follows each explicit header action');assert.equal(await page.locator('#timeline-run-start').innerHTML(),masterGlyph,'Header beacon never changes master Play/Pause glyph');
    assert.equal((await draft()).consumers.some(c=>c.index==='headers'),false,'Headers never becomes an Arm/run consumer');
    await applyNetwork({enabled:true,headers_user_paused:false,headers:{header_state:'waiting',message:'Waiting for a Bitcoin peer.'}});
    assert.equal(await page.locator('[data-control=chain],.timeline-chain-switch').count(),0,'Chain mini controls are absent');
    await page.evaluate(()=>timeline.focus(42));await settle();
    assert.deepEqual(await draft(),unchanged.draft,'Disclosure, selection, hover and navigation preserve all indexing consent');assert.equal(await mutations(),unchanged.mutations,'Read-only interactions send no settings mutations');
    await page.reload();await settle();for(const id of groups)assert.equal(await disclosure(id).getAttribute('aria-expanded'),'true','Intentional category choices survive reload');await track('txio-values').locator('.timeline-track-select').click();assert.deepEqual(await draft(),unchanged.draft,'Presentation persistence leaves consent unchanged');
    // Existing right-side track-height control is the only larger-track action.
    const viewport=page.locator('#timeline-track-viewport'),heightHandle=page.locator('#timeline-scrollbar-vertical .timeline-scroll-handle.end');
    const heights=()=>track('tx-locator').evaluate(el=>({row:el.getBoundingClientRect().height,canvas:el.querySelector('canvas.timeline-lane').height}));
    const beforeZoom=await heights();await heightHandle.focus();await page.keyboard.press('ArrowRight');await settle();const keyboardZoom=await heights();
    assert(keyboardZoom.row>beforeZoom.row&&keyboardZoom.canvas>beforeZoom.canvas,'Keyboard track zoom changes actual row and canvas backing heights');
    await rightBounds(1440);
    // Keep only Ordinals open: its two visible leaves still scale independently of fixed parent headings.
    for(const id of groups)await parentOpen(id,false);await heightHandle.focus();await page.keyboard.press('ArrowRight');await settle();assert.deepEqual(await page.locator('.timeline-group > .timeline-group-heading').evaluateAll(es=>es.map(e=>e.getBoundingClientRect().height)),[32,32,32,32,32],'Closed categories keep fixed32px parents during configured leaf zoom');const configuredClosedHeight=Number(await page.locator('#index-timeline').getAttribute('data-track-height'));await parentOpen('ordinals',true);assert.equal((await track('inscriptions').boundingBox()).height,configuredClosedHeight,'Opening Ordinals applies configured closed-category zoom to its real child');
    const inscriptionBefore=await track('inscriptions').evaluate(el=>({row:el.getBoundingClientRect().height,canvas:el.querySelector('canvas.timeline-lane').height}));
    await heightHandle.focus();await page.keyboard.press('ArrowRight');await settle();const inscriptionAfter=await track('inscriptions').evaluate(el=>({row:el.getBoundingClientRect().height,canvas:el.querySelector('canvas.timeline-lane').height}));
    assert(inscriptionAfter.row>inscriptionBefore.row&&inscriptionAfter.canvas>inscriptionBefore.canvas,'Ordinals-only keyboard zoom changes actual child row and backing canvas');
    for(const id of groups)await parentOpen(id,true);
    // Move into the middle of the list, then preserve the visible top anchor while resizing from the end.
    await viewport.evaluate(el=>el.scrollTop=Math.min(130,el.scrollHeight-el.clientHeight));await settle();
    const anchor=await viewport.evaluate(el=>{const v=el.getBoundingClientRect(),y=v.top+1,node=[...el.querySelectorAll('.timeline-track,.timeline-group-heading')].find(n=>{const b=n.getBoundingClientRect();return b.top<=y&&b.bottom>y});if(!node)return null;const b=node.getBoundingClientRect();return{index:node.dataset.index,group:node.closest('.timeline-group')?.dataset.group,fraction:(y-b.top)/b.height,y};});
    const pointerBefore=await heights();await heightHandle.scrollIntoViewIfNeeded();const handleBox=await heightHandle.boundingBox();await page.mouse.move(handleBox.x+handleBox.width/2,handleBox.y+handleBox.height/2);await page.mouse.down();await page.mouse.move(handleBox.x+handleBox.width/2,handleBox.y+handleBox.height/2-12,{steps:4});await page.mouse.up();await settle();const pointerAfter=await heights();
    assert(pointerAfter.row!==pointerBefore.row&&pointerAfter.canvas!==pointerBefore.canvas,'Pointer track zoom changes actual row and canvas backing heights');
    if(anchor){const shift=await viewport.evaluate((el,a)=>{const node=a.index?el.querySelector('.timeline-track[data-index="'+a.index+'"]'):el.querySelector('.timeline-group[data-group="'+a.group+'"] > .timeline-group-heading');if(!node)return Infinity;const b=node.getBoundingClientRect();return Math.abs(b.top+b.height*a.fraction-(el.getBoundingClientRect().top+1));},anchor);assert(shift<=5,'Mid-list visible anchor moves no more than5px: '+shift);}
    await rightBounds(1440);await page.setViewportSize({width:390,height:844});await settle();await rightBounds(390);
    assert.deepEqual(await draft(),unchanged.draft,'Both keyboard and pointer track zoom preserve consent');assert.equal(await mutations(),unchanged.mutations,'Track zoom is local presentation only');
    const bitmapIDs=['bitmap-districts-standard-v1','bitmap-parcels-standard-v1'],bitmapArm=group('bitmap').locator('.timeline-group-arm');
    // Frozen/Chain definitions remain inspect-only; even an obsolete restored row must not enter bulk consent or state counts.
    await page.evaluate(()=>{for(const id of ['bitmap-districts-canonical-v2','bitmap-district-chain-dev-v1']){snapshot.definitions.push({id,name:'Saved '+id,buildable:false,locked:true});const row=document.createElement('div');row.className='timeline-track';row.dataset.index=id;row.dataset.group='bitmap';row.dataset.bitmapScopeGuard='true';const arm=document.createElement('button');arm.dataset.control='arm';arm.checked=true;row.append(arm);document.querySelector('.timeline-group[data-group=bitmap] > .timeline-child-minis').append(row);}timeline.update(snapshot);});
    assert.equal(await bitmapArm.getAttribute('aria-pressed'),'false','Selected obsolete legacy/Chain rows do not affect Bitmap ArmAll Off state');
    await page.evaluate(()=>timeline.setGlobalRange(792435,792442));
    const districtLive=track(bitmapIDs[0]).locator('[data-control=live]');await districtLive.click();
    const bitmapBefore={draft:await draft(),marks:await page.evaluate(()=>timeline.getGlobalMarks()),selection:await page.evaluate(()=>timeline.getSelectedTracks())};
    await track(bitmapIDs[0]).locator('[data-control=arm]').click();
    assert.equal(await bitmapArm.getAttribute('aria-pressed'),'mixed','One selected Bitmap child makes ArmAll mixed');
    assert.equal((await draft()).consumers.find(c=>c.index==='tx-locator').armed,false,'Individual Bitmap Arm keeps optional locators Off');
    await declineLocatorOffer();
    await parentOpen('bitmap',false);await bitmapArm.click();await declineLocatorOffer();
    assert.equal(await bitmapArm.getAttribute('aria-pressed'),'true','Mixed Bitmap ArmAll arms both Standard children');
    const bitmapOn=await draft();for(const id of bitmapIDs)assert.equal(bitmapOn.consumers.find(c=>c.index===id).armed,true);
    assert.deepEqual(bitmapOn.consumers.filter(c=>!bitmapIDs.includes(c.index)),bitmapBefore.draft.consumers.filter(c=>!bitmapIDs.includes(c.index)),'Bitmap ArmAll changes no other tracks, companions or Chains');
    assert.deepEqual(bitmapOn.consumers.map(c=>[c.index,c.live]),bitmapBefore.draft.consumers.map(c=>[c.index,c.live]),'Bitmap ArmAll preserves every Live choice');
    assert.deepEqual(await page.evaluate(()=>timeline.getGlobalMarks()),bitmapBefore.marks,'Bitmap ArmAll preserves In/Out including hard Out');
    assert.equal(bitmapOn.in,792435);assert.equal(bitmapOn.out,792442);
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),bitmapBefore.selection,'Bitmap ArmAll preserves upper-details selection');
    assert.equal(await disclosure('bitmap').getAttribute('aria-expanded'),'false','Bitmap ArmAll leaves disclosure closed');
    await bitmapArm.click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'false','On Bitmap ArmAll turns both children Off');
    const bitmapOff=await draft();for(const id of bitmapIDs)assert.equal(bitmapOff.consumers.find(c=>c.index===id).armed,false);
    assert.deepEqual(bitmapOff.consumers.map(c=>[c.index,c.live]),bitmapBefore.draft.consumers.map(c=>[c.index,c.live]),'Bulk Off also preserves Live');
    await bitmapArm.click();await declineLocatorOffer();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'true','Off Bitmap ArmAll selects both');
    await parentOpen('bitmap',true);await track(bitmapIDs[1]).locator('[data-control=arm]').click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'mixed','Individual Parcel Off updates parent mixed state');
    await bitmapArm.click();await declineLocatorOffer();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'true');await bitmapArm.click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'false');
    await districtLive.click();assert.equal(await page.locator('[data-control=chain],.timeline-chain-switch').count(),0,'ArmAll never recreates Chain controls');
    assert.equal(await page.locator('[data-bitmap-scope-guard] [data-control=arm]').evaluateAll(buttons=>buttons.every(button=>button.checked===true)),true,'Bulk On/Off leaves restored legacy/Chain controls untouched');
    assert.equal((await draft()).consumers.some(c=>['bitmap-districts-canonical-v2','bitmap-district-chain-dev-v1'].includes(c.index)),false,'Frozen/Chain definitions never enter execution requests');
    await page.evaluate(()=>{for(const row of document.querySelectorAll('[data-bitmap-scope-guard]'))row.remove();snapshot.definitions=snapshot.definitions.filter(d=>!['bitmap-districts-canonical-v2','bitmap-district-chain-dev-v1'].includes(d.id));timeline.update(snapshot);});
    const ordinalBulk=group('ordinals').locator('.timeline-group-arm'),satArm=track('txio-sat-ranges').locator('[data-control=arm]'),satLive=track('txio-sat-ranges').locator('[data-control=live]');
    await satLive.click();const ordinalBefore={draft:await draft(),marks:await page.evaluate(()=>timeline.getGlobalMarks())};
    await satArm.click();assert.equal(await ordinalBulk.getAttribute('aria-pressed'),'mixed','Independent Sat Arm gives Ordinals mixed state');assert.equal((await draft()).consumers.find(c=>c.index==='inscriptions').armed,false,'Sat Arm never arms Inscriptions');
    await ordinalBulk.click();await declineLocatorOffer();let ordinalOn=await draft();assert.deepEqual(ordinalOn.consumers.filter(c=>['sat-state','inscriptions'].includes(c.index)).map(c=>c.armed),[true,true],'Ordinals ArmAll selects exactly its two distinct consumers');
    assert.deepEqual(ordinalOn.consumers.filter(c=>!['sat-state','inscriptions'].includes(c.index)),ordinalBefore.draft.consumers.filter(c=>!['sat-state','inscriptions'].includes(c.index)),'Ordinals ArmAll adds no hidden companions');
    assert.deepEqual(ordinalOn.consumers.map(c=>[c.index,c.live]),ordinalBefore.draft.consumers.map(c=>[c.index,c.live]),'Ordinals ArmAll preserves independent Live choices');assert.deepEqual(await page.evaluate(()=>timeline.getGlobalMarks()),ordinalBefore.marks);
    await track('inscriptions').locator('[data-control=arm]').click();assert.equal(await ordinalBulk.getAttribute('aria-pressed'),'mixed');await ordinalBulk.click();await declineLocatorOffer();assert.equal(await ordinalBulk.getAttribute('aria-pressed'),'true');await ordinalBulk.click();assert.equal(await ordinalBulk.getAttribute('aria-pressed'),'false');await satLive.click();
    const valueArm=track('txio-values').locator('[data-control=arm]');await valueArm.click();
    const valueDraft=await draft(),valueOwner=valueDraft.consumers.find(c=>c.index==='txo-spender');
    assert.equal(valueOwner.retain_output_values,true,'Values own Arm stages retained facts');assert.equal(valueOwner.armed,false,'Values own Arm does not arm parent Links');
    assert.equal(await track('txio-links').locator('[data-control=arm]').getAttribute('aria-pressed'),'false','Links indicator remains Off after Values Arm');
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),['txio-values'],'Values Arm stays isolated from label selection');
    await valueArm.click();
    const live=track('tx-locator').locator('[data-control=live]'),arm=track('tx-locator').locator('[data-control=arm]');
    await live.click();await page.waitForFunction(()=>timeline.getRunDraft().consumers.find(c=>c.index==='tx-locator')?.live===true);
    const liveDraft=await draft();assert.equal(liveDraft.consumers.find(c=>c.index==='tx-locator').armed,false,'Explicit Live remains independent of Arm');
    for(const id of ['txo-spender','utxo-state','sat-state'])assert.equal(liveDraft.consumers.find(c=>c.index===id).live,false,'Live never propagates to sibling '+id);
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),['txio-values'],'Action icon does not select another track');
    for(const theme of ['light','dark']){await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;timeline.redraw();},theme);const rgb=(await live.evaluate(e=>getComputedStyle(e).backgroundColor)).match(/[\d.]+/g).map(Number);assert(rgb[2]>240&&rgb[2]>rgb[0]+150&&rgb[1]>80,'Live remains vivid blue '+theme);}
    await arm.click();const rgb=(await arm.evaluate(e=>getComputedStyle(e).color)).match(/[\d.]+/g).map(Number);assert(rgb[0]>rgb[1]&&rgb[0]>rgb[2],'Arm remains red');
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),['txio-values'],'Arm icon is isolated from label selection');
    const selectedBeforeGroup=await page.evaluate(()=>timeline.getSelectedTracks());await group('txio').locator('.timeline-group-arm').click();
    for(const id of transactions)assert.equal(await track(id).locator('[data-control=arm]').getAttribute('aria-pressed'),'true','Explicit ArmAll scopes Transactions child '+id);
    const allDraft=await draft();for(const id of ['blocks','address-state','address-history','sat-state','inscriptions','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'])assert.equal(allDraft.consumers.find(c=>c.index===id).armed,false,'ArmAll does not reach other category '+id);
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),selectedBeforeGroup,'Parent ArmAll never changes selection');assert.equal(await disclosure('txio').getAttribute('aria-expanded'),'true','Parent ArmAll never collapses category');
    assert.equal(await target.isVisible(),true,'Bitmap parent target band survives open/closed/zoom');
    for(const id of allTracks)assert.equal(await track(id).count(),1,'No interaction duplicates track '+id);
    assert.deepEqual(errors,[]);
    await verifyLocatorConsent(browser,origin);
    console.log('Combined parent interaction independent browser PASS: '+JSON.stringify({measurements,keyboardZoom,pointerBefore,pointerAfter})+'; default collapsed categories, own-label upper details, right action isolation, real hover/hit bounds, actual row/canvas zoom, mid-list anchor, consent preserved, Bitmap target retained.');
  }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
