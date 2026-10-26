'use strict';
// Read-only acceptance against an owned disposable candidate runtime.
// The caller owns the runtime; this script never starts/stops it or changes indexing intent.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const args={};for(let i=2;i<process.argv.length;i+=2)args[process.argv[i].replace(/^--/,'')]=process.argv[i+1];
assert(args.url&&args.out,'Use --url http://127.0.0.1:PORT --out PATH');
const base=new URL(args.url).origin;assert.match(base,/^http:\/\/127\.0\.0\.1:\d+$/);fs.mkdirSync(args.out,{recursive:true});
const pw=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
async function get(endpoint){const r=await fetch(base+endpoint);assert.equal(r.status,200,endpoint);return r.json();}
(async()=>{
 const before=await get('/api/v1/index/run'),status=await get('/api/v1/index/status'),profiles=await get('/api/v1/bitmap/profiles'),updates=await get('/api/v1/updates/status');
 const id='bitmap-parcels-standard-v1',definition=status.definitions.find(d=>d.id===id),saved=status.instances.find(i=>i.definition===id),source=profiles.instances.find(i=>i.id===id);
 assert(definition);assert.equal(definition.buildable,false);assert.equal(definition.locked||false,false,'Saved Parcel inspection remains released');assert(source);assert.equal(source.execution_available,false);assert(source.profile.canonical_standard,'Disabled execution never changes the canonical identity');assert(saved?.checkpoint||saved?.coverage?.length,'This check expects saved Parcel records in the disposable fixture');assert.equal(updates.state,'disabled');
 const browser=await pw.chromium.launch({headless:true,executablePath});try{
  const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],posts=[],checks=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.method()==='POST')posts.push(new URL(r.url()).pathname);});
  await page.addInitScript(()=>localStorage.setItem('gateway.timeline.workspace.v1',JSON.stringify({collapsed:false,categoryOpen:{bitmap:true,txio:true},tracks:['bitmap-parcels-standard-v1']})));
  await page.goto(base+'/indexes?embedded=1&focus=bitmap.gateway',{waitUntil:'domcontentloaded'});await page.getByRole('heading',{name:'Bitmap',exact:true}).waitFor();
  assert.equal(await page.locator('.timeline-track[data-index^="bitmap-parcels"]').count(),0,'Saved Parcel records and stale browser preferences never restore a timeline track');
  const draft=await page.evaluate(()=>timeline.getRunDraft());assert(!draft.consumers.some(c=>/^bitmap-parcels(?:-|$)/.test(c.index)),'Prepared/start request has no hidden Parcel consumer');
  await page.evaluate(()=>inspectIndex('bitmap-parcels-standard-v1'));assert.equal(await page.locator('#bitmap-profile-settings[data-instance="'+id+'"]').count(),0,'Direct saved-index navigation exposes no Parcel settings');assert.equal(await page.locator('[data-index-export="'+id+'"]').count(),0,'No Parcel export UI entry is exposed');assert.doesNotMatch(await page.locator('body').innerText(),/parcel/i,'Normal catalog and raw UI status contain no hidden Parcel entries');
  for(const width of [1440,390]){await page.setViewportSize({width,height:1000});assert.equal(await page.locator('.timeline-track[data-index^="bitmap-parcels"]').count(),0);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:path.join(args.out,'parcel-disabled-'+width+'.png')});checks.push({width,parcel_timeline_tracks:0,parcel_settings_entries:0,parcel_export_entries:0,no_horizontal_overflow:true});}
  assert.deepEqual(errors,[]);assert(!posts.some(p=>/\/index\/(?:run\/(?:start|control|prepare|amend)|build|queue|live)/.test(p)),'Read-only inspection issues no index mutation');assert.deepEqual(await get('/api/v1/index/run'),before,'Exact Run and Prepared unchanged');assert.deepEqual((await get('/api/v1/index/status')).instances.find(i=>i.definition===id),saved,'Saved Parcel instance unchanged');
  const result={result:'PASS',real_application:true,mocked_routes:false,checks,errors,posts,exact_run_and_prepared_unchanged:true,saved_parcel_unchanged:true,updates_disabled:true};fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify(result,null,2));console.log('Parcel disabled compiled browser PASS: no timeline track, no catalog/settings/export entries, desktop/narrow, unchanged Run/Prepared and saved instance.');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
