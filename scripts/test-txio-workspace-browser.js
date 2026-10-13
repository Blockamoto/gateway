'use strict';
// Exercise the shipped workspace against isolated positional relationship
// responses. These fixtures perform no indexing, peer activity or user writes.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const pw=(()=>{try{return require('playwright');}catch{return require(path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft/Edge/Application/msedge.exe'),path.join(process.env[key],'Google/Chrome/Application/chrome.exe')]:[])].find(file=>file&&fs.existsSync(file));
const hash=height=>height.toString(16).padStart(64,'a');
const txid=(height,index)=>(height*10+index).toString(16).padStart(64,'b');
const transaction=(height,index)=>({index,txid:txid(height,index),coinbase:index===0,inputs:index===0?[{n:0,coinbase:true}]:[{n:0,prev_txid:txid(830000,1),prev_vout:0},{n:1,prev_txid:txid(830000,1),prev_vout:2}],outputs:[{n:0,value_sats:30},{n:1,value_sats:20},{n:2,value_sats:10}],version:2,lock_time:0,size:150,weight:600,vsize:150});
const block=height=>({height,hash:hash(height),transaction_count:3,transactions:[0,1,2].map(index=>({index,txid:txid(height,index),output_sats:60,coinbase:index===0})),serialized_bytes:500,verification_state:'header_anchored',source_network:'cache'});
const link=(input,output)=>{const height=Number(input.split('.')[2]),creation=Number(output.split('.')[2]);return{input,output,height,block_hash:hash(height),creation_block_hash:hash(creation)};};

(async()=>{
 const server=http.createServer((req,res)=>{res.writeHead(500);res.end('Unexpected request');});await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const origin='http://127.0.0.1:'+server.address().port,browser=await pw.chromium.launch({headless:true,executablePath});
 try{
  let knownFlow=false;
  const page=await browser.newPage({viewport:{width:1200,height:820}}),calls=[],errors=[],external=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url()),body=request.method()==='POST'?request.postDataJSON():undefined;
   const reply=(value,type='application/json')=>route.fulfill({contentType:type,body:type==='application/json'?JSON.stringify(value):value});
   if(url.origin!==origin){external.push(url.href);return route.abort();}
   if(url.pathname==='/test')return reply('<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;background:#fafafa;color:#24252c;font-family:system-ui}main{display:flex;height:800px}.timeline-inspector{width:50%;height:100%;overflow:auto}#workspace-viewport{width:50%;height:600px}#workspace-viewport-body{height:100%;width:100%}:root{--bg:#fafafa;--panel:#fff;--text:#24252c;--muted:#555;--line:#ddd}</style><link rel="stylesheet" href="/shell/block-viewport.css"><link rel="stylesheet" href="/shell/transaction-flow-layout.css"></head><body><main><section id="timeline-inspector" class="timeline-inspector"><p id="retained">Existing inspector</p></section><section id="workspace-viewport"><div id="workspace-viewport-body"></div></section></main><script src="/shell/transaction-flow-layout.js"></script><script src="/shell/block-viewport.js"></script><script src="/shell/timeline-explorer.js"></script><script>window.entities=[];window.intents=[];window.notices=[];window.traces=[];window.helper=GatewayTimelineExplorer.mount({root:document.getElementById("timeline-inspector"),viewport:document.getElementById("workspace-viewport-body"),document,onEntity:value=>entities.push(value),onNavigateIntent:value=>intents.push(value),notify:value=>notices.push(value),onTrace:value=>traces.push(value)});</script></body></html>','text/html');
   if(url.pathname.startsWith('/shell/')){const file=path.resolve(root,'ui','.'+url.pathname);assert(file.startsWith(path.resolve(root,'ui/shell')+path.sep));return reply(fs.readFileSync(file,'utf8'),url.pathname.endsWith('.js')?'application/javascript':'text/css');}
   if(url.pathname==='/favicon.ico')return reply('','image/x-icon');
   calls.push({path:url.pathname,query:url.search,body});
   if(url.pathname==='/api/v1/navigate'){
    const parts=body.address.replace(/\.bitcoin$/,'').split('.'),height=Number(parts.at(-1));
    if(parts.length===1)return reply({resource:{block:block(height)},elapsed_ms:1});
    const index=Number(parts.length===2?parts[0]:parts[1]),kind=parts.length===2?'transaction':parts[0].startsWith('i')?'input':'output',ordinal=Number(parts[0].replace(/^i/,''));
    return reply({resource:{transaction:{height,block_hash:hash(height),transaction:transaction(height,index),verification_state:'header_anchored'},coordinate:body.address,focus_kind:kind,focus_index:ordinal},elapsed_ms:1});
   }
   if(url.pathname==='/api/v1/index/block-map'){
    const selected=Number.parseInt(url.searchParams.get('block').replace(/^a+/,''),16),value=block(selected);
    return reply({height:selected,hash:value.hash,total:3,transactions:value.transactions.map(tx=>({...tx,vsize:150}))});
   }
   if(url.pathname==='/api/v1/flow')return reply({txid:body.txid,inputs:[{index:0,value:50,outpoint:'fixture:0',...(knownFlow?{previous_output:'0.1.830000',verification_state:'header_anchored'}:{})},{index:1,value:20,outpoint:'fixture:2'}],outputs:[{n:0,value_sats:30},{n:1,value_sats:20},{n:2,value_sats:10}],fee:10});
   if(url.pathname==='/api/v1/txio/lookup'){
    const direction=url.searchParams.get('direction'),position=url.searchParams.get('position'),fallback=url.searchParams.get('fallback')==='true';
    let state='unknown',rows=[],note='No relationship is known in the available coverage.';
    if(position==='i0.1.840000'){state='known';rows=[link(position,'0.1.830000')];}
    if(position==='0.1.830000'){state='known';rows=[link('i0.1.840000',position)];}
    if(position==='0.1.840000'&&fallback){state='known';rows=[link('i0.2.850000',position)];}
    if(position==='i1.1.840000'){state=fallback?'known':'unresolved';rows=[fallback?link(position,'2.1.830000'):{input:position}];}
    if(position==='2.1.840000'){state='known';rows=[{...link('i0.2.850000',position),creation_block_hash:hash(839999)}];}
    if(position==='i0.1.841000'){await new Promise(resolve=>setTimeout(resolve,400));state='known';rows=[link(position,'0.1.830000')];}
    try{return await reply({direction,position,state,rows,chain_state:'selected_chain',note});}catch{return;}
   }
   throw Error('Unexpected request '+url.pathname);
  });
  await page.goto(origin+'/test');
  const tx=async(height=840000,index=1)=>{await page.evaluate(value=>helper.entity({kind:'transaction',height:value.height,txIndex:value.index}),{height,index});await page.waitForFunction(value=>document.querySelector('#timeline-explorer-tx-detail')?.dataset.txIndex===String(value.index)&&helper.state().height===value.height&&document.querySelector('.transaction-flow-svg'),{height,index});};
  const waitAddress=address=>page.waitForFunction(address=>entities.at(-1)?.address===address,address);
  await tx();
  const firstLookup=calls.length;
  await page.locator('[data-flow-side="input"][data-flow-index="0"]').focus();await page.keyboard.press('Enter');
  await waitAddress('0.1.830000.bitcoin');
  assert.equal(calls.slice(firstLookup).filter(call=>call.path==='/api/v1/txio/lookup').length,1,'Saved input relationship navigates without a provider fallback');
  assert(calls.slice(firstLookup).some(call=>call.body?.address==='0.1.830000.bitcoin'),'A saved original output uses its direct BPS address');
  assert.equal(await page.locator('#timeline-explorer-tx-detail .io-grid>section:last-child .io.focused').count(),1);
  await page.getByRole('button',{name:'Find spending input for output 0',exact:true}).click();await waitAddress('i0.1.840000.bitcoin');
  assert.equal(await page.locator('#timeline-explorer-tx-detail').getAttribute('data-focus-kind'),'input');
  assert.equal(await page.locator('#timeline-explorer-tx-detail .io-grid>section:first-child .io.focused').count(),1,'Forward navigation highlights the spending input');
  const fallbackStart=calls.length;
  await page.getByRole('button',{name:'Find spending input for output 0',exact:true}).click();await waitAddress('i0.2.850000.bitcoin');
  assert.deepEqual(calls.slice(fallbackStart).filter(call=>call.path==='/api/v1/txio/lookup').map(call=>new URLSearchParams(call.query).get('fallback')),[null,'true'],'An explicit forward action checks saved BPS facts before verified provider fallback');
  await tx();
  await page.getByRole('button',{name:'Find spending input for output 1',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-txio-status="output:1"]')?.textContent.includes('does not establish'));
  assert.equal(await page.evaluate(()=>helper.state().height),840000,'Unknown spender coverage does not navigate or assert unspent');
  const beforeMismatch=calls.filter(call=>call.path==='/api/v1/navigate').length;
  await page.getByRole('button',{name:'Find spending input for output 2',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-txio-status="output:2"]')?.textContent.includes('different block evidence'));
  assert.equal(calls.filter(call=>call.path==='/api/v1/navigate').length,beforeMismatch,'A different creation-block anchor cannot drive navigation');
  await page.getByRole('button',{name:'Open original output for input 1',exact:true}).click();await waitAddress('2.1.830000.bitcoin');
  await tx(841000);
  const staleStart=calls.length,lookup=page.waitForRequest(request=>request.url().includes('/api/v1/txio/lookup')&&request.url().includes('841000'));
  await page.getByRole('button',{name:'Open original output for input 0',exact:true}).click();await lookup;
  await tx(842000);await page.waitForTimeout(500);
  assert.equal(await page.evaluate(()=>helper.state().height),842000,'A late relationship lookup cannot replace a newer transaction');
  assert.equal(calls.slice(staleStart).some(call=>call.body?.address==='0.1.830000.bitcoin'),false);
  await tx(840000,0);
  assert.equal(await page.locator('[data-txio-direction="input"]').count(),0,'Coinbase has no previous-output action');
  knownFlow=true;await tx(840002,1);await page.getByRole('button',{name:'Open original output 0.1.830000',exact:true}).waitFor();const articles=page.locator('#timeline-explorer-tx-detail .io-grid>section:first-child .io');assert.match(await articles.nth(0).innerText(),/Input position · i0.1.840002/);assert.match(await articles.nth(0).innerText(),/Original output · 0.1.830000/);assert.equal(await articles.nth(1).locator('[data-original-output]').count(),0,'Unresolved parents keep their native outpoint without invented BPS');assert.match(await articles.nth(1).locator('[data-native-prevout]').innerText(),/:2/);assert.equal(calls.some(call=>call.path==='/api/v1/txio/lookup'&&new URLSearchParams(call.query).get('position')==='i0.1.840002'),false,'Displaying verified flow BPS does not trigger a separate join');await page.getByRole('button',{name:'Follow a sat in output 0',exact:true}).click();await page.getByLabel('Sat offset in output 0',{exact:true}).fill('5');const beforeTrace=calls.length;await page.getByRole('button',{name:'Trace Satline',exact:true}).click();assert.equal(await page.evaluate(()=>traces.at(-1)),'5.0.1.840002','Explicit output-relative offset opens the existing Satline tool');assert.equal(calls.slice(beforeTrace).length,0,'Opening the offset trace does not start a global index');const beforeDirect=calls.length;await page.getByRole('button',{name:'Open original output 0.1.830000',exact:true}).click();await waitAddress('0.1.830000.bitcoin');assert.equal(calls.slice(beforeDirect).some(call=>call.path==='/api/v1/txio/lookup'),false,'A supplied verified BPS parent navigates directly');
  assert.equal(calls.some(call=>call.path.includes('index/build')||call.path.includes('index/live')||call.path.includes('index/browse')),false,'Relationship navigation never starts indexing');
  assert.equal(calls.some(call=>call.path.includes('transaction/lookup')),false,'Supplied BPS links do not require transaction-hash lookup');
  assert.deepEqual(external,[]);assert.deepEqual(errors,[]);
  await page.evaluate(()=>helper.teardown());
  console.log('TXIO workspace browser PASS: saved backward and forward BPS links, explicit verified fallback, input/output focus, unknown coverage, anchor rejection, stale cancellation and coinbase.');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;});
