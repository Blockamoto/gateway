'use strict';
// Workspace views use the same local APIs as the CLI. No network work starts
// from a status card; explicit actions retain their own engine authorization.
window.GatewayWorkspace = (() => {
 const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const num=v=>typeof v==='number'?v.toLocaleString('en-GB'):String(v??'unknown');
 const route=(address,label,primary=false)=>`<button class="${primary?'primary':'quiet'}" data-route="${esc(address)}">${esc(label)}</button>`;
 function servingStatus(view){
  if(!view)return '';
  const port=Number.isSafeInteger(view.listen_port)&&view.listen_port>0?view.listen_port:0;
  const text=view.enabled?'Bitcoin serving is on'+(port?' · port '+port:''):(view.error||view.requested)?'Bitcoin serving is unavailable':'Bitcoin serving is off';
  const note=view.enabled&&view.automatic_port?'The default port is busy, so Gateway chose an available port.':!view.enabled?String(view.error||''):'';
  return `<p class="small muted" data-bitcoin-serving-status><strong>${esc(text)}</strong>${note?' '+esc(note):''}</p>`;
 }
 // Keep labels stable while this shell is open, including refreshes and route
 // changes. Addresses are not written to browser storage. The sequence also
 // makes every alias distinct, even after the friendly words repeat.
 const peerAliases=new Map();
 function peerAlias(address){
  const key=String(address||'');
  if(!peerAliases.has(key)){
   const id=peerAliases.size+1;
   const colors=['Amber','Silver','Copper','Jade','Indigo','Golden','Coral','Azure'];
   const birds=['Finch','Wren','Robin','Heron','Swift','Owl','Lark','Crane'];
   peerAliases.set(key,{id,label:colors[(id-1)%colors.length]+' '+birds[Math.floor((id-1)/colors.length)%birds.length]+' '+String(id).padStart(2,'0')});
  }
  return peerAliases.get(key);
 }
 function jobCard(j){return `<section class="panel current-work"><div><div class="eyebrow">${j.id?'LATEST INDEX JOB':'READY WHEN YOU ARE'}</div><h2>${j.id?esc(({'blocks':'Bitcoin Blocks','bitmap':'Bitmap districts','inscriptions':'Inscriptions','sat-state':'Sats','txo-spender':'Spender','transaction-locator':'Transaction locations'})[j.index]||j.index)+' <span class="badge">'+esc(j.state)+'</span>':'No index job started'}</h2><p class="muted">${j.id?'Blocks '+num(j.from)+'–'+num(j.to)+' · '+esc(j.retention)+' sources · '+(j.height>=j.from?'Committed through '+num(j.height):'No block committed yet'):'Choose a bounded range. Reviewing a plan does not fetch data.'}</p>${j.error?'<p class="small muted">'+esc(j.error)+'</p>':''}</div><div class="actions">${route('indexes.gateway',j.id?(j.state==='complete'?'Inspect results':'Review job & resume'):'Review a plan →',true)}</div></section>`;}
 function retentionCards(){return `<div class="grid mb retention-cards"><section class="panel"><div class="eyebrow">EPHEMERAL</div><h3>Keep the result</h3><p class="muted">Derived records and checkpoints remain. No new raw source blocks are cached.</p></section><section class="panel"><div class="eyebrow">CACHE</div><h3>A bounded working set</h3><p class="muted">Cache source blocks within your limit. Eviction does not erase the index.</p></section><section class="panel"><div class="eyebrow">RETAIN</div><h3>Deliberate source storage</h3><p class="muted">Keep fetched sources outside cache eviction. External Core files stay read-only.</p></section></div><div class="actions mb">${route('indexes.gateway','Choose retention in an index plan',true)}${route('settings.gateway','Cache & privacy settings')}</div>`;}
 async function home(v){const {root,api,heading,isCurrent,schedule}=v;
  root.innerHTML=heading('GATEWAY','What is live right now.','Resolve from the address bar. Maintain only what you choose.')+`
   <section id="home-state" class="panel home-state" aria-live="polite"><span class="loader"></span>Reading current state…</section>
   <div class="home-doorways grid two mt"><button class="tile" data-route="indexes.gateway"><span class="tile-symbol">▦</span><h3>Indexes</h3><p>What you have, what you keep live, and what you can get.</p><small>OPEN INDEXES →</small></button><button class="tile" data-route="peers.gateway"><span class="tile-symbol">⋈</span><h3>Peers</h3><p>Your connections and the data they can provide.</p><small>OPEN PEERS →</small></button></div>`;
  async function load(){try{const [ix,p,n]=await Promise.all([api('/api/v1/index/status'),api('/api/v1/settings'),api('/api/v1/network')]);if(!isCurrent())return;
   const h=n.headers||{}, settings=p.settings||{}, live=(ix.live||[]).filter(x=>x.enabled), gateway=n.gateway_connected||0, bitcoin=n.connected||0;
   const headerState=h.header_state||'unknown';
   const headerFresh=headerState==='current'&&n.enabled===true&&bitcoin>0&&!settings.headers_paused&&!h.syncing&&!h.error;
   const suffix=Number.isFinite(h.header_height)&&h.header_height>=0?' · '+num(h.header_height):'';
   const headerText=headerFresh?'Headers caught up'+suffix:n.enabled===false?'Headers offline'+suffix:settings.headers_paused?'Headers paused'+suffix:headerState==='current'?'Header freshness unknown'+suffix:'Headers '+headerState.replaceAll('_',' ')+suffix;
   const liveCurrent=x=>x.state==='synced'&&x.tip_fresh===true&&x.lag_known===true&&x.lag===0;
   const liveText=live.length?live.map(x=>{const name=x.index==='bitmap'?'Bitmap':x.index==='inscriptions'?'Inscriptions':x.index;const state=liveCurrent(x)?'caught up':x.state==='synced'?'freshness unknown':String(x.state||'unknown').replaceAll('_',' ');return `${name} ${state}${x.lag_known&&x.lag>0?' · '+num(x.lag)+' blocks behind':''}`}).join(' · '):'Nothing is being maintained yet.';
   const job=ix.job||{}, issues=(ix.errors||[]).map(String);
   const text=`<div class="home-line"><span class="home-dot ${headerFresh?'ok':''}" aria-hidden="true"></span><strong>${esc(headerText)}</strong><small>${bitcoin?num(bitcoin)+' Bitcoin peer'+(bitcoin===1?'':'s')+' connected':'No Bitcoin peer currently connected'}</small></div><div class="home-line"><span class="home-dot ${live.length&&live.every(liveCurrent)?'ok':''}" aria-hidden="true"></span><strong>${esc(liveText)}</strong><small>${job.id?esc((job.index||'Index')+' job '+(job.state||'unknown')+(job.height>=0?' · through '+num(job.height):'')):'No index job running'}${job.error?'<br>'+esc(job.error):''}</small></div><div class="home-line"><span aria-hidden="true">🔒</span><strong>Gateway peerhood is locked</strong><small>Bitcoin peers supply headers and blocks in this testing build. Additional peer features will be enabled separately.</small></div>${issues.length?'<p class="hint">'+esc(issues.join('; '))+'</p>':''}`;
   const state=root.querySelector('#home-state');if(state.innerHTML!==text)state.innerHTML=text;
  }catch(e){if(isCurrent())root.querySelector('#home-state').innerHTML=`<div class="home-line"><strong>Status unavailable</strong><small>${esc(e.message)}</small></div>`;}finally{if(isCurrent())schedule(load);}}
  await load();
 }
 async function connections(v){const {root,api,heading,isCurrent,schedule,notice}=v;
  root.innerHTML=heading('YOUR NODE / CONNECTIONS','Bitcoin connections.','Bitcoin peers supply headers and requested blocks.')+`<div id="connection-summary" class="status-grid mb"><div class="status-cell">Checking connections…</div></div><section class="panel" aria-labelledby="gateway-peerhood-title"><h2 id="gateway-peerhood-title"><span aria-hidden="true">🔒</span> Gateway peerhood</h2><p>Locked in this testing build. Gateway-to-Gateway connections and sharing will be enabled after separate testing.</p><button type="button" disabled aria-describedby="peerhood-lock-note" title="Gateway peerhood is locked in this testing build.">Connect to a Gateway peer</button><p id="peerhood-lock-note" class="small muted">Your saved peer settings are retained. Bitcoin connections below remain available.</p></section><div class="actions mt"><button id="refresh-peers" type="button">Retry Bitcoin connections</button></div><div id="peer-data" class="mt" aria-live="polite"></div>`;
  const $=id=>root.querySelector('#'+id);
  const revealed=new Set();
  async function load(){try{const p=await api('/api/v1/peers?core=1');if(!isCurrent())return;const n=p.network||{};
   $('connection-summary').innerHTML=`<div class="status-cell"><div class="eyebrow">BITCOIN</div><strong>${num(n.connected||0)} connections</strong><small>For headers and requested block data</small>${servingStatus(p.listener)}</div><div class="status-cell"><div class="eyebrow">GATEWAY PEERHOOD</div><strong>🔒 Locked</strong><small>Unavailable in this testing build</small></div>`;
   const peers=(p.connections||[]).map(x=>({...x,alias:peerAlias(x.address)}));
   const focus=root.ownerDocument.activeElement;
   const focusID=$('peer-data').contains(focus)?focus.id:'';
   const detailsOpen=$('peer-diagnostics')?.open;
   $('peer-data').innerHTML=`<section class="panel"><h2>Active Bitcoin connections</h2>${peers.length?`<p class="small muted">Select an alias to show or hide its address. Aliases stay the same while this app page is open.</p><div class="table-wrap"><table><thead><tr><th>Peer / route</th><th>State</th><th>Protocols</th><th>Action</th></tr></thead><tbody>${peers.map(x=>`<tr><td><button type="button" class="quiet" id="peer-alias-${x.alias.id}" data-peer-alias="${x.alias.id}" aria-expanded="${revealed.has(x.address)}" aria-controls="peer-address-${x.alias.id}" aria-label="${revealed.has(x.address)?'Hide':'Show'} address for ${esc(x.alias.label)}">${esc(x.alias.label)}</button><span id="peer-address-${x.alias.id}" class="block mono" ${revealed.has(x.address)?'':'hidden'}>${esc(x.address)}</span><small class="block muted">${esc(x.discovery_source||x.direction||'inbound')}</small></td><td>${esc(x.state)}</td><td>${esc((x.protocols||[]).join(', ')||'Connecting')}</td><td><button type="button" id="peer-disconnect-${x.alias.id}" data-disconnect="${x.alias.id}" aria-label="Disconnect ${esc(x.alias.label)}">Disconnect</button></td></tr>`).join('')}</tbody></table></div>`:'<div class="empty"><h3>No active connections</h3><p>Check that outbound Bitcoin connections are enabled in Settings. Local data remains available offline.</p></div>'}${n.bootstrap_error?'<p class="hint">Some connection attempts failed. Open connection details below for more information.</p>':''}<details id="peer-diagnostics" ${detailsOpen?'open':''}><summary>Connection details, recent attempts and Core-owned peers (includes addresses)</summary><p class="small muted">Core manages its own connections.</p><pre>${esc(JSON.stringify({bootstrap_error:n.bootstrap_error||'',recent:n.recent||[],core_peers:p.core_peers||[],core_error:p.core_error||''},null,2))}</pre></details></section>`;
   for(const x of peers){
    const button=$('peer-alias-'+x.alias.id);
    button.onclick=()=>{
     const show=!revealed.has(x.address);
     if(show)revealed.add(x.address);else revealed.delete(x.address);
     $('peer-address-'+x.alias.id).hidden=!show;
     button.setAttribute('aria-expanded',String(show));
     button.setAttribute('aria-label',(show?'Hide':'Show')+' address for '+x.alias.label);
    };
    $('peer-disconnect-'+x.alias.id).onclick=()=>act(()=>api('/api/v1/peers',{action:'disconnect',address:x.address}));
   }
   if(focusID)$(focusID)?.focus({preventScroll:true});
  }catch(e){if(isCurrent())notice(e.message,true)}finally{if(isCurrent())schedule(load)}}
  async function act(fn){try{await fn();if(isCurrent())await load()}catch(e){if(isCurrent())notice(e.message,true)}}
  $('refresh-peers').onclick=()=>act(()=>api('/api/v1/peers',{action:'refresh'}));await load();
 }
 function organizeSettings(root){
  const body=root.querySelector('#settings-body'),form=body?.querySelector('#settings-form');if(!form)return;
  const groups=[...form.querySelectorAll(':scope > section'),...body.querySelectorAll(':scope > section')];
  const labels=['Data providers','Privacy & storage','Browser access','Desktop integration','Updates'];
  const nav=document.createElement('nav');nav.className='settings-sections';nav.setAttribute('aria-label','Settings sections');
  const select=i=>{groups.forEach((g,n)=>g.hidden=n!==i);form.hidden=i>=2;[...nav.children].forEach((b,n)=>b.setAttribute('aria-pressed',String(n===i)))};
  groups.forEach((g,i)=>{const b=document.createElement('button');b.type='button';b.textContent=labels[i]||'Advanced';if(g.dataset.settingsSection)b.dataset.settingsSection=g.dataset.settingsSection;b.onclick=()=>select(i);nav.appendChild(b)});
  body.prepend(nav);select(0);
 }
 return {home,connections,jobCard,retentionCards,organizeSettings,servingStatus};
})();
