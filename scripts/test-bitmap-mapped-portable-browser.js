'use strict';
// Read-only, real compiled application check. No routes or chain data are mocked.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const args={};for(let n=2;n<process.argv.length;n+=2)args[process.argv[n].replace(/^--/,'')]=process.argv[n+1];
assert(args.url&&args.out,'--url and --out required');const endpoint=new URL(args.url).origin;
assert(/^http:\/\/127\.0\.0\.1:\d+$/.test(endpoint),'A loopback application URL is required');fs.mkdirSync(args.out,{recursive:true});
const district=Number(args.district??0);assert(Number.isSafeInteger(district)&&district>=0);
const pw=require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
async function get(route){const response=await fetch(endpoint+route,{signal:AbortSignal.timeout(15000)});assert.equal(response.status,200,route);return response.json();}
async function main(){
 const before=await get('/api/v1/index/status'),mapping=await get('/api/v1/bitmap/view?kind=district&district='+district);
 assert.equal(mapping.state,'selected','A saved selected canonical mapping is required');assert.equal(mapping.instance,'bitmap-districts-standard-v1');
 const blocator=mapping.result?.winner_blocator;assert.match(blocator,/^[0-9]+i[0-9]+\.[0-9]+$/);assert.equal(mapping.result.winner.blocator,blocator);
 fs.writeFileSync(path.join(args.out,'mapping.json'),JSON.stringify(mapping,null,2)+'\n');
 const browser=await pw.chromium.launch({headless:true,executablePath}),errors=[],requests=[],responses=[],contentResponses=[],result={endpoint,mocked_routes:false,district,blocator,reveal_height:mapping.result.winner.reveal_height};let page;
 try{
  page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(90000);
  page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.method()==='POST')requests.push({path:new URL(r.url()).pathname,body:r.postDataJSON()});});page.on('response',r=>{if(/\/api\/v1\/(bitmap\/view|ord\/resolve)/.test(r.url()))responses.push({url:r.url(),status:r.status()});if(/^\/content\//.test(new URL(r.url()).pathname))contentResponses.push(r);});
  const frame=()=>page.frameLocator('#indexes-module');
  async function inspect(label,address){
   await frame().locator('#timeline-inscription-panel h2').filter({hasText:blocator}).waitFor({state:'visible'});
   assert.equal(await frame().locator('#timeline-inscription-panel h2').innerText(),blocator,'The existing inscription detail viewer shows the canonical winner');
   await frame().locator('#timeline-inscription-panel[data-state="ready"]').waitFor();
   assert.equal(await frame().getByRole('tab',{name:'Bitmap',exact:true}).getAttribute('aria-selected'),'true');
   await page.waitForFunction(v=>document.querySelector('#address')?.value===v,address);
   const state=await frame().locator('#index-timeline').evaluate(()=>explorer.state());assert.equal(state.height,district,'The focus remains on the mapped district rather than the reveal block');
   const content=frame().frameLocator('iframe[title="Isolated inscription content"]').frameLocator('iframe');await content.locator('body').filter({hasText:district+'.bitmap'}).waitFor();
   const body=await content.locator('body').innerText();assert.equal(body.trim(),district+'.bitmap','Real isolated content displays the winning claim');
   const raw=await frame().getByRole('link',{name:'Raw content',exact:true}).getAttribute('href'),rawURL=new URL(raw),response=contentResponses.findLast(r=>{const u=new URL(r.url());return u.origin===rawURL.origin&&u.pathname===rawURL.pathname&&u.searchParams.get('session')===rawURL.searchParams.get('session');});assert(response,'The browser fetched actual isolated content bytes');assert.equal(response.status(),200);const bytes=await response.body();
   assert.equal(bytes.toString('utf8'),district+'.bitmap');assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'),mapping.result.winner.content_hash);
   const detail=await frame().locator('#timeline-inscription-panel').innerText();assert.match(detail,new RegExp('Reveal block\\s+'+mapping.result.winner.reveal_height));
   const observation={address:await page.locator('#address').inputValue(),state,viewer_heading:blocator,viewer_state:'ready',content_text:body.trim(),content_origin:new URL(raw).origin,raw_sha256:mapping.result.winner.content_hash,detail};
   assert.notEqual(new URL(raw).origin,endpoint,'Untrusted content uses a separate origin');await page.screenshot({path:path.join(args.out,label+'.png')});return observation;
  }
  await page.goto(endpoint+'/?resolve='+district+'.bitmap');result.direct=await inspect('direct-bitmap',district+'.bitmap');
  await page.locator('#address').fill(district+'.bitcoin');await page.locator('#address-form').evaluate(form=>form.requestSubmit());
  await frame().locator('#timeline-explorer-panel[data-state="ready"] h2').filter({hasText:'Block '+district.toLocaleString('en-GB')}).waitFor({state:'attached'});
  // Bitmap remains selected when navigating blocks; clicking the visible tab is also safe if initial state changes.
  const tab=frame().getByRole('tab',{name:'Bitmap',exact:true});await tab.waitFor({state:'visible'});if(await tab.getAttribute('aria-selected')!=='true')await tab.click();
  result.block=await inspect('block-bitmap-tab',district+'.bitcoin');
  assert(requests.some(r=>r.path==='/api/v1/ord/resolve'&&r.body.id===blocator),'The canonical blocator is passed to the existing inscription resolver');
  assert.equal(requests.some(r=>/\/api\/v1\/index\/(?:run|build|queue|live)|\/api\/v1\/bitmap\/(?:profiles|compare)/.test(r.path)),false,'Browsing sends no index control or profile mutation');
  const after=await get('/api/v1/index/status');assert.deepEqual(after.jobs,before.jobs);assert.deepEqual(after.run.run,before.run.run);assert.deepEqual(after.run.prepared,before.run.prepared);assert.deepEqual(errors,[]);
  Object.assign(result,{result:'PASS',no_index_controls:true,shared_run_unchanged:true,errors,requests,responses});
  fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify(result,null,2)+'\n');console.log('Actual mapped Bitmap browser PASS: '+district+'.bitmap and block '+district+' reuse existing inscription detail viewer at '+blocator+', isolated exact claim bytes, district focus retained, no index controls or run changes.');
 }catch(error){Object.assign(result,{result:'FAIL',error:error.stack,errors,requests,responses});if(page){const workspace=page.frames().find(f=>/\/indexes(?:\?|$)/.test(f.url()));if(workspace)result.workspace=await workspace.evaluate(()=>({state:explorer.state(),panes:[...document.querySelectorAll('#timeline-inspector [role="tabpanel"]')].map(p=>({id:p.id,hidden:p.hidden,text:p.textContent.slice(0,500)}))})).catch(()=>null);await page.screenshot({path:path.join(args.out,'failure.png')}).catch(()=>{});}fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify(result,null,2)+'\n');throw error;}
 finally{await browser.close();}
}
main().catch(error=>{console.error(error);process.exitCode=1;});
