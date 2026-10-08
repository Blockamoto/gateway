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

  function mount({root, document:doc, notify}) {
    if (!root || !doc) throw Error('An Inspector pane is required for the timeline Explorer.');
    const win = doc.defaultView || window;
    const element = (tag, text, cls) => { const node=doc.createElement(tag); if(text!==undefined)node.textContent=text; if(cls)node.className=cls; return node; };
    const tabs = element('div',undefined,'timeline-pane-tabs'); tabs.setAttribute('role','tablist'); tabs.setAttribute('aria-label','Timeline details');
    const inspector = element('div',undefined,'timeline-inspector-panel'); inspector.id='timeline-inspector-panel'; inspector.setAttribute('role','tabpanel'); inspector.setAttribute('aria-labelledby','timeline-inspector-tab'); inspector.tabIndex=0;
    while(root.firstChild)inspector.append(root.firstChild);
    const explorer = element('div',undefined,'timeline-explorer-panel'); explorer.id='timeline-explorer-panel'; explorer.setAttribute('role','tabpanel'); explorer.setAttribute('aria-labelledby','timeline-explorer-tab'); explorer.tabIndex=0; explorer.hidden=true;
    const inspectorTab = element('button','Inspector'); inspectorTab.id='timeline-inspector-tab'; inspectorTab.type='button'; inspectorTab.setAttribute('role','tab'); inspectorTab.setAttribute('aria-controls',inspector.id);
    const explorerTab = element('button','Block Explorer'); explorerTab.id='timeline-explorer-tab'; explorerTab.type='button'; explorerTab.setAttribute('role','tab'); explorerTab.setAttribute('aria-controls',explorer.id);
    tabs.append(inspectorTab,explorerTab); root.append(tabs,inspector,explorer); root.classList.add('timeline-tabbed-inspector');
    let active='inspector', explicitTab=false, dead=false, available=true, pending=null, focused=null, loaded=null, generation=0, txGeneration=0, pageGeneration=0, timer=null;
    const requests=new Set();
    const setTab = (name, explicit=true) => {
      if(dead)return;
      active=name==='explorer'?'explorer':'inspector'; if(explicit)explicitTab=true;
      inspector.hidden=active!=='inspector'; explorer.hidden=active!=='explorer';
      for(const [name,node] of [['inspector',inspectorTab],['explorer',explorerTab]]) { const selected=name===active; node.setAttribute('aria-selected',String(selected)); node.tabIndex=selected?0:-1; }
      root.dataset.activePane=active;
    };
    inspectorTab.addEventListener('click',()=>setTab('inspector'));
    explorerTab.addEventListener('click',()=>setTab('explorer'));
    tabs.addEventListener('keydown',event=>{
      if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;
      event.preventDefault(); const name=event.key==='Home'?'inspector':event.key==='End'?'explorer':active==='inspector'?'explorer':'inspector'; setTab(name); (name==='inspector'?inspectorTab:explorerTab).focus();
    });
    setTab('inspector',false);

    function cancel() {
      if(timer!==null){win.clearTimeout(timer);timer=null;}
      for(const request of requests)request.abort(); requests.clear();
      generation++; txGeneration++; pageGeneration++;
    }
    async function api(path, data, ticket) {
      const Controller=win.AbortController || AbortController, controller=new Controller(); requests.add(controller);
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
      } finally {win.clearTimeout(timeout);requests.delete(controller);}
    }
    const current = ticket => !dead && ticket===generation;
    function blank(message, state='idle', retry=false) {
      explorer.dataset.state=state;
      explorer.setAttribute('aria-busy',String(state==='loading'));
      explorer.replaceChildren();
      const box=element('div',undefined,'timeline-explorer-state'),title=element('h2',focused?'Block '+number(focused.height):'Block Explorer');
      const text=element('p',message,'small muted');text.setAttribute('role',state==='error'?'alert':'status');box.append(title,text);
      if(state==='loading')text.prepend(element('span',undefined,'loader'));
      if(retry){const button=element('button','Retry block');button.type='button';button.id='timeline-explorer-retry';button.addEventListener('click',()=>{if(focused)focus({...focused,immediate:true,refresh:true});});box.append(button);}
      explorer.append(box); explorer.scrollTop=0;
    }
    blank('Select a block or move the playhead to explore its data.');

    function health(ok) {
      if(dead || available===Boolean(ok))return;
      available=Boolean(ok);cancel();loaded=null;pending=null;
      if(!available)blank('Local index status is unavailable. Block loading is paused until status recovers.','paused');
      else if(focused)loadBlock(focused,generation);
      else blank('Select a block or move the playhead to explore its data.');
    }

    function focus(value) {
      if(dead || !value || !validHeight(value.height))return;
      const next={index:String(value.index || 'blocks'),height:value.height};
      if(!available) {
        cancel();focused=next;loaded=null;pending=null;
        blank('Local index status is unavailable. Block loading is paused until status recovers.','paused');
        return;
      }
      if(focused?.height===next.height && !value.refresh && !(value.immediate!==false && explorer.dataset.state==='error')) {
        focused=next;
        if(pending && value.immediate!==false){cancel();loaded=null;pending=null;loadBlock(next,generation);}
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
        loaded={block,elapsed:result.elapsed_ms};renderBlock(block,result.elapsed_ms,ticket);
      } catch(error) {if(current(ticket)&&error.name!=='AbortError')blank(error.message,'error',true);}
    }

    function renderBlock(block,elapsed,ticket) {
      if(!current(ticket))return;
      explorer.dataset.state='ready';
      explorer.setAttribute('aria-busy','false');
      explorer.innerHTML='<div class="timeline-explorer-content"><header class="timeline-explorer-heading"><div><div class="eyebrow">BITCOIN BLOCK</div><h2>Block '+esc(number(block.height))+'</h2></div><button type="button" id="timeline-explorer-refresh">Refresh block</button></header>'+
        '<section class="panel timeline-explorer-summary"><div class="actions mb"><span class="badge" title="Verification state. Header anchoring is not full consensus validation.">'+esc(String(block.verification_state||'unknown').replaceAll('_',' '))+'</span><span class="badge">'+esc(sourceName(block.evidence?.bytes||block.source_network))+'</span>'+(Number.isFinite(elapsed)?'<span class="small muted">'+esc(number(elapsed))+' ms server-side</span>':'')+'</div><p class="mono">'+esc(block.hash)+'</p><div class="stats"><div class="stat"><strong>'+esc(number(block.transaction_count))+'</strong><span>transactions</span></div><div class="stat"><strong>'+esc(bytes(block.serialized_bytes))+'</strong><span>serialized size</span></div><div class="stat"><strong>'+esc(number(block.total_output_sats))+'</strong><span>total output sats</span></div></div>'+
        pairs([['Time',block.time_iso],['Source',block.source_peer],['Verifier receipt',block.verification?.verifier_version || 'Needs recheck']])+evidence(block)+
        '<details><summary>Block header and dimensions</summary>'+pairs([['Previous block',block.previous_block_hash],['Merkle root',block.merkle_root],['Version',block.version],['Difficulty bits',block.bits],['Nonce',block.nonce],['Weight',block.weight],['Virtual size',block.vsize],['Inputs',block.input_count],['Outputs',block.output_count]])+'</details></section>'+
        '<section class="panel timeline-explorer-transactions"><div class="section-head"><h3>Transactions</h3><span class="small muted">Original block order</span></div><div id="timeline-explorer-tx-table"></div><div class="actions mt"><button type="button" id="timeline-explorer-tx-prev" disabled>Previous</button><button type="button" id="timeline-explorer-tx-next">Next 40</button><span class="small muted" id="timeline-explorer-tx-page"></span></div><p id="timeline-explorer-page-error" class="small" role="alert" hidden></p></section>'+
        '<section id="timeline-explorer-tx-detail" class="timeline-explorer-transaction" hidden></section></div>';
      explorer.scrollTop=0;
      explorer.querySelector('#timeline-explorer-refresh').onclick=()=>focus({...focused,immediate:true,refresh:true});
      let offset=0;
      const table=explorer.querySelector('#timeline-explorer-tx-table'),prev=explorer.querySelector('#timeline-explorer-tx-prev'),next=explorer.querySelector('#timeline-explorer-tx-next'),pageLabel=explorer.querySelector('#timeline-explorer-tx-page'),pageError=explorer.querySelector('#timeline-explorer-page-error');
      function renderPage(transactions,total,end) {
        table.innerHTML='<div class="table-wrap"><table><thead><tr><th>Index</th><th>Transaction</th><th>Output value</th><th></th></tr></thead><tbody>'+(transactions||[]).map(tx=>'<tr><td>'+esc(number(tx.index))+'</td><td class="mono">'+esc(short(tx.txid))+(tx.coinbase?' · coinbase':'')+'</td><td>'+esc(number(tx.output_sats))+' sats</td><td><button type="button" data-explorer-tx="'+esc(tx.index)+'">Inspect</button></td></tr>').join('')+'</tbody></table></div>';
        prev.disabled=offset===0;next.disabled=end>=total;pageLabel.textContent=total?(offset+1)+'–'+end+' of '+number(total):'No transactions';
        table.querySelectorAll('[data-explorer-tx]').forEach(button=>button.onclick=()=>{const index=Number(button.dataset.explorerTx);if(validHeight(index))loadTransaction(index,block,ticket);});
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
    }
    function evidence(block) {
      const record=block.evidence||{};
      return '<details><summary>Source, authority and verification</summary>'+pairs([['Locator',record.locator||block.locator_peer||'Not recorded'],['Bytes',sourceName(record.bytes||block.source_network)],['Byte provider',record.byte_provider||block.source_peer],['Chain authority',record.chain_authority||'Pending'],['Snapshot',validHeight(record.snapshot_height)?number(record.snapshot_height)+' · '+short(record.snapshot_hash):'Not yet anchored'],['Original byte source',record.original_bytes?sourceName(record.original_bytes):'Not recorded for a previous fetch'],['Original provider',record.original_provider||'Not recorded']])+'<details><summary>Technical verification receipt</summary><pre>'+esc(JSON.stringify({evidence:block.evidence,checks:block.verification},null,2))+'</pre></details></details>';
    }
    async function loadTransaction(index,block,ticket) {
      const detail=explorer.querySelector('#timeline-explorer-tx-detail');if(!detail)return;
      const requestTicket=++txGeneration;detail.hidden=false;detail.innerHTML='<p role="status"><span class="loader"></span>Loading transaction '+esc(number(index))+' in block '+esc(number(block.height))+'…</p>';
      try {
        // A block-position address resolves through its known block, never a standalone locator.
        const response=await api('/api/v1/navigate',{address:index+'.'+block.height+'.bitcoin'},ticket);
        if(!current(ticket)||requestTicket!==txGeneration)return;
        const record=response?.resource?.transaction,tx=record?.transaction;
        if(!tx || record.height!==block.height || tx.index!==index || record.block_hash!==block.hash)throw Error('The transaction did not match the selected block. Refresh the block and retry.');
        renderTransaction(detail,record,block,ticket,requestTicket);
      } catch(error) {
        if(!current(ticket)||requestTicket!==txGeneration||error.name==='AbortError')return;
        detail.innerHTML='<p role="alert">'+esc(error.message)+'</p><button type="button">Retry transaction</button>';detail.querySelector('button').onclick=()=>loadTransaction(index,block,ticket);
      }
    }
    function renderTransaction(detail,record,block,ticket,requestTicket) {
      const tx=record.transaction;
      detail.innerHTML='<section class="panel"><div class="section-head"><h3>Transaction '+esc(number(tx.index))+'</h3><button type="button" id="timeline-explorer-close-tx">Close details</button></div><p class="small muted">Block '+esc(number(block.height))+' · '+esc(record.verification_state||'Unknown verification')+'</p><p class="mono">'+esc(tx.txid)+'</p><div class="actions"><button type="button" data-explorer-copy="'+esc(tx.txid)+'">Copy transaction ID</button><button type="button" id="timeline-explorer-flow">Show value flow</button></div><div id="timeline-explorer-flow-result" class="flow-box" hidden></div><p id="timeline-explorer-flow-note" class="small muted" role="status" hidden></p>'+pairs([['Version',tx.version],['Lock time',tx.lock_time],['Size',bytes(tx.size)],['Weight',tx.weight],['Virtual size',tx.vsize],['Segwit',tx.segwit?'Yes':'No'],['Coinbase',tx.coinbase?'Yes':'No']])+'</section><div class="io-grid"><section><div class="eyebrow">INPUTS · '+esc((tx.inputs||[]).length)+'</div>'+(tx.inputs||[]).map(input=>'<article class="io"><div class="io-title"><strong>Input '+esc(input.n)+'</strong><span class="muted">'+(input.coinbase?'Coinbase':'Previous output')+'</span></div><p class="mono">'+(input.coinbase?'Subsidy and block fees':esc(input.prev_txid+':'+input.prev_vout))+'</p>'+(!input.coinbase?'<button type="button" data-explorer-copy="'+esc(input.prev_txid+':'+input.prev_vout)+'">Copy previous outpoint</button>':'')+'<details><summary>Input evidence</summary><pre>'+esc(JSON.stringify(input,null,2))+'</pre></details></article>').join('')+'</section><section><div class="eyebrow">OUTPUTS · '+esc((tx.outputs||[]).length)+'</div>'+(tx.outputs||[]).map(output=>'<article class="io"><div class="io-title"><strong>Output '+esc(output.n)+'</strong><span>'+esc(number(output.value_sats))+' sats</span></div><p class="mono muted">'+esc(output.address||output.type||'script')+'</p><div class="io-actions"><button type="button" data-explorer-copy="'+esc(tx.txid+':'+output.n)+'">Copy outpoint</button><button type="button" disabled title="Requires a later index">🔒 Spender</button><button type="button" disabled title="Requires a later index">🔒 Satline</button></div><details><summary>Output evidence</summary><pre>'+esc(JSON.stringify(output,null,2))+'</pre></details></article>').join('')+'</section></div><details class="panel"><summary>Full transaction evidence</summary><pre>'+esc(JSON.stringify(tx,null,2))+'</pre></details>';
      detail.querySelector('#timeline-explorer-close-tx').onclick=()=>{txGeneration++;detail.hidden=true;detail.replaceChildren();};
      detail.querySelectorAll('[data-explorer-copy]').forEach(button=>button.onclick=async()=>{try{await win.navigator.clipboard.writeText(button.dataset.explorerCopy);notify?.('Copied to clipboard.');}catch{notify?.('Clipboard access is unavailable. Select the displayed text to copy it.',true);}});
      const flowButton=detail.querySelector('#timeline-explorer-flow'),flow=detail.querySelector('#timeline-explorer-flow-result'),flowNote=detail.querySelector('#timeline-explorer-flow-note');
      flowButton.onclick=async()=>{
        flowButton.disabled=true;flowButton.textContent='Resolving input values…';
        try {
          const result=await api('/api/v1/flow',{txid:tx.txid,block_hash:block.hash,height:block.height},ticket);
          if(!current(ticket)||requestTicket!==txGeneration)return;
          flow.innerHTML=flowSVG(result);flow.hidden=false;flowNote.hidden=false;flowNote.textContent=result.note||'Unknown input values are not inferred.';
        } catch(error) {if(current(ticket)&&requestTicket===txGeneration&&error.name!=='AbortError'){flowNote.hidden=false;flowNote.textContent=error.message;}}
        finally {if(current(ticket)&&requestTicket===txGeneration){flowButton.disabled=false;flowButton.textContent='Refresh value flow';}}
      };
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
      if(dead)return;cancel();dead=true;
      while(inspector.firstChild)root.insertBefore(inspector.firstChild,tabs);
      tabs.remove();inspector.remove();explorer.remove();root.classList.remove('timeline-tabbed-inspector');delete root.dataset.activePane;
    }
    return {focus,health,teardown,setTab:name=>setTab(name),state:()=>({active,height:focused?.height,index:focused?.index,healthy:available,loading:explorer.dataset.state,loadedHeight:loaded?.block.height})};
  }
  return {mount};
})();
