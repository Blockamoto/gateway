'use strict';
// Real compiled application's browser surface; no routes, APIs or chain data mocked.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const args={};for(let n=2;n<process.argv.length;n+=2)args[process.argv[n].replace(/^--/,'')]=process.argv[n+1];
assert(args.url&&args.out,'--url and --out required');const endpoint=new URL(args.url).origin;
assert(/^http:\/\/127\.0\.0\.1:\d+$/.test(endpoint));fs.mkdirSync(args.out,{recursive:true});
const pw=require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
async function get(route){const response=await fetch(endpoint+route,{signal:AbortSignal.timeout(15000)});assert.equal(response.status,200,route);return response.json();}
const expectedRules={
  "district": [
    "schema: `<district>.bitmap` · regex = `^(?:0|[1-9][0-9]*)\\.bitmap(?![\\s\\S])`",
    "body: claimant's own inscription envelope body bytes",
    "text: parsed MIME type `text/plain` (case-insensitive, parameters permitted)",
    "ascii: ASCII bytes (UTF-8 compatible), ignoring declared charset",
    "height: `<district>` ≤ reveal block height",
    "order: first qualifying inscription reveal per `<district>.bitmap`"
  ],
  "parcel": [
    "schema: `<parcel>.<district>.bitmap` · regex = `^(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.bitmap(?![\\s\\S])`",
    "body: claimant's own inscription envelope body bytes",
    "text: parsed MIME type `text/plain` (case-insensitive, parameters permitted)",
    "ascii: ASCII bytes (UTF-8 compatible), ignoring declared charset",
    "index: `<parcel>` ≤ (`TX count` at `block height` = `<district>`) − 1",
    "child: claimant is a child inscription of the valid `<district>.bitmap` inscription (parent tag 3)",
    "order: first qualifying inscription reveal per `<parcel>.<district>.bitmap`"
  ]
};
(async()=>{const browser=await pw.chromium.launch({headless:true,executablePath});try{
 const before=await get('/api/v1/index/status'),updates=await get('/api/v1/updates/status'),registry=await get('/api/v1/bitmap/profiles');assert.equal(updates.state,'disabled');assert.equal(updates.can_apply,false);assert.equal(registry.read_only,true);
 const fixed={district:'bitmap-districts-standard-v1',parcel:'bitmap-parcels-standard-v1'},profiles=Object.fromEntries(registry.instances.map(i=>[i.id,i]));
 for(const[kind,id]of Object.entries(fixed)){const i=profiles[id];assert(i?.execution_available);assert.equal(i.label,kind==='district'?'Bitmap Districts Standard V1':'Bitmap Parcels Standard V1');assert.equal(i.start_height,792435);assert.deepEqual(i.profile.rules,expectedRules[kind],'Published '+kind+' rule bytes match the pinned standard');assert(i.profile.implementation_limits?.length);assert.equal(i.rules_editable||false,false);}
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],mutations=[];page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(request.method()==='POST')mutations.push(new URL(request.url()).pathname);});
 await page.goto(endpoint+'/?resolve=bitmap.gateway');const frame=page.frameLocator('#indexes-module');await frame.getByRole('heading',{name:'Bitmap',exact:true}).waitFor();
 if(await frame.locator('#timeline-collapse').getAttribute('aria-expanded')==='false')await frame.locator('#timeline-collapse').click();if(await frame.locator('#timeline-group-bitmap').getAttribute('aria-expanded')==='false')await frame.locator('#timeline-group-bitmap').click();
 for(const[kind,id]of Object.entries(fixed)){
   const row=frame.locator('.timeline-track[data-index="'+id+'"]');await row.waitFor({state:'visible'});assert.equal(await row.getAttribute('data-district-parent'),null,'Bitmap tracks are siblings');assert.equal(await row.locator('.timeline-chain-disclosure').count(),0,'No third index nesting disclosure');
   const potential=JSON.parse(await row.locator('.timeline-lane').getAttribute('data-potential')||'[]');assert(potential.every(r=>r.from>=792435),'Potential coverage never precedes the Bitmap origin');
   const chain=row.locator('[data-control=chain]');assert.equal(await chain.isDisabled(),true);assert.match(await chain.getAttribute('title'),/methodology is being developed/);
   await row.locator('.timeline-track-select').click();const rules=frame.locator('#bitmap-profile-settings[data-instance="'+id+'"]');await rules.getByRole('heading',{name:'Published Bitmap rules',exact:true}).waitFor();
   assert.deepEqual(await rules.locator('ol li').allTextContents(),expectedRules[kind],'The UI renders all published rules verbatim');assert.match(await rules.innerText(),/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/);
   assert.equal(await rules.getByRole('heading',{name:'Implementation limits',exact:true}).count(),1);assert.deepEqual(await rules.locator('ul li').allTextContents(),profiles[id].profile.implementation_limits,'Implementation limits have a separate list');
   assert.equal(await rules.getByRole('button',{name:'Chain On',exact:true}).isDisabled(),true);assert.equal(await rules.getByRole('button',{name:'Chain Live',exact:true}).isDisabled(),true);assert.match(await rules.innerText(),/Bitmap Chain methodology is being developed/);
   assert.equal(await rules.getByRole('button',{name:'Save rules',exact:true}).count(),0);assert.equal(await rules.locator('input,select').count(),0,'Fixed rules have no authoring or storage selector');
   assert.doesNotMatch(await rules.innerText(),/development codec v1|versioned development codec|BPS/);
   if(kind==='district'){await page.screenshot({path:path.join(args.out,'canonical-rules-desktop.png')});await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);assert.equal(await frame.locator('#index-timeline').evaluate(el=>el.ownerDocument.documentElement.scrollWidth<=el.ownerDocument.defaultView.innerWidth),true);await page.screenshot({path:path.join(args.out,'canonical-rules-narrow.png')});await page.setViewportSize({width:1440,height:1000});}
 }
 if(args.height){const height=Number(args.height);assert(Number.isSafeInteger(height)&&height>=0);await page.goto(endpoint+'/?resolve='+height+'.bitcoin');const blockFrame=page.frameLocator('#indexes-module');await blockFrame.getByRole('heading',{name:'Block '+height.toLocaleString('en-GB'),exact:true}).waitFor({timeout:90000});await blockFrame.getByRole('heading',{name:/^Transactions \([0-9,]+\)$/}).waitFor();await page.screenshot({path:path.join(args.out,'live-bitcoin-block.png')});}
 const after=await get('/api/v1/index/status');assert.deepEqual(after.jobs,before.jobs);assert.deepEqual(after.run.run,before.run.run);assert.deepEqual(after.run.prepared,before.run.prepared,'Inspection and browsing do not change selected indexing work');assert.equal(mutations.some(route=>/\/index\/(?:run\/(?:start|control|prepare)|build|queue|live)|\/bitmap\/(?:profiles|compare)/.test(route)),false);assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify({result:'PASS',endpoint,real_application:true,mocked_routes:false,live_block_height:args.height?Number(args.height):null,canonical_ids:fixed,rule_counts:{district:6,parcel:7},origin_height:792435,sibling_tracks:true,disabled_chain_controls:true,separate_implementation_limits:true,errors,mutations,update_application:updates.state},null,2)+'\n');
 console.log('Portable browser PASS: exact published6/7 rules and separate limits, sibling Standard V1 Bitmap tracks at792435, grey chain controls, disabled updates, read-only inspection and no hidden indexing'+(args.height?', actual Bitcoin block browsing':'')+'.');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1;});
