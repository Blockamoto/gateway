'use strict';
// Real compiled application's browser surface; no routes, APIs or chain data mocked.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const args={};for(let n=2;n<process.argv.length;n+=2)args[process.argv[n].replace(/^--/,'')]=process.argv[n+1];
assert(args.url&&args.out,'--url and --out required');const endpoint=new URL(args.url).origin;
assert(/^http:\/\/127\.0\.0\.1:\d+$/.test(endpoint));fs.mkdirSync(args.out,{recursive:true});
const pw=require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(k=>process.env[k]?[path.join(process.env[k],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[k],'Google/Chrome/Application/chrome.exe')]:[])].find(p=>p&&fs.existsSync(p));
async function get(route){const response=await fetch(endpoint+route,{signal:AbortSignal.timeout(15000)});assert.equal(response.status,200,route);return response.json();}
(async()=>{const browser=await pw.chromium.launch({headless:true,executablePath});try{
 const before=await get('/api/v1/index/status'),updates=await get('/api/v1/updates/status');assert.equal(updates.state,'disabled');assert.equal(updates.can_apply,false);
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],mutations=[];page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(request.method()==='POST')mutations.push(new URL(request.url()).pathname);});
 await page.goto(endpoint+'/?resolve=bitmap.gateway');const frame=page.frameLocator('#indexes-module');await frame.getByRole('heading',{name:'Bitmap',exact:true}).waitFor();
 await frame.getByRole('button',{name:'Inspect rules and saved state',exact:true}).first().click();await frame.getByRole('heading',{name:'Implemented rules',exact:true}).waitFor();
 const rules=frame.locator('#bitmap-profile-settings');assert.match(await rules.innerText(),/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/);assert.match(await rules.innerText(),/ASCII body bytes/);assert.equal(await frame.getByRole('button',{name:'Save rules',exact:true}).count(),0);
 await page.screenshot({path:path.join(args.out,'canonical-rules-desktop.png')});await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(args.out,'canonical-rules-narrow.png')});
 if(args.height){await page.setViewportSize({width:1440,height:1000});await page.goto(endpoint+'/?resolve='+Number(args.height)+'.bitcoin');await page.frameLocator('#indexes-module').getByRole('heading',{name:'Block '+Number(args.height).toLocaleString('en-GB'),exact:true}).waitFor({timeout:90000});await page.screenshot({path:path.join(args.out,'live-bitcoin-block.png')});}
 const after=await get('/api/v1/index/status');assert.deepEqual(after.jobs,before.jobs);assert.deepEqual(after.run.run,before.run.run);assert.equal(mutations.some(route=>/\/index\/(?:run\/(?:start|control)|build|queue|live)/.test(route)),false);assert.deepEqual(errors,[]);
 fs.writeFileSync(path.join(args.out,'result.json'),JSON.stringify({result:'PASS',endpoint,real_application:true,mocked_routes:false,live_block_height:args.height?Number(args.height):null,errors,mutations,update_application:updates.state},null,2)+'\n');
 console.log('Portable browser PASS: actual canonical rule metadata, desktop/narrow layout, disabled updates and no hidden indexing'+(args.height?', live Bitcoin block navigation':'')+'.');
}finally{await browser.close();}})().catch(error=>{console.error(error);process.exitCode=1;});
