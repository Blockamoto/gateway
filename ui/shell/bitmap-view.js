'use strict';
// Bitmap claims and optional chain records do not establish ownership.
window.GatewayBitmapView=(()=>{
  const known=n=>Number.isSafeInteger(n)&&n>=0,number=n=>known(n)?n.toLocaleString('en-GB'):'Unavailable';
  // The local MAC attests this installation's completed scan; it is not a
  // portable, independently replayed proof of the negative interval.
  function proofNote(candidate){
    const proof=candidate?.provenance;
    if(proof?.evaluation_kind==='local-selected-raw-no-spend-attestation-v1'&&proof.relation_state==='rejected')return 'Locally evaluated negative relationship: this profile checked the selected interval and retained a local attestation. The local attestation may remain usable if Raw blocks are removed; independent replay requires the retained Raw blocks for that interval. An independent replay action is not yet available in the interface. Opening this view does not start a scan.';
    if(proof?.evaluation_kind==='selected-chain-satpoint-replay-v3')return 'Relationship evidence was replayed from retained receipt blocks against the selected chain. Viewing this result does not start a historical scan.';
    return '';
  }
  function rawInventoryNote(status,candidate){
    const route=status?.parcel_prerequisites?.routes?.find(r=>r.id==='raw_blocks'),proof=candidate?.provenance||{},start=proof.scan_from,end=proof.scan_through;
    if(!route)return 'Raw inventory status is unavailable; independent replay availability is unknown.';
    const scoped=known(start)&&known(end)&&end>=start&&known(route.from)&&known(route.to)&&start>=route.from&&end<=route.to;
    const from=scoped?start:route.from,to=scoped?end:route.to,scope=scoped?'this proof interval':'the broader inventory scope';
    if(route.state==='coverage_gaps'&&Array.isArray(route.gaps)){
      const gaps=route.gaps.filter(r=>known(r.from)&&known(r.to)&&r.to>=from&&r.from<=to).map(r=>number(Math.max(from,r.from))+'–'+number(Math.min(to,r.to)));
      if(gaps.length)return 'Reported Raw inventory gaps in '+scope+' (blocks '+number(from)+'–'+number(to)+'): '+gaps.join(', ')+'.'+(scoped?'':' The exact proof interval is unavailable.');
      if(scoped)return 'No Raw inventory gaps are reported in this proof interval (blocks '+number(from)+'–'+number(to)+'). Inventory is not a fresh independent replay.';
    }
    if(route.state==='coverage_observed')return 'Raw inventory reports coverage of '+scope+' (blocks '+number(from)+'–'+number(to)+'). This is not a fresh independent replay.';
    return 'Raw inventory completeness is unknown for '+scope+'; independent replay availability is not established.';
  }
  function parseAddress(address){
    const [base,search='']=String(address).split('?'),params=new URLSearchParams(search),instance=params.get('instance');if(instance&&!/^(?:bitmap-(?:districts|parcels)(?:-canonical(?:-v2)?|-standard-v1(?:-replay)?|-[0-9a-f]{32})?|bitmap-chain(?:-[0-9a-f]{32})?|bitmap-(?:district|parcel)-chain-dev-v1)$/.test(instance))return null;
    if(base==='bitmap.gateway')return {kind:'district',landing:true,overview:true};
    if(search){const parsed=parseAddress(base);return parsed?{...parsed,...(instance?{instance}:{}),...(params.get('expected_profile')?{expected_profile:params.get('expected_profile')}:{})}:null;}
    let m;if((m=/^(0|[1-9][0-9]*)\.bitmap$/.exec(address)))return known(Number(m[1]))?{kind:'district',district:Number(m[1])}:null;
    if((m=/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.bitmap$/.exec(address)))return known(Number(m[1]))&&known(Number(m[2]))?{kind:'parcel',transaction:Number(m[1]),district:Number(m[2])}:null;
    if((m=/^(0|[1-9][0-9]*)\.bitmapchain$/.exec(address)))return known(Number(m[1]))?{kind:'chain',height:Number(m[1])}:null;
    if(['bitmap.gateway','bitmapchain.gateway'].includes(address))return {kind:address==='bitmapchain.gateway'?'chain':'district',landing:true};
    return null;
  }
  function mount({root,target,api,onNavigate,onBuild,onResolved,availabilityNote,getStatus}){
    const doc=root.ownerDocument,el=(tag,text,cls)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=String(text);if(cls)node.className=cls;return node;};
    if((target.kind==='parcel'&&target.instance!=='bitmap-parcels-standard-v1-replay')||(target.kind!=='parcel'&&/^bitmap-(?:parcels(?:-|$)|parcel-chain(?:-|$))/.test(target.instance||''))){root.textContent='This resource is unavailable in this version.';return {teardown(){root.replaceChildren();},refresh(){}};}
    const title={district:'Bitmap district',parcel:'Bitmap parcel',chain:'Bitmap Chain'}[target.kind],heading=el('h1',title),status=el('p','','small'),body=el('section',undefined,'panel'),records=el('section',undefined,'panel'),list=el('div'),more=el('button','Load more records'),advanced=el('details'),evidence=el('pre');heading.tabIndex=-1;status.id='bitmap-view-state';status.setAttribute('role','status');body.id='bitmap-view-result';records.append(el('h2','Saved records'),list,more);more.type='button';more.hidden=true;advanced.append(el('summary','Advanced profile, commitments and raw evidence'),evidence);root.replaceChildren(heading,el('p','Canonical Bitmap claims. Chain methodology is being developed. Ownership remains separate.','small muted'),status,body,records,advanced);
    let configurationRevision=target.expected_configuration_revision||null,dead=false,serial=0,cursor='',anchor=null,busy=false,snapshotCommitment='',resolvedDistrict=null;
    const link=(label,address)=>{const button=el('button',label,'text-button mono');button.type='button';button.onclick=()=>{const parsed=parseAddress(address),next=parsed&&target.instance&&parsed.kind===target.kind?address+(address.includes('?')?'&':'?')+new URLSearchParams({instance:target.instance}):address;onNavigate?.(next);};return button;};
    const fields=rows=>{const dl=el('dl',undefined,'kv');for(const [label,value]of rows)dl.append(el('dt',label),el('dd',value??'Unavailable'));return dl;};
    const query=()=>{const p=new URLSearchParams({kind:target.kind});for(const key of ['district','transaction','height'])if(known(target[key]))p.set(key,String(target[key]));if(target.instance)p.set('instance',target.instance);if(target.expected_profile)p.set('expected_profile',target.expected_profile);if(configurationRevision)p.set('expected_configuration_revision',String(configurationRevision));return p;};
    const checkpoint=value=>value.snapshot&&JSON.stringify([value.snapshot.height,value.snapshot.block_hash,value.snapshot.commitment,value.snapshot.profile_hash]);
    function identity(value){if(value.kind!==target.kind)throw Error('The Bitmap response belongs to a different resource kind.');if(target.instance&&value.instance!==target.instance)throw Error('The Bitmap response belongs to a different ruleset track.');if(target.expected_profile&&(value.profile_hash||value.snapshot?.profile_hash)!==target.expected_profile)throw Error('The Bitmap response belongs to a different interpretation.');if(configurationRevision&&value.configuration_revision!==configurationRevision)throw Error('The Bitmap ruleset configuration changed. Reopen this resource before continuing.');}
    function render(value){
      if(known(value.configuration_revision)&&value.configuration_revision>0)configurationRevision=value.configuration_revision;body.replaceChildren();snapshotCommitment=value.snapshot?.commitment||'';resolvedDistrict=value.district_instance||null;status.textContent=value.note||String(value.state||'unavailable').replaceAll('_',' ');root.dataset.state=value.state||'unavailable';
      if(availabilityNote)body.append(el('p',availabilityNote,'small bitmap-availability'));
      const result=value.result||{},snapshot=value.snapshot||{},coverage=(value.coverage||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', '),gaps=(value.gaps||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', ');
      body.append(fields([['State',String(value.state||'unavailable').replaceAll('_',' ')],['Profile',snapshot.profile_id||value.profile?.id||'Not established'],['Source',snapshot.verification||'Saved source evidence not established'],['Scope start',number(snapshot.segment_start)],['Saved checkpoint',number(snapshot.height)],['Coverage',coverage||'Not established'],['Gaps',gaps||'None reported; inspect scope'],['Ownership','Unknown']]));
      if(target.kind==='chain'){
        const evaluated=value.block_evaluated??result.block_evaluated,ids=value.ordered_winner_ids??result.ordered_winner_ids;
        body.append(el('h2',evaluated===true?'Evaluated block '+number(target.height):'Block outcome unavailable'));
        body.append(el('p',evaluated===true?Array.isArray(ids)&&ids.length===0?'Evaluated: no selected claims in this block.':Array.isArray(ids)?number(ids.length)+' ordered selected claims.':'Evaluated; selected-claim count unavailable.':'This block is not established as evaluated. Missing evidence is not a zero outcome.'));
        if(Array.isArray(ids))for(const id of ids)body.append(link(String(id),String(id)));
        const complete=value.complete_scope??result.complete_scope;body.append(el('p',complete===true?'The reported chain scope is complete at this checkpoint.':'The reported scope is partial. A partial outcome chain is not a complete BitmapChain commitment.','small muted'));
        for(const [label,key]of [['Cumulative selected districts','cumulative_district_count'],['Cumulative selected parcels','cumulative_parcel_count']])if(known(value[key]??result[key]))body.append(fields([[label,number(value[key]??result[key])]]));
      }else{
        body.append(el('h2',target.kind==='parcel'?'Parcel '+number(target.transaction)+' · district '+number(target.district):'District '+number(target.district)));
        const winner=result.winner_blocator??result.winner_id??value.winner_id;if(winner)body.append(link('Selected claim '+winner,winner));else body.append(el('p',value.state==='unclaimed_at_snapshot'?'No selected claim in the established snapshot.':'A selected claim has not been established.'));
        if(known(target.district))body.append(link('Explore district block',target.district+'.bitcoin'));
        if(target.kind==='parcel'){const parent=el('button','Parent district','text-button mono');parent.type='button';parent.disabled=!!target.instance&&!resolvedDistrict;parent.title=parent.disabled?'The pinned District instance has not been established.':'Open the exact pinned District interpretation';parent.onclick=()=>onNavigate?.(target.district+'.bitmap'+(resolvedDistrict?'?'+new URLSearchParams({instance:resolvedDistrict}):''));body.append(parent);}
        for(const candidate of [...(result.candidates||value.candidates||[]),...(value.pending_candidates||[])]){const row=el('article',undefined,'address-output-row');row.append(link(candidate.id||candidate.reveal_coordinate,candidate.reveal_coordinate||candidate.id),el('p',[candidate.eligibility,candidate.selection,candidate.reason].filter(Boolean).join(' · '),'small muted'));if(candidate.unresolved_dependencies?.length)row.append(el('p','Unresolved dependencies: '+candidate.unresolved_dependencies.join(', '),'small muted'));const note=proofNote(candidate);if(note){row.append(el('p',note,'small muted'));if(candidate.provenance?.evaluation_kind==='local-selected-raw-no-spend-attestation-v1')row.append(el('p',rawInventoryNote(getStatus?.(),candidate),'small muted'));}body.append(row);}
      }
      evidence.textContent=JSON.stringify(value,null,2);
      if(['not_built','unavailable','unresolved','stale','not_retained'].includes(value.state)){const inspect=el('button',target.kind==='parcel'?'Inspect saved Parcel index':'Inspect '+title+' track');inspect.type='button';inspect.onclick=()=>onBuild?.(target.instance||{district:'bitmap-districts-standard-v1',parcel:'bitmap-parcels-standard-v1-replay',chain:'bitmap-district-chain-dev-v1'}[target.kind]);body.append(inspect);}
      onResolved?.(value);
    }
    async function loadRecords(append=false){if(dead||busy)return;const ticket=++serial;busy=true;more.disabled=true;try{const p=query();p.set('limit','50');if(snapshotCommitment)p.set('snapshot',snapshotCommitment);if(append&&cursor)p.set('cursor',cursor);const value=await api('/api/v1/bitmap/records?'+p);if(dead||ticket!==serial)return;identity(value);const next=checkpoint(value);if(append&&anchor!==next)throw Error('The Bitmap snapshot changed during pagination. Refresh the resource before continuing.');if(!append){list.replaceChildren();anchor=next;}for(const record of value.rows||[]){const row=el('article',undefined,'address-output-row'),coordinate=record.coordinate||(target.kind==='chain'&&known(record.height)?record.height+'.bitmapchain':record.reveal_coordinate||record.winner_id||record.id);if(coordinate)row.append(link(coordinate,coordinate));row.append(el('p',record.block_evaluated===false?'Not evaluated':record.block_evaluated===true?'Evaluated'+(Array.isArray(record.ordered_winner_ids)?' · '+number(record.ordered_winner_ids.length)+' selected claims':''):record.reason||record.state||'Saved record','small muted'));list.append(row);}cursor=value.next_cursor||'';more.hidden=!value.has_more||!cursor;}catch(error){if(!dead&&ticket===serial){cursor='';more.hidden=true;list.replaceChildren(el('p',error.message,'small muted'));}}finally{if(!dead){busy=false;more.disabled=false;}}}
    more.onclick=()=>loadRecords(true);
    async function load(){const ticket=++serial;status.textContent='Reading saved Bitmap evidence…';try{if(target.landing){body.append(el('p','Use the main address bar: H.bitmap for a district or H.bitmapchain for an evaluated chain block.'));records.hidden=true;onResolved?.({state:'landing'});return;}const value=await api('/api/v1/bitmap/view?'+query());if(dead||ticket!==serial)return;identity(value);if(['ready','selected','unclaimed_at_snapshot','provisional'].includes(value.state)&&(!known(value.snapshot?.height)||!value.snapshot?.block_hash||!value.snapshot?.commitment))throw Error('The Bitmap response has no authenticated saved checkpoint.');render(value);await loadRecords();}catch(error){if(!dead&&ticket===serial){status.textContent=error.message;root.dataset.state='unavailable';body.replaceChildren(el('p','Saved Bitmap evidence is unavailable. No index build was started.'));onResolved?.({state:'unavailable'});}}}
    load();return {teardown(){dead=true;serial++;},refresh:load};
  }
  return {parseAddress,mount,proofNote,rawInventoryNote};
})();
