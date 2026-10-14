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
assert.deepStrictEqual([...manifest.permissions].sort(), ['nativeMessaging','storage']);
assert.strictEqual(manifest.host_permissions, undefined, 'Sat support adds no host permissions');
(async () => {
  let messageHandler, stored, calls=[];
  const chrome={runtime:{sendNativeMessage:async(host,body)=>{calls.push({host,...body});return {valid:true,address:body.address};},getURL:()=> 'chrome-extension://fixture/',onInstalled:{addListener(){}},onStartup:{addListener(){}},onMessage:{addListener(fn){messageHandler=fn;}}},storage:{local:{get:async()=>stored||{},set:async()=>{}}}};
  vm.runInNewContext(worker,{chrome,importScripts(){},GatewaySearch:require('./search.js')});
  const validate=address=>new Promise(resolve=>messageHandler({type:'validateGatewayAddress',address},{url:'https://www.google.com/search'},resolve));
  for(const address of ['0.sat','5000000000.sat','5000000000.SAT','nvtdijuwxlp.sat','nvtcsezkbth.sat']) {
    calls=[];assert((await validate(address)).valid);assert.strictEqual(calls.length,1);assert.strictEqual(calls[0].action,'validate');assert.strictEqual(calls[0].address,address);
  }
  for(const address of ['1','5000000000','nvtdijuwxlp','word','what is 0.sat','bad-name.sat','01.sat','-1.sat','1.2.sat','0.sat.evil','https://0.sat','0.sat/path']) {
    calls=[];assert.strictEqual((await validate(address)).valid,false,address);assert.strictEqual(calls.length,0,'Private ordinary/noncanonical search was sent to native host: '+address);
  }
  stored={gatewayNamespaces:[{suffix:'.sat',module:'Sats'}]};assert((await validate('0.sat')).valid,'Authenticated namespace objects support Sats');
  stored={gatewayNamespaces:['.bitcoin','.gateway']};calls=[];assert.strictEqual((await validate('0.sat')).valid,false);assert.strictEqual(calls.length,0,'A cached registry without Sats remains authoritative');
  console.log('Gateway Browser Companion numeric/name .sat worker and unchanged permissions: PASS');
})().catch(error=>{console.error(error);process.exitCode=1;});
