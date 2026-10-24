'use strict';
// Independent production-asset probe. Every API response/profile is disposable.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..'),pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
const html=`<!doctype html><html data-theme="light"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/index-timeline.css"><link rel="stylesheet" href="/shell/bitmap-workspace.css"><link rel="stylesheet" href="/shell/index-help.css"></head><body class="indexes-workspace embedded"><main><section id="index-timeline"></section></main><script src="/shell/index-cards.js"></script><script src="/shell/index-help-content.js"></script><script src="/shell/index-help.js"></script><script src="/shell/index-transport.js"></script><script src="/shell/index-scrollbar-geometry.js"></script><script src="/shell/index-timeline.js"></script><script src="/shell/index-workspace.js"></script><script>
window.calls=[];const ids=['headers','blocks','tx-locator','txo-spender','utxo-state','sat-state','address-state','address-history','inscriptions','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'];window.snapshot={definitions:ids.map(id=>({id,name:({'headers':'Headers','blocks':'Raw blocks','tx-locator':'Transaction locators','txo-spender':'Positional links','utxo-state':'UTXO set','address-state':'Current Addresses','address-history':'Historical Outputs','inscriptions':'Inscriptions','bitmap-districts-standard-v1':'Bitmap Districts Standard V1','bitmap-parcels-standard-v1':'Bitmap Parcels Standard V1'})[id]||id,buildable:id!=='headers',start_height:id.startsWith('bitmap-')?792435:0,version:1,rules:[],dependencies:[]})),instances:[],providers:[],jobs:[],job:{},live:[],run:{schema:1,configured:true,prepared:{consumers:ids.map(index=>({index,armed:false,live:false}))},run:null},timeline:{tip_height:999999,tracks:ids.map(id=>({id,coverage:id==='headers'?[{from:0,to:999999}]:[],coverage_complete:true}))}};window.layout={schema:2,revision:0,order:{}};window.timeline=GatewayIndexTimeline.mount({document,root:document.querySelector('#index-timeline'),select:id=>timeline.select(id),loadHierarchy:async p=>({...p,count:p.kind==='block'?3:2,block_hash:'a'.repeat(64)}),api:async(action,body)=>{calls.push({action,body});if(action==='layout'){if(body)layout={schema:2,revision:layout.revision+1,order:body.order};return layout;}if(action==='run/prepare'){snapshot.run.prepared=body;return snapshot.run;}throw Error('Unexpected action '+action)},refresh:async()=>timeline.render(snapshot)});timeline.render(snapshot);GatewayIndexWorkspace.mountLayout(document.querySelector('#index-timeline'));
</script></body></html>`;
const transactions=['tx-locator','txio-links','txio-values','txio-scripts','utxo-state','txio-sat-ranges'];
const allTracks=['headers','blocks',...transactions,'address-state','address-history','inscriptions','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'];
const groups=['blocks','txio','addresses','bitmap'];
(async()=>{
  const server=http.createServer((req,res)=>{
    const u=new URL(req.url,'http://fixture');
    if(u.pathname==='/'){res.setHeader('Content-Type','text/html');return res.end(html);}
    if(u.pathname==='/api/v1/bitmap/target-coverage'){
      const from=Number(u.searchParams.get('from')),to=Number(u.searchParams.get('to'));
      res.setHeader('Content-Type','application/json');
      return res.end(JSON.stringify({state:'ready',axis:u.searchParams.get('axis'),...(u.searchParams.has('district')?{district:Number(u.searchParams.get('district'))}:{}),from,to,bins:[{from,to:from,targets:1,winners:1,unknown:0}],note:'Disposable target fixture'}));
    }
    if(u.pathname==='/favicon.ico'){res.writeHead(204);return res.end();}
    const file=path.resolve(root,'ui','.'+u.pathname);
    if(!file.startsWith(path.resolve(root,'ui/shell')+path.sep)||!fs.existsSync(file)){res.writeHead(404);return res.end();}
    res.setHeader('Content-Type',file.endsWith('.js')?'application/javascript':'text/css');res.end(fs.readFileSync(file));
  });
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const origin='http://127.0.0.1:'+server.address().port,browser=await pw.chromium.launch({headless:true,executablePath});
  try{
    const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],measurements=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.addInitScript(()=>{
      if(!localStorage.getItem('independent-parent-probe')){
        localStorage.setItem('independent-parent-probe','1');
        localStorage.setItem('gateway.timeline.workspace.v1',JSON.stringify({collapsed:false,groups:{blocks:true,txio:true,addresses:true,bitmap:true},expandedTracks:{'tx-locator':true}}));
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
        return{id:el.dataset.index,rowHeight:row.height,headingHeight:h.height,headingWidth:h.width,laneHeight:lane.height,label:name.textContent,labelClipped:name.scrollWidth>name.clientWidth+1,labelRight:n.right,title:name.title,aria:heading.querySelector('.timeline-track-select').getAttribute('aria-label'),actions:a&&{left:a.left,right:a.right,top:a.top,bottom:a.bottom},heading:{left:h.left,right:h.right,top:h.top,bottom:h.bottom},buttons:[...actions?.querySelectorAll('button')||[]].map(button=>{const b=button.getBoundingClientRect();return{control:button.dataset.control||button.className,width:b.width,height:b.height,left:b.left,right:b.right,top:b.top,bottom:b.bottom}}).filter(b=>b.width&&b.height)};
      }));
      for(const item of data){
        assert.equal(item.headingWidth,202,'202px label column '+item.id+' at'+width);
        assert(item.actions,'Stable right action region '+item.id);
        assert(item.actions.right<=item.heading.right+.01&&item.actions.bottom<=item.heading.bottom+1,'Actions inside header '+item.id);
        assert(item.labelRight<=item.actions.left+1&&!item.labelClipped,'Label does not overlap actions or clip '+item.id+' at'+width+' '+JSON.stringify(item));
        assert(Math.abs(item.laneHeight-item.headingHeight)<=1,'Label and lane heights align '+item.id);
        assert(item.aria?.startsWith('Select '),'Full accessible name preserved '+item.id);
        for(let i=0;i<item.buttons.length;i++){
          const button=item.buttons[i];assert(button.width>=16&&button.height>=20,'Right action usable '+item.id+' '+button.control);
          assert(button.left>=item.actions.left-1&&button.right<=item.actions.right+1&&button.top>=item.heading.top-1&&button.bottom<=item.heading.bottom+1,'Whole action stays in header '+item.id+' '+button.control);
          if(i)assert(item.buttons[i-1].right<=button.left+1,'Right action boxes do not overlap '+item.id);
        }
      }
      for(const id of groups){
        const heading=group(id).locator(':scope > .timeline-group-heading'),actions=heading.locator('.timeline-group-actions');await heading.scrollIntoViewIfNeeded();
        const h=await heading.boundingBox(),a=await actions.boundingBox(),name=await heading.locator('.timeline-group-name strong').boundingBox();
        assert(a&&a.x+a.width<=h.x+h.width+1&&name.x+name.width<=a.x+1,'Parent actions stay right of title '+id+' at'+width);
        const arm=heading.locator('.timeline-group-arm');if(await arm.count()){const b=await arm.boundingBox();assert(b.x>=a.x&&b.x+b.width<=a.x+a.width+1,'Parent ArmAll belongs to right action region');await hit(arm,'parent '+id+' ArmAll at'+width);}
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
    assert.equal(await track('inscriptions').isVisible(),true,'Inscriptions stays standalone');
    assert.equal(await track('inscriptions').locator('.timeline-group-toggle,.timeline-track-expand').count(),0,'Inscriptions has no expander/children');
    assert.deepEqual(await layout(),['group:blocks','group:txio','group:addresses','inscriptions','group:bitmap'],'Root hierarchy order preserved');
    for(const id of allTracks)assert.equal(await track(id).count(),1,'Exactly one DOM owner '+id);
    const target=group('bitmap').locator('.timeline-bitmap-target-band');await target.scrollIntoViewIfNeeded();assert.equal(await target.isVisible(),true,'Collapsed Bitmap keeps parent target band');
    await screenshot('collapsed-defaults-1440',page.locator('.timeline-tracks'));
    for(const id of groups)await parentOpen(id,true);
    assert.deepEqual(await minis('txio'),transactions,'Transactions child order preserved');
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
    // Recheck actual scaling with categories closed: only standalone Inscriptions remains.
    for(const id of groups)await parentOpen(id,false);
    const inscriptionBefore=await track('inscriptions').evaluate(el=>({row:el.getBoundingClientRect().height,canvas:el.querySelector('canvas.timeline-lane').height}));
    await heightHandle.focus();await page.keyboard.press('ArrowRight');await settle();const inscriptionAfter=await track('inscriptions').evaluate(el=>({row:el.getBoundingClientRect().height,canvas:el.querySelector('canvas.timeline-lane').height}));
    assert(inscriptionAfter.row>inscriptionBefore.row&&inscriptionAfter.canvas>inscriptionBefore.canvas,'Closed-category keyboard zoom changes visible standalone row and backing canvas');
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
    const keep=page.getByRole('button',{name:'Keep current selection',exact:true});if(await keep.count())await keep.click();
    await parentOpen('bitmap',false);await bitmapArm.click();
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
    await bitmapArm.click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'true','Off Bitmap ArmAll selects both');
    await parentOpen('bitmap',true);await track(bitmapIDs[1]).locator('[data-control=arm]').click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'mixed','Individual Parcel Off updates parent mixed state');
    await bitmapArm.click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'true');await bitmapArm.click();assert.equal(await bitmapArm.getAttribute('aria-pressed'),'false');
    await districtLive.click();assert.equal(await page.locator('[data-control=chain],.timeline-chain-switch').count(),0,'ArmAll never recreates Chain controls');
    assert.equal(await page.locator('[data-bitmap-scope-guard] [data-control=arm]').evaluateAll(buttons=>buttons.every(button=>button.checked===true)),true,'Bulk On/Off leaves restored legacy/Chain controls untouched');
    assert.equal((await draft()).consumers.some(c=>['bitmap-districts-canonical-v2','bitmap-district-chain-dev-v1'].includes(c.index)),false,'Frozen/Chain definitions never enter execution requests');
    await page.evaluate(()=>{for(const row of document.querySelectorAll('[data-bitmap-scope-guard]'))row.remove();snapshot.definitions=snapshot.definitions.filter(d=>!['bitmap-districts-canonical-v2','bitmap-district-chain-dev-v1'].includes(d.id));timeline.update(snapshot);});
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
    const allDraft=await draft();for(const id of ['blocks','address-state','address-history','inscriptions','bitmap-districts-standard-v1','bitmap-parcels-standard-v1'])assert.equal(allDraft.consumers.find(c=>c.index===id).armed,false,'ArmAll does not reach other category '+id);
    assert.deepEqual(await page.evaluate(()=>timeline.getSelectedTracks()),selectedBeforeGroup,'Parent ArmAll never changes selection');assert.equal(await disclosure('txio').getAttribute('aria-expanded'),'true','Parent ArmAll never collapses category');
    assert.equal(await target.isVisible(),true,'Bitmap parent target band survives open/closed/zoom');
    for(const id of allTracks)assert.equal(await track(id).count(),1,'No interaction duplicates track '+id);
    assert.deepEqual(errors,[]);
    console.log('Combined parent interaction independent browser PASS: '+JSON.stringify({measurements,keyboardZoom,pointerBefore,pointerAfter})+'; default collapsed categories, own-label upper details, right action isolation, real hover/hit bounds, actual row/canvas zoom, mid-list anchor, consent preserved, Bitmap target retained.');
  }finally{await browser.close();await new Promise(r=>server.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
