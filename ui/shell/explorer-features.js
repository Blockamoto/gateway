'use strict';
// Read-only presentation of existing gates, providers and saved coverage.
// This module never fetches, scans, arms a track or derives new Bitcoin facts.
(function(root,factory){const api=factory();if(typeof module==='object'&&module.exports)module.exports=api;if(root)root.GatewayExplorerFeatures=api;})(typeof window==='object'?window:null,function(){
  const hash=value=>typeof value==='string'&&/^[0-9a-f]{64}$/i.test(value);
  const snapshot=value=>value&&Number.isSafeInteger(value.height)&&value.height>=0&&hash(value.block_hash||value.hash);
  const validRanges=rows=>(rows||[]).filter(r=>Number.isSafeInteger(r.from)&&Number.isSafeInteger(r.to)&&r.from>=0&&r.to>=r.from);
  const scope=rows=>{rows=validRanges(rows);if(!rows.length)return 'Only where existing evidence answers the query; gaps remain unknown.';const first=Math.min(...rows.map(r=>r.from)),last=Math.max(...rows.map(r=>r.to));return rows.length===1?'Saved coverage: blocks '+first.toLocaleString('en-GB')+'–'+last.toLocaleString('en-GB')+'.':'Saved ranges between blocks '+first.toLocaleString('en-GB')+' and '+last.toLocaleString('en-GB')+'; gaps remain unknown.';};
  function project(status={}){
    const providers=status.providers||[],instances=status.instances||[],defs=status.definitions||[],tracks=status.timeline?.tracks||[];
    const gate=id=>{const d=defs.find(d=>d.id===id);return d?d.locked!==true:null;};
    const usable=id=>providers.filter(p=>p.id===id&&p.queryable===true&&p.readiness!=='locked');
    const trusted=i=>i.queryable===true&&!/stale_reorg|anchor_unavailable|unavailable/.test(i.verification||'');
    const owner=id=>id==='txio-links'?'txo-spender':id;
    const ranges=id=>{
      const actual=owner(id);
      const owned=instances.filter(i=>i.definition===actual),p=usable(actual).flatMap(p=>p.coverage||[]),i=owned.filter(trusted).flatMap(i=>i.coverage||[]);
      const t=tracks.find(t=>t.id===id)||tracks.find(t=>t.id===actual);
      const invalid=owned.some(i=>/stale_reorg|anchor_unavailable|unavailable/.test(i.verification||''));
      return validRanges([...p,...i,...(invalid?[]:t?.recorded_coverage||[])]);
    };
    const saved=id=>ranges(id).length>0||usable(owner(id)).length>0;
    const blockSources=providers.filter(p=>p.id==='blocks');
    const demand=blockSources.some(p=>p.on_demand===true||typeof p.on_demand!=='boolean'&&p.readiness==='connected_peers_fetch_on_demand');
    const bodyRanges=validRanges([...blockSources.filter(p=>p.queryable===true&&['gateway_cache','core_mount'].includes(p.provider)).flatMap(p=>p.coverage||[]),...(tracks.find(t=>t.id==='blocks')?.sources||[]).filter(s=>s.kind==='local_raw_blocks').flatMap(s=>s.ranges||[])]);
    const bodies=bodyRanges.length>0;
    const headers=usable('headers').length>0||status.timeline?.tip_known===true;
    const sourceState=demand&&headers?'on-demand':bodies&&headers?'scoped':'needs-source';
    const sourceScope=sourceState==='on-demand'?'A connected source can be asked for the known block. That particular body may still be unavailable.':sourceState==='scoped'?scope(bodyRanges):'Connect a block source or use a body already saved locally. Checked chain information is also needed.';
    const out=[],add=(id,name,state,need,onDemand,detail,index)=>out.push({id,name,state,need,onDemand,detail,index});
    const sourceFeature=(id,name,detail,index='blocks')=>add(id,name,gate('blocks')===false?'unavailable':gate('blocks')===null&&!defs.length?'checking':sourceState,'A known block or blocator and a verified block source. No optional index or Raw retention is required.',demand?'Yes — when the requested block is obtainable.':bodies?'Within saved body coverage.':'Needs a block source.',detail+' '+sourceScope,index);
    sourceFeature('block-browsing','Browse blocks','Open a block by its height or hash.');
    sourceFeature('known-transaction','Browse a transaction, input or output','A known block-and-position address already gives the retrieval location.');
    sourceFeature('output-address','Read an output’s address','Supported addresses are calculated from the output’s payment conditions. An Addresses index is not needed. Nonstandard outputs may have no address.','txio-scripts');
    const tx=saved('tx-locator');
    add('transaction-id','Find a transaction by its ID',gate('tx-locator')===false?'unavailable':tx?'scoped':'needs-index','A saved transaction location or an available configured Core RPC transaction index.','The body can be fetched after its location is established.','A transaction ID alone does not tell ordinary Bitcoin peers which historical block to fetch. '+scope(ranges('tx-locator')),'tx-locator');
    const links=saved('txio-links');
    add('spending-links','Open an original output or find a spending input',gate('txo-spender')===false?'unavailable':links?'scoped':'needs-evidence','A positive saved relationship, or usable original/spender location evidence. Transaction locators are optional help.','Target bytes can be fetched once the relationship is known.','Some individual relationships can already be usable outside an indexed range. Missing spender data never proves an output is unspent. '+scope(ranges('txio-links')),'txio-links');
    const u=status.utxo,utxoReady=usable('utxo-state').length>0||u&&Number.isSafeInteger(u.processed_height)&&u.processed_height>=0&&hash(u.processed_hash)&&['ready','current'].includes(u.state);
    add('unspent-state','Inspect the saved unspent-output set',gate('utxo-state')===false?'unavailable':utxoReady?'snapshot':'needs-index','Its own complete ordered UTXO state from the first Bitcoin block. Separate transaction-locator and Sat indexes are not required.','No automatic history build.',utxoReady?'Answers belong to the saved processed block'+(Number.isSafeInteger(u?.processed_height)?' '+u.processed_height.toLocaleString('en-GB'):'')+'; they are not a claim about a later tip.':'Review a UTXO build to create this state. Raw can remain Off.','utxo-state');
    add('sat-math','Read mathematical properties of a Sat number','ready','A valid global Sat number. No index is required.','Yes — calculated locally.','This describes the numbered sat’s mathematical properties, not its present output or owner.','sat-state');
    const sats=status.sats,satReady=sats?.state==='ready'&&Number.isSafeInteger(sats.processed_height)&&sats.processed_height>=0&&hash(sats.processed_hash);
    const outputRanges=validRanges(sats?.output_coverage||[]);
    add('output-sat-identity','Identify the global sats inside an output',gate('sat-state')===false?'unavailable':satReady&&outputRanges.length?'scoped':'needs-index','Saved Sat ranges derived from its own ordered history. A local output offset alone is not a global Sat number.','Saved evidence only; no hidden tracing or history build.',satReady?scope(outputRanges)+' The answer belongs to the saved Sat snapshot.':'A known output still shows its value, script and offset while global identity is unresolved.','txio-sat-ranges');
    add('sat-location','Find a Sat’s saved location',gate('sat-state')===false?'unavailable':satReady?'snapshot':'needs-index','A complete selected-chain Sat state. UTXO and Positional links are separate indexes.','Saved state only.',satReady?'Location is stated at saved block '+sats.processed_height.toLocaleString('en-GB')+', not automatically at the present tip.':'Mathematical Sat facts remain available without this state.','sat-state');
    const current=status.addresses?.current,paired=snapshot(current?.snapshot)&&snapshot(current?.utxo_snapshot)&&current.snapshot.height===current.utxo_snapshot.height&&(current.snapshot.block_hash||current.snapshot.hash)===(current.utxo_snapshot.block_hash||current.utxo_snapshot.hash);
    const addressReady=current?.state==='ready'&&paired;
    add('address-current','See current address outputs and balance',gate('address-state')===false?'unavailable':addressReady?'snapshot':'needs-index','Current Addresses projected from one complete matching UTXO checkpoint.','No automatic UTXO build or wallet scan.',addressReady?'Available at saved block '+current.snapshot.height.toLocaleString('en-GB')+'. A later tip or stale projection is not implied.':'A missing or stale address projection must not be read as a zero balance.','address-state');
    const history=status.addresses?.historical,historical=history?.state==='ready'&&validRanges(history.coverage).length>0||saved('address-history');
    add('address-history','Find historical address outputs',gate('address-history')===false?'unavailable':historical?'scoped':'needs-index','Historical Outputs coverage for the blocks of interest; no complete UTXO set is required.','Requires an explicit range scan to add missing coverage.','Lists creations, including outputs later spent. Received totals are not a current balance. '+scope(validRanges(history?.coverage||[]).concat(ranges('address-history'))),'address-history');
    add('inscription-locations','List saved inscription reveal locations',gate('inscriptions')===false?'unavailable':ranges('inscriptions').length?'scoped':'needs-index','Saved Inscriptions coverage. Lean or Full keeps the reveal locations.','A new inventory requires explicitly processing its missing blocks.',scope(ranges('inscriptions'))+' Saved locations alone do not guarantee that reveal content bytes are available.','inscriptions');
    const revealProvider=providers.find(p=>p.id==='inscriptions'&&p.provider==='gateway_known_id'),revealEnabled=revealProvider?.queryable===true;
    const fullRevealRanges=validRanges(instances.filter(i=>i.definition==='inscriptions'&&i.mode==='full'&&trusted(i)).flatMap(i=>i.coverage||[]));
    const contentState=gate('inscriptions')===false?'unavailable':revealProvider&&!revealEnabled?'needs-setting':!revealProvider?'checking':revealEnabled&&demand&&headers?'on-demand':revealEnabled&&(bodies&&headers||fullRevealRanges.length)?'scoped':'needs-evidence';
    add('inscription-content','View a known inscription reveal',contentState,'Inscription viewing enabled, a known reveal location, and verified reveal bytes or Full saved content. A retained Inscriptions inventory is not required for a known reveal blocator.','Known reveals can be read from saved bodies or an available block source.',contentState==='needs-setting'?'Enable inscription viewing in Settings.':contentState==='scoped'?scope(fullRevealRanges.concat(bodyRanges))+' A particular cached reveal may also be usable; its bytes are checked when opened.':'Lean locations alone are not content. A known inscription ID may also need its reveal transaction located. Current owner, canonical number and present location are separate questions.','inscriptions');
    for(const [kind,id]of [['District','bitmap-districts-standard-v1'],['Parcel','bitmap-parcels-standard-v1']]){
      add('bitmap-'+kind.toLowerCase(),'Inspect Bitmap '+kind+' claims',gate(id)===false?'unavailable':saved(id)?'scoped':'needs-index',kind==='District'?'Standard V1 District evaluation in order from block 792435.':'Matching Standard V1 District outcomes and checked candidate evidence, including exact earlier Sat context when required.','Saved evaluated coverage; no automatic prerequisite build.',scope(ranges(id))+' Claim results do not establish current ownership.',id);
    }
    add('bitmap-chains','Bitmap Chains','unavailable','Unavailable in this version while the method is being developed.','No.','Existing saved Chain evidence remains inspectable.','bitmap-district-chain-dev-v1');
    add('sat-tracing','Forward or reverse Sat history tracing','unavailable','Unavailable in this version.','No.','Saved Sat range and location queries are separate supported features.','satline');
    return out;
  }
  const labels={ready:'Ready',snapshot:'Ready at a saved block',scoped:'Ready in saved coverage','on-demand':'Available on demand','needs-index':'Needs an index or coverage','needs-source':'Needs a block source','needs-evidence':'Needs verified evidence','needs-setting':'Needs inscription viewing enabled',unavailable:'Unavailable in this version',checking:'Checking capabilities'};
  return Object.freeze({project,labels});
});
