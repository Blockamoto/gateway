'use strict';
// The playhead identifies an explored block; indexing selections are independent.
window.GatewayTimelineExplorer = (() => {
  const validHeight = n => Number.isSafeInteger(n) && n >= 0 && n < Number.MAX_SAFE_INTEGER;
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const number = n => typeof n === 'number' ? n.toLocaleString('en-GB') : String(n ?? 'Unknown');
  const short = value => typeof value === 'string' && value.length > 26 ? value.slice(0,14) + '…' + value.slice(-10) : String(value ?? '');
  const sourceName = value => ({bitcoin:'Bitcoin peer',bod:'Gateway peer',cache:'Gateway cache',core:'Bitcoin Core RPC',core_mount:'Mounted Bitcoin files',archive:'Gateway archive'}[value] || value || 'Not recorded');
  const bytes = n => !n ? '0 B' : n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KiB' : (n / 1048576).toFixed(1) + ' MiB';
  const pairs = rows => '<dl class="kv">' + rows.map(([label,value]) => '<dt>' + esc(label) + '</dt><dd>' + esc(value ?? 'Unknown') + '</dd>').join('') + '</dl>';

  function mount({root, document:doc, notify, viewport, onEntity, onNavigate, onNavigateIntent, onTrace}) {
    if (!root || !doc) throw Error('An Inspector pane is required for the timeline Explorer.');
    const win = doc.defaultView || window;
    const element = (tag, text, cls) => { const node=doc.createElement(tag); if(text!==undefined)node.textContent=text; if(cls)node.className=cls; return node; };
    const tabs = element('div',undefined,'timeline-pane-tabs'); tabs.setAttribute('role','tablist'); tabs.setAttribute('aria-label','Timeline details');
    const inspector = element('div',undefined,'timeline-inspector-panel'); inspector.id='timeline-inspector-panel'; inspector.setAttribute('role','tabpanel'); inspector.setAttribute('aria-labelledby','timeline-inspector-tab'); inspector.tabIndex=0;
    while(root.firstChild)inspector.append(root.firstChild);
    const explorer = element('div',undefined,'timeline-explorer-panel'); explorer.id='timeline-explorer-panel'; explorer.setAttribute('role','tabpanel'); explorer.setAttribute('aria-labelledby','timeline-explorer-tab'); explorer.tabIndex=0; explorer.hidden=true;
    const inspectorTab = element('button','Inspector'); inspectorTab.id='timeline-inspector-tab'; inspectorTab.type='button'; inspectorTab.setAttribute('role','tab'); inspectorTab.setAttribute('aria-controls',inspector.id);
    const explorerTab = element('button','Details'); explorerTab.id='timeline-explorer-tab'; explorerTab.type='button'; explorerTab.setAttribute('role','tab'); explorerTab.setAttribute('aria-controls',explorer.id);
    tabs.append(explorerTab,inspectorTab); root.append(tabs,inspector,explorer); root.classList.add('timeline-tabbed-inspector');
    const panes=new Map([['explorer',{panel:explorer,tab:explorerTab}],['inspector',{panel:inspector,tab:inspectorTab}]]), resourcePanels=new Map(), viewHandlers=new Map();
    let resource='block', viewKind='block';
    const painter=viewport&&window.GatewayBlockViewport?.mount({root:viewport,document:doc,onTransaction:(tx,map)=>entity({kind:'transaction',height:map.height,txIndex:tx.index,index:'blocks'}),onInput:(input,context)=>{if(focused?.height!==context.height||Number(explorer.querySelector('#timeline-explorer-tx-detail')?.dataset.txIndex)!==context.index)return;const button=explorer.querySelector('[data-txio-direction="input"][data-txio-index="'+input.index+'"]');if(button)button.click();else notify?.('Coinbase inputs have no previous output.');},onOutput:(output,context)=>entity({kind:'output',height:context.height,txIndex:context.index,outputIndex:output.n,index:'tx-locator'}),onInscription:entry=>viewHandlers.get('inscriptions')?.open?.(entry.coordinate||entry.id)});
    let active='inspector', explicitTab=false, dead=false, available=true, pending=null, focused=null, loaded=null, generation=0, txGeneration=0, pageGeneration=0, timer=null,pendingEntity=null;
    const requests=new Set(),txRequests=new Set(),blockRecords=new Map(),transactionRecords=new Map();
    let relationshipController=null,relationshipSerial=0;
    const retain=(cache,key,value)=>{cache.delete(key);cache.set(key,value);while(cache.size>16)cache.delete(cache.keys().next().value);return value;};
    function metadata(value,record){
      const block_hash=record?.block_hash||record?.hash||'';
      if(value.kind==='block')return {kind:'block',height:value.height,block_hash,count:Number.isSafeInteger(record?.transaction_count)?record.transaction_count:null,entries:(record?.transactions||[]).map(tx=>({index:tx.index}))};
      const tx=record?.transaction||record,outputs=tx?.outputs;
      if(!Array.isArray(outputs))return {kind:value.kind,height:value.height,txIndex:value.txIndex,block_hash,count:null};
      if(value.kind==='output'||value.kind==='satpoint'){const output=outputs.find(item=>item.n===value.outputIndex);return {kind:'output',height:value.height,txIndex:value.txIndex,outputIndex:value.outputIndex,block_hash,count:Number.isSafeInteger(output?.value_sats)?output.value_sats:null};}
      return {kind:'transaction',height:value.height,txIndex:value.txIndex,block_hash,count:outputs.length,entries:outputs.map(output=>({index:output.n,value_sats:output.value_sats}))};
    }
    function addressOf(value){if(validHeight(value.inputIndex))return 'i'+value.inputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';if(validHeight(value.satOffset)&&validHeight(value.outputIndex))return value.satOffset+'.'+value.outputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';if(validHeight(value.outputIndex))return value.outputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';if(validHeight(value.txIndex))return value.txIndex+'.'+value.height+'.bitcoin';return value.height+'.bitcoin';}
    const setTab = (name, explicit=true) => {
      if(dead)return;
      if(resourcePanels.has(name)){activateResource(name);name='explorer';}if(name==='details')name='explorer';
      active=panes.has(name)?name:'inspector'; if(explicit)explicitTab=true;
      for(const [key,{panel,tab}] of panes) { const selected=key===active;panel.hidden=!selected;tab.setAttribute('aria-selected',String(selected));tab.tabIndex=selected?0:-1; }
      explorer.hidden=active!=='explorer'||resource!=='block';
      for(const [key,panel]of resourcePanels)panel.hidden=active!=='explorer'||resource!==key;
      explorerTab.setAttribute('aria-controls',resourcePanels.get(resource)?.id||explorer.id);
      root.dataset.activePane=active;root.dataset.resource=resource;
    };
    inspectorTab.addEventListener('click',()=>setTab('inspector'));
    explorerTab.addEventListener('click',()=>setTab('explorer'));
    tabs.addEventListener('keydown',event=>{
      if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;
      event.preventDefault();const keys=['explorer','inspector'],at=keys.indexOf(active),name=event.key==='Home'?keys[0]:event.key==='End'?keys.at(-1):keys[(at+(event.key==='ArrowLeft'?-1:1)+keys.length)%keys.length];setTab(name);panes.get(name).tab.focus();
    });
    setTab('inspector',false);

    function cancel() {
      if(timer!==null){win.clearTimeout(timer);timer=null;}
      cancelRelationship();
      for(const request of requests)request.abort(); requests.clear();
      generation++; txGeneration++; pageGeneration++;
    }
    async function api(path, data, ticket, scope, signal) {
      const Controller=win.AbortController || AbortController, controller=new Controller(); requests.add(controller);if(scope==='transaction')txRequests.add(controller);
      const abort=()=>controller.abort();if(signal?.aborted)controller.abort();else signal?.addEventListener('abort',abort,{once:true});
      let timedOut=false;
      const timeout=win.setTimeout(()=>{timedOut=true;controller.abort();},90000);
      try {
        const options={signal:controller.signal,credentials:'same-origin'};
        if(data!==undefined){options.method='POST';options.headers={'Content-Type':'application/json'};options.body=JSON.stringify(data);}
        const response=await win.fetch(path,options);
        let value; try { value=await response.json(); } catch { throw Error('Gateway returned an unreadable response. Please retry.'); }
        if(dead || ticket!==generation)throw new DOMException('Superseded selection','AbortError');
        if(!response.ok)throw Error(value.error || response.statusText || 'The requested data is unavailable.');
        return value;
      } catch(error) {
        if(timedOut && current(ticket))throw Error('This block request took too long. Check your Bitcoin connection and retry.');
        throw error;
      } finally {win.clearTimeout(timeout);signal?.removeEventListener('abort',abort);requests.delete(controller);txRequests.delete(controller);}
    }
    const current = ticket => !dead && ticket===generation;
    function cancelRelationship(){relationshipSerial++;relationshipController?.abort();relationshipController=null;}
    function linkedPosition(value,kind){
      const match=String(value||'').match(kind==='input'?/^i(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/:/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/);
      if(!match||!match.slice(1).every(part=>validHeight(Number(part))))return null;
      return {kind,height:Number(match[3]),txIndex:Number(match[2]),[kind==='input'?'inputIndex':'outputIndex']:Number(match[1]),address:value+'.bitcoin'};
    }
    function showPreviousPosition(article,value){
      if(!['header_anchored','consensus_validated'].includes(value?.verification_state)&&value?.previous_output_verified!==true)return;
      const target=linkedPosition(value.previous_output,'output');if(!target)return;
      let line=article.querySelector('[data-original-output]');if(!line){line=element('p',undefined,'mono');line.dataset.originalOutput='';article.insertBefore(line,article.querySelector('.io-actions')||article.querySelector('details'));}
      const link=element('button',value.previous_output,'text-button mono');link.type='button';link.setAttribute('aria-label','Open original output '+value.previous_output);link.onclick=()=>{onNavigateIntent?.(target);navigate(target.address).catch(error=>notify?.(error.message,true));};line.replaceChildren(element('span','Original output · ','small muted'),link);
      const control=article.querySelector('[data-txio-direction="input"]');if(control)control.dataset.previousOutput=value.previous_output;
      article.querySelector('[data-native-prevout]')?.classList.add('small','muted');
    }
    function relationshipControl(article,direction,ordinal,record,block,ticket,requestTicket){
      if(!validHeight(ordinal))return;
      const actions=article.querySelector('.io-actions')||element('div',undefined,'io-actions');
      if(!actions.isConnected)article.insertBefore(actions,article.querySelector('details'));
      const button=direction==='output'?[...actions.querySelectorAll('button')].find(node=>node.textContent.includes('Spender'))||element('button'):element('button');
      button.type='button';button.disabled=false;button.removeAttribute('title');button.textContent=direction==='input'?'Open original output':'Find spending input';button.setAttribute('aria-label',button.textContent+' for '+direction+' '+ordinal);button.dataset.txioDirection=direction;button.dataset.txioIndex=String(ordinal);if(!button.isConnected)actions.append(button);
      const note=element('p','','small muted');note.setAttribute('role','status');note.dataset.txioStatus=direction+':'+ordinal;article.insertBefore(note,article.querySelector('details'));
      button.onclick=async()=>{
        if(!available||!current(ticket)||requestTicket!==txGeneration)return;
        const direct=direction==='input'?linkedPosition(button.dataset.previousOutput,'output'):null;if(direct){onNavigateIntent?.(direct);await navigate(direct.address);return;}
        cancelRelationship();const serial=relationshipSerial,Controller=win.AbortController||AbortController,controller=new Controller();relationshipController=controller;
        const position=(direction==='input'?'i':'')+ordinal+'.'+record.transaction.index+'.'+block.height;
        const path='/api/v1/txio/lookup?direction='+direction+'&position='+encodeURIComponent(position);
        const valid=()=>current(ticket)&&requestTicket===txGeneration&&serial===relationshipSerial&&!controller.signal.aborted;
        button.disabled=true;note.textContent='Checking saved positional relationship…';
        try{
          let result=await api(path,undefined,ticket,'transaction',controller.signal);
          if(!valid())return;
          if(result.direction!==direction||result.position!==position)throw Error('The relationship response does not match this position.');
          if(result.state!=='known'){
            note.textContent='Checking existing verified location providers…';
            result=await api(path+'&fallback=true',undefined,ticket,'transaction',controller.signal);if(!valid())return;
            if(result.direction!==direction||result.position!==position)throw Error('The relationship response does not match this position.');
          }
          if(result.state!=='known'){note.textContent=(result.note||(direction==='output'?'No spending input is known in the available coverage.':'The original output location is unresolved in the available coverage.'))+(direction==='output'?' This does not establish that the output is unspent.':'');return;}
          const targetKind=direction==='input'?'output':'input',rows=Array.isArray(result.rows)?result.rows:[];
          if(rows.length!==1||rows[0][direction]!==position)throw Error('A unique verified relationship was not returned.');
          const row=rows[0],anchor=direction==='input'?row.block_hash:row.creation_block_hash;
          if(anchor!==block.hash)throw Error('The relationship belongs to different block evidence. Refresh this transaction and retry.');
          const target=linkedPosition(row[targetKind],targetKind);if(!target)throw Error('The related position is malformed.');
          if(direction==='input')showPreviousPosition(article,{previous_output:row.output,previous_output_verified:true});
          note.textContent='Opening '+targetKind+' '+number(target[targetKind==='input'?'inputIndex':'outputIndex'])+' in block '+number(target.height)+'…';
          onNavigateIntent?.(target);await navigate(target.address);
        }catch(error){if(valid()&&error.name!=='AbortError')note.textContent=error.message;}
        finally{if(current(ticket)&&requestTicket===txGeneration&&button.isConnected){button.disabled=false;if(!valid()&&note.textContent.includes('Checking '))note.textContent='Lookup cancelled.';}if(relationshipController===controller)relationshipController=null;}
      };
    }
    function blank(message, state='idle', retry=false) {
      explorer.dataset.state=state;painter?.message(message,state);
      explorer.setAttribute('aria-busy',String(state==='loading'));
      explorer.replaceChildren();
      const box=element('div',undefined,'timeline-explorer-state'),title=element('h2',focused?'Block '+number(focused.height):'Block Explorer');
      const text=element('p',message,'small muted');text.setAttribute('role',state==='error'?'alert':'status');box.append(title,text);
      if(state==='loading')text.prepend(element('span',undefined,'loader'));
      if(retry){const button=element('button','Retry block');button.type='button';button.id='timeline-explorer-retry';button.addEventListener('click',()=>{if(focused){onNavigateIntent?.(focused);focus({...focused,immediate:true,refresh:true});}});box.append(button);}
      explorer.append(box); explorer.scrollTop=0;
    }
    blank('Select a block or move the playhead to explore its data.');

    function health(ok) {
      if(dead || available===Boolean(ok))return;
      available=Boolean(ok);if(focused?.savedSatPoint){cancel();pending=null;blank(available?'Select the Sat again to retry its saved identity. No history scan was started.':'Local index status is unavailable. Saved Sat lookup is paused.','paused');return;}if(resource!=='block'||focused?.locationOnly)return;cancel();loaded=null;pending=null;
      if(!available)blank('Local index status is unavailable. Block loading is paused until status recovers.','paused');
      else if(focused)loadBlock(focused,generation);
      else blank('Select a block or move the playhead to explore its data.');
    }

    function focus(value) {
      if(dead || !value || !validHeight(value.height))return;
      activateResource('block');
      const next={index:String(value.index || 'blocks'),height:value.height};
      if(!value.preserveEntity)pendingEntity=null;
      if(!available) {
        cancel();focused=next;loaded=null;pending=null;
        blank('Local index status is unavailable. Block loading is paused until status recovers.','paused');
        return;
      }
      if(focused?.height===next.height && !focused.locationOnly && !value.refresh && !(value.immediate!==false && explorer.dataset.state==='error')) {
        focused=next;
        if(pending && value.immediate!==false){cancel();loaded=null;pending=null;loadBlock(next,generation);}
        else if(!value.preserveEntity&&(viewKind!=='block'||painter&&painter.state().kind!=='block-map')){
          cancel();pending=null;
          if(loaded?.block&&!loaded.block.synthetic){const block=loaded.block,map=loaded.map;renderBlock(block,loaded.elapsed,generation);if(map)painter?.block(map);else loadMap(block,generation);onEntity?.({kind:'block',height:block.height,address:block.height+'.bitcoin',resolved:true,metadata:metadata({kind:'block',height:block.height},block)});}
          else loadBlock(next,generation);
        }
        return;
      }
      cancel();focused=next;loaded=null;pending=next;
      if(!explicitTab)setTab('explorer',false);
      blank(value.immediate===false?'Move along the timeline; this block will load once the playhead settles.':'Loading from local sources first. Missing block data may be fetched from a connected Bitcoin provider.',value.immediate===false?'settling':'loading');
      if(value.immediate===false) {
        const ticket=generation;
        timer=win.setTimeout(()=>{timer=null;if(current(ticket)&&pending?.height===next.height)loadBlock(next,ticket);},1000);
      } else loadBlock(next,generation);
    }
    async function loadBlock(target,ticket) {
      if(!current(ticket)||!available)return;
      pending=null;
      blank('Loading from local sources first. Missing block data may be fetched from a connected Bitcoin provider.','loading');
      try {
        const result=await api('/api/v1/navigate',{address:target.height+'.bitcoin'},ticket);
        if(!current(ticket))return;
        const block=result?.resource?.block;
        if(!block || block.height!==target.height)throw Error('The selected block was not returned. Please retry once headers and a Bitcoin provider are available.');
        loaded={block,elapsed:result.elapsed_ms};retain(blockRecords,block.height,block);renderBlock(block,result.elapsed_ms,ticket);loadMap(block,ticket);onEntity?.({kind:'block',height:block.height,address:block.height+'.bitcoin',resolved:true,metadata:metadata({kind:'block',height:block.height},block)});
      } catch(error) {if(current(ticket)&&error.name!=='AbortError')blank(error.message,'error',true);}
    }

    function renderBlock(block,elapsed,ticket) {
      if(!current(ticket))return;
      explorer.dataset.state='ready';explorer.dataset.resource='block';viewKind='block';
      explorer.setAttribute('aria-busy','false');
      explorer.innerHTML='<div class="timeline-explorer-content"><header class="timeline-explorer-heading"><div><div class="eyebrow">BITCOIN BLOCK</div><h2>Block '+esc(number(block.height))+'</h2></div><button type="button" id="timeline-explorer-refresh">Refresh block</button></header>'+
        '<section class="panel timeline-explorer-summary"><div class="actions mb"><span class="badge" title="Verification state. Header anchoring is not full consensus validation.">'+esc(String(block.verification_state||'unknown').replaceAll('_',' '))+'</span><span class="badge">'+esc(sourceName(block.evidence?.bytes||block.source_network))+'</span>'+(Number.isFinite(elapsed)?'<span class="small muted">'+esc(number(elapsed))+' ms server-side</span>':'')+'</div><p class="mono">'+esc(block.hash)+'</p><div class="stats"><div class="stat"><strong>'+esc(number(block.transaction_count))+'</strong><span>transactions</span></div><div class="stat"><strong>'+esc(bytes(block.serialized_bytes))+'</strong><span>serialized size</span></div><div class="stat"><strong>'+esc(number(block.total_output_sats))+'</strong><span>total output sats</span></div></div>'+
        pairs([['Time',block.time_iso],['Source',block.source_peer],['Verifier receipt',block.verification?.verifier_version || 'Needs recheck']])+evidence(block)+
        '<details><summary>Block header and dimensions</summary>'+pairs([['Previous block',block.previous_block_hash],['Merkle root',block.merkle_root],['Version',block.version],['Difficulty bits',block.bits],['Nonce',block.nonce],['Weight',block.weight],['Virtual size',block.vsize],['Inputs',block.input_count],['Outputs',block.output_count]])+'</details></section>'+
        '<section class="panel timeline-explorer-transactions"><div class="section-head"><h3>Transactions</h3><span class="small muted">Original block order</span></div><div id="timeline-explorer-tx-table"></div><div class="actions mt"><button type="button" id="timeline-explorer-tx-prev" disabled>Previous</button><button type="button" id="timeline-explorer-tx-next">Next 40</button><span class="small muted" id="timeline-explorer-tx-page"></span></div><p id="timeline-explorer-page-error" class="small" role="alert" hidden></p></section>'+
        '<section id="timeline-explorer-tx-detail" class="timeline-explorer-transaction" hidden></section></div>';
      explorer.scrollTop=0;
      explorer.querySelector('#timeline-explorer-refresh').onclick=()=>{onNavigateIntent?.(focused);focus({...focused,immediate:true,refresh:true});};
      let offset=0;
      const table=explorer.querySelector('#timeline-explorer-tx-table'),prev=explorer.querySelector('#timeline-explorer-tx-prev'),next=explorer.querySelector('#timeline-explorer-tx-next'),pageLabel=explorer.querySelector('#timeline-explorer-tx-page'),pageError=explorer.querySelector('#timeline-explorer-page-error');
      function renderPage(transactions,total,end) {
        table.innerHTML='<div class="table-wrap"><table><thead><tr><th>Index</th><th>Transaction</th><th>Output value</th><th></th></tr></thead><tbody>'+(transactions||[]).map(tx=>'<tr><td>'+esc(number(tx.index))+'</td><td class="mono">'+esc(short(tx.txid))+(tx.coinbase?' · coinbase':'')+'</td><td>'+esc(number(tx.output_sats))+' sats</td><td><button type="button" data-explorer-tx="'+esc(tx.index)+'">Inspect</button></td></tr>').join('')+'</tbody></table></div>';
        prev.disabled=offset===0;next.disabled=end>=total;pageLabel.textContent=total?(offset+1)+'–'+end+' of '+number(total):'No transactions';
        table.querySelectorAll('[data-explorer-tx]').forEach(button=>button.onclick=()=>{const index=Number(button.dataset.explorerTx);if(validHeight(index)){onNavigateIntent?.({kind:'transaction',height:block.height,txIndex:index});loadTransaction(index,block,ticket);}});
      }
      renderPage(block.transactions||[],Number(block.transaction_count)||0,Math.min(40,Number(block.transaction_count)||0));
      async function page(delta) {
        const pageTicket=++pageGeneration,target=Math.max(0,offset+delta);prev.disabled=true;next.disabled=true;pageError.hidden=true;
        try {
          const result=await api('/api/v1/block/transactions?block='+encodeURIComponent(block.hash)+'&start='+target,undefined,ticket);
          if(!current(ticket)||pageTicket!==pageGeneration)return;
          if(result.height!==block.height || result.hash!==block.hash)throw Error('Transaction page does not match the selected block. Refresh the block and retry.');
          offset=target;renderPage(result.transactions,result.total,result.next);
        } catch(error) {
          if(!current(ticket)||pageTicket!==pageGeneration||error.name==='AbortError')return;
          pageError.textContent=error.message;pageError.hidden=false;prev.disabled=offset===0;next.disabled=offset+40>=block.transaction_count;
        }
      }
      prev.onclick=()=>page(-40);next.onclick=()=>page(40);
      if(pendingEntity?.height===block.height&&validHeight(pendingEntity.txIndex))loadTransaction(pendingEntity.txIndex,block,ticket);
    }
    function evidence(block) {
      const record=block.evidence||{};
      return '<details><summary>Source, authority and verification</summary>'+pairs([['Locator',record.locator||block.locator_peer||'Not recorded'],['Bytes',sourceName(record.bytes||block.source_network)],['Byte provider',record.byte_provider||block.source_peer],['Chain authority',record.chain_authority||'Pending'],['Snapshot',validHeight(record.snapshot_height)?number(record.snapshot_height)+' · '+short(record.snapshot_hash):'Not yet anchored'],['Original byte source',record.original_bytes?sourceName(record.original_bytes):'Not recorded for a previous fetch'],['Original provider',record.original_provider||'Not recorded']])+'<details><summary>Technical verification receipt</summary><pre>'+esc(JSON.stringify({evidence:block.evidence,checks:block.verification},null,2))+'</pre></details></details>';
    }
    async function loadTransaction(index,block,ticket) {
      const detail=explorer.querySelector('#timeline-explorer-tx-detail');if(!detail)return;
      cancelRelationship();
      for(const request of txRequests)request.abort();txRequests.clear();
      const requestTicket=++txGeneration;viewKind='transaction';explorer.dataset.resource='transaction';painter?.message('Loading transaction '+number(index)+'…','loading');detail.hidden=false;detail.innerHTML='<p role="status"><span class="loader"></span>Loading transaction '+esc(number(index))+' in block '+esc(number(block.height))+'…</p>';
      try {
        // A block-position address resolves through its known block, never a standalone locator.
        const response=await api('/api/v1/navigate',{address:index+'.'+block.height+'.bitcoin'},ticket,'transaction');
        if(!current(ticket)||requestTicket!==txGeneration)return;
        const record=response?.resource?.transaction,tx=record?.transaction;
        if(!tx || record.height!==block.height || tx.index!==index || record.block_hash!==block.hash)throw Error('The transaction did not match the selected block. Refresh the block and retry.');
        retain(transactionRecords,block.height+':'+index,record);
        const selectedEntity=pendingEntity?.height===block.height&&pendingEntity.txIndex===index?{...pendingEntity}:{kind:'transaction',height:block.height,txIndex:index};
        renderTransaction(detail,record,block,ticket,requestTicket);
        onEntity?.({...selectedEntity,address:addressOf(selectedEntity),resolved:true,metadata:metadata(selectedEntity,record)});
      } catch(error) {
        if(!current(ticket)||requestTicket!==txGeneration||error.name==='AbortError')return;
        painter?.message(error.message,'error');detail.innerHTML='<p role="alert">'+esc(error.message)+'</p><button type="button">Retry transaction</button>';detail.querySelector('button').onclick=()=>{onNavigateIntent?.({kind:'transaction',height:block.height,txIndex:index});loadTransaction(index,block,ticket);};
      }
    }
    function destinationHTML(output){const target=window.GatewayAddressView.destination(output);return target.value?(target.isPublicKey?'<span class="small">P2PK public key</span><br>':'')+'<button type="button" class="text-button mono destination-link" data-explorer-address="'+esc(target.value)+'">'+esc(target.value)+'</button>':esc(output.type||'script');}
    function renderTransaction(detail,record,block,ticket,requestTicket) {
      cancelRelationship();
      const tx=record.transaction;viewKind='transaction';explorer.dataset.resource='transaction';retain(transactionRecords,block.height+':'+tx.index,record);if(viewport?.closest('#workspace-viewport'))viewport.closest('#workspace-viewport').dataset.resource='transaction';
      detail.innerHTML='<section class="panel"><div class="section-head"><h3>Transaction '+esc(number(tx.index))+'</h3><button type="button" id="timeline-explorer-close-tx">Close details</button></div><p class="small muted">Block '+esc(number(block.height))+' · '+esc(record.verification_state||'Unknown verification')+'</p><p class="mono">'+esc(tx.txid)+'</p><div class="actions"><button type="button" data-explorer-copy="'+esc(tx.txid)+'">Copy transaction ID</button><button type="button" id="timeline-explorer-flow">Show value flow</button></div><div id="timeline-explorer-flow-result" class="flow-box" hidden></div><p id="timeline-explorer-flow-note" class="small muted" role="status" hidden></p>'+pairs([['Version',tx.version],['Lock time',tx.lock_time],['Size',bytes(tx.size)],['Weight',tx.weight],['Virtual size',tx.vsize],['Segwit',tx.segwit?'Yes':'No'],['Coinbase',tx.coinbase?'Yes':'No']])+'</section><div class="io-grid"><section><div class="eyebrow">INPUTS · '+esc((tx.inputs||[]).length)+'</div>'+(tx.inputs||[]).map(input=>'<article class="io"><div class="io-title"><strong>Input '+esc(input.n)+'</strong><span class="muted">'+(input.coinbase?'Coinbase':'Previous output')+'</span></div><p class="mono">'+(input.coinbase?'Subsidy and block fees':esc(input.prev_txid+':'+input.prev_vout))+'</p>'+(!input.coinbase?'<button type="button" data-explorer-copy="'+esc(input.prev_txid+':'+input.prev_vout)+'">Copy previous outpoint</button>':'')+'<details><summary>Input evidence</summary><pre>'+esc(JSON.stringify(input,null,2))+'</pre></details></article>').join('')+'</section><section><div class="eyebrow">OUTPUTS · '+esc((tx.outputs||[]).length)+'</div>'+(tx.outputs||[]).map(output=>'<article class="io"><div class="io-title"><strong>Output '+esc(output.n)+'</strong><span>'+esc(number(output.value_sats))+' sats</span></div><p class="mono muted">'+(destinationHTML(output))+'</p><div class="io-actions"><button type="button" data-explorer-copy="'+esc(tx.txid+':'+output.n)+'">Copy outpoint</button><button type="button" disabled title="Requires a later index">🔒 Spender</button></div><details><summary>Output evidence</summary><pre>'+esc(JSON.stringify(output,null,2))+'</pre></details></article>').join('')+'</section></div><details class="panel"><summary>Full transaction evidence</summary><pre>'+esc(JSON.stringify(tx,null,2))+'</pre></details>';
      detail.querySelector('#timeline-explorer-close-tx').textContent='Back to block';
      const sections=[...detail.querySelectorAll('.io-grid>section')];
      sections[0]?.querySelectorAll('.io').forEach((article,index)=>{const input=tx.inputs[index],position=element('p','Input position · i'+input.n+'.'+tx.index+'.'+block.height,'small mono muted');article.insertBefore(position,article.querySelector('p'));if(!input.coinbase){article.querySelector('p.mono:not(.small)')?.setAttribute('data-native-prevout','');relationshipControl(article,'input',input.n,record,block,ticket,requestTicket);showPreviousPosition(article,input);}});
      sections[1]?.querySelectorAll('.io').forEach((article,index)=>relationshipControl(article,'output',tx.outputs[index].n,record,block,ticket,requestTicket));
      detail.querySelector('#timeline-explorer-close-tx').onclick=()=>{onNavigateIntent?.({kind:'block',height:block.height});txGeneration++;for(const request of txRequests)request.abort();txRequests.clear();if(!Number.isSafeInteger(block.transaction_count))return focus({...focused,immediate:true,refresh:true});detail.hidden=true;detail.replaceChildren();viewKind='block';explorer.dataset.resource='block';if(loaded?.map)painter?.block(loaded.map);else if(loaded?.block)loadMap(loaded.block,generation);onEntity?.({kind:'block',height:block.height,address:block.height+'.bitcoin',resolved:true,metadata:metadata({kind:'block',height:block.height},block)});};
      if(pendingEntity?.height===block.height&&pendingEntity.txIndex===tx.index){const value=pendingEntity;detail.dataset.focusKind=value.kind||'transaction';if(validHeight(value.inputIndex)){const articles=[...detail.querySelectorAll('.io-grid>section:first-child .io')];articles.find((item,index)=>tx.inputs[index]?.n===value.inputIndex)?.classList.add('focused');}if(validHeight(value.outputIndex)){const articles=[...detail.querySelectorAll('.io-grid>section:last-child .io')],output=articles.find((item,index)=>tx.outputs[index]?.n===value.outputIndex);output?.classList.add('focused');if(validHeight(value.satOffset))output?.append(element('p','Output-local sat offset: '+number(value.satOffset),'sat-focus'));}pendingEntity=null;}
      detail.querySelectorAll('[data-explorer-copy]').forEach(button=>button.onclick=async()=>{try{await win.navigator.clipboard.writeText(button.dataset.explorerCopy);notify?.('Copied to clipboard.');}catch{notify?.('Clipboard access is unavailable. Select the displayed text to copy it.',true);}});
      detail.querySelectorAll('[data-explorer-address]').forEach(button=>button.onclick=()=>{const address=button.dataset.explorerAddress;if(win.parent!==win)win.parent.postMessage({type:'gateway:navigate',address},win.location.origin);else win.location.href='/?resolve='+encodeURIComponent(address);});
      const flowButton=detail.querySelector('#timeline-explorer-flow'),flow=detail.querySelector('#timeline-explorer-flow-result'),flowNote=detail.querySelector('#timeline-explorer-flow-note');
      flowButton.onclick=async()=>{
        flowButton.disabled=true;flowButton.textContent='Resolving input values…';
        try {
          const result=await api('/api/v1/flow',{txid:tx.txid,block_hash:block.hash,height:block.height},ticket,'transaction');
          if(!current(ticket)||requestTicket!==txGeneration)return;
          for(const input of result.inputs||[]){const article=[...sections[0]?.querySelectorAll('.io')||[]].find((item,index)=>tx.inputs[index]?.n===input.index);if(article)showPreviousPosition(article,input);}
          painter?.flow(result,{index:tx.index,height:block.height});flow.innerHTML=flowSVG(result);flow.hidden=Boolean(painter);flowNote.hidden=false;flowNote.textContent=result.note||'Unknown input values are not inferred.';
        } catch(error) {if(current(ticket)&&requestTicket===txGeneration&&error.name!=='AbortError'){painter?.message(error.message,'error');flowNote.hidden=false;flowNote.textContent=error.message;}}
        finally {if(current(ticket)&&requestTicket===txGeneration){flowButton.disabled=false;flowButton.textContent='Refresh value flow';}}
      };
      if(painter){flowButton.hidden=true;flowButton.onclick();}
      detail.dataset.txIndex=String(tx.index);showRelatedInscription();
    }
    function showRelatedInscription(){
      const detail=explorer.querySelector('#timeline-explorer-tx-detail');
      if(!detail||detail.hidden||viewKind!=='transaction')return;
      const related=loaded?.map?.transactions?.find(item=>item.index===Number(detail.dataset.txIndex))?.inscription;
      if(!related||detail.querySelector('[data-explorer-inscription]'))return;
      const open=element('button','View inscription '+related.coordinate);open.type='button';open.dataset.explorerInscription=related.coordinate;open.onclick=()=>viewHandlers.get('inscriptions')?.open?.(related.coordinate);detail.querySelector('.actions')?.append(open);
    }
    function flowSVG(data) {
      const ins=data.inputs||[],outs=data.outputs||[],count=Math.max(ins.length,outs.length,1),height=Math.min(1800,Math.max(230,count*45+60));
      const maximum=Math.max(1,...ins.map(input=>input.value||0),...outs.map(output=>output.value_sats||0)),row=(height-70)/count,thickness=value=>value==null?0:Math.max(value>0?2:0,Math.min(row*.75,50)*(value/maximum));
      let shapes='';
      ins.forEach((input,index)=>{const y=45+index*row;shapes+='<text x="8" y="'+(y-8)+'">vin '+index+' · '+esc(input.value==null?'unknown':number(input.value)+' sats')+'</text>';if(input.value!=null)shapes+='<path class="band" d="M 150 '+y+' C 235 '+y+', 235 '+height/2+', 320 '+height/2+'" stroke-width="'+thickness(input.value)+'"><title>'+esc(input.outpoint)+': '+esc(number(input.value))+' sats</title></path>';});
      outs.forEach((output,index)=>{const y=45+index*row;shapes+='<path class="band out-band" d="M 340 '+height/2+' C 420 '+height/2+', 420 '+y+', 500 '+y+'" stroke-width="'+thickness(output.value_sats)+'"><title>Output '+esc(output.n)+': '+esc(number(output.value_sats))+' sats</title></path><text x="505" y="'+(y-8)+'">vout '+esc(output.n)+' · '+esc(number(output.value_sats))+'</text>';});
      return '<svg viewBox="0 0 720 '+height+'" role="img" aria-label="Transaction input and output values, in original order"><rect x="318" y="'+(height/2-35)+'" width="24" height="70" rx="8" fill="#c4ef9a"/>'+shapes+'<text x="245" y="'+(height-10)+'">Fee: '+esc(data.fee==null?'not yet established':number(data.fee)+' sats')+'</text></svg>';
    }
    function teardown() {
      if(dead)return;cancel();dead=true;for(const handler of viewHandlers.values())handler.deactivate?.();painter?.teardown();cleanupFullscreen();
      while(inspector.firstChild)root.insertBefore(inspector.firstChild,tabs);
      tabs.remove();for(const {panel} of panes.values())panel.remove();root.classList.remove('timeline-tabbed-inspector');delete root.dataset.activePane;
    }
    function addPane(name,label,panel) {
      if(dead||resourcePanels.has(name))throw Error('Duplicate resource details.');
      panel.setAttribute('role','tabpanel');panel.setAttribute('aria-labelledby',explorerTab.id);panel.hidden=true;resourcePanels.set(name,panel);root.append(panel);return explorerTab;
    }
    function activateResource(name) {
      if(resource!==name){if(resource==='block')cancel();viewHandlers.get(resource)?.deactivate?.();resource=name;}
      if(active){explorer.hidden=active!=='explorer'||resource!=='block';for(const[key,panel]of resourcePanels)panel.hidden=active!=='explorer'||resource!==key;root.dataset.resource=resource;explorerTab.setAttribute('aria-controls',resourcePanels.get(resource)?.id||explorer.id);}
    }
    async function loadMap(block,ticket) {
      if(!painter||!current(ticket))return;
      try {
        const map=await api('/api/v1/index/block-map?block='+encodeURIComponent(block.hash),undefined,ticket);
        if(!current(ticket)||resource!=='block')return;
        if(map.height!==block.height||map.hash!==block.hash||(block.transaction_count>0&&map.total!==block.transaction_count))throw Error('The full transaction map does not match the selected block.');
        if(loaded)loaded.map=map;
        showRelatedInscription();
        if(viewKind==='block')painter.block(map);
        const failures=(map.indexing||[]).filter(item=>item.error);if(failures.length)notify?.('Browsing index: '+failures.map(item=>item.error).join('; '),true);
      }catch(error){if(current(ticket)&&error.name!=='AbortError'&&viewKind==='block')painter.message(error.message,'error');}
    }
    async function navigate(address,{blockHint}={}) {
      const query=String(address||'').trim();if(!query||dead)return;
      if(/(?:i[0-9]+(?:\.[0-9]+(?:\.bitcoin)?)?|\.ord)$/i.test(query)&&viewHandlers.get('inscriptions')?.open)return viewHandlers.get('inscriptions').open(query,{block:blockHint});
      activateResource('block');cancel();loaded=null;pendingEntity=null;focused=null;setTab('explorer');blank('Resolving the address through local sources and connected Bitcoin providers.','loading');const ticket=generation;
      try {
        const result=await api('/api/v1/navigate',{address:query},ticket);if(!current(ticket))return;const value=result.resource||{};
        if(value.inscription)return viewHandlers.get('inscriptions')?.open?.(query,{record:value.inscription,block:blockHint,intent:false});
        if(value.block){focused={height:value.block.height,index:'blocks'};loaded={block:value.block,elapsed:result.elapsed_ms};retain(blockRecords,value.block.height,value.block);renderBlock(value.block,result.elapsed_ms,ticket);loadMap(value.block,ticket);onEntity?.({kind:'block',height:value.block.height,address:value.coordinate||value.query||query,resolved:true,metadata:metadata({kind:'block',height:value.block.height},value.block)});}
        else if(value.transaction){
          const record=value.transaction,index=validHeight(record.tx_index)?record.tx_index:record.transaction?.index;
          if(record.resolution_state==='location_unknown')throw Error(record.note||'The transaction location is not known. A saved locator or containing block is required.');
          if(!validHeight(record.height)||!validHeight(index))throw Error(record.note||'The transaction location is not known. A saved locator or containing block is required.');
          if(record.resolution_state==='located_bytes_unavailable'){
            focused={height:record.height,index:'blocks',locationOnly:true};viewKind='transaction';explorer.dataset.resource='transaction';explorer.dataset.state='located_bytes_unavailable';explorer.setAttribute('aria-busy','false');
            const box=element('section',undefined,'panel'),heading=element('h2','Transaction '+number(index)),note=element('p',record.note||'The transaction location is known, but its block bytes are unavailable.','hint');note.setAttribute('role','status');box.append(heading,note);const fields=element('div');fields.innerHTML=pairs([['Transaction ID',record.txid],['Block height',number(record.height)],['Transaction position',number(index)],['Block hash',record.block_hash],['Locator source',record.locator_peer],['Verification',record.verification_state]]);box.append(fields);
            const retry=element('button','Fetch block and retry');retry.type='button';retry.id='timeline-explorer-retry';retry.onclick=()=>{onNavigateIntent?.({kind:'transaction',height:record.height,txIndex:index});navigate(query,{blockHint});};box.append(retry);explorer.replaceChildren(box);painter?.message('Location known; block bytes unavailable. Retry when a Bitcoin provider has this block.','located_bytes_unavailable');onEntity?.({kind:'transaction',height:record.height,txIndex:index,address:value.coordinate||value.query||query,resolved:true});return record;
          }
          focused={height:record.height,index:'blocks'};const block={height:record.height,hash:record.block_hash,transaction_count:null,transactions:[]};loaded={block,elapsed:result.elapsed_ms};
          const nextEntity={height:record.height,txIndex:index,kind:value.focus_kind||'transaction',inputIndex:value.focus_kind==='input'?value.focus_index:undefined,outputIndex:['output','satpoint'].includes(value.focus_kind)?value.focus_index:undefined,satOffset:value.focus_offset};
          renderBlock(block,result.elapsed_ms,ticket);pendingEntity=nextEntity;
          if(record.transaction?.inputs&&record.transaction?.outputs){const detail=explorer.querySelector('#timeline-explorer-tx-detail');detail.hidden=false;renderTransaction(detail,record,block,ticket,++txGeneration);onEntity?.({...nextEntity,address:value.coordinate||value.query||query,resolved:true,metadata:metadata(nextEntity,record)});pendingEntity=null;}
          else loadTransaction(index,block,ticket);
          if(block.hash)loadMap(block,ticket);
        }else throw Error('This address did not resolve to a block, transaction or inscription.');
      }catch(error){if(current(ticket)&&error.name!=='AbortError')blank(error.message,'error',Boolean(focused));}
    }
    async function loadHierarchy(value,{signal}={}){
      if(dead||!validHeight(value?.height))throw Error('Choose a known block before zooming into its contents.');
      if(signal?.aborted)throw new DOMException('Superseded zoom','AbortError');const ticket=generation;
      if(value.kind==='block'){
        const result=await api('/api/v1/navigate',{address:value.height+'.bitcoin'},ticket,undefined,signal),block=result.resource?.block;
        if(!block||block.height!==value.height||!Number.isSafeInteger(block.transaction_count))throw Error('The block contents are not available.');
        const previous=blockRecords.get(value.height);if(previous?.hash&&block.hash!==previous.hash)for(const key of transactionRecords.keys())if(key.startsWith(value.height+':'))transactionRecords.delete(key);retain(blockRecords,value.height,block);
        if(signal?.aborted)throw new DOMException('Superseded zoom','AbortError');return metadata(value,block);
      }
      if(!validHeight(value.txIndex))throw Error('Choose a transaction before zooming into its outputs.');
      const response=await api('/api/v1/navigate',{address:value.txIndex+'.'+value.height+'.bitcoin'},ticket,undefined,signal),record=response.resource?.transaction;
      if(record?.height!==value.height||record.transaction?.index!==value.txIndex||!Array.isArray(record.transaction.outputs))throw Error('The transaction contents are not available.');retain(transactionRecords,value.height+':'+value.txIndex,record);
      if(signal?.aborted)throw new DOMException('Superseded zoom','AbortError');const result=metadata(value,record);if(result.count===null)throw Error('The output does not exist in this transaction.');return result;
    }
    const fullscreenHost=viewport?.closest('#workspace-viewport'),fullscreenButton=doc.getElementById('workspace-viewport-fullscreen');
    const isFullscreen=()=>doc.fullscreenElement===fullscreenHost||fullscreenHost?.classList.contains('fullscreen-fallback');
    const fullscreenState=()=>{if(fullscreenButton){fullscreenButton.setAttribute('aria-label',isFullscreen()?'Exit fullscreen':'Open viewport fullscreen');fullscreenButton.setAttribute('aria-pressed',String(Boolean(isFullscreen())));fullscreenButton.title=isFullscreen()?'Exit fullscreen (Escape)':'Fullscreen viewport';}};
    const exitFullscreen=()=>{fullscreenHost?.classList.remove('fullscreen-fallback');if(doc.fullscreenElement===fullscreenHost)doc.exitFullscreen?.().catch(()=>{});fullscreenState();};
    const fullscreenKey=event=>{if(event.key==='Escape'&&isFullscreen()){event.preventDefault();exitFullscreen();}};
    if(fullscreenButton&&fullscreenHost){fullscreenButton.onclick=async()=>{if(isFullscreen())return exitFullscreen();try{if(!fullscreenHost.requestFullscreen)throw Error('Unavailable');await fullscreenHost.requestFullscreen();}catch{fullscreenHost.classList.add('fullscreen-fallback');}fullscreenState();};doc.addEventListener('keydown',fullscreenKey);doc.addEventListener('fullscreenchange',fullscreenState);fullscreenState();}
    function cleanupFullscreen(){exitFullscreen();doc.removeEventListener('keydown',fullscreenKey);doc.removeEventListener('fullscreenchange',fullscreenState);if(fullscreenButton)fullscreenButton.onclick=null;}
    async function openSatPoint(value){
      const output=value.outputIndex+'.'+value.txIndex+'.'+value.height,anchor=loaded?.block.height===value.height?loaded.block.hash:blockRecords.get(value.height)?.hash;
      cancel();const ticket=generation;focused={...value,index:'sat-state',savedSatPoint:true,locationOnly:true};resource='block';viewKind='satpoint';pendingEntity=null;setTab('explorer');blank('Reading Sat identity from saved output ranges…','loading');explorer.querySelector('h2').textContent='Sat at '+output+' · offset '+number(value.satOffset);explorer.dataset.resource='satpoint';
      try{
        if(!/^[a-f0-9]{64}$/i.test(anchor||''))throw Error('The output block anchor is unavailable. Open its block before selecting a Sat. No history scan was started.');
        const result=await api('/api/v1/sats/point?output='+encodeURIComponent(output)+'&offset='+value.satOffset+'&block_hash='+encodeURIComponent(anchor),undefined,ticket);
        if(result.output_coordinate!==output||result.offset!==value.satOffset||result.block_hash?.toLowerCase()!==anchor.toLowerCase())throw Error('The Sat identity response does not match this output and offset.');
        if(result.state!=='ready'){blank(result.note||'Sat identity is unavailable in saved output ranges. No history scan was started.','unavailable');return;}
        if(!validHeight(result.sat_number)||!validHeight(result.snapshot?.height)||result.snapshot.height<value.height||!/^[a-f0-9]{64}$/i.test(result.snapshot.block_hash||'')||!/^[a-f0-9]{64}$/i.test(result.snapshot.commitment||''))throw Error('The saved Sat identity is missing its authenticated checkpoint.');
        const address=result.sat_number+'.sat';if(onNavigate)onNavigate(address);else if(win.parent!==win)win.parent.postMessage({type:'gateway:navigate',address},win.location.origin);else win.location.href='/?resolve='+encodeURIComponent(address);
      }catch(error){if(current(ticket)&&error.name!=='AbortError')blank(error.message,'error');}
    }
    function entity(value){if(!validHeight(value?.height))return;onNavigateIntent?.(value);if(value.kind==='satpoint'&&validHeight(value.txIndex)&&validHeight(value.outputIndex)&&validHeight(value.satOffset)){openSatPoint(value);return;}const saved=value;focus({...value,immediate:true,preserveEntity:true});pendingEntity=saved;setTab('explorer');if(loaded?.block.height===saved.height&&validHeight(saved.txIndex))loadTransaction(saved.txIndex,loaded.block,generation);}
    return {focus,entity,navigate,loadHierarchy,health,teardown,addPane,activateResource,registerView:(name,handler)=>viewHandlers.set(name,handler),viewport:painter,setTab:name=>setTab(name),state:()=>({active,height:focused?.height,index:focused?.index,healthy:available,loading:explorer.dataset.state,loadedHeight:loaded?.block.height})};
  }
  return {mount};
})();
