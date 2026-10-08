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
  ['https://search.brave.com/search?q=0.bitmap', '0.bitmap']
];
for (const [u, want] of yes) assert.strictEqual(extractCandidate(u), want, u);
const no = [
  'https://www.google.com/search?q=what+is+0.bitcoin',
  'https://www.google.com/search?q=bitcoin',
  'https://example.com/search?q=0.bitcoin',
  'https://www.google.com/search?q=http%3A%2F%2F0.bitcoin',
  'https://www.google.com/search?q=0.bitcoin%2Ffoo'
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
