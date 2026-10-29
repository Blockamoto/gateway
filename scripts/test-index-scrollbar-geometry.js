'use strict';
const assert = require('node:assert/strict');
const g = require('../ui/shell/index-scrollbar-geometry.js');
const span = v => v.to-v.from+1;
const near = (a,b) => Math.abs(a-b) <= Math.max(1e-8, Math.abs(b)*1e-12);
for (const total of [1,2,100,1000000,Number.MAX_SAFE_INTEGER]) for (const length of [0,40,80,180,900]) {
  let previous = -1;
  for (const fraction of [0,.000001,.001,.01,.1,.5,1]) {
    const actual = 1 + fraction*(total-1), size = g.spanToSize(actual,total,length);
    assert(size >= previous && size <= length+1e-8 && size >= 0);
    if(length > 0) assert(near(g.sizeToSpan(size,total,length),actual), 'Continuous forward/inverse agree across full extent');
    previous = size;
  }
}
for (const length of [80,180,900]) {
  const total=1000000;
  const one=g.spanToSize(1,total,length), two=g.spanToSize(2,total,length), three=g.spanToSize(3,total,length);
  assert(one<two && two<three,'No clamped minimum-thumb dead zone while actual zoom remains');
  assert.equal(g.spanToSize(total,total,length),length,'Full extent is reachable');
  assert(g.spanToSize(2,total,length)-one > g.spanToSize(3,total,length)-two,'Pixels per additional item increase progressively at high zoom');
}

for (const length of [80,180,900]) for (const view of [{from:0,to:0},{from:0,to:9},{from:400000,to:400099},{from:999900,to:999999},{from:0,to:999999}]) {
  const before=JSON.stringify(view), visual=g.geometry(view,999999,length);
  assert.equal(visual.from,view.from);assert.equal(visual.to,view.to);
  assert(visual.offset >= 0 && visual.end <= length+1e-8);
  assert(visual.handleSize>=24,'Both edge targets can be at least24px on usable narrow bars');
  assert(visual.size>=visual.handleSize,'Centered start/end hit areas do not overlap');
  assert.deepEqual(g.resize(view,999999,length,'start',0),view);
  assert.deepEqual(g.resize(view,999999,length,'end',0),view);
  for(const edge of ['start','end']) for(const delta of [-length*2,-2,-.25,.25,2,length*2]) {
    const changed=g.resize(view,999999,length,edge,delta);
    assert(Number.isSafeInteger(changed.from)&&Number.isSafeInteger(changed.to));
    assert(changed.from>=0&&changed.to<=999999&&changed.from<=changed.to);
    assert.equal(edge==='start'?changed.to:changed.from,edge==='start'?view.to:view.from,'Opposite actual anchor stays exact');
  }
  for(const delta of [-length*2,-1,0,1,length*2]) {
    const moved=g.pan(view,999999,length,delta);
    assert.equal(span(moved),span(view),'Panning never changes actual zoom');
    assert(moved.from>=0&&moved.to<=999999);
    const expectedFrom=Math.max(0,Math.min(1000000-span(view),view.from+Math.round(delta/Math.max(visual.travel,Number.MIN_VALUE)*(1000000-span(view)))));
    assert.equal(moved.from,visual.travel?expectedFrom:view.from,'Pan uses the exact visual travel and actual extent');
  }
  assert.equal(JSON.stringify(view),before,'Geometry never mutates the actual viewport');
}

for(const length of [120,900]) for(const edge of ['start','end']) {
  const view={from:400000,to:400099}, direction=edge==='start'?-1:1;
  const tiny=g.resize(view,999999,length,edge,direction);
  assert(span(tiny)>100 && span(tiny)<200,'One pixel at high zoom changes a fine actual range, including narrow bars');
  const coarse=g.resize({from:200000,to:799999},999999,length,edge,direction);
  assert(span(coarse)-600000>span(tiny)-100,'The same pixel becomes progressively finer as zoom increases');
  const one=g.resize(view,999999,length,edge,-direction*length*2);
  assert.equal(span(one),1,'Both handles reach one actual item');
  const all=g.resize(edge==='start'?{from:999900,to:999999}:{from:0,to:99},999999,length,edge,direction*length*2);
  assert.deepEqual(all,{from:0,to:999999},'Both handles can reach the full extent with the other endpoint anchored');
}

// Independent rendered-edge oracle: moving exactly to another view's visual
// edge recovers that exact integer view, with no accumulation or zoom drift.
for(const length of [80,180,900]) for(const edge of ['start','end']) {
  const original={from:400000,to:400099}, before=g.geometry(original,999999,length);
  for(const wantedSpan of [1,2,20,99,100,101,150,1000,10000]) {
    const expected=edge==='start'?{from:original.to-wantedSpan+1,to:original.to}:{from:original.from,to:original.from+wantedSpan-1};
    const after=g.geometry(expected,999999,length), delta=(edge==='start'?after.start-before.start:after.end-before.end);
    assert.deepEqual(g.resize(original,999999,length,edge,delta),expected,'Inverse rendered-edge mapping recovers the exact range');
  }
}
console.log('Index scrollbar geometry: PASS (continuous invertible zoom, fine high-zoom response, narrow targets, exact anchors/ranges/pan, single/full extent)');
