'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const sandbox={window:{}};vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../ui/shell/block-viewport.js'),'utf8'),sandbox);
const viewport=sandbox.window.GatewayBlockViewport;
let seed=0x703;const random=()=>((seed=Math.imul(seed,1664525)+1013904223)>>>0)/4294967296;
for(let sample=0;sample<80;sample++){
 const txs=Array.from({length:sample===0?0:1+Math.floor(random()*180)},(_,index)=>({index,value:index%17===0?0:Math.floor(10**(random()*14)),vbytes:60+Math.floor(random()*5000)}));
 for(const mode of ['value','vsize']){
  const layout=viewport.pack(txs,mode);assert.equal(layout.tiles.length,txs.length,'No actual transaction is dropped');
  for(let i=0;i<layout.tiles.length;i++){
   const tile=layout.tiles[i];assert.equal(tile.tx,txs[i],'Packing retains original transaction order and identity');assert(Number.isInteger(tile.s)&&tile.s>=1,'Zero-value transactions retain selectable area');assert(tile.x>=0&&tile.y>=0&&tile.x+tile.s<=layout.width&&tile.y+tile.s<=layout.height,'Every transaction remains inside the complete block map');
   for(let j=0;j<i;j++){const other=layout.tiles[j];assert(tile.x+tile.s<=other.x||other.x+other.s<=tile.x||tile.y+tile.s<=other.y||other.y+other.s<=tile.y,'Transactions do not overlap and hide one another');}
  }
 }
}
const svg=viewport.flowSVG({inputs:[{value:null,outpoint:'unknown'},{value:0,outpoint:'<img onerror="alert(1)">'}],outputs:[{n:0,value_sats:0}],fee:null,note:'fixture'});
assert.match(svg,/unknown/,'Unknown source value stays unknown');assert.match(svg,/not yet established/,'Unknown fee stays unknown');assert.match(svg,/vout 0/,'Output zero is represented');assert.doesNotMatch(svg,/<img/,'Source outpoint is escaped in flow markup');assert.match(svg,/&lt;img/);assert.doesNotMatch(svg,/NaN|Infinity/,'Unknown and zero values do not create invalid geometry');
console.log('Block viewport helpers PASS:160 independently checked layouts; every transaction retained, bounded and nonoverlapping; zero-value selectability; unknown flow values and escaped labels.');
