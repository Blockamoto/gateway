'use strict';
// Shipped shell and transaction Explorer against isolated API fixtures. No
// Bitcoin provider, index writer, running Gateway or saved user profile is used.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const root=path.resolve(__dirname,'..'),read=name=>fs.readFileSync(path.join(root,'ui/shell',name),'utf8');
const outArg=process.argv.indexOf('--out'),out=outArg<0?null:path.resolve(process.argv[outArg+1]);if(out)fs.mkdirSync(out,{recursive:true});
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
const x='79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798',y='483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8';
const compressed='02'+x,uncompressed='04'+x+y,address='1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa';
const anchor={height:170,block_hash:'b'.repeat(64),commitment:'destination-head',validation:'local_selected_chain',consensus_validated:false};
const previous='c'.repeat(64),spending='d'.repeat(64),block=height=>height.toString(16).padStart(64,'b');
const conventional={n:1,value_sats:456,type:'p2pkh',address};
function transaction(predecessor=false){return{height:predecessor?9:170,block_hash:block(predecessor?9:170),verification_state:'header_anchored',transaction:{txid:predecessor?previous:spending,index:predecessor?0:1,inputs:predecessor?[{n:0,coinbase:true}]:[{n:0,prev_txid:previous,prev_vout:0}],outputs:[{n:0,value_sats:1234,type:'p2pk',address:'',destination:predecessor?uncompressed:compressed},conventional]}};}
const fixtures={
 '/api/v1/settings':{settings:{bitcoin_peers:[],ord_enabled:true},core:{connected:false},core_store:{}},
 '/api/v1/browser/status':{browser:{native_active:false}},'/api/v1/system/status':{},
 '/api/v1/updates/status':{state:'not_configured',current_version:'test',configured:false,auto_check:false},
 '/api/v1/peers':{connections:[],network:{},core_peers:[]},'/api/v1/graph/status':{},'/api/v1/jobs':{jobs:[]},
 '/api/v1/index/status':{job:{},instances:[],live:[]},
 '/api/v1/network':{enabled:false,connected:0,gateway_connected:0,headers:{header_height:170,header_state:'ready'}},
 '/api/v1/setup':{show_automatically:false,settings:{},progress:{stage:0}},
 '/api/v1/data-coverage':{headers:{header_height:170},cache:{},transaction_locations:{},spender_knowledge:{},mounted:{},core:{connected:false},serving:{}},
};
async function run(){
 const server=http.createServer((req,res)=>{res.writeHead(500);res.end('Unexpected fixture request');});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const origin='http://127.0.0.1:'+server.address().port,browser=await pw.chromium.launch({headless:true,executablePath});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900},serviceWorkers:'block',reducedMotion:'reduce'}),page=await context.newPage(),calls=[],errors=[],unexpected=[];
  let currentState='ready',historyState='ready',appearance={theme:'',default:'light'};
  page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',async route=>{
   const req=route.request(),url=new URL(req.url()),body=req.method()==='POST'?req.postDataJSON():undefined;
   const reply=(value,type='application/json',status=200)=>route.fulfill({status,contentType:type,body:type==='application/json'?JSON.stringify(value):value});
   if(url.origin!==origin){unexpected.push(url.href);return route.abort();}
   if(url.pathname==='/')return reply(read('index.html').replace('__GATEWAY_BOOTSTRAP__',JSON.stringify({version:'test',address:'legacy-p2pk-fixture',release_availability:{'txo-spender':true,inscriptions:true}})),'text/html');
   if(url.pathname==='/indexes')return reply('<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/shell/style.css"><link rel="stylesheet" href="/shell/theme.css"></head><body><section id="inspector"></section><script src="/shell/address-view.js"></script><script src="/shell/timeline-explorer.js"></script><script>window.helper=GatewayTimelineExplorer.mount({root:document.getElementById("inspector"),document});window.addEventListener("message",event=>{if(event.source===parent&&event.origin===location.origin&&event.data.type==="gateway:workspace-address")helper.navigate(event.data.address);});const focus=new URLSearchParams(location.search).get("focus");if(focus)helper.navigate(focus);</script></body></html>','text/html');
   if(url.pathname.startsWith('/shell/')){const file=url.pathname.slice(7);assert(!file.includes('/'));if(file.endsWith('.js')||file.endsWith('.css'))return reply(read(file),file.endsWith('.js')?'application/javascript':'text/css');return reply('','image/x-icon');}
   calls.push({method:req.method(),path:url.pathname,query:url.search,body});
   if(url.pathname==='/api/v1/appearance'){if(body)appearance={...appearance,...body};return reply(appearance);}
   if(url.pathname==='/api/v1/navigate'){
    const query=body.address,canonical=/^(?:02|03|04)[0-9a-f]+$/i.test(query)?query.toLowerCase():query;
    if([compressed,uncompressed,address].includes(canonical))return reply({resource:{kind:'address',query:canonical,address_index:{address:canonical,destination_type:canonical===address?'p2pkh':'p2pk'}}});
    if(query==='legacy-p2pk-fixture'||query==='1.170.bitcoin')return reply({resource:{query,transaction:transaction()}});
    if(query==='0.0.9.bitcoin')return reply({resource:{query,transaction:transaction(true),focus_kind:'output',focus_index:0}});
   }
   if(url.pathname==='/api/v1/address/view'){
    const key=url.searchParams.get('address'),mode=url.searchParams.get('mode');assert([compressed,uncompressed,address].includes(key));assert.equal(url.searchParams.get('limit'),'50');
    const common={address:key,destination_type:key===address?'p2pkh':'p2pk',mode};
    const historicalAmount=key===uncompressed?500:250;if(mode==='historical')return reply({...common,state:historyState,snapshot:historyState==='ready'?anchor:null,current_balance_sats:null,received_sats:historyState==='ready'?historicalAmount:null,received_output_count:historyState==='ready'?1:null,coverage:historyState==='ready'?[{from:9,to:10}]:[],gaps:[{from:11,to:170}],complete:false,outputs:historyState==='ready'?[{output:'0.0.9',value_sats:historicalAmount,script_type:key===address?'p2pkh':'p2pk',spent_status:'unknown'}]:[],has_more:false,note:historyState==='ready'?'':'P2PK history requires reviewed backfill.'});
    const ready=currentState==='ready',amount=key===compressed?1234:key===uncompressed?0:456;
    return reply({...common,state:currentState,snapshot:ready||currentState==='rebuild_required'?anchor:null,current_balance_sats:ready?amount:null,current_output_count:ready?(amount?1:0):null,complete:ready,outputs:ready&&amount?[{output:'0.1.170',value_sats:amount,script_type:key===address?'p2pkh':'p2pk',spent_status:'unspent'}]:[],has_more:false,note:ready?'':'Rebuild the earlier address projection to include P2PK destinations.'});
   }
   if(url.pathname==='/api/v1/txio/lookup'){assert.equal(url.searchParams.get('direction'),'input');assert.equal(url.searchParams.get('position'),'i0.1.170');return reply({direction:'input',position:'i0.1.170',state:'known',rows:[{input:'i0.1.170',output:'0.0.9',height:170,block_hash:block(170),creation_block_hash:block(9)}]});}
   if(req.method()==='GET'&&Object.hasOwn(fixtures,url.pathname))return reply(fixtures[url.pathname]);
   unexpected.push(req.method()+' '+url.pathname);return reply({error:'Unexpected request'},'application/json',500);
  });
  const search=async value=>{await page.locator('#address').fill(value);await page.locator('#address-form').evaluate(form=>form.requestSubmit());};
  const destination=async(key,balance)=>{await page.getByRole('heading',{name:key===address?'Address':'P2PK public key',exact:true}).waitFor();await page.waitForFunction(value=>document.querySelector('#address-balance')?.textContent===value,balance);assert.equal(await page.locator('#address').inputValue(),key);assert.equal(new URL(page.url()).searchParams.get('resolve'),key);};
  await page.goto(origin);await page.locator('#output-0 [data-route]').waitFor();assert.equal(await page.locator('#output-0 [data-route]').getAttribute('data-route'),compressed);assert.match(await page.locator('#output-0').innerText(),/P2PK public key/);assert.equal(await page.locator('#output-1 [data-route]').getAttribute('data-route'),address);
  await page.locator('#output-0 [data-route]').click();await destination(compressed,'1,234 sats');assert.match(await page.locator('.page-heading').innerText(),/exact P2PK locking script/);
  await page.locator('#address-historical-summary').click();await page.locator('#address-historical .address-output-row').waitFor();assert.match(await page.locator('#address-historical').innerText(),/Spend status unknown/);assert.match(await page.locator('#address-historical').innerText(),/Gaps: 11–170/);assert.equal(await page.locator('#address-balance').innerText(),'1,234 sats','Historical receipts never replace the current balance');
  for(const key of [uncompressed,compressed]){await search(key.toUpperCase());await destination(key,key===uncompressed?'0 sats':'1,234 sats');await page.locator('#address-historical-summary').click();await page.locator('#address-historical .address-output-row').waitFor();assert.match(await page.locator('#address-historical').innerText(),new RegExp((key===uncompressed?'500':'250')+' sats'));}
  await search('1.170.bitcoin');const frame=page.frameLocator('#indexes-module');await frame.locator('[data-explorer-address]').first().waitFor();assert.equal(await frame.locator('[data-explorer-address]').first().getAttribute('data-explorer-address'),compressed);assert.match(await frame.locator('#timeline-explorer-tx-detail').innerText(),/P2PK public key/);
  await frame.getByRole('button',{name:'Open original output for input 0',exact:true}).click();await frame.locator('.io.focused [data-explorer-address]').waitFor();assert.equal(await frame.locator('.io.focused [data-explorer-address]').getAttribute('data-explorer-address'),uncompressed);await frame.locator('.io.focused [data-explorer-address]').click();await destination(uncompressed,'0 sats');
  currentState=historyState='rebuild_required';await search(compressed);await destination(compressed,'Unavailable');assert.equal(await page.getByRole('button',{name:'Review Current UTXOs rebuild'}).count(),1);await page.locator('#address-historical-summary').click();await page.getByText('P2PK history requires reviewed backfill.',{exact:false}).waitFor();assert.equal(await page.locator('#address-historical .address-output-row').count(),0);assert.doesNotMatch(await page.locator('#address-historical-summary').innerText(),/ · 0/,'Unknown history is not zero');
  currentState=historyState='ready';await search(address);await destination(address,'456 sats');assert.doesNotMatch(await page.locator('.page-heading').innerText(),/P2PK/);await page.locator('#address-historical-summary').click();await page.locator('#address-historical .address-output-row').waitFor();
  assert.equal(calls.some(call=>call.path==='/api/v1/index/build'||call.path==='/api/v1/index/plan'),false,'Destination navigation and history expansion never start or plan builds');
  for(const theme of ['light','dark']){await search(uncompressed);await destination(uncompressed,'0 sats');await page.setViewportSize({width:390,height:844});await page.evaluate(theme=>GatewayTheme.choose(theme),theme);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Full public key wraps at narrow width');if(out)await page.screenshot({path:path.join(out,'p2pk-narrow-'+theme+'.png'),fullPage:true});}
  assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);console.log('P2PK destination browser PASS: output click, predecessor click, compressed/uncompressed paste, exact destination separation, current/history, unknown versus zero, conventional addresses, narrow light/dark, no implicit builds or external requests.');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
}
run().catch(error=>{console.error(error);process.exitCode=1;});
