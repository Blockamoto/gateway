'use strict';
// Bitmap results are scoped experimental evidence, not ownership assertions.
window.GatewayBitmapView=(()=>{
  const known=n=>Number.isSafeInteger(n)&&n>=0,number=n=>known(n)?n.toLocaleString('en-GB'):'Unavailable';
  function parseAddress(address){
    let m;if((m=/^(0|[1-9][0-9]*)\.bitmap$/.exec(address)))return known(Number(m[1]))?{kind:'district',district:Number(m[1])}:null;
    if((m=/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.bitmap$/.exec(address)))return known(Number(m[1]))&&known(Number(m[2]))?{kind:'parcel',transaction:Number(m[1]),district:Number(m[2])}:null;
    if((m=/^(0|[1-9][0-9]*)\.bitmapchain$/.exec(address)))return known(Number(m[1]))?{kind:'chain',height:Number(m[1])}:null;
    if(['bitmap.gateway','bitmapchain.gateway'].includes(address))return {kind:address==='bitmapchain.gateway'?'chain':'district',landing:true};
    return null;
  }
  function mount({root,target,api,onNavigate,onBuild,onResolved}){
    const doc=root.ownerDocument,el=(tag,text,cls)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=String(text);if(cls)node.className=cls;return node;};
    const title={district:'Bitmap district',parcel:'Bitmap parcel',chain:'BitmapChain'}[target.kind],heading=el('h1',title),status=el('p','','small'),body=el('section',undefined,'panel'),records=el('section',undefined,'panel'),list=el('div'),more=el('button','Load more records'),advanced=el('details'),evidence=el('pre');heading.tabIndex=-1;status.id='bitmap-view-state';status.setAttribute('role','status');body.id='bitmap-view-result';records.append(el('h2','Saved records'),list,more);more.type='button';more.hidden=true;advanced.append(el('summary','Advanced profile, commitments and raw evidence'),evidence);root.replaceChildren(heading,el('p','Experimental Gateway interpretation. Claim selection and scoped chain outcomes do not establish ownership.','small muted'),status,body,records,advanced);
    let dead=false,serial=0,cursor='',anchor=null,busy=false;
    const link=(label,address)=>{const button=el('button',label,'text-button mono');button.type='button';button.onclick=()=>onNavigate?.(address);return button;};
    const fields=rows=>{const dl=el('dl',undefined,'kv');for(const [label,value]of rows)dl.append(el('dt',label),el('dd',value??'Unavailable'));return dl;};
    const query=()=>{const p=new URLSearchParams({kind:target.kind});for(const key of ['district','transaction','height'])if(known(target[key]))p.set(key,String(target[key]));return p;};
    const checkpoint=value=>value.snapshot&&JSON.stringify([value.snapshot.height,value.snapshot.block_hash,value.snapshot.commitment,value.snapshot.profile_hash]);
    function render(value){
      body.replaceChildren();status.textContent=value.note||String(value.state||'unavailable').replaceAll('_',' ');root.dataset.state=value.state||'unavailable';
      const result=value.result||{},snapshot=value.snapshot||{},coverage=(value.coverage||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', '),gaps=(value.gaps||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', ');
      body.append(fields([['State',String(value.state||'unavailable').replaceAll('_',' ')],['Profile',snapshot.profile_id||value.profile?.id||'Not established'],['Source',snapshot.verification||'Saved source evidence not established'],['Scope start',number(snapshot.segment_start)],['Saved checkpoint',number(snapshot.height)],['Coverage',coverage||'Not established'],['Gaps',gaps||'None reported; inspect scope'],['Ownership','Unknown']]));
      if(target.kind==='chain'){
        const evaluated=value.block_evaluated??result.block_evaluated,ids=value.ordered_winner_ids??result.ordered_winner_ids;
        body.append(el('h2',evaluated===true?'Evaluated block '+number(target.height):'Block outcome unavailable'));
        body.append(el('p',evaluated===true?Array.isArray(ids)&&ids.length===0?'Evaluated: no selected district claims in this block.':Array.isArray(ids)?number(ids.length)+' ordered selected claims.':'Evaluated; selected-claim count unavailable.':'This block is not established as evaluated. Missing evidence is not a zero outcome.'));
        if(Array.isArray(ids))for(const id of ids)body.append(link(String(id),String(id)));
        const complete=value.complete_scope??result.complete_scope;body.append(el('p',complete===true?'The reported chain scope is complete at this checkpoint.':'The reported scope is partial. A partial outcome chain is not a complete BitmapChain commitment.','small muted'));
        for(const [label,key]of [['Cumulative selected districts','cumulative_district_count'],['Cumulative selected parcels','cumulative_parcel_count']])if(known(value[key]??result[key]))body.append(fields([[label,number(value[key]??result[key])]]));
      }else{
        body.append(el('h2',target.kind==='parcel'?'Parcel '+number(target.transaction)+' · district '+number(target.district):'District '+number(target.district)));
        const winner=result.winner_id??value.winner_id;if(winner)body.append(link('Selected claim '+winner,winner));else body.append(el('p',value.state==='unclaimed_at_snapshot'?'No selected claim in the established snapshot.':'A selected claim has not been established.'));
        if(known(target.district))body.append(link('Explore district block',target.district+'.bitcoin'));
        if(target.kind==='parcel')body.append(link('Parent district',target.district+'.bitmap'));
        for(const candidate of [...(result.candidates||value.candidates||[]),...(value.pending_candidates||[])]){const row=el('article',undefined,'address-output-row');row.append(link(candidate.id||candidate.reveal_coordinate,candidate.reveal_coordinate||candidate.id),el('p',[candidate.eligibility,candidate.selection,candidate.reason].filter(Boolean).join(' · '),'small muted'));if(candidate.unresolved_dependencies?.length)row.append(el('p','Unresolved dependencies: '+candidate.unresolved_dependencies.join(', '),'small muted'));body.append(row);}
      }
      evidence.textContent=JSON.stringify(value,null,2);
      if(['not_built','unavailable','unresolved','stale'].includes(value.state)){const inspect=el('button','Inspect '+title+' track');inspect.type='button';inspect.onclick=()=>onBuild?.({district:'bitmap-districts',parcel:'bitmap-parcels',chain:'bitmap-chain'}[target.kind]);body.append(inspect);}
      onResolved?.(value);
    }
    async function loadRecords(append=false){if(dead||busy)return;const ticket=++serial;busy=true;more.disabled=true;try{const p=query();p.set('limit','50');if(append&&cursor)p.set('cursor',cursor);const value=await api('/api/v1/bitmap/records?'+p);if(dead||ticket!==serial)return;if(value.kind!==target.kind)throw Error('The Bitmap records belong to a different resource kind.');const next=checkpoint(value);if(append&&anchor!==next)throw Error('The Bitmap snapshot changed during pagination. Refresh the resource before continuing.');if(!append){list.replaceChildren();anchor=next;}for(const record of value.rows||[]){const row=el('article',undefined,'address-output-row'),coordinate=record.coordinate||(target.kind==='chain'&&known(record.height)?record.height+'.bitmapchain':record.reveal_coordinate||record.winner_id||record.id);if(coordinate)row.append(link(coordinate,coordinate));row.append(el('p',record.block_evaluated===false?'Not evaluated':record.block_evaluated===true?'Evaluated'+(Array.isArray(record.ordered_winner_ids)?' · '+number(record.ordered_winner_ids.length)+' selected claims':''):record.reason||record.state||'Saved record','small muted'));list.append(row);}cursor=value.next_cursor||'';more.hidden=!value.has_more||!cursor;}catch(error){if(!dead&&ticket===serial){cursor='';more.hidden=true;list.replaceChildren(el('p',error.message,'small muted'));}}finally{if(!dead){busy=false;more.disabled=false;}}}
    more.onclick=()=>loadRecords(true);
    async function load(){const ticket=++serial;status.textContent='Reading saved Bitmap evidence…';try{if(target.landing){body.append(el('p','Use the main address bar: H.bitmap for a district, T.H.bitmap for a parcel, or H.bitmapchain for an evaluated chain block.'));records.hidden=true;onResolved?.({state:'landing'});return;}const value=await api('/api/v1/bitmap/view?'+query());if(dead||ticket!==serial)return;if(value.kind!==target.kind)throw Error('The Bitmap response belongs to a different resource kind.');if(['ready','selected','unclaimed_at_snapshot','provisional'].includes(value.state)&&(!known(value.snapshot?.height)||!value.snapshot?.block_hash||!value.snapshot?.commitment))throw Error('The Bitmap response has no authenticated saved checkpoint.');render(value);await loadRecords();}catch(error){if(!dead&&ticket===serial){status.textContent=error.message;root.dataset.state='unavailable';body.replaceChildren(el('p','Saved Bitmap evidence is unavailable. No index build was started.'));onResolved?.({state:'unavailable'});}}}
    load();return {teardown(){dead=true;serial++;},refresh:load};
  }
  return {parseAddress,mount};
})();
