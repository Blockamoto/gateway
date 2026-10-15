'use strict';
// Exact resource marks must never invent placement from a previous view.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const context={window:{}};vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../ui/shell/index-transport.js'),'utf8'),context);
const {point,compare,project,owner,executionRequest}=context.window.GatewayIndexTransport,plain=value=>JSON.parse(JSON.stringify(value)),anchor='a'.repeat(64),other='b'.repeat(64);
assert.equal(point({resource:'address',address:'fixture'}),null);
assert.equal(point({kind:'sat',sat_number:5000000000,origin:{height:1}}),null,'Issuance origin is not current location');
assert.equal(point({kind:'output',height:170,txIndex:1}),null,'Incomplete deep coordinates do not become block marks');
assert.equal(point({kind:'satpoint',height:170,txIndex:1,outputIndex:0,satOffset:-1}),null);
const location={state:'unspent',output_coordinate:'0.1.170',offset:3},snapshot={height:226,block_hash:anchor,commitment:other};
const sat=point({resource:'sat',sat_number:5000000000,location,snapshot});
assert.equal(sat.height,170);assert.equal(sat.satOffset,3);assert.equal(sat.block_hash,null,'State-tip hash is not a creation-block hash');
assert.equal(point({resource:'sat',sat_number:5000000000,location:{...location,state:'lost'},snapshot}),null);
const a=point({kind:'output',height:170,txIndex:1,outputIndex:0,block_hash:anchor}),b=point({kind:'output',height:170,txIndex:1,outputIndex:2,block_hash:anchor});
assert.equal(compare(a,b),-1);assert.equal(project({in:b,out:a}).valid,false,'Reversed fine endpoints reject even within one block');
assert.equal(project({in:a,out:{...b,block_hash:other}}).valid,false,'Same-height branch mismatch rejects');
assert.equal(compare(point({kind:'input',height:170,txIndex:1,inputIndex:0}),a),null,'Input and output offsets have no invented relative order');
assert.deepEqual(plain(project({in:a,out:b})),{valid:true,in:170,out:170,in_hash:anchor,out_hash:anchor});
assert.deepEqual(plain(project({in:sat,out:null})),{valid:true,in:170,out:null});
assert.equal(owner('txio-values'),'txo-spender');assert.equal(owner('txio-scripts'),'txo-spender');assert.equal(owner('txio-sat-ranges'),'sat-state','Presentation parent does not imply execution dependency');
console.log('Index transport coordinate checks PASS: exact placement, unknown/lost rejection, branch binding, fine ordering, whole-block projection and independent Sat owner.');

const definitions=[{id:'blocks',buildable:true},{id:'sat-state',buildable:true},{id:'txo-spender',buildable:true},{id:'address-state',buildable:true},{id:'unsupported',buildable:true,live_supported:false},{id:'locked',buildable:true,locked:true}];
const prepared={in:170,out:226,in_hash:anchor,out_hash:other,consumers:[{index:'blocks',armed:true,live:true},{index:'sat-state',armed:true,live:true,retain_sat_history:true},{index:'txo-spender',armed:true,live:false,retain_output_values:true,retain_output_scripts:false},{index:'address-state',armed:false,live:true,rebuild:true,rebuild_snapshot:{height:170,block_hash:anchor,commitment:other}},{index:'unsupported',armed:true,live:false},{index:'locked',armed:false,live:false}]};
const unchanged=plain(prepared),finite=plain(executionRequest(prepared,'play',definitions)),record=plain(executionRequest(prepared,'record',definitions));
assert(finite.consumers.filter(c=>c.armed).every(c=>c.live===false),'Play stages a finite run even when armed tracks were Live');
for(const id of ['blocks','sat-state','txo-spender'])assert.equal(record.consumers.find(c=>c.index===id).live,true,'Record stages eligible armed consumers for Live');
assert.equal(record.consumers.find(c=>c.index==='unsupported').live,false,'Record does not enable unsupported Live');
for(const next of [finite,record]){assert.equal(next.in,170);assert.equal(next.out,226,'Explicit Out remains a hard cap');assert.equal(next.in_hash,anchor);assert.equal(next.out_hash,other);assert.equal(next.consumers.find(c=>c.index==='sat-state').retain_sat_history,true);assert.equal(next.consumers.find(c=>c.index==='txo-spender').retain_output_values,true);assert.deepEqual(next.consumers.find(c=>c.index==='address-state'),unchanged.consumers.find(c=>c.index==='address-state'),'Unarmed choices and rebuild guard remain intact');}
assert.deepEqual(plain(prepared),unchanged,'Choosing an execution intent does not mutate its source request');
assert.throws(()=>executionRequest(prepared,'playback',definitions),/Choose Play or Record/);
console.log('Index transport intent checks PASS: finite Play, eligible Live Record, hard Out/hash boundaries, independent optional data and unarmed recovery choices.');
