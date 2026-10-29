'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const help = require('../ui/shell/index-help-content.js');
const file = path.resolve(__dirname, '../ui/shell/index-help-content.js');
const ids = ['group:blocks', 'group:txio', 'group:addresses', 'group:ordinals', 'group:bitmap', 'headers', 'blocks', 'tx-locator', 'txio-links', 'txio-values', 'txio-scripts', 'utxo-state', 'txio-sat-ranges', 'address-state', 'address-history', 'inscriptions', 'bitmap-districts-standard-v1', 'bitmap-chain', 'bitmap-district-chain-dev-v1'];
const text = card => card.paragraphs.join(' ');
const rows = id => help.dependencies(id).dependencies;
const by = (id, name) => rows(id).find(row => row.name === name);
for (const id of ids) {
  const purpose = help.get(id);
  assert.equal(typeof purpose.title, 'string', id + ' has a title');
  assert(purpose.title.length > 0 && purpose.paragraphs.length >= 2, id + ' explains purpose and use');
  assert(purpose.paragraphs.every(p => typeof p === 'string' && p.length > 25), id + ' has readable plain text');
  assert(!Object.hasOwn(purpose, 'dependencies'), id + ' keeps dependencies out of question help');
  assert(!/\b(?:requires?|prerequisites?|depends on|needed to|needs? a)\b/i.test(text(purpose)), id + ' keeps other-track requirements in the separate card');
  const dependencies = help.dependencies(id);
  if (dependencies) {
    assert.equal(typeof dependencies.title, 'string');
    assert(Array.isArray(dependencies.paragraphs));
    assert(Array.isArray(dependencies.dependencies));
    for (const row of dependencies.dependencies) {
      assert(['hard', 'conditional', 'optional', 'input'].includes(row.requirement), id + ' classifies dependency strength');
      assert(row.name && row.why, id + ' explains what and why');
    }
  }
}

const externalCards = ['address-state','group:addresses'];
for (const id of externalCards) {
  const card = help.dependencies(id);
  assert.equal(card.showIndicator, true, id+' shows its real other-track dependency');
  assert(card.dependencies.some(row=>row.otherTrack===true && ['hard','conditional'].includes(row.requirement)));
}
for (const id of ids.filter(id=>!externalCards.includes(id))) {
  const card = help.dependencies(id);
  assert(!card?.showIndicator, id+' has no dependency badge for own state, generic inputs or optional tools');
  assert(!card?.dependencies.some(row=>row.otherTrack===true),id+' does not misclassify input/own history as another track');
}
assert.equal(by('address-state','A complete matching UTXO snapshot').otherTrack,true);
assert.equal(by('txio-links','TX ID Locators').otherTrack,undefined,'Optional locator help is not a dependency badge');
assert.match(text(help.get('utxo-state')),/every block in order from the first Bitcoin block/,'Own state continuity remains discoverable in general help');
assert.match(text(help.get('txio-sat-ranges')),/retention option is explicitly enabled/,'Own historical retention remains discoverable in general help');
for(const id of ['tx-locator','txio-links','txio-values','txio-scripts','utxo-state','txio-sat-ranges','address-history','inscriptions']) assert.match(text(help.get(id)),/temporarily with Raw Off/,id+' source context remains available without a badge');

assert.deepEqual(help.get('group:transactions'), help.get('group:txio'));
assert.deepEqual(help.dependencies('group:transactions'), help.dependencies('group:txio'));
assert.equal(help.get('group:ordinals').title, 'Ordinals');
assert.match(text(help.get('group:ordinals')), /Sat Ranges.*Inscriptions/);
assert.match(text(help.get('group:ordinals')), /own processing start, saved state, coverage and retention choices/);
assert.match(text(help.get('group:ordinals')), /Historical Sat ranges remain a separate retention option/);
assert(!/individual sats|Sat Ranges|Inscriptions/.test(text(help.get('group:txio'))), 'Transaction purpose excludes the independent Ordinals tracks');
assert.deepEqual(help.get('txo-spender'), help.get('txio-links'));
assert.deepEqual(help.get('sat-state'), help.get('txio-sat-ranges'));
assert.match(text(help.get('group:addresses')), /Historical received amounts are not a current balance/);
assert.match(text(help.dependencies('group:addresses')), /Current Addresses requires.*matching UTXO snapshot/);
assert.match(text(help.dependencies('group:addresses')), /Historical Outputs.*without building UTXO/);

const sparse = ['tx-locator', 'txio-links', 'txio-values', 'txio-scripts', 'address-history', 'inscriptions'];
for (const id of sparse) {
  assert.match(text(help.dependencies(id)), /temporarily with Raw Off/, id + ' distinguishes temporary source from Raw retention');
  assert(!rows(id).some(row => row.requirement === 'hard'), id + ' has no invented retained-index prerequisite');
  assert(rows(id).some(row => row.requirement === 'input' && /complete verified Bitcoin block/.test(row.name)));
}
assert.equal(by('txio-links', 'TX ID Locators').requirement, 'optional');
assert.match(by('txio-links', 'TX ID Locators').why, /known block-and-position address can be read directly/);
for (const id of ['txio-values', 'txio-scripts']) {
  assert.match(text(help.dependencies(id)), /without enabling IO Links/);
  assert.match(text(help.get(id)), /does not (?:tell you whether an output has been spent|establish whether an output is unspent)/);
}
assert.match(text(help.dependencies('txio-scripts')), /address is derived from the script itself/);
assert.match(text(help.get('group:addresses')), /without building either address track/);

assert.equal(by('address-state', 'A complete matching UTXO snapshot').requirement, 'hard');
assert.equal(rows('address-state').filter(row => row.requirement === 'hard').length, 1);
assert.match(text(help.dependencies('address-state')), /does not start a UTXO build/);
for (const id of ['utxo-state', 'txio-sat-ranges']) {
  assert.match(text(help.dependencies(id)), /every block in order from the first Bitcoin block/);
  assert(!rows(id).some(row => /TX ID Locators|IO Links/.test(row.name) && row.requirement === 'hard'));
}
assert(!rows('txio-sat-ranges').some(row => /UTXO/.test(row.name)));
assert.equal(by('txio-sat-ranges', 'Keep historical output ranges').requirement, 'hard');
assert.match(text(help.dependencies('txio-sat-ranges')), /Turning it on later does not recreate older range history/);

assert.match(text(help.dependencies('bitmap-districts-standard-v1')), /792435/);
assert.match(text(help.dependencies('bitmap-districts-standard-v1')), /does not require a separately retained Inscriptions index/);
for (const id of ['bitmap-chain', 'bitmap-district-chain-dev-v1']) {
  assert.match(text(help.get(id)), /disabled/);
  assert.equal(help.dependencies(id), null, 'Disabled chains have no prerequisites that can enable execution');
}

const definition = Object.freeze({id:'bitmap-districts-canonical', name:'Saved District results', locked:true, rules:Object.freeze(['unchanged rule']), dependencies:Object.freeze(['blocks'])});
assert.match(text(help.get(definition.id, definition)), /inspection only/);
assert.match(text(help.get('bitmap-districts-0123456789abcdef0123456789abcdef', {name:'Custom Districts'})), /own saved interpretation/);
assert.match(text(help.dependencies('unknown-track', {dependencies:['blocks']})), /not established/);
assert(!help.dependencies('unknown-track', {dependencies:['blocks']}).dependencies.some(row => row.requirement === 'hard'), 'Catalog source labels do not invent Raw retention prerequisites');
assert(!text(help.get('unavailable-tool', {locked:true})).includes('Bitmap'), 'Generic locked fallback does not invent a Bitmap purpose');
assert.equal(help.get(definition.id, definition).title, definition.name);
assert.deepEqual(definition.rules, ['unchanged rule']);
assert.deepEqual(definition.dependencies, ['blocks']);
assert.doesNotThrow(() => help.get(null));
assert.doesNotThrow(() => help.dependencies(undefined));

const first = help.get('txio-values');
first.paragraphs[0] = 'mutated';
assert.notEqual(help.get('txio-values').paragraphs[0], 'mutated', 'Returned purpose text cannot mutate the shared content');
const deps = help.dependencies('txio-values');
deps.dependencies[0].requirement = 'hard';
deps.paragraphs.length = 0;
assert.equal(help.dependencies('txio-values').dependencies[0].requirement, 'input', 'Returned dependency rows cannot alter another track');
assert(help.dependencies('txio-values').paragraphs.length > 0);

let sideEffects = 0;
const sandbox = {window:{}, fetch:()=>{sideEffects++;throw Error('No network');}, document:new Proxy({}, {get(){sideEffects++;throw Error('No DOM');}})};
vm.runInNewContext(fs.readFileSync(file, 'utf8'), sandbox, {filename:file});
for (const id of ids) { sandbox.window.GatewayIndexHelpContent.get(id); sandbox.window.GatewayIndexHelpContent.dependencies(id); }
assert.equal(sideEffects, 0, 'Loading and reading help does not touch controls, DOM, network or storage');
console.log('Index help content: PASS (all groups/tracks, separate dependency strengths, state/history/source distinctions, disabled/frozen fallbacks, immutable reads, no side effects)');

for(const id of ['bitmap-parcels','bitmap-parcels-standard-v1','bitmap-parcels-canonical-v2','bitmap-parcel-chain-dev-v1']){assert.equal(help.get(id).title,'Unavailable index');assert(!/parcel/i.test(text(help.get(id))));assert.equal(help.dependencies(id),null);}
assert(!/parcel/i.test(JSON.stringify(help.get('group:bitmap'))+JSON.stringify(help.dependencies('group:bitmap'))),'Normal Bitmap help does not expose a hidden index');

const replayHelp=help.get('bitmap-parcels-standard-v1-replay',{id:'bitmap-parcels-standard-v1-replay',kind:'parcel',buildable:true});assert.match(text(replayHelp),/Discovery is not validation/);assert.doesNotMatch(text(replayHelp),/processing is temporarily disabled/i);const replayDeps=text(help.dependencies('bitmap-parcels-standard-v1-replay',{id:'bitmap-parcels-standard-v1-replay',kind:'parcel',buildable:true}));assert.match(replayDeps,/parent evidence/i);assert.match(replayDeps,/no parent-proof scan starts until/i);
