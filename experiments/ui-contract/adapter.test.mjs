import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {normalizeLegacyStatus, keepNewestRun, legacyCommands, legacyError} from './adapter.mjs';

const sample=()=>({
  observation:{mode:'progress',details_observed_at:'2026-10-26T10:00:00Z',details_age_milliseconds:90000,details_refreshing:true},
  definitions:[{id:'blocks',name:'Blocks'},{id:'tx-locator',name:'Transaction IDs'}],
  timeline:{observed_at:'2026-10-26T10:00:00Z',tip_known:true,tip_height:100,tip_hash:'a'.repeat(64),tracks:[
    {id:'blocks',locked:false,coverage:[{from:10,to:12}],gaps:[{from:0,to:9},{from:13,to:100}],coverage_complete:true,
      recorded_coverage:[],reported_coverage:[],retained_coverage:{scope:'gateway_retained_raw',state:'complete',complete:true,revision:'0',ranges:[],total_blocks:0,observed_blocks:0},
      sources:[{id:'gateway_archive',kind:'local_raw_blocks',ranges:[{from:10,to:12}],complete:true,verification:'header_and_file_extent_checked'}]},
    {id:'tx-locator',locked:false,coverage:[{from:10,to:12}],gaps:[{from:0,to:9},{from:13,to:100}],coverage_complete:true,
      recorded_coverage:[{from:10,to:12}],reported_coverage:[],sources:[{id:'gateway_committed_index',kind:'committed_records',ranges:[{from:10,to:12}],complete:true,verification:'selected_chain'}]}
  ]},
  run:{schema:1,revision:12,configured:true,prepared:{in:10,out:12,consumers:[{index:'tx-locator',armed:true,live:false}]},
    run:{id:'run-1',state:'running',request:{in:10,out:12,consumers:[{index:'tx-locator',armed:true,live:false}]},
      consumers:[{index:'tx-locator',height:12,staged_height:14,staged_blocks:2}]}},
  browse:[{index:'tx-locator',enabled:true,capability:'sparse'}], providers:[], instances:[], errors:[]
});

test('coverage distinguishes stored Raw, archive, committed facts, gaps and staging',()=>{
  const s=normalizeLegacyStatus(sample()); assert.equal(s.contract,'gateway.workspace/1');
  const [raw,tx]=s.timeline.tracks;
  assert.deepEqual(raw.retainedRaw.ranges,[]); assert.deepEqual(raw.localCoverage.ranges,[{from:10,to:12}]);
  assert.equal(raw.sources[0].id,'gateway_archive'); assert.equal(raw.sources[0].kind,'local_raw_blocks');
  assert.deepEqual(tx.recorded,[{from:10,to:12}]); assert.deepEqual(tx.gaps,[{from:0,to:9},{from:13,to:100}]);
  assert.equal(tx.localCoverage.enumerationComplete,true); // describes enumeration, not a gap-free chain
  assert.equal(s.run.run.consumers[0].staged_height,14); assert.equal(tx.localCoverage.ranges[0].to,12);
  assert.equal(s.observation.detailsAgeMilliseconds,90000); assert.equal(s.observation.detailsRefreshing,true);
});
test('unavailable coverage remains unknown; all original fields survive',()=>{
  const input=sample(); delete input.timeline.tracks[1].coverage; delete input.timeline.tracks[1].coverage_complete; delete input.timeline.tracks[1].sources;
  input.extraFutureField={value:'preserve'};const result=normalizeLegacyStatus(input);
  assert.equal(result.timeline.tracks[1].localCoverage.ranges,null);
  assert.equal(result.timeline.tracks[1].localCoverage.enumerationComplete,null);
  assert.equal(result.timeline.tracks[1].sources,null);
  assert.deepEqual(result.legacy.extraFutureField,{value:'preserve'});
  input.definitions[0].name='changed';assert.equal(result.legacy.definitions[0].name,'Blocks');
});
test('old run response cannot replace newer run; detail snapshot is not mislabeled fresh',()=>{
  const old=normalizeLegacyStatus(sample());const next=structuredClone(old);
  old.run.revision=13; old.run.run.state='paused'; next.run.revision=12; next.run.run.state='running';
  next.observation.detailsAgeMilliseconds=100000;
  const result=keepNewestRun(old,next);
  assert.equal(result.run.run.state,'paused');assert.equal(result.observation.detailsAgeMilliseconds,100000);
});
test('commands preserve current UI request shapes and never execute by construction',()=>{
  const request={in:10,out:12,in_hash:'a'.repeat(64),consumers:[{index:'tx-locator',armed:true,live:false,mode:'full'}]};
  assert.deepEqual(legacyCommands.prepare(request),{method:'POST',path:'/api/v1/index/run/prepare',body:request});
  assert.deepEqual(legacyCommands.plan(request).body,request);
  const start=legacyCommands.start(request);assert.deepEqual(start.body,{request});assert.equal('plan_id' in start.body,false);
  assert.equal(legacyCommands.start(request,'plan-1').body.plan_id,'plan-1');
  assert.deepEqual(legacyCommands.control('run-1','resume').body,{id:'run-1',action:'resume'});
  assert.deepEqual(legacyCommands.amend('run-1',2,[{index:'tx-locator',armed:false}]).body,{run_id:'run-1',admission_revision:2,consumers:[{index:'tx-locator',armed:false}]});
  assert.equal(legacyCommands.blockMap('10.0?').path,'/api/v1/index/block-map?block=10.0%3F');
  request.consumers[0].armed=false;assert.equal(start.body.request.consumers[0].armed,true);
  assert.throws(()=>legacyCommands.control('run-1','delete'),/invalid run action/);
});
test('legacy conflict retains the full blocked plan and does not invent a typed reason',()=>{
  const payload={error:'Cannot start',plan:{ready:false,consumers:[{index:'utxo-state',state:'blocked',reason:'Missing prefix'}]}};
  const result=legacyError(409,payload);assert.equal(result.code,'legacy_http_error');
  assert.deepEqual(result.details,payload);payload.plan.consumers[0].reason='changed';assert.equal(result.details.plan.consumers[0].reason,'Missing prefix');
});
test('malformed evidence is rejected rather than converted to zero coverage',()=>{
  const bad=sample(); bad.timeline.tracks[0].coverage=[{from:15,to:12}];
  assert.throws(()=>normalizeLegacyStatus(bad),/invalid closed block interval/);
  const dupe=sample();dupe.timeline.tracks.push(dupe.timeline.tracks[0]);assert.throws(()=>normalizeLegacyStatus(dupe),/duplicate/);
  assert.throws(()=>legacyError(409,{error:12}),/invalid legacy error/);
});

test('optional real owned 0.7.8 status maps archive separately from retained Raw',{skip:!process.env.GATEWAY_078_STATUS},()=>{
  const native=JSON.parse(fs.readFileSync(process.env.GATEWAY_078_STATUS,'utf8'));
  const actual=normalizeLegacyStatus(native); const blocks=actual.timeline.tracks.find(t=>t.id==='blocks');
  assert.ok(blocks);assert.deepEqual(blocks.retainedRaw.ranges,[]);
  assert.ok(blocks.sources.some(s=>s.id==='gateway_archive' && s.ranges?.some(r=>r.from===792435 && r.to===792466)));
  const tx=actual.timeline.tracks.find(t=>t.id==='tx-locator');assert.ok(tx.recorded.some(r=>r.from===792435 && r.to===792466));
});
