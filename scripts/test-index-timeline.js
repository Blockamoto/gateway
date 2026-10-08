'use strict';
// Independent discrete-set oracles exercise interval arithmetic, including
// one-block gaps that must survive visual aggregation on very long chains.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const sandbox = {window:{}};
for (const file of ['index-cards.js','index-timeline.js']) {
  vm.runInNewContext(fs.readFileSync(path.join(root,'ui/shell',file),'utf8'),sandbox,{filename:file});
}
const {normalizeRanges,subtractRanges,clampView,coverageBins,trackModel,established,rulerMarks} = sandbox.window.GatewayIndexTimeline;
const plain = value => JSON.parse(JSON.stringify(value));
const valid = range => range && Number.isSafeInteger(range.from) && Number.isSafeInteger(range.to) && range.from >= 0 && range.to >= range.from;
function asSet(ranges) {
  const set = new Set();
  for (const range of ranges.filter(valid)) for (let height=range.from;height<=range.to;height++) set.add(height);
  return [...set].sort((a,b)=>a-b);
}
function exact(ranges) {
  assert(ranges.every(valid),'Output contains only valid inclusive ranges');
  for (let i=1;i<ranges.length;i++) assert(ranges[i].from>ranges[i-1].to+1,'Ranges are sorted, disjoint and fully normalized');
}

assert.deepEqual(plain(normalizeRanges([{from:10,to:12},{from:0,to:3},{from:4,to:6},{from:11,to:15},{from:8,to:8}])),[{from:0,to:6},{from:8,to:8},{from:10,to:15}]);
assert.deepEqual(plain(normalizeRanges([null,{}, {from:-1,to:4},{from:2,to:1},{from:0.1,to:2},{from:0,to:Infinity},{from:'0',to:2},{from:0,to:0}])),[{from:0,to:0}], 'Malformed coverage cannot turn into fictional coverage');
assert.deepEqual(plain(subtractRanges([{from:0,to:10}], [{from:0,to:0},{from:3,to:5},{from:10,to:10}])),[{from:1,to:2},{from:6,to:9}]);
assert.deepEqual(plain(subtractRanges([{from:5,to:5}], [{from:5,to:5}])),[],'A one-block gap removes that exact block');
assert.deepEqual(plain(subtractRanges([{from:4,to:8}], [{from:0,to:20}])),[]);
assert.deepEqual(plain(normalizeRanges([{from:999999,to:999999},{from:1000001,to:1000001}])),[{from:999999,to:999999},{from:1000001,to:1000001}], 'Single-block gaps survive normalization at long-chain scale');
assert.deepEqual(plain(clampView({from:900,to:1100},999)),{from:799,to:999},'Panning through the tip preserves span while bounding it');
assert.deepEqual(plain(clampView({from:50,to:200},99)),{from:0,to:99},'A view wider than the chain is clamped to the full extent');
assert.deepEqual(plain(clampView({from:0,to:0},0)),{from:0,to:0},'Genesis-only viewport remains inclusive and nonempty');
assert.deepEqual(plain(clampView({from:5,to:2},99)),{from:5,to:5},'A reversed request cannot create a negative viewport span');

let seed=0x071cafe;
function random(limit) {seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed%limit;}
for (let sample=0;sample<300;sample++) {
  const input=Array.from({length:random(16)},()=>{const from=random(120);return{from,to:from+random(10)}});
  const holes=Array.from({length:random(10)},()=>{const from=random(120);return{from,to:from+random(10)}});
  const before=JSON.stringify({input,holes});
  const normalized=plain(normalizeRanges(input));
  exact(normalized);
  assert.deepEqual(asSet(normalized),asSet(input),'Normalization preserves exactly the enumerated covered heights');
  assert.deepEqual(plain(normalizeRanges(normalized)),normalized,'Normalization is idempotent');
  const result=plain(subtractRanges(input,holes)),removed=new Set(asSet(holes));
  exact(result);
  assert.deepEqual(asSet(result),asSet(input).filter(height=>!removed.has(height)),'Subtraction matches an independently enumerated set difference');
  assert.equal(JSON.stringify({input,holes}),before,'Helpers must never modify server evidence');
  const view={from:random(40),to:80+random(40)},pixels=1+random(24),bins=plain(coverageBins(input,view,pixels));
  const expected=asSet(input).filter(height=>height>=view.from&&height<=view.to);
  assert.equal(bins.reduce((total,bin)=>total+bin.count,0),expected.length,'Viewport aggregation counts only distinct covered blocks inside the viewport');
  for (const bin of bins) {
    assert.equal(bin.total,bin.to-bin.from+1,'Pixel occupancy denominator matches its exact inclusive height interval');
    assert.equal(bin.count,expected.filter(height=>height>=bin.from&&height<=bin.to).length,'Each pixel retains its independently counted occupied heights');
  }
}

const sparse = [{from:0,to:9},{from:499990,to:499999},{from:500003,to:500010},{from:999990,to:999999}];
const longBins=plain(coverageBins(sparse,{from:0,to:999999},600));
assert(longBins.length<=600,'A million-height view is bounded by pixels, not block count');
assert.equal(longBins.reduce((count,bin)=>count+bin.count,0),38,'Pixel aggregation preserves the exact occupied block count');
assert(longBins.some(bin=>bin.count<bin.total),'Subpixel gaps must remain represented as partial occupancy');
for (const bin of longBins) {
  assert(bin.x>=0&&bin.width>0&&bin.x+bin.width<=600,'Bins stay inside the supplied viewport width');
  assert(bin.count>0&&bin.count<=bin.total,'No pixel may be more than fully covered');
}
const detailBins=plain(coverageBins([{from:0,to:3},{from:5,to:9}],{from:0,to:9},10));
assert.equal(detailBins.reduce((count,bin)=>count+bin.count,0),9,'At block resolution the hole remains exactly one block wide');
assert.equal(detailBins.some(bin=>bin.from<=4&&bin.to>=4&&bin.count===bin.total),false,'A missing block cannot be painted as full coverage');
assert.deepEqual(plain(coverageBins([],{from:0,to:999999},600)),[]);

const blocks={id:'blocks',name:'Bitcoin blocks',buildable:true,start_height:0};
const misleading={definitions:[blocks],instances:[{definition:'blocks',checkpoint:{from:0,height:999999},coverage:[{from:0,to:999999}],retention:'ephemeral'}],providers:[{id:'blocks',queryable:true,coverage:[{from:0,to:999999}]}],live:[{index:'blocks',on:true,enabled:true}],job:{index:'blocks',state:'complete',from:0,to:999999,height:999999}};
assert.deepEqual(plain(trackModel(blocks,misleading).coverage),[],'Blocks need explicit local availability evidence; processing checkpoints/providers/jobs never supply it');
const snapshot={...misleading,timeline:{tip_height:999999,target_height:999999,tracks:[{id:'blocks',coverage:[{from:10,to:20}],gaps:[{from:14,to:16}]}]}};
assert.deepEqual(plain(trackModel(blocks,snapshot).coverage),[{from:10,to:13},{from:17,to:20}]);
const derived={id:'test-derived',name:'Derived test',buildable:true,start_height:0};
assert.deepEqual(plain(trackModel(derived,{instances:[{definition:derived.id,coverage:[{from:10,to:20}],gaps:[{from:11,to:15}]}]}).coverage),[{from:10,to:10},{from:16,to:20}],'Derived committed coverage retains its own explicit gaps');
assert.deepEqual(plain(trackModel(derived,{instances:[{definition:derived.id,checkpoint:{from:0,height:999999}}]}).coverage),[],'A checkpoint height alone never establishes an entire range');
for(const verification of ['stale_reorg','anchor_unavailable'])assert.deepEqual(plain(trackModel(derived,{instances:[{definition:derived.id,coverage:[{from:0,to:10}],verification}]}).coverage),[],'Unverified chain evidence cannot establish current fallback coverage');

assert.equal(established({id:'headers'},{}),true,'Headers is always the foundational track');
assert.equal(established(blocks,{providers:[{id:'blocks',coverage:[{from:0,to:100}]}]}),false,'An external provider alone does not add a track');
assert.equal(established({...blocks,locked:true},misleading),false,'Saved jobs or data cannot unlock a track');
assert.equal(established(blocks,{live:[{index:'blocks',on:false,enabled:false}]}),false,'Default inactive policy is not an added index');
assert.equal(established(blocks,{jobs:[{id:'saved',index:'blocks',state:'paused'}]}),true,'Existing work remains reachable');
assert.equal(established(blocks,snapshot),true,'Stored local data appears without being added again');
const fullMarks=plain(rulerMarks({from:0,to:999999},1000));
assert.deepEqual(fullMarks.filter(m=>m.kind==='halving').map(m=>m.height),[210000,420000,630000,840000]);
assert.equal(fullMarks.some(m=>m.kind==='difficulty'),false,'Distant views avoid densely packed difficulty labels');
const closeMarks=plain(rulerMarks({from:839990,to:846100},1000));
assert(closeMarks.some(m=>m.kind==='halving'&&m.height===840000));
assert.deepEqual(closeMarks.filter(m=>m.kind==='difficulty').map(m=>m.height),[840672,842688,844704],'Difficulty periods are independently anchored to genesis, never restarted at a halving');
for(const m of closeMarks) assert(m.height>=839990&&m.height<=846100&&Number.isSafeInteger(m.height),'Landmarks stay in the visible block range');

console.log('Index timeline helpers PASS: exact coverage with 300 independent set-oracle cases, sparse pixel occupancy, evidence-only track membership, and independently anchored Bitcoin ruler landmarks.');
