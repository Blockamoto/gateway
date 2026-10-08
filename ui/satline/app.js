/* Satline presents results; Bitcoin/ordinal calculations remain in the Go engine. */
(() => {
  'use strict';
  const escapeHTML = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const pointText = p => p?.txid ? `${p.txid}:${p.vout}:${p.offset}` : '';
  const pointCoordinate = p => p?.txid && p.height >= 0 && p.tx_index >= 0 ? `${p.offset}.${p.vout}.${p.tx_index}.${p.height}.bitcoin` : '';
  const stateLabel = state => ({CURRENTLY_UNSPENT:'Unspent at snapshot',UNRESOLVED:'Unresolved',UNMINED:'Not yet mined',STEP_LIMIT:'Ready for next hop',PAUSED:'Paused',STALE_CHAIN:'Chain changed',LOST_AT_BIRTH:'Lost at birth',LOST_UNCLAIMED_COINBASE:'Unclaimed coinbase',LOST_DUPLICATE_TXID:'Duplicate-txid loss',INVALID:'Invalid input',MODULE_DISABLED:'Module disabled',STORAGE_ERROR:'Storage error'}[state] || 'Resolving');
  if (typeof module !== 'undefined') module.exports = {escapeHTML,pointText,pointCoordinate,stateLabel};
  if (typeof document === 'undefined') return;
  const $ = id => document.getElementById(id), e=escapeHTML;
  const fmt = n => n === null || n === undefined ? '—' : Number(n).toLocaleString('en-GB');
  const short = s => String(s||'').length > 26 ? `${s.slice(0,12)}…${s.slice(-9)}` : String(s||'');
  const byteText = n => n < 1024 ? `${n} B` : n < 1048576 ? `${(n/1024).toFixed(1)} KB` : `${(n/1048576).toFixed(1)} MB`;
  const state={input:'',result:null,job:'',shown:50,network:null,published:false,checked:false,elapsed:0,expanded:new Set(),explanations:new Map(),returnScroll:0};
  async function api(path,body) {
    const ctrl=new AbortController(),timer=setTimeout(()=>ctrl.abort(),60000);
    try { const response=await fetch(`/api/v1/satline${path}`,{method:body===undefined?'GET':'POST',headers:body===undefined?{}:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:ctrl.signal,cache:'no-store'});
      const value=await response.json();if(!response.ok) throw new Error(value.error||`Request failed (${response.status})`);return value;
    } finally {clearTimeout(timer);}
  }
  function message(text,error=false,working=false){$('message').hidden=!text;$('message').className=`notice${error?' error':''}${working?' working':''}`;$('message').textContent=text||'';}
  function toast(text){$('toast').textContent=text;$('toast').hidden=false;setTimeout(()=>$('toast').hidden=true,2300);}
  function saveView(){try{sessionStorage.setItem('satline-view',JSON.stringify({input:state.input,scroll:window.scrollY,shown:state.shown,expanded:[...state.expanded]}));}catch{}}
  function bitcoinLink(query,label,hop){return `<a data-bitcoin="${e(query)}" data-hop="${hop??''}" href="/?resolve=${encodeURIComponent(query)}&from_satline=${encodeURIComponent(state.input)}&satline_hop=${hop??''}">${e(label)} ↗</a>`;}
  function pointLinks(p){if(!p?.txid)return '';const coordinate=pointCoordinate(p);return `<div class="evidence-actions">${coordinate?bitcoinLink(coordinate,'Open satpoint'):''}${p.height>=0?bitcoinLink(`${p.height}.bitcoin`,'Open block'):''}<button data-copy="${e(pointText(p))}">Copy satpoint</button></div>`;}
  function updateControls(){const busy=!!state.job,r=state.result;for(const id of ['resolve','step'])$(id).disabled=busy;$('stop').hidden=!busy;
    $('next').disabled=busy||!r?.current_satpoint||['CURRENTLY_UNSPENT','UNMINED','INVALID','MODULE_DISABLED','STORAGE_ERROR'].includes(r.state)||String(r.state||'').startsWith('LOST_');
    $('fetchPeer').disabled=busy||!state.network?.use_peers||!$('query').value.trim();$('publish').disabled=busy||!r?.current_satpoint||!state.checked;
    $('unpublish').hidden=!state.published;$('publish').textContent=state.published?'Republish this snapshot':'Publish this snapshot';
    $('publicationStatus').textContent=state.published?'Published snapshot':'Private by default';
  }
  function startNode(r){const birth=r.mode==='sat',p=birth?r.birth_satpoint:r.start_satpoint;if(!p?.txid)return '';
    return `<article class="node start" id="start-node"><header><span class="eyebrow">${birth?'BIRTH':'STARTING SATPOINT'}</span><span class="tag">${birth?'GLOBAL SAT NUMBER':'FORWARD FOLLOW'}</span></header><h3>${birth?`Sat ${fmt(r.sat_number)}`:'Start from this output'}</h3><div class="point">${e(pointText(p))}</div><p class="muted small">Block ${fmt(p.height)} · output ${fmt(p.vout)} · offset ${fmt(p.offset)}${birth&&r.issuance?`<br>Subsidy position ${fmt(r.issuance.subsidy_offset)} of ${fmt(r.issuance.subsidy_sats)} sats`:'<br>Birth history and global sat number have not been resolved.'}</p>${pointLinks(p)}</article>`;
  }
  function renderHop(h,i){const fee=h.type==='fee_to_coinbase',dest=h.destination?.txid?`vout ${h.destination.vout} : ${fmt(h.destination.offset)}`:'unclaimed value';const p=h.source;const consumed=`i${h.spending_vin}.${h.destination?.tx_index??0}.${h.block_height}.bitcoin`;
    // The destination index is not the spending transaction index for fee hops.
    const spendLink=fee?h.spending_txid:`i${h.spending_vin}.${h.destination.tx_index}.${h.block_height}.bitcoin`;
    let arithmetic=`<div class="formula">Preceding input value + source offset<br>${fmt(Number(h.input_stream_position)-Number(p.offset))} + ${fmt(p.offset)} = <span class="target">${fmt(h.input_stream_position)} sats</span></div>`;
    if(fee) arithmetic+=`<div class="formula">Input position − ordinary outputs = fee offset<br>${fmt(h.input_stream_position)} − ${fmt(Number(h.input_stream_position)-Number(h.fee_offset))} = <span class="target">${fmt(h.fee_offset)}</span><br><br>Subsidy + preceding fees + fee offset<br>${fmt(Number(h.coinbase_stream_offset)-Number(h.prior_block_fees_sats||0)-Number(h.fee_offset||0))} + ${fmt(h.prior_block_fees_sats||0)} + ${fmt(h.fee_offset||0)} = <span class="target">${fmt(h.coinbase_stream_offset)}</span><br>Coinbase stream position → ${e(dest)}</div>`;
    else arithmetic+=`<div class="formula">FIFO destination<br>Output ${fmt(h.destination.vout)} · exact offset <span class="target">${fmt(h.destination.offset)}</span></div>`;
    return `<details class="node hop${fee?' fee':''}" id="hop-${i}" data-index="${i}" ${state.expanded.has(i)?'open':''}><summary><header><span class="hop-title"><span class="hop-id">${String(i+1).padStart(2,'0')}</span>${fee?'Fee → coinbase':'Output → output'}</span><span class="chevron" aria-hidden="true">›</span></header><div class="hop-flow"><span class="endpoint">vout ${fmt(p.vout)} : ${fmt(p.offset)}</span><span class="arrow">${fee?'→ fee →':'→'}</span><span class="endpoint">${e(dest)}</span></div><div class="hop-metadata"><span>Block ${fmt(h.block_height)} · ${e(short(h.spending_txid))}</span><span class="verification">${e((h.verification_state||'unavailable').replaceAll('_',' '))}</span></div></summary><div class="details-content"><div class="detail-grid"><div><span class="eyebrow">SOURCE SATPOINT</span><strong>${e(pointText(p))}</strong></div><div><span class="eyebrow">DESTINATION SATPOINT</span><strong>${e(pointText(h.destination)||'No assigned output')}</strong></div><div><span class="eyebrow">CONSUMING INPUT</span><strong>vin ${fmt(h.spending_vin)}</strong></div><div><span class="eyebrow">BITCOIN BLOCK</span><strong>${e(h.block_hash)}</strong></div></div>${arithmetic}<p class="muted small">Spender provider: ${e(h.spender_provider||'local evidence')}<br>${e(h.note||'')}</p><div class="evidence-actions">${bitcoinLink(spendLink,fee?'Open spending transaction':'Open consuming input',i)}${h.destination?.txid?bitcoinLink(pointCoordinate(h.destination),'Open destination',i):''}${bitcoinLink(`${h.block_height}.bitcoin`,'Open block',i)}<button data-copy="${e(h.spending_txid)}">Copy txid</button><button data-explain="${i}">Inspect input / output values</button></div><div class="explanation" id="explain-${i}">${state.explanations.get(i)||''}</div></div></details>`;
  }
  function terminalNode(r){const s=r.operational_reason?'PAUSED':(r.state||'RESOLVING'),lost=s.startsWith('LOST_'),unresolved=['UNRESOLVED','STALE_CHAIN','STEP_LIMIT','PAUSED','RESOLVING'].includes(s);let title=stateLabel(s),text=r.note||'',p=r.current_satpoint;
    if(s==='CURRENTLY_UNSPENT'){title=state.checked?'Current satpoint':'Stored endpoint';text=`Unspent in the confirmed chain through block ${fmt(r.snapshot?.height)}. ${state.checked?'':'Stored snapshot is awaiting an anchor refresh.'}`;}
    if(s==='UNRESOLVED')title='Lineage unresolved';if(s==='STEP_LIMIT')title='Ready for the next hop';if(s==='UNMINED')text=`Expected issuance block ${fmt(r.expected_issuance_height)}. Selected chain tip: ${fmt(r.chain_tip_height)}.`;
    return `<article class="node terminal${lost?' lost':''}${unresolved?' unresolved':''}" id="terminal"><div class="eyebrow">${lost?'LOSS EVENT':s==='CURRENTLY_UNSPENT'?'SNAPSHOT-RELATIVE ENDPOINT':s==='UNMINED'?'NOT YET ISSUED':'RESOLUTION STATE'}</div><h3>${e(title)}</h3>${p?.txid?`<div class="point">${e(pointText(p))}</div>`:''}<p class="muted small">${e(text)}</p>${r.snapshot?`<p class="mono muted small">${e(r.snapshot.hash)}</p>`:''}${r.pending_mempool_spend?`<div class="privacy-note">Pending mempool spend: ${e(r.pending_mempool_spend)}<br>Not part of this confirmed lineage.</div>`:''}${s==='UNRESOLVED'?'<div class="evidence-actions"><a href="/?settings=1">Inspect Bitcoin graph coverage ↗</a></div>':''}${pointLinks(p)}</article>`;
  }
  function renderResult(r){if(!r)return;const prevCount=state.result?.hops?.length||0,nearBottom=window.innerHeight+window.scrollY>document.body.scrollHeight-200;if(JSON.stringify(state.result?.hops)!==JSON.stringify(r.hops))state.explanations.clear();state.result=r;const hops=Array.isArray(r.hops)?r.hops:[];
    $('empty').hidden=true;$('summary').hidden=false;$('summary').innerHTML=`<div class="summary-item"><span class="eyebrow">${r.mode==='sat'?'GLOBAL SAT NUMBER':'SATPOINT FOLLOW'}</span><strong class="${r.mode==='sat'?'':'mono'}">${r.mode==='sat'?fmt(r.sat_number):e(short(pointText(r.start_satpoint)||state.input))}</strong></div><div class="summary-item"><span class="eyebrow">CONFIRMED HOPS</span><strong>${fmt(hops.length)}</strong></div><div class="summary-item"><span class="eyebrow">STATE</span><strong>${e(r.operational_reason?String(r.operational_reason).replaceAll('_',' '):stateLabel(r.state))}</strong></div>`;
    const shown=Math.min(hops.length,state.shown);$('timeline').innerHTML=startNode(r)+hops.slice(0,shown).map(renderHop).join('')+(shown===hops.length?terminalNode(r):'');
    $('more').hidden=shown===hops.length;$('more').textContent=`Show ${Math.min(50,hops.length-shown)} more hops (${fmt(hops.length-shown)} remaining)`;
    $('lineLabel').textContent=`${fmt(r.persistence?.historical_hops_skipped||0)} stored hops reused · ${fmt(r.persistence?.new_hops_resolved||0)} new hops`;
    $('metrics').hidden=false;$('metricsBody').innerHTML=`<dl><dt>Underlying evidence</dt><dd>${e((r.verification_state||'unavailable').replaceAll('_',' '))}</dd><dt>Persisted locally</dt><dd>${r.persistence?.stored?'Yes':'Not confirmed'}</dd><dt>Stored hops reused</dt><dd>${fmt(r.persistence?.historical_hops_skipped||0)}</dd><dt>New hops</dt><dd>${fmt(r.persistence?.new_hops_resolved||0)}</dd><dt>Elapsed</dt><dd>${(state.elapsed/1000).toFixed(2)} s</dd><dt>Storage schema</dt><dd>v${r.persistence?.storage_schema||1}</dd></dl>`;
    updateControls();if(nearBottom&&hops.length>prevCount)requestAnimationFrame(()=>document.getElementById(`hop-${Math.min(hops.length-1,state.shown-1)}`)?.scrollIntoView({block:'nearest',behavior:'smooth'}));
  }
  async function loadStored(input){try{const d=await api(`/ui/record?input=${encodeURIComponent(input)}`);state.published=!!d.published;state.checked=false;renderResult(d.result);return true;}catch{return false;}}
  async function run(operation='resolve'){
    const input=$('query').value.trim();if(!input){message('Enter a sat number or satpoint.',true);return;}if(state.job)return;
    if(operation==='rebuild'&&!confirm('Recalculate this record from the beginning? Other lineages and Bitcoin data are untouched.'))return;
    if(input!==state.input){state.input=input;state.result=null;state.shown=50;state.expanded.clear();state.explanations.clear();state.published=false;state.checked=false;await loadStored(input);}
    history.replaceState(null,'',`/satline?input=${encodeURIComponent(input)}`);saveView();
    try{message(operation==='peer'?'Requesting a bounded peer segment. It will not enter the line until local checks pass.':'Checking stored anchors and preparing Satline…',false,true);
      const job=await api('/ui/run',{input,operation,peer:$('peer').value.trim(),max_hops:operation==='next'?1:0});state.job=job.id;updateControls();poll(job.id);
    }catch(err){message(err.message,true);updateControls();}
  }
  async function poll(id){if(state.job!==id)return;try{
    const job=await api(`/ui/job?id=${encodeURIComponent(id)}`);state.elapsed=job.elapsed_ms||0;
    if(job.result?.mode){state.checked=job.result.state!=='STALE_CHAIN'&&!job.result.operational_reason;renderResult(job.result);}
    message(job.error||job.stage,!!job.error,!job.done);
    if(job.done){state.job='';updateControls();if(!job.error){message(`${stateLabel(job.result?.state)} · ${fmt(job.result?.hop_count||0)} confirmed hops${job.operation==='peer'?' · peer segment locally checked':''}.`);}
      await refreshRecents();await refreshNetwork();await refreshModule();saveView();return;}
    setTimeout(()=>poll(id),600);
  }catch(err){message(`${err.message}. Completed checkpoints remain local.`,true);state.job='';updateControls();}}
  async function refreshRecents(){try{const d=await api('/ui/records');$('recents').innerHTML=d.records.length?d.records.map(r=>`<button class="recent${r.query.input===state.input?' selected':''}" data-open="${e(r.query.input)}"><span class="title">${r.query.kind==='sat'?'Sat ':''}${e(r.query.input)}</span><small>${e(r.operational_reason?String(r.operational_reason).replaceAll('_',' '):stateLabel(r.state))} · ${fmt(r.hop_count)} hops${r.published?' · published':''}</small></button>`).join(''):'<p class="muted small">Only the sats you query create records here.</p>';}catch(err){$('recents').textContent=err.message;}}
  async function refreshModule(){try{const m=await api('/status');$('moduleStatus').textContent=m.enabled?(m.ready?'Satline ready':'Satline storage needs attention'):'Satline disabled';$('storeStatus').textContent=`${fmt(m.stored_sats)} sats · ${fmt(m.stored_satpoint_follows)} follows · ${byteText(m.storage_bytes||0)}`;if(!m.enabled)message('Satline is disabled. Enable it in Gateway settings to resolve or exchange history.',true);if(m.error)message(m.error,true);}catch(err){$('moduleStatus').textContent='Module unavailable';}}
  async function refreshNetwork(){try{const n=await api('/network');state.network=n;$('usePeers').checked=!!n.use_peers;$('servePublished').checked=!!n.serve_published;$('networkBadge').textContent=n.advertised?'SATLINE WIRE v1 · OPT-IN':'PEER EXCHANGE OFF';$('networkBadge').className=`pill${n.advertised?' on':''}`;
    $('peerList').innerHTML=(n.peers||[]).map(p=>`<option value="${e(p.addr)}"></option>`).join('');const s=n.stats||{};$('networkStats').innerHTML=`<dt>Wire protocol</dt><dd>Satline v1</dd><dt>Published records</dt><dd>${fmt(n.published_records||0)}</dd><dt>Segments served</dt><dd>${fmt(s.segments_served||0)}</dd><dt>Segments accepted</dt><dd>${fmt(s.segments_accepted||0)}</dd><dt>Locally checked hops</dt><dd>${fmt(s.hops_verified||0)}</dd><dt>Requests not accepted</dt><dd>${fmt(s.segments_rejected||0)}</dd>`;
    $('listenStatus').textContent=n.serve_published&&n.listener?`Gateway listener: ${n.listener}`:'Satline publication serving is off.';updateControls();
  }catch(err){message(err.message,true);}}
  async function explain(i){const el=$(`explain-${i}`);if(!el)return;el.textContent='Reading Bitcoin input and output values…';try{const d=await api('/ui/explain',{input:state.input,hop:i});const h=state.result.hops[i];
    let html='<div class="stream-label">INPUT STREAM</div>'+d.inputs.map(v=>`<div class="stream-row${v.selected?' selected':''}"><span>vin ${v.index}</span><span>${v.selected?'Target enters here':''}</span><span>${v.value===null?'unavailable':fmt(v.value)+' sats'}</span></div>`).join('');if(d.truncated_inputs)html+='<p class="muted small">First 64 inputs shown; open Bitcoin on Demand for the full transaction.</p>';
    html+='<div class="stream-label">OUTPUT STREAM</div>'+d.outputs.map(v=>`<div class="stream-row${h.type!=='fee_to_coinbase'&&v.n===h.destination.vout?' selected':''}"><span>vout ${v.n}</span><span>${h.type!=='fee_to_coinbase'&&v.n===h.destination.vout?'Target +'+fmt(h.destination.offset):''}</span><span>${fmt(v.value_sats)} sats</span></div>`).join('');
    if(h.type==='fee_to_coinbase')html+='<div class="stream-label">COINBASE OUTPUTS</div>'+d.coinbase_outputs.map(v=>`<div class="stream-row${h.destination?.txid&&v.n===h.destination.vout?' selected':''}"><span>vout ${v.n}</span><span>${h.destination?.txid&&v.n===h.destination.vout?'Target +'+fmt(h.destination.offset):''}</span><span>${fmt(v.value_sats)} sats</span></div>`).join('');
    state.explanations.set(i,html);el.innerHTML=html;
  }catch(err){el.textContent=err.message;}}
  $('queryForm').addEventListener('submit',ev=>{ev.preventDefault();run('resolve');});$('step').onclick=()=>run('start');$('next').onclick=()=>run('next');$('fetchPeer').onclick=()=>run('peer');
  $('stop').onclick=async()=>{try{await api('/ui/cancel',{id:state.job});message('Stopping at the last completed checkpoint…',false,true);}catch(err){message(err.message,true);}};
  $('query').oninput=()=>{const v=$('query').value.trim();$('inputHelp').textContent=/^\d+$/.test(v)?'Global sat number: resolve birth and forward history.':v?'Satpoint follow: forward history only; birth is not inferred.':'A sat number starts at birth. A satpoint starts at that output.';updateControls();};
  $('example').onclick=()=>{$('query').value='0';run('start');};$('more').onclick=()=>{state.shown+=50;renderResult(state.result);};$('refreshRecents').onclick=refreshRecents;
  $('saveNetwork').onclick=async()=>{try{await api('/network',{use_peers:$('usePeers').checked,serve_published:$('servePublished').checked});await refreshNetwork();toast('Network settings saved. Private records stay private.');}catch(err){message(err.message,true);}};
  $('publish').onclick=async()=>{if(!state.input||!confirm('Publish a snapshot of this specific lineage? Peers may request it while serving is enabled. Future hops are not automatically published.'))return;try{await api('/publish',{input:state.input,publish:true});state.published=true;updateControls();refreshRecents();refreshNetwork();toast('This snapshot is published.');}catch(err){message(err.message,true);}};
  $('unpublish').onclick=async()=>{try{await api('/publish',{input:state.input,publish:false});state.published=false;updateControls();refreshRecents();refreshNetwork();toast('Publication withdrawn from this client.');}catch(err){message(err.message,true);}};
  $('timeline').addEventListener('toggle',ev=>{const d=ev.target;if(d instanceof HTMLDetailsElement&&d.dataset.index!==undefined){const i=Number(d.dataset.index);if(d.open)state.expanded.add(i);else state.expanded.delete(i);saveView();}},true);
  document.addEventListener('click',async ev=>{const target=ev.target.closest('[data-open],[data-copy],[data-explain],[data-action],[data-bitcoin]');if(!target)return;
    if(target.dataset.bitcoin!==undefined){saveView();return;}
    if(target.dataset.open!==undefined){if(state.job){toast('Stop the active job before opening another record.');return;}$('query').value=target.dataset.open;await run('start');return;}
    if(target.dataset.explain!==undefined){explain(Number(target.dataset.explain));return;}
    if(target.dataset.copy!==undefined){try{await navigator.clipboard.writeText(target.dataset.copy);toast('Copied.');}catch{message('Clipboard unavailable; select and copy the displayed value.',true);}return;}
    const action=target.dataset.action;if(!action)return;target.closest('details')?.removeAttribute('open');
    if(action==='refresh'||action==='recheck'||action==='rebuild'){run(action==='refresh'?'resolve':action);return;}
    if(action==='copy-query'){try{await navigator.clipboard.writeText(state.input);toast('Copied.');}catch{}return;}
    if(action==='remove'&&state.result&&!state.job&&confirm('Remove this Satline record and withdraw its publication? Bitcoin data is retained.')){try{const kind=state.result.mode,key=state.result.persistence?.record_key;if(!key)throw new Error('Refresh the record before removing it.');await api('/cache/remove',kind==='sat'?{sat_number:state.input}:{satpoint:state.input});state.result=null;state.published=false;$('timeline').innerHTML='';$('summary').hidden=true;$('metrics').hidden=true;$('empty').hidden=false;updateControls();refreshRecents();refreshNetwork();message('Satline record removed.');}catch(err){message(err.message,true);}}
  });
  window.addEventListener('pagehide',saveView);
  async function boot(){await Promise.all([refreshRecents(),refreshModule(),refreshNetwork()]);try{const m=await fetch('/api/v1/meta').then(r=>r.json());$('version').textContent=m.app_version?`v${m.app_version} · LOCAL PROTOCOL STACK`:'LOCAL PROTOCOL STACK';$('footer-version').textContent='Gateway Client'+(m.app_version?' '+m.app_version:'');}catch{}
    const params=new URLSearchParams(location.search),input=params.get('input');if(input){$('query').value=input;state.input=input;try{const saved=JSON.parse(sessionStorage.getItem('satline-view')||'null');if(saved?.input===input){state.shown=saved.shown||50;state.expanded=new Set(saved.expanded||[]);state.returnScroll=saved.scroll||0;}}catch{}
      if(await loadStored(input)){message('Stored local history loaded. Checking chain anchors before continuing…',false,true);await run('start');setTimeout(()=>window.scrollTo(0,state.returnScroll),150);}else{await run('start');}}
  }
  boot();
})();

// Trusted module-to-shell navigation retains the selected lineage in the parent.
if(typeof location !== 'undefined' && typeof document !== 'undefined' && new URLSearchParams(location.search).has('embedded')){
 document.body.classList.add('embedded');
 document.addEventListener('click',event=>{const link=event.target.closest('a');if(!link)return;const u=new URL(link.href,location.href);if(u.origin!==location.origin)return;const address=u.searchParams.get('resolve');if(address){event.preventDefault();parent.postMessage({type:'gateway:navigate',address,from:document.getElementById('query').value},location.origin);}else if(u.pathname==='/'&&u.searchParams.has('settings')){event.preventDefault();parent.postMessage({type:'gateway:navigate',address:'settings.gateway'},location.origin);}},true);
}
