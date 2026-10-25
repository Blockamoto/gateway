'use strict';
// Fixed Bitmap rules are read-only. The shared timeline owns execution.
window.GatewayBitmapWorkspace = (() => {
  const known = n => Number.isSafeInteger(n) && n >= 0;
  const number = n => known(n) ? n.toLocaleString('en-GB') : 'Not established';
  const kindOf = value => value?.kind || (/^bitmap-parcels(?:-|$)/.test(value?.id || '') ? 'parcel' : /^bitmap-(?:district|parcel)-chain-dev-v1$|^bitmap-chain(?:-|$)/.test(value?.id || '') ? 'chain' : 'district');
  const instanceID = value => typeof value === 'string' && /^(?:bitmap-(?:districts|parcels)(?:-canonical(?:-v2)?|-standard-v1|-[0-9a-f]{32})?|bitmap-chain(?:-[0-9a-f]{32})?|bitmap-(?:district|parcel)-chain-dev-v1)$/.test(value);
  const answerBits = (ids, values) => ids.map(id => values[id]).join('').replace(/0+$/, '');
  const customInstance = id => /^bitmap-(?:districts|parcels|chain)-[0-9a-f]{32}$/.test(id || '');
  const capabilities = value => value?.capabilities || value?.bitmap_capabilities || value?.bitmap?.capabilities || {};
  const featureEnabled = (value, feature) => capabilities(value)[feature] === true;
  const deferredReason = 'Legacy experimental definitions and saved results are available for inspection and deletion. Current District and Parcel indexes use fixed canonical rules. Bitmap Chain methodology is being developed.';
  function address(target) {
    const p = new URLSearchParams();
    if (instanceID(target.instance)) p.set('instance', target.instance);
    return (target.kind === 'chain' ? target.height + '.bitmapchain' : target.kind === 'parcel' ? target.transaction + '.' + target.district + '.bitmap' : target.district + '.bitmap') + (p.size ? '?' + p : '');
  }
  function helpers(root) {
    const doc = root.ownerDocument;
    const el = (tag, text, cls) => { const node = doc.createElement(tag); if (text !== undefined) node.textContent = String(text); if (cls) node.className = cls; return node; };
    const button = (text, action, cls) => { const node = el('button', text, cls); node.type = 'button'; node.onclick = action; return node; };
    const field = (text, input) => { const label = el('label', undefined, 'bitmap-field'); label.append(el('span', text), input); return label; };
    const select = (label, rows) => { const node = el('select'); node.setAttribute('aria-label', label); for (const [value, text] of rows) { const option = el('option', text); option.value = value; node.append(option); } return node; };
    return {doc, el, button, field, select};
  }
  function mountDelete({root,index,indices,name,api,onDeleted,onInspect,getRange,receipt,onReceipt,autoOpen=false}) {
    const {el,button}=helpers(root);let dead=false,serial=0,busy=false,deleting=false;
    const selected=[...new Set(indices?.length?indices:[index])].filter(Boolean);
    const bin=button('',review,'index-delete-button');bin.title='Review deletion of '+name;bin.setAttribute('aria-label',bin.title);bin.innerHTML='<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 6h16M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7m4-7v7"/></svg>';
    const result=el('section',undefined,'index-delete-receipt');result.setAttribute('role','status');result.hidden=true;
    const panel=el('section',undefined,'panel index-delete-review');panel.hidden=true;panel.setAttribute('aria-label','Review index deletion');root.replaceChildren(bin,result,panel);
    function showReceipt(value){if(!value)return;result.hidden=false;result.replaceChildren(el('strong',value.failed?'Deletion did not complete':value.cleanup_pending?'Index data detached; cleanup pending':'Index deletion complete'),el('p',value.note||'The selected owned scope was removed. Independent sources are preserved.','small'));for(const item of value.items||[]){result.append(el('p',(item.name||item.index)+': '+(item.note||item.status||item.operation||'Processed'),'small'));if(item.range)result.append(el('p','Applied inclusive blocks '+item.range.from+'–'+item.range.to,'small'));rawInventory(item.raw,result);}}
    const list=(holder,title,values,links=false)=>{if(!values?.length)return;holder.append(el('h4',title));const ul=el('ul');for(const value of values){const li=el('li',typeof value==='string'?value:[value.name||value.index,value.reason||value.note].filter(Boolean).join(' · '));if(links&&value.index){li.append(button('Open '+(value.name||value.index),()=>onInspect?.(value.index),'text-button'));if(!selected.includes(value.index))li.append(button('Include '+(value.name||value.index),()=>{selected.push(value.index);review();},'text-button'));}ul.append(li);}holder.append(ul);};
    function rawInventory(value,holder){if(!value)return;const rows=[['Removable owned files',value.removable_files],['Removable owned bytes',value.removable_bytes],['Preserved files',value.retained_files],['Preserved bytes',value.retained_bytes]];const dl=el('dl',undefined,'kv');for(const[label,count]of rows)if(known(count))dl.append(el('dt',label),el('dd',number(count)));holder.append(dl);list(holder,'Preserved source reasons',Array.isArray(value.retained_reasons)?value.retained_reasons:[]);}
    function scopeItem(value,holder){rawInventory(value.raw,holder);if(value.range)holder.append(el('p','Exact inclusive blocks '+(value.range.from??'saved start')+'–'+(value.range.to??'saved end'),'small'));list(holder,'Will be removed',value.scope);list(holder,'Will be kept',value.retained);list(holder,'Dependent saved indexes',value.dependencies,true);list(holder,'Cannot delete yet',value.blockers);list(holder,'Available from independent sources',value.available_sources||value.sources);if(value.operation==='remove_track')holder.append(el('p','Whole deletion removes this custom track definition and its reviewed owned data.','small'));if(value.exists===false&&value.noop!==false)holder.append(el('p',value.operation==='remove_track'?'No owned saved data exists; the selected custom track definition will still be removed.':value.index==='blocks'?'No owned Raw blocks store exists in this scope. Green timeline coverage may still come from shared index sources, cache or external providers; those are preserved.':'No owned saved index data exists in this scope. The track definition remains available.','small'));}
    showReceipt(receipt);if(receipt?.items?.some(item=>item.operation==='remove_track'))bin.hidden=true;
    async function review(){if(busy)return;busy=true;bin.disabled=true;panel.hidden=false;panel.replaceChildren(el('p','Checking the exact saved-data scope…','small'));const ticket=++serial;try{const range=getRange?.(),query=new URLSearchParams();for(const id of selected)query.append('index',id);if(range?.from!==undefined)query.set('from',String(range.from));if(range?.to!==undefined)query.set('to',String(range.to));const value=await api('/api/v1/index/delete?'+query);if(dead||ticket!==serial)return;
      panel.replaceChildren(el('h3','Delete '+(selected.length>1?selected.length+' selected indexes':value.name||name)+' index data?'),el('p',value.note||'Only the reviewed scope is removed. Custom track removal and retained independent sources are listed below.','small'));
      const items=value.items?.length?value.items:[value];if(items.length>1){for(const item of items){const section=el('section');section.append(el('h4',item.name||item.index));scopeItem(item,section);panel.append(section);}list(panel,'Cannot delete yet',value.blockers);}else scopeItem(items[0],panel);
      const actions=el('div',undefined,'actions');actions.append(button('Keep index',()=>{serial++;panel.hidden=true;bin.focus();}));
      if(value.ready&&value.review_id&&items.some(item=>item.operation==='remove_track'||item.noop===false||item.noop===undefined&&item.exists!==false)){const confirm=button('Delete selected scope',async()=>{if(busy)return;busy=true;deleting=true;confirm.disabled=true;try{const removed=await api('/api/v1/index/delete',{indices:selected,...(range?{range}:{}),review_id:value.review_id});if(dead||ticket!==serial)return;showReceipt(removed);onReceipt?.(removed);panel.hidden=true;await onDeleted?.(selected,removed);}catch(error){if(!dead&&ticket===serial){const failed={failed:true,indices:[...selected],...(range?{range}:{}),note:error.message+' Review the exact scope again before retrying.'};showReceipt(failed);onReceipt?.(failed);panel.append(el('p',failed.note,'small bitmap-validation-issue'));confirm.disabled=true;}}finally{busy=false;deleting=false;}});confirm.className='danger';actions.append(confirm);}panel.append(actions);
    }catch(error){if(!dead&&ticket===serial)panel.replaceChildren(el('p',error.message,'small bitmap-validation-issue'));}finally{if(!dead){busy=false;bin.disabled=false;}}}
    const rangeChanged=()=>{if(panel.hidden)return;if(deleting){if(!panel.querySelector('.delete-range-pending'))panel.append(el('p','Deletion was already submitted for its reviewed scope. New marks apply only to later actions.','small delete-range-pending'));return;}serial++;panel.replaceChildren(el('p','The global In/Out scope changed. Review the new exact deletion scope before confirming.','small'));};root.ownerDocument.addEventListener('gateway:timeline-range-changed',rangeChanged);
    if(autoOpen)Promise.resolve().then(review);
    return {review,teardown(){dead=true;serial++;root.ownerDocument.removeEventListener('gateway:timeline-range-changed',rangeChanged);root.replaceChildren();}};
  }
  function metadata(holder, rows, el) {
    const dl=el('dl',undefined,'kv');
    for(const [label,value] of rows)if(value!==undefined&&value!==null&&value!=='')dl.append(el('dt',label),el('dd',String(value)));
    holder.append(dl);
  }
  function sourceLink(holder, profile, el) {
    if(!profile?.reference_url)return;
    const a=el('a','Pinned upstream source');a.href=profile.reference_url;a.target='_blank';a.rel='noopener noreferrer';holder.append(a);
  }
  function mountSettings({root,target={},api,onInspect,onNavigate,timeline,getStatus}) {
    const {el,button,field,select}=helpers(root);let dead=false,exportView=null;
    const status=el('p','Reading the fixed definition…','small');status.setAttribute('role','status');
    const content=el('section',undefined,'bitmap-settings-content');root.replaceChildren(el('h2','Bitmap index rules'),el('p','Read-only rules and authenticated implementation metadata. Use the timeline to start, pause, resume, choose range or follow Live.','small muted'),status,content);
    function chainControls(chain){
      if(!chain)return;const box=el('section',undefined,'bitmap-chain-controls');box.append(el('h3',sourceKindLabel(chain)+' Chain'));if(window.GatewayIndexHelp)box.append(window.GatewayIndexHelp.controls({document:root.ownerDocument,id:chain.id||'bitmap-chain',definition:chain}));
      const reason='Bitmap Chain methodology is being developed. Saved evidence remains available for inspection.';
      for(const label of ['Chain On','Chain Live']){const control=button(label,()=>{},'bitmap-chain-toggle');control.disabled=true;control.title=reason;control.setAttribute('aria-pressed','false');box.append(control);}box.append(el('p',reason,'small muted'));if(chain.id&&chain.id!==target.id)box.append(button('Inspect saved chain evidence',()=>onInspect?.(chain.id),'text-button'));content.append(box);
    }
    const sourceKindLabel=i=>i?.producer_kind==='parcel'||i?.id?.includes('parcel')?'Parcel':'District';
    const ready=api('/api/v1/bitmap/profiles').then(registry=>{
      if(dead)return;const source=[...(registry.instances||[]),...(registry.drafts||[])].find(i=>i.id===target.id);
      if(!source){status.textContent='Select an existing canonical index or saved legacy definition.';return;}
      const p=source.profile||{},active=source.execution_available===true,canonical=active&&p.canonical_standard===true;
      content.replaceChildren(el('h3',source.kind==='chain'?'Bitmap '+sourceKindLabel(source)+' Chain':source.label||source.id),el('p',canonical?'Canonical Bitmap standard · fixed rules':source.kind==='chain'?'Bitmap Chain methodology is being developed':'Legacy saved definition · inspection only','small bitmap-profile-classification'));
      metadata(content,[['Index ID',source.id],['Profile ID',p.id],['Profile hash',source.profile_hash],['Implementation',p.implementation_version||p.fingerprint_schema],['Pinned source revision',p.reference_revision],['Source blob',p.reference_blob],['Index start',source.start_height],['Exact outcome producer',source.district_instance],['Producer profile hash',source.district_profile_hash]],el);
      sourceLink(content,p,el);
      const list=el('ol');for(const rule of p.rules||[])list.append(el('li',rule));content.append(el('h3',canonical?'Published Bitmap rules':'Saved rules'),list);
      if(p.implementation_limits?.length){const limits=el('ul');for(const limit of p.implementation_limits)limits.append(el('li',limit));content.append(el('h3','Implementation limits'),limits);}
      if(!active&&source.kind!=='chain')content.append(el('p',deferredReason,'small bitmap-availability'));
      if(source.note)content.append(el('p',source.note,'small muted'));
      if(source.district_instance)content.append(button('Inspect exact dependency',()=>onInspect?.(source.district_instance),'text-button'));
      const chain=source.kind==='chain'?source:(registry.instances||[]).find(i=>i.id===source.chain_instance);
      chainControls(chain||(canonical?{kind:'chain',producer_kind:source.kind}:null));
      if(canonical&&window.GatewayIndexExport?.supported(source.id)){const holder=el('section');content.append(holder);exportView=window.GatewayIndexExport.mount({root:holder,index:source.id,timeline,getStatus});}
      const details=el('details'),pre=el('pre',JSON.stringify({definition:source,chain:registry.chain_metadata?.[source.id]},null,2));details.append(el('summary','Machine-readable definition'),pre);content.append(details);
      status.textContent=source.rules_lock_reason||'Fixed read-only definition. Opening this view starts no work.';
    }).catch(error=>{if(!dead)status.textContent=error.message;});
    return {ready,teardown(){dead=true;exportView?.teardown();root.replaceChildren();}};
  }
  function mountOverview({root,api,onInspect,onResolved,viewport}) {
    const {el,button}=helpers(root);let dead=false;
    const status=el('p','Reading Bitmap indexes…','small');status.setAttribute('role','status');
    const current=el('section'),legacy=el('details');legacy.append(el('summary','Preserved legacy definitions and saved results'));
    root.replaceChildren(el('h1','Bitmap'),el('p','Canonical Districts and Parcels have fixed rules. District and Parcel chains are unavailable while their methodology is being developed.','small muted'),status,current,legacy);
    const ready=api('/api/v1/bitmap/profiles').then(registry=>{if(dead)return;
      for(const source of [...(registry.instances||[]),...(registry.drafts||[])]){
        const active=source.execution_available===true,card=el('article',undefined,'panel bitmap-index-definition');card.append(el('h2',source.kind==='chain'?'Bitmap '+(source.id.includes('parcel')?'Parcel':'District')+' Chain':source.label||source.id),el('p',source.kind==='chain'?'Bitmap Chain methodology is being developed':active?'Canonical standard · fixed rules':'Legacy saved definition · inspection only','small'));
        metadata(card,[['Profile hash',source.profile_hash],['Pinned source revision',source.profile?.reference_revision],['Exact outcome producer',source.district_instance]],el);
        const actions=el('div',undefined,'actions');sourceLink(actions,source.profile,el);actions.append(button('Inspect rules and saved state',()=>onInspect?.(source.id),'text-button'));card.append(actions);(active?current:legacy).append(card);
      }
      legacy.hidden=legacy.children.length===1;status.textContent=registry.note||'Select an index to inspect its fixed rules. Indexing controls are on the timeline.';
      viewport?.message?.('Canonical Bitmap indexes. Chain methodology is being developed.','bitmap');onResolved?.('bitmap.gateway',{state:'canonical_preview'});
    }).catch(error=>{if(!dead){status.textContent=error.message;onResolved?.('bitmap.gateway',{state:'unavailable'});}});
    return {ready,teardown(){dead=true;root.replaceChildren();}};
  }
  return {kindOf,instanceID,customInstance,capabilities,featureEnabled,deferredReason,address,answerBits,mountSettings,mountOverview,mountDelete};
})();
