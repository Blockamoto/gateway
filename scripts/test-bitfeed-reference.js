'use strict';
// Golden data was produced by running the original MIT BitFeed free-square
// layout, size utilities and settled shader-to-CSS coordinate conversion.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const scope={window:{}};vm.createContext(scope);vm.runInContext(fs.readFileSync(path.join(__dirname,'../ui/shell/block-viewport.js'),'utf8'),scope);
const viewport=scope.window.GatewayBlockViewport,reference=JSON.parse(fs.readFileSync(path.join(__dirname,'bitfeed-reference.json'),'utf8'));
let tiles=0;
for(const sample of reference.cases){
 const transactions=sample.scenario.values.map((value,index)=>({index,value,vbytes:sample.scenario.bytes[index]})),layout=viewport.pack(transactions,sample.mode);
 assert.equal(layout.width,sample.width,sample.scenario.name+' reference grid width');assert.equal(layout.height,sample.height,sample.scenario.name+' reference grid height');
 assert.deepEqual(JSON.parse(JSON.stringify(layout.tiles.map(tile=>({index:tile.tx.index,x:tile.x,y:tile.y,s:tile.s})))),sample.grid,'Original insertion-order BitFeed packing: '+sample.scenario.name+' '+sample.mode);
 for(const [index,tile]of layout.tiles.entries()){
  const painted=viewport.tileGeometry(tile,layout),expected=sample.painted[index];
  for(const key of ['x','y','size','gap'])assert(Math.abs(painted[key]-expected[key])<1e-7,'Final painted reference '+key+' for '+sample.scenario.name+' '+sample.mode+' transaction '+index);
  assert(Math.abs(painted.size/painted.gap-(tile.s-.5)/.5)<1e-9,'Square/gap ratio survives one shared Fit transform');tiles++;
 }
}
console.log('BitFeed reference PASS: '+reference.cases.length+' original-source cases, '+tiles+' exact ordered squares; size/packing/final WebGL-to-CSS orientation/half-grid gaps match under a shared complete-grid Fit transform. Pixel and animation parity are not claimed.');
