'use strict';
window.GatewayIndexTransport = (() => {
  const integer = n => Number.isSafeInteger(n) && n >= 0;
  const hash = s => typeof s === 'string' && /^[0-9a-f]{64}$/i.test(s);
  const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));
  const outputPoint = text => {
    const match = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\.bitcoin)?$/.exec(String(text || ''));
    if (!match) return null;
    const values = match.slice(1).map(Number);
    return values.every(integer) ? {outputIndex:values[0],txIndex:values[1],height:values[2]} : null;
  };
  function point(value) {
    if (!value || typeof value !== 'object') return null;
    if (value.resource === 'address' || value.kind === 'address') return null;
    if (value.resource === 'sat' || value.kind === 'sat') {
      const location = value.location, pos = outputPoint(location?.output_coordinate);
      if (!pos || !integer(location?.offset) || !value.snapshot || !['unspent','currently_unspent','genesis_unspendable'].includes(location.state)) return null;
      return {...pos,kind:'satpoint',satOffset:location.offset,sat_number:value.sat_number,
        address:location.offset+'.'+location.output_coordinate+'.bitcoin',label:integer(value.sat_number)?value.sat_number+'.sat':location.output_coordinate,
        block_hash:hash(location.block_hash)?location.block_hash:null,snapshot:copy(value.snapshot)};
    }
    if (!integer(value.height)) return null;
    const result = {kind:value.kind || 'block',height:value.height,address:value.address || value.height+'.bitcoin'};
    for (const [key,alias] of [['txIndex','tx_index'],['inputIndex','input_index'],['outputIndex','output_index'],['satOffset','sat_offset']]) {
      const n = value[key] ?? value[alias];
      if (integer(n)) result[key] = n;
    }
    const fields={block:[],transaction:['txIndex'],input:['txIndex','inputIndex'],output:['txIndex','outputIndex'],satpoint:['txIndex','outputIndex','satOffset']}[result.kind];
    if(!fields||fields.some(key=>!integer(result[key])))return null;
    if (hash(value.block_hash || value.metadata?.block_hash)) result.block_hash = value.block_hash || value.metadata.block_hash;
    if (value.snapshot) result.snapshot = copy(value.snapshot);
    return result;
  }
  function compare(a,b) {
    if (!a || !b) return null;
    if (a.height !== b.height) return Math.sign(a.height-b.height);
    if (hash(a.block_hash) && hash(b.block_hash) && a.block_hash !== b.block_hash) return null;
    if (integer(a.txIndex) && integer(b.txIndex) && a.txIndex !== b.txIndex) return Math.sign(a.txIndex-b.txIndex);
    if ((integer(a.inputIndex) && integer(b.outputIndex)) || (integer(a.outputIndex) && integer(b.inputIndex))) return null;
    for (const field of ['inputIndex','outputIndex','satOffset']) if (integer(a[field]) && integer(b[field]) && a[field] !== b[field]) return Math.sign(a[field]-b[field]);
    return 0;
  }
  function project(marks) {
    const first=marks?.in, last=marks?.out, order=first&&last?compare(first,last):0;
    if (first&&last&&order===null) return {valid:false,reason:'The marked positions belong to incompatible coordinate or block contexts.'};
    if (first&&last&&order>0) return {valid:false,reason:'In must precede Out. Clear or move a boundary.'};
    return {valid:true,in:first?.height??null,out:last?.height??null,
      ...(hash(first?.block_hash)?{in_hash:first.block_hash}:{}),...(hash(last?.block_hash)?{out_hash:last.block_hash}:{})};
  }
  const owner = id => ['txio-links','txio-values','txio-scripts'].includes(id)?'txo-spender':id==='txio-sat-ranges'?'sat-state':id;
  function mount({document:doc,root,timeline,api,refresh,notify,getSnapshot,inspect}) {
    const win=doc.defaultView, el=(tag,text,cls)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;};
    const toolbar=el('section',undefined,'timeline-transport');toolbar.id='timeline-transport';toolbar.setAttribute('aria-label','Indexing transport');
    const actions=el('div',undefined,'timeline-transport-actions'),navigation=el('div',undefined,'timeline-step-controls'),markActions=el('div',undefined,'timeline-mark-controls');
    const button=(text,title,action)=>{const b=el('button',text);b.type='button';b.title=title;b.setAttribute('aria-label',title);b.onclick=action;return b;};
    let consumers=new Map(),marks={in:null,out:null},lastPoint=null,hasResource=false,snapshot={},runState=null,initialized=false,dirty=false,busy=false,review=null,feedback='',handles=true,saveVersion=0,savedVersion=0,saveTail=Promise.resolve(),dead=false;
    const start=button('● Start indexing','Review the armed indexes before starting',()=>act('start')),
      pause=button('Ⅱ Pause','Pause the active indexing run',()=>act('pause')),
      resume=button('▶ Resume','Resume the saved indexing run',()=>act('resume')),
      stop=button('■ Stop','Cancel the run while retaining committed data',()=>act('cancel'));
    start.id='timeline-run-start';pause.id='timeline-run-pause';resume.id='timeline-run-resume';stop.id='timeline-run-stop';actions.append(start,pause,resume,stop);
    const steps=[[-10,'−10','Skip ten'],[-1,'‹','Previous'],[1,'›','Next'],[10,'+10','Skip ten']];
    for(const [amount,label,title]of steps){const b=button(label,title,()=>timeline.step(amount));b.dataset.step=String(amount);navigation.append(b);}
    navigation.append(button('↑','Return to parent',()=>timeline.ascend()),button('↓','Explore focused item',()=>timeline.zoomIntoFocus()));
    const markIn=button('I · In','Set In from the focused position',()=>setMark('in')),markOut=button('O · Out','Set Out from the focused position',()=>setMark('out')),
      showHandles=button('↔ Handles','Show or hide global In and Out handles',()=>{handles=!handles;paint();}),clear=button('Clear range','Clear global In and Out without changing saved work',()=>{marks={in:null,out:null};changed();});
    const clearIn=button('Clear In','Clear only the global In point',()=>{marks.in=null;changed();}),clearOut=button('Clear Out','Clear only the global Out point',()=>{marks.out=null;changed();});
    markIn.id='timeline-mark-in';markOut.id='timeline-mark-out';showHandles.id='timeline-range-handles';clear.id='timeline-clear-range';clearIn.id='timeline-clear-in';clearOut.id='timeline-clear-out';markActions.append(markIn,markOut,showHandles,clearIn,clearOut,clear);
    const summary=el('p','','timeline-global-range'),state=el('p','','timeline-run-state'),reviewBox=el('div',undefined,'timeline-run-review');summary.id='timeline-global-range';state.id='timeline-run-state';state.setAttribute('role','status');reviewBox.id='timeline-run-review';reviewBox.hidden=true;
    toolbar.append(actions,navigation,markActions,summary,state,reviewBox);root.querySelector('.timeline-editor').prepend(toolbar);
    const layers=new Map();
    function name(id){const d=(snapshot.definitions||[]).find(d=>d.id===owner(id));return d?.name||({'sat-state':'Sat ranges','txo-spender':'TXIO','bitmap-districts':'Districts','bitmap-parcels':'Parcels','bitmap-chain':'Bitmap Chain'})[id]||id;}
    function draftConsumer(id){id=owner(id);if(!consumers.has(id)){const instance=(snapshot.instances||[]).find(i=>i.definition===id),policy=(snapshot.live||[]).find(p=>p.index===id),c={index:id,armed:false,live:false};if(instance?.mode||policy?.mode)c.mode=instance?.mode||policy.mode;for(const key of (id==='txo-spender'?['retain_output_values','retain_output_scripts']:id==='sat-state'?['retain_sat_history']:id==='inscriptions'?['conventional_ids']:[])){const value=policy?.[key]??instance?.[key]??(id==='sat-state'&&key==='retain_sat_history'?snapshot.sats?.retain_sat_history:undefined);if(value!==undefined)c[key]=value;}consumers.set(id,c);}return consumers.get(id);}
    function request(){const range=project(marks);if(!range.valid)throw Error(range.reason);const {valid,...bounds}=range;return {...bounds,consumers:[...consumers.values()].filter(c=>(snapshot.definitions||[]).some(d=>d.id===c.index&&d.buildable&&!d.locked)).map(c=>{const row={...c};delete row.retention;return row;})};}
    function persistMarks(){try{win.localStorage.setItem('gateway.timeline.global-range.v1',JSON.stringify(marks));}catch{}}
    try {const saved=JSON.parse(win.localStorage.getItem('gateway.timeline.global-range.v1')||'null');if(saved)marks={in:point(saved.in),out:point(saved.out)};}catch{}
    function queueSave(){const version=++saveVersion;saveTail=saveTail.catch(()=>{}).then(async()=>{if(dead||version!==saveVersion)return;let body;try{body=request();}catch{return;}try{await api('run/prepare',body);savedVersion=version;if(version===saveVersion)dirty=false;}catch(error){if(!dead&&version===saveVersion)state.textContent='Could not save the prepared controls: '+error.message;}});return saveTail;}
    function changed(){dirty=true;review=null;feedback='';reviewBox.hidden=true;persistMarks();queueSave();timeline.redraw();update(snapshot);}
    function setConsumerOptions(id,patch={}){const c=draftConsumer(id);for(const key of ['armed','live','mode','retain_output_values','retain_output_scripts','retain_sat_history','conventional_ids','rebuild','rebuild_snapshot'])if(Object.hasOwn(patch,key))c[key]=copy(patch[key]);changed();return copy(c);}
    function currentPoint(){return hasResource?lastPoint:point(timeline.getNavigationPoint());}
    function setMark(edge,value=currentPoint()){const p=point(value);if(!p){notify?.('This resource has no established timeline position. Select a known block or output location first.',true);return false;}const candidate={...marks,[edge]:p},range=project(candidate);if(!range.valid){notify?.(range.reason,true);return false;}marks=candidate;changed();return true;}
    function acceptGlobalResource(value){hasResource=true;lastPoint=point(value);paint();return lastPoint;}
    function setGlobalRange(from,to){if(!integer(from)||!integer(to)||to<from)return;marks={in:point({height:from,kind:'block'}),out:point({height:to,kind:'block'})};changed();}
    function decorate(){
      for(const row of root.querySelectorAll('.timeline-track')){
        const id=row.dataset.index,definition=(snapshot.definitions||[]).find(d=>d.id===owner(id));
        if(!definition||definition.locked)continue;
        let group=row.querySelector('.timeline-track-controls');
        if(!group){
          group=el('div',undefined,'timeline-track-controls');row.querySelector('.timeline-track-heading').append(group);
          if(id==='headers'){
            const sync=button('Sync','Pause or resume header synchronization',async()=>{if(busy)return;busy=true;try{const response=await win.fetch('/api/v1/headers',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:snapshot.timeline?.header_state==='paused'?'resume':'pause'})}),result=await response.json();if(!response.ok)throw Error(result.error||'Header synchronization could not be changed.');await refresh?.();}catch(error){notify?.(error.message,true);}finally{busy=false;update(snapshot);}});sync.dataset.headerSync='true';group.append(sync);
          }else{
            for(const type of ['arm','live']){const label=el('label',undefined,'timeline-track-switch'),input=el('input');input.type='checkbox';input.dataset.control=type;input.setAttribute('aria-label',name(id)+' '+(type==='arm'?'Arm':'Live'));label.append(input,el('span',type==='arm'?'Arm':'Live'));group.append(label);input.onchange=()=>{const c=draftConsumer(id);if(type==='live')c.live=input.checked;else if(id==='txio-values'||id==='txio-scripts'){c[id==='txio-values'?'retain_output_values':'retain_output_scripts']=input.checked;if(input.checked)c.armed=true;}else c.armed=input.checked;changed();};}
          }
          const progress=el('span','','timeline-consumer-state');progress.setAttribute('role','status');group.append(progress);
        }
        const c=draftConsumer(id),arm=group.querySelector('[data-control=arm]'),live=group.querySelector('[data-control=live]');
        const sync=group.querySelector('[data-header-sync]');if(sync){const paused=snapshot.timeline?.header_state==='paused';sync.textContent=paused?'Resume sync':'Pause sync';sync.disabled=busy;sync.setAttribute('aria-label',paused?'Resume header synchronization':'Pause header synchronization');}
        if(arm){arm.checked=!!c.armed&&(id==='txio-values'?!!c.retain_output_values:id==='txio-scripts'?!!c.retain_output_scripts:true);arm.disabled=busy;}
        if(live){live.checked=!!c.live;live.disabled=busy||id==='txio-values'||id==='txio-scripts';live.title=live.disabled?'Live follows the shared TXIO consumer':'Follow new blocks after catch-up; a marked Out caps following';}
        const progress=(runState?.consumers||[]).find(p=>p.index===owner(id)),admitted=runState?.request?.consumers?.find(p=>p.index===owner(id)),active=['running','waiting','pausing','paused'].includes(runState?.state),pending=active&&admitted&&['armed','live','mode','retain_output_values','retain_output_scripts','retain_sat_history','conventional_ids','rebuild','rebuild_snapshot'].some(key=>c[key]!==undefined&&JSON.stringify(c[key])!==JSON.stringify(admitted[key])),note=group.querySelector('.timeline-consumer-state');note.textContent=id==='headers'?'Foundation':(progress?String(progress.state||'').replaceAll('_',' ')+(progress.height>=0?' · '+progress.height.toLocaleString('en-GB'):''):c.armed?'Armed':'Off')+(pending?(!c.armed?' · Off requested':admitted.live&&!c.live?' · Live off':' · Pending review'):'');note.title=(progress?.reason||progress?.error||'')+(pending?' Choices changed since admission. Off and Live off constrain the active run; other changes require a new reviewed Start.':'');
        if(id==='blocks')group.title='Arm to retain and index newly fetched raw blocks. Off uses new bytes temporarily; existing data is kept.';
      }
      for(const group of root.querySelectorAll('.timeline-group')){
        const rows=[...root.querySelectorAll('.timeline-track')].filter(row=>row.dataset.group===group.dataset.group&&row.querySelector('[data-control=arm]'));
        if(!rows.length)continue;let label=group.querySelector('.timeline-group-arm');
        if(!label){label=el('label',undefined,'timeline-group-arm timeline-track-switch');const input=el('input');input.type='checkbox';input.setAttribute('aria-label','Arm all '+group.dataset.group+' layers');label.append(input,el('span','Arm layers'));group.querySelector('.timeline-group-heading').append(label);input.onchange=()=>{for(const row of rows){const id=row.dataset.index,c=draftConsumer(id);c.armed=input.checked;if(id==='txio-values')c.retain_output_values=input.checked;if(id==='txio-scripts')c.retain_output_scripts=input.checked;}changed();};}
        const input=label.querySelector('input'),selected=rows.filter(row=>row.querySelector('[data-control=arm]').checked).length;input.checked=selected===rows.length;input.indeterminate=selected>0&&selected<rows.length;input.disabled=busy;
      }
    }
    function update(value=getSnapshot?.()||snapshot){
      snapshot=value||{};const transport=snapshot.run;runState=transport?.run||null;
      if(!initialized&&transport?.prepared){for(const c of transport.prepared.consumers||[])consumers.set(c.index,{...c});if(!marks.in&&integer(transport.prepared.in))marks.in=point({height:transport.prepared.in,kind:'block'});if(!marks.out&&integer(transport.prepared.out))marks.out=point({height:transport.prepared.out,kind:'block'});initialized=true;}
      for(const d of snapshot.definitions||[])if(d.buildable&&!d.locked)draftConsumer(d.id);
      const running=['running','waiting','pausing','queued'].includes(runState?.state),paused=runState?.state==='paused';
      start.disabled=busy||running||root.dataset.healthy==='false'||!!transport?.error;start.textContent=review?'● Start reviewed indexing':'● Start indexing';pause.hidden=!running;resume.hidden=!paused;stop.hidden=!running&&!paused;for(const b of [pause,resume,stop])b.disabled=busy||!!transport?.error;
      if(!busy)state.textContent=transport?.error||feedback||(runState?String(runState.state||'').replaceAll('_',' ')+(runState.reason?' · '+runState.reason:'')+(dirty?' · Unsaved changes':saveVersion>savedVersion?' · Saving choices':''):'Arm tracks, set the global range, then start indexing.');
      decorate();paint();
    }
    function renderReview(plan){
      reviewBox.replaceChildren();reviewBox.hidden=false;reviewBox.append(el('strong',plan.ready?'Review shared indexing run':'Some selected indexes need attention'));
      for(const c of plan.consumers||[]){const item=el('div',undefined,'timeline-run-plan-consumer');item.append(el('strong',name(c.index)),el('span',' · '+String(c.state||'ready').replaceAll('_',' ')+' · '+(c.reason||'')));for(const dep of c.dependencies||[]){const line=el('p',name(dep.index)+' · '+(dep.kind||'dependency')+' · '+String(dep.state||'unknown').replaceAll('_',' ')+' · '+(dep.reason||''),'small');if(dep.index){line.append(button('Inspect','Inspect '+name(dep.index),()=>inspect?.(dep.index)));if(dep.kind==='state'&&['missing','needs_prerequisite'].includes(dep.state)&&!draftConsumer(dep.index).armed)line.append(button('Include in run','Include '+name(dep.index)+' in the prepared selection',()=>setConsumerOptions(dep.index,{armed:true})));}item.append(line);}if(c.requires_lead_in)item.append(button('Use genesis In','Set the shared In to block zero and review again',()=>setMark('in',{kind:'block',height:0,address:'0.bitcoin'})));reviewBox.append(item);}
      for(const note of plan.notes||[])reviewBox.append(el('p',note,'small'));
      reviewBox.append(el('p','New raw blocks: '+(draftConsumer('blocks').armed?'retain and index':'temporary, then discard')+'. Saved output facts and recovery data remain independent.','small'));
    }
    async function act(action){
      if(busy)return;busy=true;feedback='';update(snapshot);
      try{
        await saveTail;const body=request();
        if(action==='start'){
          if(!body.consumers.some(c=>c.armed))throw Error('Arm at least one index before starting.');
          if(review){await api('run/start',{request:body,plan_id:review.plan_id});review=null;reviewBox.hidden=true;await refresh?.();}
          else {const plan=await api('run/plan',body);renderReview(plan);if(plan.ready)review=plan;feedback=plan.ready?'Review the dependencies and range, then press Start reviewed indexing.':'Resolve the listed prerequisites and review again.';}
        }else{if(!runState?.id)throw Error('There is no saved coordinated run to control.');await api('run/control',{action,id:runState.id});review=null;reviewBox.hidden=true;await refresh?.();}
      }catch(error){review=null;feedback=error.message;notify?.(error.message,true);}finally{busy=false;update(snapshot);}
    }
    function positionOf(p,nav,edge){if(!p)return null;const parent=nav.parent;if(!parent)return p.height;if(p.height!==parent.height)return null;if(parent.kind==='block')return p.txIndex??(edge==='out'?nav.maximum:0);if(p.txIndex!==parent.txIndex)return null;if(parent.kind==='transaction')return p.outputIndex??(edge==='out'?nav.maximum:0);if(p.outputIndex!==parent.outputIndex)return null;return p.satOffset??(edge==='out'?nav.maximum:0);}
    function paint(){
      const projection=project(marks);summary.textContent=(marks.in?'In '+(marks.in.label||marks.in.address):'In: resume / required start')+' · '+(marks.out?'Out '+(marks.out.label||marks.out.address):'Out: selected tip'+([...consumers.values()].some(c=>c.armed&&c.live)?', then Live':''))+(projection.valid&&(marks.in||marks.out)?' · Complete boundary blocks '+(projection.in??'resume')+'–'+(projection.out??'tip'):'')+(!projection.valid?' · '+projection.reason:'');
      showHandles.setAttribute('aria-pressed',String(handles));
      clearIn.disabled=!marks.in;clearOut.disabled=!marks.out;clear.disabled=!marks.in&&!marks.out;
      const nav=timeline.getNavigationState();for(const b of navigation.querySelectorAll('[data-step]')){const amount=Number(b.dataset.step),title=(Math.abs(amount)===1?(amount<0?'Previous ':'Next '):(amount<0?'Skip back ten ':'Skip forward ten '))+nav.unit;b.title=title;b.setAttribute('aria-label',title);b.disabled=!nav.known||(amount<0?nav.position<=0:nav.position>=nav.maximum);}
      for(const host of [root.querySelector('#timeline-ruler'),root.querySelector('#timeline-compact-scrubber')?.parentElement].filter(Boolean)){
        let overlay=layers.get(host);if(!overlay){overlay=el('div',undefined,'timeline-global-markers');layers.set(host,overlay);}if(overlay.parentElement!==host)host.append(overlay);const focusedEdge=overlay.contains(doc.activeElement)?doc.activeElement.dataset.edge:null;overlay.hidden=!handles;overlay.replaceChildren();
        const canvas=host.matches('#timeline-ruler')?host:host.querySelector('#timeline-compact-scrubber'),rect=canvas?.getBoundingClientRect();if(!rect||rect.width<1)continue;const hostRect=host.getBoundingClientRect(),v=timeline.getView(),span=v.to-v.from+1;overlay.style.left=(rect.left-hostRect.left)+'px';overlay.style.width=rect.width+'px';
        for(const edge of ['in','out']){const at=positionOf(marks[edge],nav,edge);if(!integer(at)||at<v.from||at>v.to)continue;const handle=button(edge==='in'?'I':'O',edge==='in'?'Global In point':'Global Out point',()=>{});handle.className='timeline-global-handle '+edge;handle.dataset.edge=edge;handle.style.left=((at-v.from+.5)/span*100)+'%';handle.setAttribute('role','slider');handle.setAttribute('aria-valuemin','0');handle.setAttribute('aria-valuemax',String(nav.maximum));handle.setAttribute('aria-valuenow',String(at));handle.setAttribute('aria-label',(edge==='in'?'In: ':'Out: ')+(marks[edge].label||marks[edge].address));
          handle.oncontextmenu=event=>{event.preventDefault();event.stopPropagation();marks[edge]=null;changed();};handle.onkeydown=event=>{if(['Delete','Backspace'].includes(event.key)){event.preventDefault();event.stopPropagation();marks[edge]=null;changed();return;}if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;event.preventDefault();event.stopPropagation();const n=event.key==='Home'?0:event.key==='End'?nav.maximum:Math.max(0,Math.min(nav.maximum,at+(event.key==='ArrowLeft'?-1:1)));setMark(edge,timeline.pointAt(n));};
          handle.onpointerdown=event=>{if(event.button!==0)return;event.preventDefault();event.stopPropagation();const target=host,pointer=event.pointerId,initial=copy(marks),move=e=>{if(e.pointerId!==pointer)return;const n=Math.max(v.from,Math.min(v.to,v.from+Math.floor((e.clientX-rect.left)/Math.max(1,rect.width)*span))),p=point(timeline.pointAt(n));if(!p)return;const next={...marks,[edge]:p};if(project(next).valid){marks=next;paint();}},end=e=>{if(e.pointerId!==pointer)return;target.removeEventListener('pointermove',move);target.removeEventListener('pointerup',end);target.removeEventListener('pointercancel',cancel);try{target.releasePointerCapture(pointer);}catch{}changed();},cancel=e=>{marks=initial;end(e);};target.addEventListener('pointermove',move);target.addEventListener('pointerup',end);target.addEventListener('pointercancel',cancel);try{target.setPointerCapture(pointer);}catch{}};
          handle.addEventListener('click',event=>event.stopPropagation());overlay.append(handle);if(focusedEdge===edge)handle.focus({preventScroll:true});
        }
      }
    }
    function shortcut(event){if(event.defaultPrevented||event.isComposing||event.repeat||event.ctrlKey||event.metaKey||event.altKey)return;const target=event.target;if(target?.closest?.('input,textarea,select,[contenteditable]:not([contenteditable="false"]),dialog,[role="dialog"],[role="menu"]'))return;if(!root.contains(target)&&target!==doc.body)return;const key=String(event.key).toLowerCase();if(key!=='i'&&key!=='o')return;event.preventDefault();event.stopPropagation();setMark(key==='i'?'in':'out');}
    doc.addEventListener('keydown',shortcut,true);
    doc.addEventListener('gateway:timeline-options',event=>{if(event.detail?.index)setConsumerOptions(event.detail.index,event.detail.options||{});});
    const result={update,paint,getRunDraft:()=>copy(request()),setConsumerOptions,acceptGlobalResource,setGlobalRange,setMark,getGlobalMarks:()=>copy(marks),getGlobalRanges:()=>{const r=project(marks);return r.valid&&integer(r.in)&&integer(r.out)?[{from:r.in,to:r.out}]:[];},inspectIndex:(id,options)=>inspect?.(id,options),teardown(){dead=true;doc.removeEventListener('keydown',shortcut,true);toolbar.remove();for(const layer of layers.values())layer.remove();}};
    update(getSnapshot?.()||{});return result;
  }
  return {point,compare,project,owner,mount};
})();
