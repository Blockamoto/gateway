'use strict';
// Exact resource marks must never invent placement from a previous view.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const context={window:{}};vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../ui/shell/index-transport.js'),'utf8'),context);
const {point,compare,project,owner,executionRequest,stagedProgressText}=context.window.GatewayIndexTransport,plain=value=>JSON.parse(JSON.stringify(value)),anchor='a'.repeat(64),other='b'.repeat(64);
const utxoLeadIn=context.window.GatewayIndexTransport.utxoLeadIn,utxoRequest={consumers:[{index:'utxo-state',armed:true}]};
assert.equal(utxoLeadIn({consumers:[{index:'utxo-state',state:'blocked'}]},utxoRequest),null,'Unrelated failures do not open a lead-in notice');
assert.equal(utxoLeadIn({consumers:[{index:'utxo-state',requires_lead_in:true}]},utxoRequest),null,'Missing next height is not inferred');
assert.equal(utxoLeadIn({consumers:[{index:'utxo-state',requires_lead_in:true,next_required_height:0,height:-1}]},utxoRequest).fresh,true);
assert.equal(utxoLeadIn({consumers:[{index:'utxo-state',requires_lead_in:true,next_required_height:11,height:10,block_hash:anchor,commitment:other}]},{consumers:[{index:'utxo-state',armed:true,rebuild:true}]}).saved,false,'Rebuild cannot offer the saved boundary');
assert.equal(stagedProgressText({height:792434,staged_height:792435,staged_blocks:1,staged_records:3139,staged_bytes:100,logical_batch_from:792288,logical_batch_to:792623}),'No committed checkpoint yet. Staged 1 block through 792,435 · 3,139 records · 100 bytes; staged data is not yet committed. Logical batch 792,288–792,623. Run boundaries and staging limits may flush it earlier.','An uncommitted initial cursor never claims saved coverage');
assert.match(stagedProgressText({height:2,commitment:anchor,staged_height:3,staged_blocks:1}),/^Committed through block 2\. Staged 1 block through 3/);
assert.equal(stagedProgressText({height:2,commitment:anchor,staged_blocks:0}), '');
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
const unchanged=plain(prepared),next=plain(executionRequest(prepared,'play',definitions));
assert.deepEqual(next,unchanged,'Play preserves staged finite/Live choices, options, source anchors and hard Out');
assert.notEqual(next,prepared,'Review receives an independent copy');
assert.equal(next.consumers.find(c=>c.index==='blocks').live,true);
assert.equal(next.consumers.find(c=>c.index==='txo-spender').live,false,'Mixed run keeps finite consumers finite');
assert.deepEqual(plain(prepared),unchanged,'Review does not mutate source request');
assert.throws(()=>executionRequest(prepared,'record',definitions),/Use Play/);
console.log('Index transport staging checks PASS: one Play preserves per-track Live, hard Out/hash boundaries and unarmed recovery choices.');

const parcel={id:'bitmap-parcels-standard-v1',kind:'parcel',buildable:true};
assert.equal(context.window.GatewayIndexTransport.processingAvailable(parcel),false,'Even a stale catalog cannot enable Parcels');
assert.equal(context.window.GatewayIndexTransport.processingAvailable({...parcel,id:'bitmap-parcels-canonical-v2'}),false);
assert.equal(context.window.GatewayIndexTransport.processingAvailable({id:'bitmap-districts-standard-v1',buildable:true}),true,'Districts remain available');
assert.throws(()=>executionRequest({consumers:[{index:parcel.id,armed:true}]},'play',[parcel]),/This saved work is unavailable/);
assert.throws(()=>executionRequest({consumers:[{index:'bitmap-parcels-old',armed:true}]},'play',[]),/This saved work is unavailable/,'Missing definitions cannot hide a restored Parcel');
console.log('Parcel transport guard PASS: canonical, legacy and missing-definition activation rejected; Districts remain available.');

const rawSnapshot={definitions:[{id:'alias',kind:'parcel',buildable:true},{id:'bitmap-parcel-chain-dev-v1',producer_kind:'parcel'},{id:'bitmap-districts-standard-v1',buildable:true}],instances:[{definition:'alias',coverage:[{from:1,to:1}]},{definition:'bitmap-parcels-canonical-v2',checkpoint:{height:1}}],live:[{index:'alias',on:true,enabled:true}],providers:[{id:'bitmap-parcels'}],jobs:[{id:'old',index:'alias',state:'paused'}],job:{id:'old',index:'alias'},run:{prepared:{consumers:[{index:'alias',armed:true}]},run:{id:'mixed',state:'paused',request:{consumers:[{index:'alias',armed:true},{index:'bitmap-districts-standard-v1',armed:true}]},consumers:[{index:'alias',state:'paused'}]}},timeline:{tracks:[{id:'alias'},{id:'bitmap-parcels-standard-v1'}]}};
const rawCopy=plain(rawSnapshot),visible=plain(context.window.GatewayIndexTransport.visibleSnapshot(rawSnapshot));
assert.deepEqual(plain(rawSnapshot),rawCopy,'UI filtering never mutates backend evidence');
assert.equal(visible.definitions.length,1);for(const key of ['instances','live','providers','jobs'])assert.equal(visible[key].length,0,key);assert.equal(visible.job,null);assert.equal(visible.timeline.tracks.length,0);assert.equal(visible.run.prepared.consumers.length,0);assert.equal(visible.run.run.consumers.length,0);assert.equal(visible.run.run.request.consumers.length,1);assert.equal(visible.run.run.hidden_processing_unavailable,true,'Filtered old run cannot silently resume hidden work');
assert.equal(context.window.GatewayIndexTransport.parcelDefinition({id:'alias'}),true,'Custom Parcel aliases remain known after their UI row is removed');
console.log('Parcel hidden UI projection PASS: all generations, aliases, related chains, saved evidence, live/jobs/run/status hidden without altering API data.');

const waiting=context.window.GatewayIndexTransport.visibleSnapshot({jobs:[{id:'waiting',index:'sat-state',state:'waiting',error:'Missing authenticated source bytes.',progress:[{index:'sat-state',state:'running'}]}]});
assert.equal(Object.prototype.hasOwnProperty.call(waiting.jobs[0].progress[0],'error'),false,'Absent progress error stays absent so it cannot shadow the parent error');
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../ui/shell/index-cards.js'),'utf8'),context);
assert.equal(context.window.GatewayIndexCards.jobFor('sat-state',waiting).error,'Missing authenticated source bytes.');
const withProgressError=context.window.GatewayIndexTransport.visibleSnapshot({jobs:[{id:'waiting',index:'sat-state',state:'waiting',error:'Parent error',progress:[{index:'sat-state',state:'running',error:'Specific error'}]}]});
assert.equal(context.window.GatewayIndexCards.jobFor('sat-state',withProgressError).error,'Specific error','A present progress error still takes precedence');

const replay={id:'bitmap-parcels-standard-v1-replay',kind:'parcel',buildable:true};
assert.equal(context.window.GatewayIndexTransport.processingAvailable(replay),true,'Only the replay track can be offered for processing');
assert.equal(context.window.GatewayIndexTransport.processingAvailable({...replay,buildable:false}),false,'Backend buildability gates admission');
assert.equal(context.window.GatewayIndexTransport.visibleSnapshot({definitions:[replay,parcel],timeline:{tracks:[{id:replay.id},{id:parcel.id}]}}).definitions.length,1);
assert.equal(executionRequest({consumers:[{index:replay.id,armed:true}]},'play',[replay]).consumers[0].index,replay.id);
console.log('Replay Parcel UI guard PASS: exact track visible, backend buildability honored, legacy fenced.');
assert.throws(()=>executionRequest({consumers:[{index:replay.id,armed:true}]},'play',[]),/not available/,'Replay requires current buildable catalog evidence');

const options=context.window.GatewayIndexTransport.parcelEvidenceOptions;
assert.deepEqual(plain(options('',100)),{parcel_evidence_route:'',parcel_raw_scan_budget:0},'Default authorizes no scan');
assert.deepEqual(plain(options('retained-raw','')), {parcel_evidence_route:'retained-raw',parcel_raw_scan_budget:0},'Choosing route does not supply a budget');
for(const n of [-1,1.5,10000001,'bad'])assert.equal(options('retained-raw',n).parcel_raw_scan_budget,0);
assert.equal(options('retained-raw',250).parcel_raw_scan_budget,250,'Explicit bounded read budget preserved');
console.log('Parcel evidence consent PASS: no implicit route or scan budget, explicit positive bounded reads only.');

for(const route of ['tx-locators','txio-links']){assert.deepEqual(plain(options(route,0)),{parcel_evidence_route:route,parcel_raw_scan_budget:0},'Route alone authorizes no block work');assert.deepEqual(plain(options(route,21)),{parcel_evidence_route:route,parcel_raw_scan_budget:21});}
