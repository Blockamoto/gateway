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
    const heading=el('div',undefined,'utxo-heading'),label=el('span','','timeline-state');label.id='utxo-state';heading.append(el('h2','UTXO set'),label);
    const position=el('p','','utxo-target'),stage=el('p','','utxo-stage'),metrics=el('dl',undefined,'utxo-metrics'),progress=el('progress'),advanced=el('details'),rebuild=el('input'),rebuildLabel=el('label',undefined,'check'),storage=el('button','Inspect saved storage'),storageNote=el('p','','small muted');position.id='utxo-position';stage.id='utxo-stage';metrics.id='utxo-metrics';progress.id='utxo-progress';progress.setAttribute('aria-label','Committed UTXO prefix');stage.setAttribute('role','status');rebuild.type='checkbox';rebuild.id='utxo-rebuild-draft';rebuildLabel.append(rebuild,el('span','Prepare a replacement from genesis'));storage.type='button';storage.id='utxo-storage-refresh';storageNote.id='utxo-storage-note';
    advanced.append(el('summary','Advanced UTXO settings and evidence'),rebuildLabel,el('p','This stages recovery settings only. Arm the UTXO track and review the shared run from the timeline. The previous state stays preserved until a replacement is committed. Raw block retention follows the Raw blocks track.','small muted'),storage,storageNote);
    root.classList.add('utxo-build-panel');root.append(heading,position,progress,stage,metrics,advanced);let snapshot={},healthy=false,storageBusy=false;
    const stageOptions=()=>doc.dispatchEvent(new doc.defaultView.CustomEvent('gateway:timeline-options',{detail:{index:'utxo-state',options:{rebuild:rebuild.checked}}}));rebuild.onchange=stageOptions;
    function render(value=snapshot,available=healthy){snapshot=value;healthy=available;const m=model(snapshot);root.dataset.state=m.state;label.textContent=healthy?m.label:'Status unavailable';position.textContent='From genesis · processed '+(m.processed===null?'none':number(m.processed))+' / target '+number(m.target);progress.hidden=m.target===null;progress.max=(m.target??0)+1;progress.value=m.processed===null?0:Math.min(m.processed+1,progress.max);stage.textContent=!healthy?'Status unavailable; showing the last known saved checkpoint.':m.job?.error||m.error||(m.stage==='maintenance'?'Reclaiming obsolete pages.':null)||({ready:'Use the timeline to arm this track and prepare a shared run.',current:'Saved state matches the selected target.',paused:'Saved work is paused. Resume it from the timeline.',rebuild_required:'Recovery requires an explicitly reviewed replacement from genesis.',waiting:'Waiting for block '+number(m.waiting_height)+'.',building:'Processing in chain order.',reconciling:'Rechecking selected-chain evidence.'})[m.state]||'Inspect the committed state before continuing.';metrics.replaceChildren();for(const [name,value]of [['Live outputs',number(m.live_output_count)],['Live output value',height(m.live_value_sats)?number(m.live_value_sats)+' sats':'Unavailable'],['Measured UTXO storage',height(m.storage_bytes)?number(m.storage_bytes)+' bytes':'Unavailable'],['Recovery','Fixed 288 self-contained transitions'],['Retained transitions',number(m.recovery_transitions)],['Recovery boundary',number(m.recovery_boundary_height)],['Processed block',m.processed_hash||'Unavailable']])metrics.append(el('dt',name),el('dd',value));rebuild.disabled=!healthy;storage.disabled=!healthy||storageBusy;}
    storage.onclick=async()=>{if(storage.disabled)return;storageBusy=true;render();try{const result=await api('storage?index=utxo-state');storageNote.textContent=(result.state==='partial'?'Partial inspection: at least ':'Measured ')+number(result.total_bytes)+' bytes'+(result.measured_at?' · '+result.measured_at:'')+'. '+(result.note||'Includes saved pages and preserved rebuild archives.')+(result.errors?.length?' '+result.errors.join(' '):'');await refresh?.();}catch(error){notify?.(error.message,true);}finally{storageBusy=false;render();}};
    return {render,health:value=>render(snapshot,value)};
  }
  return {model,request,mount};
})();
