'use strict';
window.GatewayAddressPanel=(()=>{
  const known=n=>Number.isSafeInteger(n)&&n>=0,number=n=>known(n)?n.toLocaleString('en-GB'):'Unavailable';
  const request=({rebuild=false}={})=>({index:'address-state',...(rebuild?{rebuild:true}:{})});
  const identity=value=>known(value?.height)&&typeof value.block_hash==='string'&&!!value.block_hash&&typeof value.commitment==='string'&&!!value.commitment?{height:value.height,block_hash:value.block_hash,commitment:value.commitment}:null;
  const matches=(a,b)=>!!identity(a)&&!!identity(b)&&JSON.stringify(identity(a))===JSON.stringify(identity(b));
  function model(snapshot={},rebuildSnapshot=null){
    const c=snapshot.addresses?.current||{},job=window.GatewayIndexCards.jobFor('address-state',snapshot),s=c.snapshot||{},u=c.utxo_snapshot||{};
    const sourceReady=known(u.height)&&!!u.block_hash&&!!u.commitment,exact=sourceReady&&known(s.height)&&s.height===u.height&&s.block_hash===u.block_hash&&!!s.commitment;
    let state=c.state==='corrupt'?'rebuild_required':c.state||'not_built';if(state==='ready'&&!exact)state='stale';
    const hintedRebuild=state==='ready'&&exact&&matches(s,rebuildSnapshot);if(hintedRebuild)state='rebuild_required';
    if(['running','waiting','pausing','queued'].includes(job?.state)||sourceReady&&state!=='dependency_missing'&&(state!=='rebuild_required'&&['paused','failed'].includes(job?.state)||state==='rebuild_required'&&job?.state==='paused'&&job.rebuild===true))state=job.state;
    const utxo=snapshot.utxo||{},sourceOutputCount=sourceReady&&u.height===utxo.processed_height&&u.block_hash===utxo.processed_hash&&known(utxo.live_output_count)?utxo.live_output_count:null;
    return {...c,job,state,exact,sourceReady,sourceOutputCount,rebuildSnapshot:hintedRebuild?identity(rebuildSnapshot):null,outputsChecked:known(job?.blocks_checked)?job.blocks_checked:null,label:({ready:'Current',not_built:'Not built',dependency_missing:'UTXO set required',stale:'Snapshot changed',rebuild_required:'Rebuild required',unavailable:'Source unavailable',waiting:'Waiting',running:'Building',pausing:'Pausing',paused:'Paused',queued:'Queued',failed:'Needs attention'})[state]||'State unavailable'};
  }
  function mount({root,api,refresh,notify,select}){
    const doc=root.ownerDocument,el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const heading=el('div',undefined,'utxo-heading'),status=el('span','Not built','timeline-state');status.id='address-current-state';heading.append(el('h2','Current UTXOs'),status);
    const note=el('p','Build address membership from the saved UTXO set. No block range is required.','utxo-target'),stage=el('p','','utxo-stage'),metrics=el('dl',undefined,'utxo-metrics'),actions=el('div',undefined,'actions'),primary=el('button','Review build','primary'),plan=el('section',undefined,'utxo-plan'),planText=el('p'),cancel=el('button','Cancel review');
    stage.id='address-current-note';stage.setAttribute('role','status');metrics.id='address-current-metrics';primary.id='address-current-primary';primary.type=cancel.type='button';plan.id='address-current-plan';plan.hidden=true;plan.append(planText,cancel);actions.append(primary);
    root.classList.add('utxo-build-panel');root.append(heading,note,stage,plan,actions,metrics,el('p','Saved output values and exact scripts establish address membership at one UTXO checkpoint. Historical outputs are built separately over explicit creation ranges.','small muted'));
    let snapshot={},healthy=false,busy=false,reviewed=null,rebuildSnapshot=null;
    const currentModel=()=>model(snapshot,rebuildSnapshot);
    const requested=m=>request({rebuild:m.state==='rebuild_required'});
    const key=(m,body=requested(m))=>JSON.stringify([m.utxo_snapshot?.height,m.utxo_snapshot?.block_hash,m.utxo_snapshot?.commitment,m.rebuildSnapshot,body]);
    function render(value=snapshot,available=healthy){snapshot=value;healthy=available;if(rebuildSnapshot&&!currentModel().rebuildSnapshot)rebuildSnapshot=null;const m=currentModel(),active=['running','waiting','pausing'].includes(m.state)&&!!m.job;
      status.textContent=healthy?m.label:'Status unavailable';root.dataset.state=m.state;stage.textContent=!healthy?'Showing last known state. Controls are paused until status returns.':m.rebuildSnapshot?'An address lookup found damaged data in this saved checkpoint. Review a replacement from the verified UTXO source; prior state is preserved.':m.note||({dependency_missing:'Build the UTXO set first, then return here. Opening the dependency does not start a build.',unavailable:'The saved UTXO source is unavailable. Open the UTXO set to inspect or recover it.',stale:'The saved address snapshot differs from the UTXO checkpoint. Review a replacement build.',rebuild_required:'Saved address data needs a rebuild. Review a replacement from the verified UTXO source; prior state is preserved.',not_built:'Review a build from the saved UTXO checkpoint.',ready:'Address membership matches the saved UTXO checkpoint.',paused:'Paused. Resume the saved address build.',running:'Building address membership from saved UTXOs.',waiting:'Waiting for the required local state.'})[m.state]||'Inspect the saved state before continuing.';
      metrics.replaceChildren();for(const [label,value]of [['UTXO source height',number(m.utxo_snapshot?.height)],['UTXO source block',m.utxo_snapshot?.block_hash||'Unavailable'],['Address snapshot height',number(m.snapshot?.height)],['Indexed outputs',number(m.output_count)],['Outputs without a supported address',number(m.excluded_output_count)]])metrics.append(el('dt',label),el('dd',value));
      if(m.outputsChecked!==null)metrics.append(el('dt','Live outputs checked'),el('dd',number(m.outputsChecked)+(m.sourceOutputCount===null?'':' / '+number(m.sourceOutputCount))));
      if(reviewed&&(!m.sourceReady||reviewed.key!==key(m)))reviewed=null;plan.hidden=!reviewed;if(reviewed)planText.textContent=(reviewed.body.rebuild?'Rebuild from saved UTXO block ':'Build from saved UTXO block ')+number(m.utxo_snapshot?.height)+'. A queued build uses the latest complete saved UTXO checkpoint when it starts. '+(reviewed.body.rebuild?'Previous retained state is preserved until a verified replacement is ready. ':'')+(reviewed.plan.notes||[]).join(' ');
      primary.textContent=busy?'Working…':active?'Pause':!m.sourceReady||m.state==='dependency_missing'?'Open UTXO set':m.state==='paused'?'Resume':m.state==='ready'?'Up to date':m.state==='queued'?'Queued':reviewed?reviewed.body.rebuild?'Start reviewed rebuild':'Start reviewed build':m.state==='rebuild_required'?'Review rebuild':'Review build';
      primary.dataset.action=active?'pause':!m.sourceReady||m.state==='dependency_missing'?'dependency':m.state==='paused'?'resume':reviewed?'start':'review';primary.disabled=busy||!healthy||primary.dataset.action!=='dependency'&&['ready','pausing','queued'].includes(m.state)||!!snapshot.definitions?.find(d=>d.id==='address-state')?.locked;
    }
    cancel.onclick=()=>{reviewed=null;render();};
    primary.onclick=async()=>{if(busy||!healthy)return;const m=currentModel(),action=primary.dataset.action;if(action==='dependency'){select?.('utxo-state','build');return;}busy=true;render();try{
      if(action==='pause'){await api('pause',{index:'address-state'});reviewed=null;}
      else if(action==='resume'){if(m.job?.queue_managed&&m.job?.id)await api('queue',{id:m.job.id,action:'resume'});else if(m.job?.rebuild===true)throw Error('The saved rebuild queue entry is unavailable. Refresh before resuming.');else await api('build',request());}
      else if(action==='review'){const body=requested(m),reviewKey=key(m,body),plan=await api('plan',body);if(plan.to!==undefined&&plan.to!==null&&plan.to!==m.utxo_snapshot?.height){await refresh();throw Error('The saved UTXO checkpoint changed. Review the refreshed source.');}if(!currentModel().sourceReady||key(currentModel())!==reviewKey)throw Error('The UTXO checkpoint or rebuild requirement changed. Review the new source.');reviewed={key:reviewKey,body,plan};}
      else if(action==='start'){if(!reviewed||reviewed.key!==key(m)||!m.sourceReady)throw Error('Review the current UTXO checkpoint and rebuild request before starting.');await api('build',reviewed.body);reviewed=null;}
      if(action!=='review')await refresh();
    }catch(error){reviewed=null;notify?.(error.message,true);}finally{busy=false;render();}};
    return {render,health(value){healthy=value;render();},reviewRebuild(value){const hint=identity(value),m=model(snapshot,hint);if(!hint||!m.rebuildSnapshot){notify?.('The address checkpoint changed or its UTXO source is unavailable. Inspect the refreshed saved state before reviewing a rebuild.',true);return false;}rebuildSnapshot=hint;reviewed=null;render();return true;}};
  }
  return {model,request,mount};
})();
