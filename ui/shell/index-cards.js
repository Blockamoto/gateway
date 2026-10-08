'use strict';
// Cards project real definition/provider/instance state. They do not create
// placeholder indexes or automatically fetch advertised peer claims.
window.GatewayIndexCards = (() => {
  const active = job => ['running', 'waiting', 'pausing'].includes(job?.state);
  const owns = (job,id) => job && (job.index === id || (job.outputs || []).includes(id));
  function jobFor(id,snapshot) {
    const seen = new Set(), all = [...(snapshot.jobs || []), ...(snapshot.job?.id ? [snapshot.job] : [])].filter(j=>{if(seen.has(j.id))return false;seen.add(j.id);return owns(j,id)});
    const job = all.find(active) || all.find(j=>j.state==='queued') || all.filter(j=>j.state==='paused').at(-1) || all.at(-1) || null;
    const progress = job?.progress?.find(p=>p.index===id);
    return progress ? {...job,...progress,id:job.id,parent_index:job.index,index:id,group_state:job.state,state:['failed','complete'].includes(progress.state)?progress.state:['paused','pausing','cancelled'].includes(job.state)?job.state:job.state==='yielded'?'catching_up':progress.state||job.state} : job;
  }
  const names = {synced:'Caught up', catching_up:'Catching up', waiting:'Waiting', paused:'Paused', offline_unknown:'Freshness unknown', stale_reorg:'Chain changed', error:'Needs attention', off:'Ready'};
  const format = n => Number.isSafeInteger(n) && n >= 0 ? n.toLocaleString('en-GB') : 'Unknown';
  const initialFrom = (definition, policy) => Number.isSafeInteger(policy?.initial_from) && policy.initial_from >= 0 ? policy.initial_from : definition.id === 'inscriptions' && definition.network === 'bitcoin-mainnet' ? 767430 : definition.start_height;
  function model(definition, snapshot) {
    const instance = (snapshot.instances || []).find(x => x.definition === definition.id);
    const policy = (snapshot.live || []).find(x => x.index === definition.id);
    const job = jobFor(definition.id, snapshot);
    const providers = (snapshot.providers || []).filter(x => x.id === definition.id);
    const retained = Boolean(instance?.checkpoint);
    const on = typeof policy?.on === 'boolean' ? policy.on : !policy?.stopped && !policy?.paused && (retained || active(job) || job?.state==='queued' || Boolean(policy?.updated) || Boolean(policy?.enabled));
    let state = retained ? 'Ready' : 'Not built';
    if (policy?.enabled) state = policy.state === 'synced' && !(policy.tip_fresh && policy.lag_known && policy.lag === 0) ? 'Freshness unknown' : names[policy.state] || 'Unknown';
    if (active(job)) state = job.state === 'waiting' ? 'Waiting for data' : job.state === 'pausing' ? 'Stopping' : 'Indexing';
    if (job?.state === 'queued') state = 'Queued';
    if (job?.state === 'catching_up') state = 'Coverage incomplete';
    if (job?.state === 'failed') state = 'Needs attention';
    if (instance?.verification?.includes('stale_reorg')) state = 'Chain changed';
    if (instance?.verification?.includes('anchor_unavailable')) state = 'Anchor unavailable';
    if (!active(job) && job?.state !== 'queued' && (policy?.paused || job?.state === 'paused' && !policy?.enabled)) state = 'Paused';
    if (policy?.stopped) state = active(job) ? 'Stopping' : 'Off';
    else if (!retained && on && !policy?.enabled && !active(job) && !['queued','catching_up','failed'].includes(job?.state)) state = 'Ready to build';
    if (!definition.buildable) state = definition.id === 'bitmap-compatibility' ? 'Limited eligibility analysis' : ['address-history','inscription-numbering'].includes(definition.id) ? 'Not implemented' : ['sat-state','satline'].includes(definition.id) ? 'On-demand derivation; coverage required' : providers.some(x => x.queryable) ? 'Resolver enabled; check coverage' : 'Needs coverage or provider';
    const locked = Boolean(definition.locked);
    if (locked) state = '🔒 Locked for testing';
    return {definition, instance, policy, job, providers, retained, on:!locked && on, live:!locked && Boolean(policy?.enabled), initialFrom:initialFrom(definition, policy), state, locked};
  }
  function mount({document:doc, primary, other, workspace, api, refresh, select, notify}) {
    const entries = new Map();
    let snapshot = {}, selected = '', healthy = false, busy = false;
    const el = (tag, text, cls) => { const n = doc.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
    function switchControl(id, text, hint) {
      const label = el('label', undefined, 'index-switch');
      const input = el('input'); input.type = 'checkbox'; input.setAttribute('role', 'switch'); input.setAttribute('aria-label', id + ' ' + text); input.setAttribute('aria-describedby', 'card-help-' + id);
      const track = el('span', undefined, 'switch-track'); track.setAttribute('aria-hidden', 'true');
      label.append(el('span', text), input, track); label.title = hint;
      return {label, input, text:label.children[0]};
    }
    function create(definition) {
      const article = el('article', undefined, 'index-card'); article.dataset.index = definition.id;
      const title = el('h2', definition.name); title.id = 'card-title-' + definition.id;
      article.setAttribute('aria-labelledby', title.id);
      const state = el('span', 'Loading', 'badge');
      const heading = el('div', undefined, 'index-card-heading'); heading.append(title, state);
      const summary = el('p', definition.theory, 'index-card-description');
      const meta = el('p', '', 'index-card-meta');
      const detail = el('p', '', 'index-card-detail');
      const progress = el('progress'); progress.hidden=true; progress.setAttribute('aria-label',definition.name+' committed progress');
      const progressText = el('p','','index-card-progress-text');
      const help = el('p', '', 'index-card-help'); help.id = 'card-help-' + definition.id;
      const controls = el('div', undefined, 'index-card-controls');
      const on = switchControl(definition.id, 'On', 'Allow indexing work. Turning off keeps saved records and sharing unchanged.');
      const live = switchControl(definition.id, 'Live', 'Turn On, catch up the applicable history, then follow new blocks. This may fetch many Bitcoin blocks; it does not publish your data.');
      if (definition.buildable) controls.append(on.label, live.label);
      const open = el('button', 'Details & records', 'quiet'); open.type = 'button'; open.addEventListener('click', () => {
        const m = model(definition, snapshot);
        if (m.locked) { const e=entries.get(definition.id);e.lockExpanded=!e.lockExpanded;e.error.textContent=definition.lock_reason;e.error.hidden=!e.lockExpanded;e.open.setAttribute('aria-expanded',String(e.lockExpanded));return; }
        select(selected===definition.id?'':definition.id, selected===definition.id?'catalog':definition.buildable ? m.retained ? 'results' : 'build' : 'providers');
      });
      const error = el('p', '', 'index-card-error'); error.setAttribute('role', 'status'); error.id='card-lock-'+definition.id;
      const expansion = el('div',undefined,'index-card-workspace'); expansion.hidden=true;
      article.append(heading, summary, meta, detail, progress, progressText, controls, help, open, error, expansion);
      const entry = {article, title, state, summary, meta, detail, help, on:on.input, onText:on.text, live:live.input, open, error, progress, progressText, expansion};
      on.input.addEventListener('change', () => change(definition, 'on', on.input.checked));
      live.input.addEventListener('change', () => change(definition, 'live', live.input.checked));
      ((!definition.locked && (definition.buildable || definition.id === 'headers')) ? primary : other).append(article);
      entries.set(definition.id, entry);
      return entry;
    }
    function render(value = snapshot) {
      snapshot = value;
      for (const definition of snapshot.definitions || []) {
        const e = entries.get(definition.id) || create(definition), m = model(definition, snapshot);
        const expanded=selected===definition.id;
        e.article.dataset.selected = String(expanded);e.expansion.hidden=!expanded;e.open.setAttribute('aria-expanded',String(expanded));
        e.article.setAttribute('aria-busy', String(busy));
        e.state.textContent = healthy ? m.state : 'Status unavailable';
        e.title.textContent = definition.name; e.summary.textContent = definition.theory;
        const height = m.instance?.checkpoint?.height;
        e.meta.textContent = m.retained ? (m.instance.coverage?.length ? 'Coverage '+m.instance.coverage.map(r=>format(r.from)+'–'+format(r.to)).join(', ') : 'Saved through block ' + format(height)) : definition.buildable ? 'No committed local records' : m.providers.length + ' local provider' + (m.providers.length === 1 ? '' : 's');
        if (m.retained && m.policy?.lag_known && m.policy.tip_fresh) e.meta.textContent += ' · ' + format(m.policy.lag) + ' blocks behind';
        else if (m.live && m.retained) e.meta.textContent += ' · lag unknown';
        const retention = m.policy?.retention || m.instance?.retention || m.job?.retention || 'ephemeral';
        const retentionText = {ephemeral:'Discard new source blocks',cache:'Bounded source cache',retain:'Retain source blocks'}[retention] || 'Retention unknown';
        e.detail.textContent = definition.buildable ? retentionText + ' · private local data; sharing locked' : (m.providers.some(x => x.queryable) ? 'Use existing providers; no whole-history build is implied.' : 'No usable local provider. Inspect details rather than starting unsupported work.');
        e.on.checked = m.on; e.onText.textContent=m.on?'On':'Off'; e.live.checked = m.live;
        const j=m.job, known=j && Number.isSafeInteger(j.to) && j.to>=j.from;
        e.progress.hidden=!known;if(known){e.progress.max=j.to-j.from+1;e.progress.value=Math.max(0,Math.min(j.to,j.height)-j.from+1)}
        e.progressText.textContent=j?.state==='queued'?'Queue position '+(j.queue_position||'pending')+' · '+(j.waiting_reason||'Waiting for the active job'):j?.id?'Build '+format(j.from)+'–'+format(j.to)+' · '+(j.height>=j.from?'committed through '+format(j.height):'No block committed yet'):'';
        if (m.live) {
          e.progress.hidden = true;
          e.progressText.textContent = 'Live target: follow the chain tip · tip ' + format(m.policy?.tip_height) + (m.policy?.tip_fresh ? ' (fresh)' : ' (freshness unknown)') + (j?.id ? ' · Current job boundary: ' + format(j.to) + ' · Committed through ' + format(j.height) : ' · Waiting for the next processing batch');
        }
        e.on.disabled = !healthy || busy || !definition.buildable;
        e.live.disabled = !healthy || busy || !definition.buildable;
        e.help.textContent = !definition.buildable ? 'Provider-managed capability' : !m.retained ? 'Live catches up all applicable history from block ' + format(m.initialFrom) + '. Source blocks are discarded by default; results stay private.' : !m.on ? 'Saved records remain readable. Switch On to allow new work.' : m.live ? 'Live continues from saved progress and follows new blocks. Results stay private.' : 'On, with Live off: build only the ranges you choose.';
        e.error.textContent = m.policy?.error || m.job?.error || ''; e.error.hidden=false;
        e.open.textContent = expanded ? 'Close details' : definition.buildable ? m.retained ? 'Details & records' : 'Review a build' : 'Inspect capability';
        e.open.disabled = busy;
        if (m.locked) {
          e.article.dataset.locked = 'true';
          e.article.setAttribute('aria-describedby', e.help.id);
          e.article.title = definition.lock_reason;
          e.on.disabled = true; e.live.disabled = true;
          e.help.textContent = definition.lock_reason;
          e.detail.textContent = m.retained ? 'Saved records are retained while this feature is locked.' : 'Available in a later testing stage.';
          e.progress.hidden = true; e.progressText.textContent = ''; e.error.textContent = e.lockExpanded ? definition.lock_reason : ''; e.error.hidden=!e.lockExpanded;
          e.open.textContent = '🔒 Why is this locked?';
          e.open.disabled=false;
          e.open.setAttribute('aria-expanded',String(!!e.lockExpanded));
          e.open.setAttribute('aria-controls',e.error.id);
          e.open.title = definition.lock_reason;
          e.open.setAttribute('aria-describedby', e.help.id);
        }
      }
    }
    async function change(definition, control, checked) {
      if (!healthy || busy || definition.locked) { render(); return; }
      const m = model(definition, snapshot), e = entries.get(definition.id);
      render(); // Do not display an unacknowledged preference as saved.
      busy = true; render();
      try {
        const action = control === 'on' ? checked ? 'start' : 'stop' : checked ? 'enable' : 'disable';
        const result = await api('live', {index:definition.id, action});
        snapshot = {...snapshot, live:[...(snapshot.live || []).filter(x => x.index !== definition.id), result]};
        const saved = model(definition, snapshot);
        notify(control === 'on' ? checked ? 'On saved. With Live off, choose a range to build.' : 'Indexing stopped. Saved records and publication are unchanged.' : checked ? 'On and Live saved. Gateway catches up from ' + (m.retained ? 'saved progress' : 'block ' + format(saved.initialFrom)) + ', then follows new blocks. Results stay private.' : 'Live disabled. A separately requested one-off build is not cancelled.');
        await refresh();
      } catch (error) {
        notify(error.message, true);
      } finally { busy = false; render(); }
    }
    return {render, select:id => {selected = id;const e=entries.get(id);if(e&&workspace){e.expansion.append(workspace);workspace.hidden=false}render();}, health:ok => {healthy = ok; render();}, model:id => model((snapshot.definitions || []).find(x=>x.id===id) || {id}, snapshot)};
  }
  return {mount, model, jobFor};
})();
