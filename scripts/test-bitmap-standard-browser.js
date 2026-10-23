'use strict';
// Real workspace assets with isolated API state; no application or user profile.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const pw=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
const origin='http://bitmap-canonical.test',hash='a'.repeat(64),custom='bitmap-districts-'+'1'.repeat(32),calls=[];
const capabilities={custom_profiles:false,comparisons:false,custom_execution:false,reason:'Legacy experimental indexing, custom rules and comparisons are unavailable.'};
const instance=(id,label,kind,parent,chain,legacy=false)=>({id,label,kind,start_height:0,profile_hash:hash,configuration_revision:1,rules_editable:false,execution_available:!legacy,classification:legacy?'legacy':kind==='chain'?'development_chain':'canonical',rules_lock_reason:'Fixed read-only definition.',district_instance:parent,chain_instance:chain,profile:{id:label,canonical_standard:!legacy&&kind!=='chain',fingerprint_schema:legacy?'old-v1':'gateway-bitmap-canonical-rules-v1',reference_revision:'335958311eb1c8ce4d14c48bc5c21d23c7a4b134',reference_url:'https://github.com/Blockamoto/bitmap/blob/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/README.md',rules:['Parse text/plain case-insensitively; parameters permitted.','Interpret ASCII body bytes; ignore declared charset.','First qualifying reveal wins.']}});
const profiles={capabilities,read_only:true,note:'Fixed canonical indexes; legacy saved definitions remain available.',instances:[
 instance('bitmap-districts-canonical','Bitmap Districts','district',null,'bitmap-district-chain-dev-v1'),
 instance('bitmap-parcels-canonical','Bitmap Parcels','parcel','bitmap-districts-canonical','bitmap-parcel-chain-dev-v1'),
 instance('bitmap-district-chain-dev-v1','District Chain','chain','bitmap-districts-canonical'),
 instance('bitmap-parcel-chain-dev-v1','Parcel Chain','chain','bitmap-parcels-canonical'),
 instance(custom,'Saved legacy District','district',null,null,true)],drafts:[],chain_metadata:{}};
for(const item of profiles.instances.filter(i=>i.kind==='chain'))profiles.chain_metadata[item.id]={codec:'gateway-bitmap-chain-dev-v1',draft:true,source_instance:item.district_instance,immutable_definitions:[{definition_bytes:'fixed bytes',activation_heights:[],retirement_heights:[]}]};
const definitions=[{id:'headers',name:'Headers',buildable:false},{id:'blocks',name:'Raw blocks',buildable:true},...profiles.instances.map(i=>({id:i.id,name:i.label,kind:i.kind,district_instance:i.district_instance,chain_instance:i.chain_instance,profile_hash:i.profile_hash,locked:!i.execution_available,buildable:i.execution_available,lock_reason:capabilities.reason}))].map(d=>({...d,start_height:0,network:'bitcoin-mainnet',rules:[],dependencies:[],version:1}));
const state={definitions,bitmap_capabilities:capabilities,instances:[{definition:custom,coverage:[{from:0,to:0}],queryable:true}],providers:[],live:[],jobs:[],job:{},errors:[],run:{configured:true,prepared:{consumers:[]}},timeline:{tip_known:true,tip_height:1,tip_hash:hash,tracks:[]}};
async function main(){const browser=await pw.chromium.launch({headless:true,executablePath});try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{const req=route.request(),url=new URL(req.url()),reply=(data,status=200,type='application/json')=>route.fulfill({status,contentType:type,body:type==='application/json'?JSON.stringify(data):data});if(url.origin!==origin)return route.abort();
  if(url.pathname==='/indexes')return reply(fs.readFileSync(path.join(root,'ui/shell/indexes.html'),'utf8').replaceAll('__INDEX_NONCE__','fixture').replace('__INDEX_BOOTSTRAP__','{}'),200,'text/html');
  if(url.pathname.startsWith('/shell/')){const file=path.join(root,'ui',url.pathname);return reply(fs.readFileSync(file),200,file.endsWith('.js')?'application/javascript':'text/css');}
  if(url.pathname==='/favicon.ico')return reply('',200,'image/x-icon');
  calls.push({path:url.pathname,method:req.method(),body:req.postDataJSON()});
  if(url.pathname==='/api/v1/index/status')return reply(state);
  if(url.pathname==='/api/v1/index/layout')return reply({schema:2,revision:1,order:{}});
  if(url.pathname==='/api/v1/index/run/prepare'){state.run.prepared=req.postDataJSON();return reply(state.run);}
  if(url.pathname==='/api/v1/bitmap/profiles')return reply(profiles);
  if(url.pathname==='/api/v1/bitmap/view')return reply({kind:'chain',state:'not_built',note:'No saved chain state.'});
  if(url.pathname==='/api/v1/index/delete')return reply({ready:false,scope:[],retained:[],dependencies:[],blockers:['Read-only fixture.']});
  return reply({error:'Unexpected endpoint '+url.pathname},404);
 });
 await page.goto(origin+'/indexes?embedded=1&focus=bitmap.gateway');await page.getByRole('heading',{name:'Bitmap',exact:true}).waitFor();
 assert.equal(await page.locator('#timeline-add-index').isVisible(),false);assert.equal(await page.locator('.timeline-add-parcel-row').count(),0);
 assert.equal(await page.getByRole('combobox',{name:'Baseline ruleset'}).count(),0);assert.equal(calls.some(c=>c.path==='/api/v1/bitmap/compare'),false);
 const district=page.locator('.timeline-track[data-index="bitmap-districts-canonical"]');await page.getByRole('button',{name:'Expand index tracks',exact:true}).click();await district.locator('.timeline-track-select').click();await page.locator('#bitmap-profile-settings[data-instance="bitmap-districts-canonical"]').waitFor();
 await page.getByRole('heading',{name:'Implemented rules',exact:true}).waitFor();assert.match(await page.locator('#bitmap-profile-settings').innerText(),/ASCII body bytes/);assert.match(await page.locator('#bitmap-profile-settings').innerText(),/335958311/);
 assert.equal(await page.locator('#bitmap-profile-settings input').count(),0);assert.equal(await page.getByRole('button',{name:'Save rules',exact:true}).count(),0);assert.equal(await page.getByRole('button',{name:'Rename',exact:true}).count(),0);
 await page.getByText('Immutable definitions, activation history and development codec',{exact:true}).click();assert.match(await page.locator('#bitmap-profile-settings').innerText(),/gateway-bitmap-chain-dev-v1/);
 await page.getByRole('button',{name:'Chain Live',exact:true}).click();await page.getByRole('combobox',{name:'Chain history retention'}).selectOption('pruned');await district.locator('[data-control=chain]').click();
 let draft=await page.evaluate(()=>timeline.getRunDraft());assert(draft.consumers.some(c=>c.index==='bitmap-district-chain-dev-v1'&&c.armed&&c.live&&c.options?.retention==='pruned'));
 const disclosure=district.locator('.timeline-chain-disclosure');if(await disclosure.count())await disclosure.click();
 await page.evaluate(()=>inspectIndex('bitmap-parcels-canonical'));await page.locator('#bitmap-profile-settings[data-instance="bitmap-parcels-canonical"]').waitFor();await page.getByRole('button',{name:'Optional chain On',exact:true}).click();
 draft=await page.evaluate(()=>timeline.getRunDraft());assert(draft.consumers.some(c=>c.index==='bitmap-parcel-chain-dev-v1'&&c.armed),'Parcel chain has its own optional control');
 await page.evaluate(id=>inspectIndex(id),custom);await page.locator('#bitmap-profile-settings[data-instance="'+custom+'"]').waitFor();assert.match(await page.locator('#bitmap-profile-settings').innerText(),/inspection only/);assert.equal(await page.getByRole('button',{name:'Optional chain On',exact:true}).count(),0);
 assert.equal(calls.some(c=>c.path==='/api/v1/bitmap/profiles'&&c.method==='POST'),false);assert.equal(calls.some(c=>c.path==='/api/v1/bitmap/profiles/validate'),false);
 if(process.env.GATEWAY_TEST_ARTIFACTS){fs.mkdirSync(process.env.GATEWAY_TEST_ARTIFACTS,{recursive:true});await page.screenshot({path:path.join(process.env.GATEWAY_TEST_ARTIFACTS,'bitmap-canonical-desktop.png')});await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(process.env.GATEWAY_TEST_ARTIFACTS,'bitmap-canonical-narrow.png')});}
 assert.deepEqual(errors,[]);console.log('Canonical Bitmap browser PASS: fixed rules and pinned source, inspect-only legacy data, no authoring/comparison requests, independent District/Parcel chains and retention staging.');
}finally{await browser.close();}}
main().catch(e=>{console.error(e);process.exitCode=1;});
