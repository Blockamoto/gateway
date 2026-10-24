const assert = require('assert');
const fs = require('node:fs');
const path = require('node:path');
const { extractCandidate } = require('./search.js');
const version = fs.readFileSync(path.join(__dirname, '../VERSION.txt'), 'utf8').trim();
const manifest = JSON.parse(fs.readFileSync(path.join(__dirname, 'manifest.json'), 'utf8'));
const worker = fs.readFileSync(path.join(__dirname, 'service-worker.js'), 'utf8');
assert.strictEqual(manifest.version, version, 'bundled companion must match the release version');
assert.strictEqual(worker.match(/const VERSION = '([^']+)'/)[1], version, 'native handshake must report the companion release version');
const txid = 'a'.repeat(64);
const yes = [
  ['https://www.google.com/search?q=0.bitcoin', '0.bitcoin'],
  ['https://www.google.co.uk/search?q=2.123.840000.bitcoin', '2.123.840000.bitcoin'],
  ['https://www.bing.com/search?q=i1.123.840000.bitcoin', 'i1.123.840000.bitcoin'],
  ['https://duckduckgo.com/?q=500.2.123.840000.bitcoin', '500.2.123.840000.bitcoin'],
  ['https://search.brave.com/search?q=123.840000.bitcoin', '123.840000.bitcoin'],
  [`https://www.google.com/search?q=${txid}.840000.bitcoin`, `${txid}.840000.bitcoin`],
  [`https://www.bing.com/search?q=${txid}i2.840000.bitcoin`, `${txid}i2.840000.bitcoin`],
  [`https://duckduckgo.com/?q=${txid}.i2.840000.bitcoin`, `${txid}.i2.840000.bitcoin`],
  ['https://search.brave.com/search?q=0.bitmap', '0.bitmap'],
  ['https://www.google.com/search?q=123.792435.BITMAP', '123.792435.bitmap'],
  ['https://www.google.com/search?q=0.sat', '0.sat'],
  ['https://www.bing.com/search?q=5000000000.SAT', '5000000000.sat']
];
for (const [u, want] of yes) assert.strictEqual(extractCandidate(u), want, u);
const no = [
  'https://www.google.com/search?q=what+is+0.bitcoin',
  'https://www.google.com/search?q=bitcoin',
  'https://example.com/search?q=0.bitcoin',
  'https://www.google.com/search?q=http%3A%2F%2F0.bitcoin',
  'https://www.google.com/search?q=0.bitcoin%2Ffoo',
  'https://www.google.com/search?q=1',
  'https://www.google.com/search?q=5000000000',
  'https://www.google.com/search?q=what+is+0.sat'
];
for (const u of no) assert.strictEqual(extractCandidate(u), '', u);
console.log('Gateway Browser Companion search parsing: PASS');

const { matchesNamespace } = require('./search.js');
for (const q of ['0.bitcoin', '2.123.840000.bitcoin', '.gateway', 'home.gateway']) {
  assert(matchesNamespace(q, ['.bitcoin', '.gateway']));
}
assert(matchesNamespace(`${txid}i2.840000.bitcoin`, ['.bitcoin', '.gateway', '.bitmap']));
assert(matchesNamespace('0.bitmap', ['.bitcoin', '.gateway', '.bitmap']));
for (const q of ['example.com', 'notbitcoin', 'hello.whatever', 'what is 0.bitcoin']) {
  assert(!matchesNamespace(q, ['.bitcoin', '.gateway']));
}
assert(matchesNamespace('123.custom', [{suffix: '.custom'}]));
console.log('Gateway Browser Companion namespace privacy filter: PASS');

// Execute the shipped worker without registration, browser profiles or network.
const vm = require('node:vm');
assert.deepStrictEqual([...manifest.permissions].sort(), ['nativeMessaging','storage','webNavigation']);
assert.strictEqual(manifest.host_permissions, undefined, 'Direct handoff adds no broad host permissions');
(async () => {
  const search=require('./search.js'),settle=()=>new Promise(resolve=>setImmediate(resolve));
  function harness(status={ok:true,namespaces:['.bitcoin','.gateway','.bitmap','.sat']}){
    let messageHandler,startup,removed,gate=null;const calls=[],updates=[],navigation=[],stored={gatewayNamespaces:['.bitcoin','.gateway']};
    const chrome={runtime:{sendNativeMessage:async(host,body)=>{calls.push({host,...body});if(body.action==='status')return status;if(gate)await gate;return {valid:true,address:['home.gateway','gateway.gateway'].includes(body.address)?'.gateway':body.address,web_url:'http://127.0.0.1:19878/?activation=fixture'};},getURL:()=> 'chrome-extension://fixture/',onInstalled:{addListener(){}},onStartup:{addListener(fn){startup=fn;}},onMessage:{addListener(fn){messageHandler=fn;}}},storage:{local:{get:async()=>{throw Error('Unauthenticated cache must not be trusted');},set:async value=>Object.assign(stored,value)}},webNavigation:{onBeforeNavigate:{addListener(fn,filter){navigation.push({fn,filter});}}},tabs:{update:async(tabId,body)=>updates.push({tabId,...body}),onRemoved:{addListener(fn){removed=fn;}}}};
    vm.runInNewContext(worker,{chrome,URL,Date,importScripts(){},GatewaySearch:search});
    const validate=address=>new Promise(resolve=>messageHandler({type:'validateGatewayAddress',address},{url:'https://www.google.com/search?q='+encodeURIComponent(address)},resolve));
    return {calls,updates,stored,validate,own:type=>new Promise(resolve=>messageHandler({type},{url:'chrome-extension://fixture/popup.html'},resolve)),startup:()=>startup(),hold:promise=>{gate=promise;},navigate:details=>navigation.forEach(entry=>entry.fn({frameId:0,tabId:7,...details})),removed:tab=>removed(tab),filters:navigation.map(entry=>entry.filter).filter(Boolean)};
  }
  const h=harness();assert((await h.validate('0.792435.bitmap')).valid,'Fresh authenticated status replaces a pre-Bitmap cache');assert.deepStrictEqual(h.calls.map(row=>row.action),['status','validate']);assert(h.stored.gatewayNamespaces.includes('.bitmap'));
  for(const address of ['0.sat','5000000000.sat','5000000000.SAT','nvtdijuwxlp.sat','nvtcsezkbth.sat','0.bitcoin','2.123.840000.bitcoin','.gateway','home.gateway','0.bitmap','123.792435.BITMAP']) {
    h.calls.length=0;assert((await h.validate(address)).valid,address);assert.strictEqual(h.calls.length,1);assert.strictEqual(h.calls[0].action,'validate');assert.strictEqual(h.calls[0].address,address.toLowerCase());
  }
  for(const address of ['1','5000000000','nvtdijuwxlp','word','what is 0.sat','bad-name.sat','01.sat','-1.sat','1.2.sat','0.sat.evil','https://0.sat','0.sat/path','01.bitmap','1.01.bitmap','01.1.bitmap','1.2.3.bitmap','-1.bitmap','+1.bitmap','1e3.bitmap','18446744073709551616.bitmap','what is 0.bitmap','0.bitmap/path','0.bitmap?x=1','example.com']) {
    h.calls.length=0;assert.strictEqual((await h.validate(address)).valid,false,address);assert.strictEqual(h.calls.length,0,'Private ordinary/noncanonical search was sent to native host: '+address);
  }
  h.calls.length=0;await h.startup();assert.deepStrictEqual(h.calls.map(row=>row.action),['status'],'Browser startup refreshes authenticated namespace metadata');
  const unavailable=harness({ok:false});assert.strictEqual((await unavailable.validate('0.bitmap')).valid,false);assert.deepStrictEqual(unavailable.calls.map(row=>row.action),['status'],'No blind namespace fallback when current host handshake fails');
  const missing=harness({ok:true,namespaces:['.bitcoin','.gateway']});assert.strictEqual((await missing.validate('0.bitmap')).valid,false);assert.deepStrictEqual(missing.calls.map(row=>row.action),['status'],'Current authenticated registry remains authoritative');
  const changingStatus={ok:true,namespaces:['.bitcoin','.gateway']},changing=harness(changingStatus);assert.strictEqual((await changing.validate('0.bitmap')).valid,false);changingStatus.namespaces=['.bitcoin','.gateway','.bitmap'];await changing.own('gatewayStatus');assert((await changing.validate('0.bitmap')).valid,'Popup authenticated status refreshes both in-memory and persisted namespace metadata in the same worker');assert(changing.stored.gatewayNamespaces.includes('.bitmap'));
  assert.strictEqual(h.filters.length,1);assert.deepStrictEqual(JSON.parse(JSON.stringify(h.filters[0])).url.map(row=>row.hostSuffix),['.bitcoin','.gateway','.bitmap','.sat']);
  for(const url of ['http://0.bitmap/','https://123.792435.bitmap/','http://0.bitcoin/','https://home.gateway/','https://0.sat/']){
    h.calls.length=0;h.updates.length=0;h.navigate({url});await settle();await settle();assert.strictEqual(h.calls.length,1,url);assert.strictEqual(h.calls[0].address,search.directCandidate(url));assert.deepStrictEqual(h.updates,[{tabId:7,url:'http://127.0.0.1:19878/?activation=fixture'}]);
  }
  for(const url of ['https://example.com/','https://0.bitmap.evil/','http://01.bitmap/','http://0.01.bitmap/','http://user@0.bitmap/','http://0.bitmap:80/','https://0.bitmap:443/','http://0.bitmap:9000/','http://0.bitmap/path','http://0.bitmap/?q=private','http://0.bitmap/#secret','http://0.bitmap/?','http://0.bitmap/#','file://0.bitmap/']){
    h.calls.length=0;h.updates.length=0;h.navigate({url});await settle();assert.strictEqual(h.calls.length,0,'Ordinary/unsafe URL reached native host: '+url);assert.deepStrictEqual(h.updates,[]);
  }
  h.calls.length=0;h.navigate({url:'https://0.bitmap/',frameId:2});await settle();assert.strictEqual(h.calls.length,0,'Subframes do not trigger native handoff');
  let release;h.hold(new Promise(resolve=>release=resolve));h.updates.length=0;h.navigate({url:'https://9.bitmap/'});await settle();h.navigate({url:'https://example.com/private?q=secret'});release();await settle();await settle();assert.deepStrictEqual(h.updates,[],'A later ordinary navigation cancels a pending native reply without forwarding the ordinary URL');
  let releaseClosed;h.hold(new Promise(resolve=>releaseClosed=resolve));h.navigate({url:'https://10.bitmap/'});await settle();h.removed(7);releaseClosed();await settle();await settle();assert.deepStrictEqual(h.updates,[],'A removed tab cannot be redirected');
  console.log('Gateway Browser Companion worker PASS: canonical Bitmap/Sat privacy, fresh authenticated registry, exact direct-host handoff, no broad host permissions, subframe/stale/closed-tab guards and Bitcoin/Gateway/Sat regression.');
})().catch(error=>{console.error(error);process.exitCode=1;});
