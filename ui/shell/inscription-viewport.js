'use strict';
// Trusted controls live here. Inscription bytes only enter the read-only content origin.
window.GatewayInscriptionViewport = (() => {
  const integer=n=>Number.isSafeInteger(n)&&n>=0;
  const coordinate=record=>record?.reveal_coordinate || (integer(record?.tx_index)&&integer(record?.height)&&integer(record?.index)?record.tx_index+'i'+record.index+'.'+record.height:'');
  function contentURL(value, origin, management) {
    try {
      const url=new URL(value),allowed=new URL(origin);
      if(!['http:','https:'].includes(url.protocol)||url.origin!==allowed.origin||url.origin===management||url.username||url.password||!/^\/(content|preview)\//.test(url.pathname))return null;
      return url;
    } catch {return null;}
  }
  function mount({document:doc,explorer,contentOrigin,notify,onEntity,onNavigate}) {
    const win=doc.defaultView,el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const panel=el('section',undefined,'timeline-inscription-panel');panel.id='timeline-inscription-panel';
    const addressForm=el('form',undefined,'inscription-address-form'),label=el('label','Inscription address'),address=el('input'),submit=el('button','Open');
    address.id='inscription-address';address.spellcheck=false;address.autocomplete='off';address.placeholder='12i0.792435.bitcoin or inscription ID';address.required=true;label.htmlFor=address.id;submit.type='submit';addressForm.append(label,address,submit);
    const hint=el('details'),hintSummary=el('summary','Known reveal block'),blockHint=el('input');blockHint.setAttribute('aria-label','Optional reveal block height or hash');blockHint.placeholder='Height or block hash';hint.append(hintSummary,blockHint);
    const status=el('p','Open an inscription by position, or use an ID with a known transaction locator.','small muted');status.id='inscription-viewer-status';status.setAttribute('role','status');
    const body=el('div',undefined,'inscription-viewer-body'),dependency=el('section',undefined,'inscription-dependencies');dependency.id='inscription-dependencies';dependency.hidden=true;dependency.setAttribute('aria-label','Requested inscription dependencies');
    panel.append(addressForm,hint,status,body,dependency);explorer.addPane('inscriptions','Inscription viewer',panel);
    let enabled=false,dead=false,generation=0,current=null,known=[],poll=null,controller=null,requestTimer=null,pollController=null;
    function closeViewer(record){if(record?.viewer_session)win.fetch('/api/v1/ord/dependencies',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({session:record.viewer_session,action:'close'}),keepalive:true}).catch(()=>{});}
    function cancel(){generation++;controller?.abort();controller=null;pollController?.abort();pollController=null;closeViewer(current);if(poll!==null){win.clearTimeout(poll);poll=null;}if(requestTimer!==null){win.clearTimeout(requestTimer);requestTimer=null;}body.replaceChildren();dependency.replaceChildren();dependency.hidden=true;}
    function state(text,type='idle'){panel.dataset.state=type;panel.setAttribute('aria-busy',String(type==='loading'));status.textContent=text;status.setAttribute('role',type==='error'?'alert':'status');}
    function available(value){enabled=Boolean(value);submit.disabled=!enabled;address.disabled=!enabled;if(!enabled){cancel();current=null;state('Inscriptions are unavailable in this build.','locked');}}
    function action(text,callback){const button=el('button',text);button.type='button';button.onclick=callback;return button;}
    function metadata(rows){const list=el('dl',undefined,'kv');for(const [name,value]of rows)list.append(el('dt',name),el('dd',value===null||value===undefined||value===''?'Not established':String(value)));return list;}
    async function copy(value){try{await win.navigator.clipboard.writeText(value);notify?.('Copied to clipboard.');}catch{notify?.('Clipboard unavailable. Select the displayed address to copy.',true);}}
    function safeLink(record,download=false,preview=false){const url=contentURL(download?(record.raw_url||record.content_url):preview?(record.preview_url||record.content_url):record.content_url,contentOrigin,win.location.origin);if(!url)return null;if(download)url.searchParams.set('download','1');return url.href;}
    function render(record){
      const position=coordinate(record),env=record.envelope||{},resolved=record.resolution_state!=='location_unknown'&&record.resolution_state!=='located_bytes_unavailable';
      const media=el('section',undefined,'inscription-monitor'),evidence=el('section',undefined,'inscription-evidence'),heading=el('h2',position||record.id||'Inscription');evidence.append(heading);
      const toolbar=el('div',undefined,'inscription-actions');if(position)toolbar.append(action('Copy position',()=>copy(position+'.bitcoin')));if(record.id)toolbar.append(action('Copy inscription ID',()=>copy(record.id)));
      const raw=safeLink(record,true);if(raw&&resolved){const link=el('a','Raw content');link.href=raw;link.download=record.id||'inscription';link.className='button';link.rel='noopener noreferrer';toolbar.append(link);}
      if(integer(record.tx_index)&&integer(record.height))toolbar.append(action('Containing transaction',()=>onNavigate?.({kind:'transaction',height:record.height,txIndex:record.tx_index,address:record.tx_index+'.'+record.height+'.bitcoin'})));
      const at=known.findIndex(r=>r===position||r===record.id);for(const [step,text]of [[-1,'Previous'],[1,'Next']]){const button=action(text,()=>open(known[at+step]));button.disabled=at<0||!known[at+step];toolbar.append(button);}evidence.append(toolbar);
      const sourceType=env.content_type||'application/octet-stream',size=record.content_size??record.size;
      evidence.append(metadata([['Conventional ID',record.id],['Reveal block',integer(record.height)?record.height:null],['Transaction position',record.tx_index],['Inscription index',record.index],['Input',env.input],['Content type',sourceType],['Content bytes',size],['SHA-256',record.content_sha256||record.sha256],['Verification',record.evidence],['Provider',record.provider],['Interpretation profile',record.interpretation_profile||record.profile],['Inscription number',record.canonical_number],['Sat number',record.sat_number],['Current location',record.current_satpoint?JSON.stringify(record.current_satpoint):null]]));
      if(record.note)evidence.append(el('p',record.note,'hint small'));
      for(const [title,ids]of [['Delegate',env.delegate?[env.delegate]:[]],['Parent claims',env.parent_claims||[]]])if(ids.length){const box=el('div',undefined,'inscription-reference-list');box.append(el('h3',title));for(const id of ids)box.append(action(id,()=>open(id)));if(title==='Parent claims')box.append(el('small','Loading a referenced inscription does not prove the parent relationship.','block muted'));evidence.append(box);}
      const technical=el('details');technical.append(el('summary','Reveal evidence'),el('pre',JSON.stringify(record,null,2)));evidence.append(technical);
      const url=safeLink(record,false,true);
      if(!resolved)media.append(el('p',record.resolution_state==='location_unknown'?'Location unknown. This ID needs a saved locator or a reveal block hint. Gateway peer lookup is not enabled.':'Location known; block bytes unavailable. Connect a Bitcoin provider with this block, then retry.','inscription-empty'));
      else if(!url)media.append(el('p','The read-only content origin is unavailable. No inscription bytes were inserted into Gateway.','inscription-empty'));
      else {
        const type=sourceType.split(';',1)[0].trim().toLowerCase();let view;
        if(type.startsWith('image/')||type.startsWith('audio/')||type.startsWith('video/')||type==='text/html'||type.startsWith('text/')||type==='application/json'||type.endsWith('+json')){view=el('iframe');view.title='Isolated inscription content';view.setAttribute('sandbox','allow-scripts allow-same-origin');view.setAttribute('allow',"camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'; payment 'none'");view.referrerPolicy='no-referrer';view.src=url;}
        else {view=el('div',undefined,'inscription-empty');view.append(el('p','No preview for this binary content type.'),el('p',sourceType+' · '+(size??'Unknown')+' bytes'));}
        media.append(view);
      }
      body.replaceChildren(media,evidence);state(resolved?'Reveal resolved. Dependencies are checked when content requests them.':record.resolution_state==='location_unknown'?'Inscription location is missing.':'Inscription located; its content is unavailable.',resolved?'ready':record.resolution_state);
      if(record.viewer_session)pollDependencies(record.viewer_session,generation);
    }
    async function pollDependencies(session,ticket){
      if(dead||ticket!==generation||!enabled)return;
      const abort=new win.AbortController();pollController=abort;const timer=win.setTimeout(()=>abort.abort(),10000);
      try{
        const response=await win.fetch('/api/v1/ord/dependencies?session='+encodeURIComponent(session),{credentials:'same-origin',signal:abort.signal});const value=await response.json();if(dead||ticket!==generation)return;if(!response.ok)throw Error(value.error||'Dependency status unavailable.');
        const rows=value.dependencies||value.requests||[];dependency.replaceChildren();dependency.hidden=!rows.length;
        if(rows.length){dependency.append(el('h3','Content dependencies'));for(const row of rows){const line=el('div',undefined,'inscription-dependency');line.dataset.state=row.state||row.resolution_state||'unknown';line.append(el('span',row.id||row.inscription_id||'Referenced inscription','mono'),el('span',({resolved:'Available',location_unknown:'Missing locator',located_bytes_unavailable:'Located; block bytes unavailable',resolving:'Loading',request_limit:'Request limit reached'})[line.dataset.state]||row.error||'Not resolved'));dependency.append(line);}if(rows.some(r=>['location_unknown','located_bytes_unavailable'].includes(r.state||r.resolution_state)))dependency.append(el('p','Some referenced content could not load. Your index may not cover these inscriptions; Gateway peer lookup will be available in a later stage.','hint small'));}
      }catch(error){if(ticket===generation){dependency.hidden=false;dependency.replaceChildren(el('p',error.name==='AbortError'?'Dependency status took too long; retrying.':error.message,'small muted'));}}finally{win.clearTimeout(timer);if(pollController===abort)pollController=null;}
      if(!dead&&ticket===generation)poll=win.setTimeout(()=>pollDependencies(session,ticket),1500);
    }
    async function open(value,{record,block,activate=true}={}){
      if(dead||!enabled)return;const query=String(value||coordinate(record)||record?.id||'').trim();if(!query)return;
      cancel();const ticket=generation;current=null;address.value=query;blockHint.value=String(block??'');if(activate)explorer.setTab('inscriptions');state('Resolving locally first. Missing block data may be fetched from Bitcoin peers.','loading');
      try{
        let rec=record;
        if(!rec){controller=new win.AbortController();requestTimer=win.setTimeout(()=>controller?.abort(),90000);const response=await win.fetch('/api/v1/ord/resolve',{method:'POST',credentials:'same-origin',signal:controller.signal,headers:{'Content-Type':'application/json'},body:JSON.stringify({id:query,block_hash:blockHint.value.trim()})});const result=await response.json();if(dead||ticket!==generation){closeViewer(result);return;}if(!response.ok)throw Error(result.error||'Inscription resolution failed.');rec=result;}
        if(ticket!==generation||dead){closeViewer(rec);return;}current=rec;render(rec);const position=coordinate(rec);if(integer(rec.height))onEntity?.({kind:'inscription',height:rec.height,txIndex:rec.tx_index,inscriptionIndex:rec.index,address:position||rec.id,index:'inscriptions'});
      }catch(error){if(ticket===generation&&!dead){state(error.name==='AbortError'?'The inscription request took too long. Check the connection and retry.':error.message,'error');body.append(action('Retry',()=>open(query,{block:blockHint.value})));if(/inscription.*disabled|ord.*disabled/i.test(error.message))body.append(action('Enable inscriptions',async()=>{try{const response=await win.fetch('/api/v1/settings'),saved=await response.json();if(!response.ok)throw Error(saved.error||'Settings unavailable.');const result=await win.fetch('/api/v1/settings',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({...saved.settings,ord_enabled:true})});if(!result.ok){const value=await result.json();throw Error(value.error||'Settings could not be saved.');}if(ticket===generation)open(query,{block:blockHint.value});}catch(error){if(ticket===generation)state(error.message,'error');}}));}}
      finally{if(ticket===generation){if(requestTimer!==null)win.clearTimeout(requestTimer);requestTimer=null;controller=null;}}
    }
    addressForm.onsubmit=event=>{event.preventDefault();open(address.value,{block:blockHint.value});};
    function clear(){cancel();current=null;state('Select an inscription marker or enter its position.');}
    return {open,clear,available,setKnown:rows=>{known=[...new Set(rows.filter(r=>typeof r==='string'&&r))];},state:()=>({state:panel.dataset.state,id:current?.id,position:coordinate(current)}),teardown(){cancel();dead=true;panel.remove();}};
  }
  return {mount,coordinate,contentURL};
})();
