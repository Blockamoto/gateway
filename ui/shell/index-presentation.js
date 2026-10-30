(function(global){
  'use strict';
  // These are display references only: no run, live, dependency, or arming API.
  function flatten(nodes,expanded={}){
    const byParent=new Map();for(const n of nodes){const key=n.parent||'';if(!byParent.has(key))byParent.set(key,[]);byParent.get(key).push(n);}
    const rows=[],seen=new Set();function visit(parent,depth){for(const node of byParent.get(parent)||[]){if(seen.has(node.id))continue;seen.add(node.id);const children=byParent.get(node.id)||[];rows.push({node,depth,hasChildren:children.length>0});if(expanded[node.id]!==false)visit(node.id,depth+1);}}visit('',0);return rows;
  }
  function mount({document:doc,controls,container,api,definitionFor,drawLane,select,afterLayout}){
    const el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const label=(text,input)=>{const x=el('label',text);x.append(input);return x;};
    const button=(text,fn)=>{const x=el('button',text);x.type='button';x.addEventListener('click',fn);return x;};
    const details=el('details',undefined,'presentation-editor'),summary=el('summary','Add display group or track');
    const note=el('p','Saving a display hierarchy upgrades this profile’s timeline layout to schema 3. Gateway 0.7.9 cannot read that modified layout. These rows reference existing tracks; the original tracks stay in place. This does not alter index data, execution dependencies, or indexing controls.','presentation-note');
    const name=el('input');name.type='text';name.maxLength=128;name.placeholder='Name';
    const kind=el('select');for(const [v,t] of [['category','Category'],['track','Track']]){const o=el('option',t);o.value=v;kind.append(o);}kind.value='category';
    const parent=el('select');parent.setAttribute('aria-label','Display parent');
    const index=el('input');index.type='text';index.maxLength=256;index.placeholder='Index ID';index.disabled=true;
    const fields=el('div',undefined,'presentation-fields');fields.append(label('Name',name),label('Type',kind),label('Parent',parent),label('Index ID for a track',index));
    const status=el('p','','presentation-status');status.setAttribute('role','status');status.setAttribute('aria-live','polite');
    let nodes=[],revision=null,busy=false,epoch=0,serial=0,formRevision=0,lanes=[],availability='',expanded=Object.create(null);
    const add=button('Add to timeline',save),reload=button('Reload display hierarchy',load);add.disabled=true;
    const actions=el('div',undefined,'presentation-actions');actions.append(add,reload);details.append(summary,note,fields,actions,status);controls.append(details);
    for(const input of [name,index,parent])input.addEventListener('input',()=>{formRevision++;});
    parent.addEventListener('change',()=>{formRevision++;});
    kind.addEventListener('change',()=>{formRevision++;index.disabled=kind.value!=='track';});
    function parentOptions(){const previous=parent.value||'',choices=[['','Root']];for(const row of flatten(nodes,{}))choices.push([row.node.id,'— '.repeat(Math.min(row.depth,8))+row.node.name+' ('+row.node.kind+')']);
      if(previous&&!choices.some(([id])=>id===previous))choices.push([previous,'Previous parent unavailable: '+previous]);
      parent.replaceChildren(...choices.map(([id,title])=>{const o=el('option',title);o.value=id;return o;}));parent.value=previous;}
    function paint(){for(const entry of lanes){const d=definitionFor(entry.node.index);if(d&&!d.locked)drawLane(entry.canvas,d);}}
    function availabilityKey(){return JSON.stringify(nodes.filter(n=>n.kind==='track').map(n=>{const d=definitionFor(n.index);return [n.index,!!d,!!d?.locked];}));}
    function render(){availability=availabilityKey();lanes=[];const rows=flatten(nodes,expanded),items=[];
      for(const {node,depth,hasChildren} of rows){const row=el('div',undefined,'presentation-row');row.dataset.presentationId=node.id;row.dataset.presentationKind=node.kind;
        const head=el('div',undefined,'presentation-heading');head.style.paddingLeft=(8+Math.min(depth,32)*14)+'px';
        if(hasChildren){const toggle=button(expanded[node.id]===false?'▸':'▾',()=>{expanded[node.id]=expanded[node.id]===false;render();afterLayout?.();});toggle.setAttribute('aria-label',(expanded[node.id]===false?'Expand ':'Collapse ')+node.name);toggle.setAttribute('aria-expanded',String(expanded[node.id]!==false));head.append(toggle);}
        else head.append(el('span','', 'presentation-spacer'));
        const d=node.kind==='track'?definitionFor(node.index):null;
        if(node.kind==='track'&&d&&!d.locked){const go=button(node.name,()=>select(node.index));go.title='Open '+d.name+' settings; this display reference does not change indexing.';head.append(go);}
        else head.append(el('span',node.name));
        row.append(head);
        const lane=el('div',undefined,'presentation-lane');
        if(node.kind==='category')lane.append(el('span','Category · display only'));
        else if(!d || d.locked)lane.append(el('span','Unavailable reference: '+node.index));
        else {const canvas=el('canvas');canvas.setAttribute('role','img');lane.append(canvas);lanes.push({node,canvas});}
        row.append(lane);items.push(row);
      }
      container.replaceChildren(...items);container.hidden=!rows.length;parentOptions();paint();
    }
    async function load(){if(busy)return;const ticket=++epoch;busy=true;add.disabled=reload.disabled=true;
      try{const data=await api('layout/hierarchy');if(ticket!==epoch)return;if(!Number.isSafeInteger(data?.revision)||!Array.isArray(data.nodes))throw Error('Invalid hierarchy response.');nodes=data.nodes;revision=data.revision;render();status.textContent='Display hierarchy loaded. No indexing was started.';}
      catch(e){if(ticket===epoch)status.textContent='Display hierarchy: '+e.message;}
      finally{if(ticket===epoch){busy=false;add.disabled=revision===null;reload.disabled=false;afterLayout?.();}}
    }
    async function save(){if(busy||revision===null)return;const text=name.value.trim(),type=kind.value,ref=index.value.trim(),parentID=parent.value;
      if(!text || (type==='track'&&!ref)){status.textContent='Enter a name and, for a track, its index ID.';return;}
      if(parentID&&!nodes.some(n=>n.id===parentID)){status.textContent='The selected parent is unavailable; reload or choose another parent.';return;}
      let id;do{id='node-'+Date.now().toString(36)+'-'+(++serial).toString(36);}while(nodes.some(n=>n.id===id));
      const node={id,kind:type,name:text};if(parentID)node.parent=parentID;if(type==='track')node.index=ref;
      const requested=[...nodes,node],ticket=++epoch,submittedForm=formRevision;busy=true;add.disabled=reload.disabled=true;
      try{const result=await api('layout/hierarchy',{expected_revision:revision,nodes:requested});if(ticket!==epoch)return;if(!Number.isSafeInteger(result?.revision)||!Array.isArray(result.nodes))throw Error('Invalid hierarchy response.');nodes=result.nodes;revision=result.revision;if(formRevision===submittedForm){name.value='';index.value='';}render();status.textContent=formRevision===submittedForm?'Added to the timeline. Display placement does not start indexing.':'Added the earlier entry; your newer form edits remain. Display placement does not start indexing.';}
      catch(e){if(ticket===epoch)status.textContent='Could not save: '+e.message+' Reload the hierarchy if the layout changed.';}
      finally{if(ticket===epoch){busy=false;add.disabled=revision===null;reload.disabled=false;afterLayout?.();}}
    }
    load();return {draw:paint,refresh:()=>{if(availability!==availabilityKey())render();else paint();}};
  }
  global.GatewayIndexPresentation={flatten,mount};
})(typeof window==='undefined'?globalThis:window);
