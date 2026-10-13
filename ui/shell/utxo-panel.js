'use strict';
// The panel displays committed backend state. Missing measurements stay unknown.
window.GatewayUTXOPanel=(()=>{
  const height=n=>Number.isSafeInteger(n)&&n>=0;
  const number=n=>height(n)?n.toLocaleString('en-GB'):'Unavailable';
  const names={ready:'Ready',building:'Building',paused:'Paused',waiting:'Waiting for block',current:'Current',reconciling:'Reconciling',rebuild_required:'Rebuild required',pausing:'Pausing',queued:'Queued',error:'Needs attention'};
  const request=({rebuild=false}={})=>({index:'utxo-state',from:0,live:true,live_configured:true,retention:'pruned',...(rebuild?{rebuild:true}:{})});
  function model(snapshot){
    const u=snapshot.utxo||{},job=window.GatewayIndexCards.jobFor('utxo-state',snapshot);
    let state=String(u.state||'ready').replaceAll('-','_');
    if(state==='paused'&&job?.state==='pausing')state='pausing';
    // A job completing alone cannot establish an exact selected-chain state.
    const processed=height(u.processed_height)?u.processed_height:null,target=height(u.target_height)?u.target_height:null;
    const exact=processed!==null&&processed===target&&!!u.processed_hash&&u.processed_hash===u.target_hash;
    const safeState=state==='current'&&!exact?'reconciling':state;
    return {...u,job,processed,target,state:safeState,label:names[safeState]||'State unavailable',exact};
  }
  function mount({root,api,refresh,notify}){
    const doc=root.ownerDocument,el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const heading=el('div',undefined,'utxo-heading'),title=el('h2','UTXO set'),status=el('span','Ready','timeline-state');status.id='utxo-state';heading.append(title,status);
    const target=el('p','Build from genesis to the selected chain tip.','utxo-target');
    const progressHeading=el('div',undefined,'utxo-progress-heading'),position=el('span'),percent=el('span');position.id='utxo-position';progressHeading.append(position,percent);
    const progress=el('progress');progress.id='utxo-progress';progress.setAttribute('aria-label','Committed UTXO prefix');
    const stage=el('p','','utxo-stage');stage.id='utxo-stage';stage.setAttribute('role','status');
    const metrics=el('dl',undefined,'utxo-metrics');metrics.id='utxo-metrics';
    const storageActions=el('div',undefined,'actions'),storageButton=el('button','Refresh storage');storageButton.type='button';storageButton.id='utxo-storage-refresh';storageActions.append(storageButton);
    const storageNote=el('p','','utxo-stage');storageNote.id='utxo-storage-note';storageNote.setAttribute('role','status');
    const facts=el('dl',undefined,'utxo-policy');
    for(const [label,value]of [['Retention','Pruned'],['Required output facts','Values and exact ScriptPubKeys retained automatically'],['Raw block bodies','Separate cache and retention settings; bodies may be evicted after a durable commit'],['Full consensus validation','Deferred to v0.8; not performed in this release']])facts.append(el('dt',label),el('dd',value));
    const actions=el('div',undefined,'actions'),primary=el('button','Build UTXO set','primary');primary.type='button';primary.id='utxo-primary';actions.append(primary);
    const plan=el('section',undefined,'utxo-plan');plan.id='utxo-plan';plan.hidden=true;const planText=el('p'),cancel=el('button','Cancel review');cancel.type='button';cancel.addEventListener('click',()=>{reviewed=null;render();});plan.append(planText,cancel);
    const process=el('details');process.append(el('summary','Build process'));const steps=el('ol');for(const text of ['Read local blocks first; fetch missing blocks through your configured providers.','Process from genesis, with transactions in block order.','Commit live membership, exact output facts, the native reference bridge and self-contained undo together.','Advance from the last consistent committed head. Raw bodies can then follow your separate retention policy.'])steps.append(el('li',text));process.append(steps);
    const anchors=el('details');anchors.append(el('summary','Exact chain anchors'));const anchorRows=el('dl',undefined,'utxo-policy');anchors.append(anchorRows);
    root.classList.add('utxo-build-panel');root.append(heading,target,progressHeading,progress,stage,plan,actions,metrics,storageActions,storageNote,facts,process,anchors);
    let snapshot={},healthy=false,busy=false,reviewed=null,storageBusy=false,storageReport=null;
    function row(label,value){metrics.append(el('dt',label),el('dd',value));}
    function render(value=snapshot,available=healthy){snapshot=value;healthy=available;const m=model(snapshot),active=['building','waiting','pausing'].includes(m.state),job=m.job;root.dataset.state=m.state;status.textContent=healthy?(names[m.state]||'State unavailable'):'Status unavailable';
      target.textContent='From genesis · Selected target '+(m.target===null?'unavailable':'block '+number(m.target));
      position.textContent='Processed height '+(m.processed===null?'none yet':number(m.processed))+' / '+(m.target===null?'unknown target':number(m.target));
      progress.hidden=m.target===null;progress.max=(m.target??0)+1;progress.value=m.processed===null?0:Math.min(m.processed+1,progress.max);const ratio=progress.value/progress.max*100;percent.textContent=m.target===null?'':(m.processed===m.target?100:Math.min(99.9,ratio)).toFixed(1)+'%';
      const stages={ready:'Use local blocks first; fetch missing blocks.',building:'Processing in chain order. Progress advances after a durable commit.',paused:'Paused at the last consistent committed head. Resume from the next block.',waiting:'Waiting for '+(height(m.waiting_height)?'block '+number(m.waiting_height):'a source block')+' from your configured providers.',current:'The committed state matches the exact selected target tip.',reconciling:'Reconciling the committed state with the selected chain.',rebuild_required:'The selected chain cannot be recovered within the retained undo window. Rebuild is required.',pausing:'Pause requested. Waiting for the in-flight operation to finish.',queued:'Build queued. Saved state remains available.'};stage.textContent=healthy?(m.state==='building'&&m.stage==='maintenance'?'Reclaiming obsolete pages at the committed boundary. Pause remains available.':stages[m.state]||'Inspect local state before continuing.')+(m.error?' '+m.error:''):'Showing the last known committed state. Current chain status is unavailable; controls are paused.';
      metrics.replaceChildren();row('Live outputs',number(m.live_output_count));row('Live output value',m.live_value_sats===null||m.live_value_sats===undefined?'Unavailable':(height(m.live_value_sats)?number(m.live_value_sats):String(m.live_value_sats))+' sats');row('Measured UTXO storage',height(m.storage_bytes)?number(m.storage_bytes)+' bytes':'Unavailable');row('Recovery','Fixed 288 self-contained block transitions');row('Retained transitions',number(m.recovery_transitions));row('Recovery boundary',height(m.recovery_boundary_height)?'Block '+number(m.recovery_boundary_height):'Unavailable');
      storageButton.disabled=storageBusy||!healthy;storageButton.textContent=storageBusy?'Measuring storage…':'Refresh storage';
      storageNote.textContent=storageReport?.state==='partial'?'Partial inspection'+(storageReport.measured_at?' at '+storageReport.measured_at:'')+': at least '+number(storageReport.total_bytes)+' bytes. '+[...(storageReport.limitations||[]),...(storageReport.errors||[])].join(' '):m.storage_measured_at?'Measured '+m.storage_measured_at+'. Includes preserved rebuild archives and shared output pages counted once; raw blocks and optional TXIO-only history are excluded.':'Storage is measured on request. A new committed revision requires a fresh measurement.';
      if(storageReport?.state==='partial'&&storageReport.note)storageNote.textContent+=' '+storageReport.note;
      anchorRows.replaceChildren();for(const [label,value]of [['Processed block',m.processed_hash||'Unavailable'],['Selected target',m.target_hash||'Unavailable'],['Recovery boundary',m.recovery_boundary_hash||'Unavailable']])anchorRows.append(el('dt',label),el('dd',value));
      const rebuild=m.state==='rebuild_required',key=JSON.stringify([m.target,m.target_hash,rebuild]);if(reviewed&&reviewed.key!==key)reviewed=null;plan.hidden=!reviewed;
      if(reviewed)planText.textContent=(reviewed.rebuild?'Rebuild from genesis. Previous retained state is preserved until a verified replacement is available. ':'From genesis to the selected tip, then follow new blocks. ')+'Pruned raw storage; mandatory live and recovery facts remain local. '+(reviewed.plan.notes||[]).join(' ');
      primary.textContent=busy?'Working…':m.state==='current'?'Up to date':active?'Pause':m.state==='paused'?'Resume':m.state==='reconciling'?'Reconciling…':rebuild?reviewed?'Start reviewed rebuild':'Review rebuild':reviewed?'Start reviewed build':'Build UTXO set';
      primary.disabled=busy||!healthy||['current','reconciling','pausing','queued'].includes(m.state)||!active&&m.state!=='paused'&&m.target===null||!!snapshot.definitions?.find(d=>d.id==='utxo-state')?.locked;
      primary.dataset.action=active?'pause':m.state==='paused'?'resume':reviewed?'start':rebuild?'review_rebuild':'review';
    }
    primary.addEventListener('click',async()=>{if(primary.disabled)return;const action=primary.dataset.action,m=model(snapshot);busy=true;render();try{
      if(action==='pause')await api('pause',{index:'utxo-state'});
      else if(action==='resume'){if(m.job?.queue_managed&&m.job.id)await api('queue',{id:m.job.id,action:'resume'});else await api('build',request());}
      else if(action==='review'||action==='review_rebuild'){const rebuild=action==='review_rebuild',planResult=await api('plan',request({rebuild}));reviewed={plan:planResult,rebuild,key:JSON.stringify([m.target,m.target_hash,rebuild])};}
      else {const rebuilding=reviewed?.rebuild===true;await api('build',request({rebuild:rebuilding}));reviewed=null;}
      if(!['review','review_rebuild'].includes(action)){notify?.(action==='pause'?'UTXO pause requested.':action==='resume'?'UTXO resume requested.':'UTXO build started from genesis.');await refresh();}
    }catch(error){notify?.(error.message,true);}finally{busy=false;render();}});
    storageButton.addEventListener('click',async()=>{if(storageButton.disabled)return;storageBusy=true;render();try{storageReport=await api('storage?index=utxo-state');await refresh();notify?.(storageReport.state==='complete'?'UTXO storage measured.':'UTXO storage inspection is partial; the displayed total is a lower bound.');}catch(error){notify?.(error.message,true);}finally{storageBusy=false;render();}});
    return {render,health:available=>render(snapshot,available)};
  }
  return {model,request,mount};
})();
