'use strict';
// Profile editing changes configuration only. The shared timeline owns execution.
window.GatewayBitmapWorkspace = (() => {
  const known = n => Number.isSafeInteger(n) && n >= 0;
  const number = n => known(n) ? n.toLocaleString('en-GB') : 'Not established';
  const kindOf = value => value?.kind || (/^bitmap-parcels(?:-|$)/.test(value?.id || '') ? 'parcel' : /^bitmap-chain(?:-|$)/.test(value?.id || '') ? 'chain' : 'district');
  const instanceID = value => typeof value === 'string' && /^bitmap-(?:districts|parcels|chain)(?:-[0-9a-f]{32})?$/.test(value);
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
  function mountDelete({root,index,name,api,onDeleted}) {
    const {el,button}=helpers(root);let dead=false,serial=0,busy=false;
    const bin=button('',review,'index-delete-button');bin.title='Review deletion of '+name;bin.setAttribute('aria-label',bin.title);bin.innerHTML='<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M4 6h16M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7m4-7v7"/></svg>';
    const panel=el('section',undefined,'panel index-delete-review');panel.hidden=true;panel.setAttribute('aria-label','Review index deletion');root.replaceChildren(bin,panel);
    const list=(title,values)=>{if(!values?.length)return;panel.append(el('h4',title));const ul=el('ul');for(const value of values)ul.append(el('li',typeof value==='string'?value:[value.name||value.index,value.reason].filter(Boolean).join(' · ')));panel.append(ul);};
    async function review(){if(busy)return;busy=true;bin.disabled=true;panel.hidden=false;panel.replaceChildren(el('p','Checking the exact saved-data scope…','small'));const ticket=++serial;try{const value=await api('/api/v1/index/delete?'+new URLSearchParams({index}));if(dead||ticket!==serial)return;panel.replaceChildren(el('h3','Delete '+(value.name||name)+' index data?'),el('p',value.note||'This removes saved index data. It does not remove the track.','small'));list('Will be removed',value.scope);list('Will be kept',value.retained);list('Dependent saved indexes',value.dependencies);list('Cannot delete yet',value.blockers);const actions=el('div',undefined,'actions');actions.append(button('Keep index',()=>{serial++;panel.hidden=true;bin.focus();}));if(value.ready&&value.review_id){const confirm=button('Delete index data',async()=>{if(busy)return;busy=true;confirm.disabled=true;try{await api('/api/v1/index/delete',{index,review_id:value.review_id});if(dead||ticket!==serial)return;panel.replaceChildren(el('p','Saved index data deleted. The track remains available to build again.','small'));await onDeleted?.(index);}catch(error){if(!dead&&ticket===serial){panel.append(el('p',error.message+' Review again before retrying.','small bitmap-validation-issue'));confirm.disabled=true;}}finally{busy=false;}});confirm.className='danger';actions.append(confirm);}panel.append(actions);}catch(error){if(!dead&&ticket===serial)panel.replaceChildren(el('p',error.message,'small bitmap-validation-issue'));}finally{if(!dead){busy=false;bin.disabled=false;}}}
    return {teardown(){dead=true;serial++;root.replaceChildren();}};
  }
  function mountSettings({root, target = {}, api, onSaved, onInspect, onNavigate, timeline}) {
    const {el, button, field, select} = helpers(root), win = root.ownerDocument.defaultView;
    let dead = false, generation = 0, timer = null, registry = null, source = null, validation = null, validationKey = '', saving = false;
    let rules = {}, kind = target.kind === 'parcel' ? 'parcel' : 'district';
    const heading = el('h2', target.id ? 'Bitmap ruleset settings' : 'Add Bitmap ruleset'), note = el('p', 'One ruleset creates one track. Saving never starts indexing; use the timeline Arm and reviewed Play control.', 'small muted');
    const status = el('p', 'Reading saved rulesets…', 'small'); status.setAttribute('role', 'status');
    const content = el('div', undefined, 'bitmap-settings-content'); root.replaceChildren(heading, note, status, content);
    function issueList(value, holder) {
      holder.replaceChildren();
      for (const issue of value?.issues || []) { const item = el('p', (issue.rule ? issue.rule + ' · ' : '') + (issue.reason || issue.code || 'Review this rule'), 'bitmap-validation-issue small'); item.dataset.code = issue.code || ''; holder.append(item); }
      if (value?.state === 'ready') holder.append(el('p', 'Supported ruleset. Source and saved-state availability are checked separately when you review indexing.', 'small'));
      else if (value) holder.prepend(el('p', value.state === 'unsupported' ? 'This interpretation is not implemented. You can keep a draft; it cannot be armed.' : 'Resolve the configuration issues before creating an executable ruleset.', 'small'));
    }
    function render() {
      content.replaceChildren();
      source = [...(registry.instances || []),...(registry.drafts||[])].find(item => item.id === target.id) || null;
      if(target.id&&!source){status.textContent='This ruleset is not present in the saved registry. Refresh the workspace before changing it.';return;}
      if (source && !target.clone&&!source.draft) { renderSaved(); return; }
      if (source && source.kind !== 'chain') kind = kindOf(source);
      const type = select('Ruleset type', [['district', 'Districts'], ['parcel', 'Parcels']]); type.value = kind; type.disabled = !!source;
      const name = el('input'); name.type = 'text'; name.maxLength = 80; name.value = target.label || (source ? (source.label || 'Bitmap ruleset')+(source.draft?'':' copy') : ''); name.placeholder = 'Give this ruleset a name'; name.setAttribute('aria-label', 'Ruleset name');
      const district = select('Linked District ruleset', (registry.instances || []).filter(item => kindOf(item) === 'district').map(item => [item.id, item.label || item.id]));
      if (target.district_instance || source?.district_instance) district.value = target.district_instance || source.district_instance;
      const districtField = field('District ruleset this Parcel uses', district), linked = el('input'); linked.type = 'checkbox'; linked.checked = false; linked.setAttribute('role', 'switch'); linked.setAttribute('aria-label', 'Also add linked Parcel ruleset');
      const linkedLabel = field('Also add a linked Parcel ruleset', linked), parcelName = el('input'); parcelName.type = 'text'; parcelName.maxLength = 80; parcelName.setAttribute('aria-label', 'Linked Parcel ruleset name'); parcelName.placeholder = 'Parcel ruleset name';
      const parcelField = field('Linked Parcel name', parcelName), ruleList = el('fieldset', undefined, 'bitmap-rule-list'), issues = el('section', undefined, 'bitmap-validation'); issues.setAttribute('aria-label', 'Ruleset validation');
      const save = button('Save profile', () => submit('create'), 'primary'), draft = button('Save draft', () => submit('save_draft')), actions = el('div', undefined, 'actions'); actions.append(save, draft);
      content.append(field('Ruleset type', type), field('Ruleset name', name), districtField, linkedLabel, parcelField, ruleList, issues, actions);
      if (source?.legacy) content.insertBefore(el('p', 'The legacy profile has no editable named rule map. This clone explicitly starts from the reference preset; it does not claim to reproduce the legacy fingerprint.', 'small bitmap-validation-issue'), ruleList);
      function payload() { return {kind, label: name.value.trim(), rules: {...rules}, ...(kind === 'parcel' ? {district_instance: district.value} : {}), ...(kind === 'district' && linked.checked ? {linked_parcel: true, parcel_label: parcelName.value.trim() || (name.value.trim() + ' · Parcels')} : {})}; }
      function key() { return JSON.stringify(payload()); }
      function invalidate() { validation = null; validationKey = ''; save.disabled = true; issues.replaceChildren(el('p', 'Checking rule support…', 'small')); win.clearTimeout(timer); const ticket = ++generation; timer = win.setTimeout(() => validate(ticket), 180); }
      async function validate(ticket) {
        const body = payload(), current = JSON.stringify(body);
        try { const value = await api('/api/v1/bitmap/profiles/validate', body); if (dead || ticket !== generation || current !== key()) return; validation = value; validationKey = current; issueList(value, issues); save.disabled = saving || value.state !== 'ready' || !name.value.trim(); }
        catch (error) { if (!dead && ticket === generation) { save.disabled = true; issues.replaceChildren(el('p', error.message, 'small bitmap-validation-issue')); } }
      }
      function fillRules() {
        rules = {...(source && !source.legacy ? source.rules || source.profile?.answers || {} : registry.reference?.[kind] || {})};
        ruleList.replaceChildren(el('legend', 'Interpretation rules · 0 / 1'));
        for (const rule of registry.registry?.[kind] || []) {
          const row = el('label', undefined, 'bitmap-rule'), input = el('input'), detail = el('span'), title = el('strong', rule.question || rule.label || rule.id), bit = el('output', String(rules[rule.id] ?? rule.reference_value ?? 0), 'bitmap-rule-bit');
          if (!(rule.id in rules)) rules[rule.id] = rule.reference_value ?? 0;
          input.type = 'checkbox'; input.setAttribute('role', 'switch'); input.setAttribute('aria-label', (rule.question || rule.label || rule.id) + ' (' + rule.id + ')'); input.checked = rules[rule.id] === 1;
          detail.append(title, el('small', rule.id + ' · reference ' + rule.reference_value, 'block muted'));
          if (rule.description) detail.append(el('small', rule.description, 'block muted'));
          if (rule.unsupported_reason) detail.append(el('small', 'Unsupported choices: ' + rule.unsupported_reason, 'block muted'));
          input.onchange = () => { rules[rule.id] = input.checked ? 1 : 0; bit.value = String(rules[rule.id]); bit.textContent = bit.value; const supported = !Array.isArray(rule.supported_values) || rule.supported_values.includes(rules[rule.id]); row.dataset.supported = String(supported); invalidate(); };
          row.dataset.rule = rule.id; row.dataset.supported = String(!Array.isArray(rule.supported_values) || rule.supported_values.includes(rules[rule.id])); row.append(detail, input, bit); ruleList.append(row);
        }
        if (!(registry.registry?.[kind] || []).length) { save.disabled = true; ruleList.append(el('p', 'Named rule metadata is unavailable. A runnable profile cannot be created.')); }
      }
      function sync() { kind = type.value; districtField.hidden = kind !== 'parcel'; linkedLabel.hidden = kind !== 'district'; parcelField.hidden = kind !== 'district' || !linked.checked; }
      type.onchange = () => { sync(); fillRules(); invalidate(); }; linked.onchange = () => { sync(); invalidate(); }; for (const node of [name, parcelName]) node.oninput = invalidate; district.onchange = invalidate;
      async function submit(action) {
        if (saving || action === 'create' && (!validation || validation.state !== 'ready' || validationKey !== key())) return;
        saving = true; save.disabled = draft.disabled = true; status.textContent = action === 'create' ? 'Saving the new track configuration…' : 'Saving draft only…';
        const ticket = ++generation;
        try { const value = await api('/api/v1/bitmap/profiles', {...payload(), action}); if (dead || ticket !== generation) return; if (action === 'save_draft') { status.textContent = 'Draft saved. No track has been armed or started.'; } else { status.textContent = 'Profile saved. Its new track is Off; use the timeline to review indexing.'; await onSaved?.(value.instance, value); } }
        catch (error) { if (!dead && ticket === generation) status.textContent = error.message; }
        finally { if (!dead) { saving = false; draft.disabled = false; save.disabled = !validation || validation.state !== 'ready' || validationKey !== key(); } }
      }
      sync(); fillRules(); invalidate(); status.textContent = source ? 'Cloning creates a distinct track and leaves the original unchanged.' : 'Choose a preset interpretation, then save a separate track.';
    }
    function renderSaved() {
      const kind = kindOf(source), name = el('input'); name.type = 'text'; name.maxLength = 80; name.value = source.label || source.id; name.setAttribute('aria-label', 'Ruleset name');
      const actions = el('div', undefined, 'actions'); actions.append(button('Rename', async () => { const next = name.value.trim(); if (!next) return; try { await api('/api/v1/bitmap/profiles', {action: 'rename', id: source.id, label: next}); if (!dead) { status.textContent = 'Name saved. Interpretation and saved results are unchanged.'; await onSaved?.(source, {renamed:true}); } } catch (error) { if (!dead) status.textContent = error.message; } }));
      if (kind !== 'chain') actions.append(button('Clone ruleset', () => onInspect?.(source.id, {clone:true})));
      if (kind === 'district') actions.append(button('Add linked Parcel ruleset…', () => onInspect?.(null, {kind:'parcel',district_instance:source.id})));
      content.append(field('Track name', name), actions);
      if (kind === 'parcel') content.append(el('p', 'Pinned District: ' + ((registry.instances || []).find(item => item.id === source.district_instance)?.label || source.district_instance || 'Not established'), 'small'));
      if (kind === 'district') content.append(el('p', 'The Chain child is bound to this exact District interpretation. Its On switch is its Arm control. Live and derived-history retention are separate choices.', 'small muted'));
      if(kind!=='chain'){const answers=source.rules||source.profile?.answers;if(answers){const summary=el('details'),list=el('dl',undefined,'kv');summary.append(el('summary','Saved interpretation rules'));for(const rule of registry.registry?.[kind]||[])list.append(el('dt',rule.question||rule.id),el('dd',String(answers[rule.id]??'Not established')));summary.append(list);content.append(summary);}}
      if (kind === 'chain') {
        const draft = timeline?.getRunDraft?.().consumers?.find(c => c.index === source.id), retention = select('Chain history retention', [['full', 'Full — retain evaluated block history'], ['pruned', 'Pruned — current state and recovery checkpoints']]); retention.value = draft?.options?.retention || draft?.retention || source.retention || 'full';
        retention.onchange = () => { timeline?.setConsumerOptions?.(source.id, {retention:retention.value}); status.textContent = 'Retention staged. Review Play before applying it.'; };
        const advanced = el('details'); advanced.append(el('summary', 'Advanced Chain retention'), field('Derived Chain history', retention), el('p', 'Pruned retains the current cumulative result and recovery dependencies. It can remove older comparison evidence. Full does not recreate history previously discarded. Raw block retention is controlled separately by the Raw blocks track.', 'small muted')); content.append(advanced);
        const coverage=el('p','Reading actual retained Chain history…','small muted');advanced.append(coverage);const ticket=generation;api('/api/v1/bitmap/view?'+new URLSearchParams({kind:'chain',instance:source.id,expected_profile:source.profile_hash})).then(value=>{if(dead||ticket!==generation)return;const policy=value.retention;if(!policy){coverage.textContent='No saved Chain state. New Chain history defaults to Full until a different policy is reviewed.';return;}coverage.textContent='Saved policy: '+policy.mode+' · Recovery checkpoints: '+(policy.restore_points||[]).map(p=>number(p.height)).join(', ')+' · Retained query intervals: '+((policy.retained_intervals||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', ')||'None established')+'. '+(policy.note||'');}).catch(error=>{if(!dead&&ticket===generation)coverage.textContent='Retained Chain history unavailable: '+error.message;});
        content.append(el('p', 'Consumes completed outcomes from ' + ((registry.instances || []).find(item => item.id === source.district_instance)?.label || source.district_instance || 'its pinned District') + '. It cannot advance beyond that producer. Live can wait for newly completed saved outcomes while the producer is Off.', 'small'));
      }
      const details = el('details'), data = el('pre'); data.textContent = JSON.stringify(source, null, 2); details.append(el('summary', 'Advanced profile and saved coverage'), data); content.append(details);
      status.textContent = source.legacy ? 'Legacy reference instance. Cloning explicitly starts from the named reference preset.' : 'Saved interpretation is immutable. Clone it to change rules; rename changes only its label.';
    }
    api('/api/v1/bitmap/profiles').then(value => { if (!dead) { registry = value; render(); } }).catch(error => { if (!dead) status.textContent = error.message; });
    return {teardown() { dead = true; generation++; win.clearTimeout(timer); }};
  }
  function mountOverview({root, target = {}, api, onNavigate, onInspect, timeline, viewport, onResolved,onActivate}) {
    const {el, button, field, select} = helpers(root), win = root.ownerDocument.defaultView;
    let dead = false, generation = 0, timer = null, cursor = '', current = null, busy = false, registry = null, rows = [], totals = null,accumulated={compared_blocks:0,differing_blocks:0,unknown_blocks:0},batchBase={...accumulated},frontier=null,frontierHash=null,scope=null,pairedGeneration=0;
    const heading = el('h1', 'Bitmap interpretations'), status = el('p', 'Reading saved rulesets…', 'small'), catalogue = el('section', undefined, 'panel'), compare = el('section', undefined, 'panel bitmap-comparison'), summary = el('div'), list = el('div', undefined, 'bitmap-comparison-list'), more = button('Compare next saved blocks', () => load(true)), controls = el('div', undefined, 'bitmap-comparison-controls');
    status.setAttribute('role', 'status'); catalogue.append(el('h2', 'Ruleset tracks'), button('Add Bitmap ruleset…', () => onInspect?.(null, {})));
    const baseline = select('Baseline ruleset', []), variant = select('Variant ruleset', []), kind = select('Comparison type', [['district', 'Districts'], ['parcel', 'Parcels']]); kind.value = target.kind === 'parcel' ? 'parcel' : 'district'; controls.append(field('Compare', kind), field('Baseline', baseline), field('Variant', variant));
    const follow=button('Follow live comparison',()=>{delete target.height;follow.hidden=true;reset();},'text-button');follow.hidden=!known(target.height);compare.append(el('h2', 'Compare actual results'), controls, el('p', 'Reads completed saved results. Chain is optional. Missing, lagging and unresolved evidence is not agreement.', 'small muted'), button('Restart saved comparison',reset,'text-button'),follow,summary, list, more); more.hidden = true;list.setAttribute('role','list');list.setAttribute('aria-label','Compared saved blocks');
    root.replaceChildren(heading, status, catalogue, compare); root.classList.add('bitmap-workspace');
    const key = () => baseline.value + ':' + variant.value + ':' + kind.value;
    function updateAddress() { const p = new URLSearchParams({baseline:baseline.value,variant:variant.value,kind:kind.value});if(known(target.height))p.set('height',String(target.height)); onResolved?.('bitmap.gateway?' + p, {state:'comparison'}); }
    function clearComparison(){cursor='';current=null;rows=[];totals=null;accumulated={compared_blocks:0,differing_blocks:0,unknown_blocks:0};batchBase={...accumulated};frontier=null;frontierHash=null;scope=null;pairedGeneration++;compare.querySelector('.bitmap-paired-result')?.remove();list.replaceChildren();summary.replaceChildren();more.hidden=true;timeline?.setBitmapComparison?.(null);}
    function reset() { generation++;clearComparison();win.clearTimeout(timer);updateAddress();if(!busy)load();else timer=win.setTimeout(()=>load(),200); }
    function fillInstances() {
      const instances = (registry.instances || []).filter(item => kindOf(item) === kind.value), oldA = baseline.value || target.baseline, oldB = variant.value || target.variant;
      for (const node of [baseline, variant]) { node.replaceChildren(); for (const item of instances) { const option = el('option', item.label || item.id); option.value = item.id; node.append(option); } }
      if (instances.some(item => item.id === oldA)) baseline.value = oldA;
      variant.value = instances.some(item => item.id === oldB) ? oldB : instances.find(item => item.id !== baseline.value)?.id || baseline.value;
    }
    function showCatalogue() {
      for (const row of catalogue.querySelectorAll('.bitmap-instance-card')) row.remove();
      for (const item of registry.instances || []) {
        if (kindOf(item) === 'chain') continue;
        const row = el('article', undefined, 'bitmap-instance-card'), title = button(item.label || item.id, () => onInspect?.(item.id, {}), 'text-button'); row.append(title, el('span', kindOf(item) === 'parcel' ? 'Parcels' : 'Districts', 'badge'), el('p', item.state === 'unsupported' ? 'Unsupported draft' : item.state || 'Saved configuration', 'small muted')); catalogue.append(row);
      }
      if (!(registry.instances || []).length) catalogue.append(el('p', 'No saved Bitmap ruleset configurations are available.', 'small muted'));
      for (const draft of registry.drafts || []) { const row = el('article', undefined, 'bitmap-instance-card'); row.append(button(draft.label||'Open saved draft',()=>onInspect?.(draft.id,{draft:true}),'text-button'),el('p', 'Draft only · cannot be armed until its interpretation is supported.', 'small muted')); catalogue.append(row); }
    }
    function display(value, append) {
      current = value;totals={...value};for(const key of ['compared_blocks','differing_blocks','unknown_blocks'])totals[key]=known(value[key])?batchBase[key]+value[key]:null;root.dataset.comparisonState = value.state || 'unavailable';
      summary.replaceChildren(el('p', (known(value.common_frontier) ? 'Common completed frontier ' + number(value.common_frontier) : 'Common completed frontier not established') + ' · ' + number(totals.compared_blocks) + ' compared · Δ ' + number(totals.differing_blocks) + ' differing · ? ' + number(totals.unknown_blocks) + ' unknown', 'bitmap-comparison-summary'));
      summary.append(el('p','Inspected through '+number(value.compared_through)+' · request scope '+number(value.from)+'–'+number(value.to)+(value.has_more?' · more saved blocks remain in this comparison.':'.'),'small muted'));
      const lag = value.lag || {}; summary.append(el('p', 'Lag: baseline ' + number(lag.baseline) + ' blocks · variant ' + number(lag.variant) + ' blocks. ' + (value.note || ''), 'small muted'));
      const byHeight=new Map(rows.map(row=>[row.height,row]));for(const row of value.rows||[])byHeight.set(row.height,{...row,_snapshots:value.snapshots});rows=[...byHeight.values()].sort((a,b)=>a.height-b.height).slice(-300);list.replaceChildren();
      for (const row of rows) {
        const different = row.winner_difference === true || row.mapping_difference === true || row.audit_difference === true, unknown = !['equal','same','different','compared','ready'].includes(row.state) && !different;
        const item = el('article', undefined, 'bitmap-comparison-row');item.setAttribute('role','listitem'); item.dataset.state = unknown ? 'unknown' : different ? 'different' : 'equal';
        item.append(button((unknown ? '? ' : different ? 'Δ ' : '= ') + 'Block ' + number(row.height), () => paired(row), 'text-button'));
        item.append(el('p', unknown ? row.reason || row.state || 'Comparison evidence unavailable' : [row.winner_difference ? 'Winner differs' : '', row.mapping_difference ? 'Mapping differs' : '', row.audit_difference ? 'Candidate audit differs' : ''].filter(Boolean).join(' · ') || 'Saved results agree', 'small'));
        list.append(item);
      }
      if (!rows.length) list.append(el('p', value.state === 'ready' ? 'No rows in this saved interval.' : 'A comparable completed interval is not available.', 'small muted'));
      if(rows.length===300)list.prepend(el('p','Showing the latest 300 inspected blocks. Counters cover the accumulated compared scope.','small muted'));
      cursor = value.next_cursor || ''; more.hidden = !value.has_more || !cursor;
      if(!value.has_more&&known(value.common_frontier)&&(value.compared_through===undefined||value.compared_through===value.common_frontier)&&/^[0-9a-f]{64}$/i.test(value.common_frontier_hash||'')){frontier=value.common_frontier;frontierHash=value.common_frontier_hash;for(const key of ['compared_blocks','differing_blocks','unknown_blocks'])if(known(totals[key]))accumulated[key]=totals[key];}
      timeline?.setBitmapComparison?.({baseline:baseline.value,variant:variant.value,rows,common_frontier:value.common_frontier,lag:value.lag,differing_blocks:totals.differing_blocks,unknown_blocks:totals.unknown_blocks,onSelect:row=>paired(row)});
    }
    function paired(row, publish = true) {
      if(publish)onActivate?.();
      const auditTicket=++pairedGeneration;
      let detail = compare.querySelector('.bitmap-paired-result'); if (!detail) { detail = el('section', undefined, 'panel bitmap-paired-result'); compare.append(detail); }
      detail.replaceChildren(el('h3', 'Paired results · block ' + number(row.height)));
      const grid = el('div', undefined, 'bitmap-paired-grid');
      for (const side of ['baseline','variant']) { const box = el('article'), id = side === 'baseline' ? baseline.value : variant.value, name = (registry.instances || []).find(item=>item.id===id)?.label || side; box.append(el('h4', name)); const result = row[side]; if (!result) box.append(el('p', 'Saved evidence unavailable.')); else {box.append(el('p',String(result.state||'Saved outcome').replaceAll('_',' '),'small'));if(result.state==='complete'&&known(result.candidate_count))box.append(el('p',number(result.candidate_count)+' candidates evaluated','small'));const ids=result.ordered_winner_ids||(result.winner_id?[result.winner_id]:[]);box.append(el('h5','Selected claims'));if(ids.length)for(const winner of ids)box.append(button(String(winner),()=>onNavigate?.(String(winner)),'text-button mono'));else box.append(el('p',result.state==='complete'?'Evaluated: no selected claims.':'Selected claims not established.','small'));if(result.resolved_mappings?.length){box.append(el('h5','Resolved mappings'));for(const mapping of result.resolved_mappings){const target={kind:kind.value,district:mapping.district,transaction:mapping.transaction,instance:id};box.append(button((known(mapping.transaction)?mapping.transaction+'.':'')+mapping.district+'.bitmap',()=>onNavigate?.(address(target)),'text-button'));}}const advanced=el('details'),pre=el('pre');pre.textContent=JSON.stringify(result,null,2);advanced.append(el('summary','Advanced evidence and digests'),pre);box.append(advanced);} box.append(button('Inspect ruleset',()=>onInspect?.(id,{}))); grid.append(box); }
      for(const [offset,side]of ['baseline','variant'].entries()){const box=grid.children[offset],id=side==='baseline'?baseline.value:variant.value,snapshot=row._snapshots?.[side]||current?.snapshots?.[side],name=(registry.instances||[]).find(item=>item.id===id)?.label||side,audit=el('section'),auditRows=el('div'),auditStatus=el('p','','small muted');let auditCursor='',auditBusy=false;const read=button('Inspect '+name+' candidates',()=>loadAudit()),next=button('More '+name+' candidates',()=>loadAudit(true));read.disabled=!snapshot?.commitment;read.title=read.disabled?'The saved audit snapshot is unavailable.':'Read saved candidates, including losing and unresolved claims, at this exact comparison snapshot.';next.hidden=true;audit.append(read,auditStatus,auditRows,next);box.append(audit);async function loadAudit(append=false){if(auditBusy||!snapshot?.commitment)return;auditBusy=true;read.disabled=next.disabled=true;const p=new URLSearchParams({instance:id,snapshot:snapshot.commitment,height:String(row.height),limit:'25'});if(append&&auditCursor)p.set('cursor',auditCursor);try{const value=await api('/api/v1/bitmap/audit?'+p);if(dead||auditTicket!==pairedGeneration)return;if(value.instance!==id||value.height!==row.height||value.profile_hash!==snapshot.profile_hash||(value.snapshot&&value.snapshot.commitment!==snapshot.commitment))throw Error('The candidate audit belongs to a different saved interpretation or checkpoint.');if(value.state!=='complete'){auditCursor='';next.hidden=true;if(value.state==='not_retained'||value.state==='reset_required')auditRows.replaceChildren();auditStatus.textContent=String(value.state||'unavailable').replaceAll('_',' ')+' · '+(value.note||'Saved candidate evaluation is unavailable.')+(auditRows.childElementCount?' Previously read candidates remain tied to their saved checkpoint; no new candidates were counted.':'');return;}if(value.snapshot?.commitment!==snapshot.commitment)throw Error('The candidate audit snapshot is unavailable.');if(!append)auditRows.replaceChildren();auditStatus.textContent=number(value.candidate_count)+' saved candidates · '+(value.note||'');for(const candidate of value.rows||[]){const entry=el('article',undefined,'bitmap-candidate-audit');if(candidate.id)entry.append(button(candidate.id,()=>onNavigate?.(candidate.reveal_coordinate||candidate.id),'text-button mono'));entry.append(el('p',[candidate.eligibility,candidate.selection,candidate.reason].filter(Boolean).join(' · '),'small'));for(const rule of candidate.rule_results||[])entry.append(el('p',rule.id+' = '+rule.value+' · '+rule.result+' · '+rule.reason,'small muted'));auditRows.append(entry);}auditCursor=value.next_cursor||'';next.hidden=!value.has_more||!auditCursor;}catch(error){if(!dead&&auditTicket===pairedGeneration){auditCursor='';next.hidden=true;auditStatus.textContent=error.message;}}finally{auditBusy=false;read.disabled=!snapshot?.commitment;next.disabled=false;}}}
      detail.append(grid); viewport?.message?.('Paired Bitmap results at block ' + number(row.height) + '. Δ indicates a result difference; ? indicates missing evidence.', 'bitmap-comparison');
      // Keep focus and the playhead stable during polling. Only explicit selection opens details.
      if (publish && known(row.height)) onResolved?.('bitmap.gateway?' + new URLSearchParams({baseline:baseline.value,variant:variant.value,kind:kind.value,height:String(row.height)}), {state:'comparison',row});
    }
    async function load(append = false) {
      if (dead || busy) return;
      win.clearTimeout(timer);timer=null;
      if (!baseline.value || !variant.value || baseline.value === variant.value) { summary.replaceChildren(el('p', 'Choose two distinct saved ruleset tracks of the same type.', 'small')); return; }
      busy = true; more.disabled = true; const ticket = generation, selection = key();
      const continuing=!!cursor;const p = new URLSearchParams({baseline:baseline.value,variant:variant.value,limit:'50'}); if (continuing) p.set('cursor',cursor);else{batchBase={...accumulated};if(known(frontier)&&frontierHash){p.set('after_height',String(frontier));p.set('after_hash',frontierHash);}}
      if(known(target.height)){p.set('from',String(target.height));p.set('to',String(target.height));}else{if (known(target.from)) p.set('from',String(target.from)); if (known(target.to)) p.set('to',String(target.to));}
      try { const value = await api('/api/v1/bitmap/compare?' + p); if (dead || ticket !== generation || selection !== key()) return;if(value.state==='unavailable'){status.textContent=(value.note||'Source verification is unavailable.')+' Previously verified totals are held at their saved checkpoint; no new results were counted.';root.dataset.comparisonState='unavailable';return;}if(value.state==='scope_mismatch'){clearComparison();status.textContent=value.note||'These saved scopes cannot be compared.';root.dataset.comparisonState='scope_mismatch';return;}const nextScope=JSON.stringify(['baseline','variant'].map(side=>{const s=value.snapshots?.[side];return[s?.profile_hash,s?.segment_start];}));if(value.state==='reset_required'||scope&&scope!==nextScope){clearComparison();status.textContent='Source or interpretation changed. Prior comparison totals cleared; checking saved coverage again.';return;}scope=nextScope;display(value,continuing);status.textContent = 'Saved comparison updated. No indexing was started.'; if (known(target.height)) { const selected = rows.find(row=>row.height===target.height); if(selected&&!compare.querySelector('.bitmap-paired-result')) paired(selected,false); } }
      catch (error) { if (!dead && ticket === generation) {status.textContent=error.message+' Comparison has not advanced. Previously verified results remain tied to their saved checkpoint; restart the saved comparison if its cursor is no longer retained.';root.dataset.comparisonState='unavailable';if(!totals)summary.replaceChildren(el('p','Comparison unavailable. Missing evidence is not equality.','small bitmap-validation-issue'));} }
      finally { busy = false; more.disabled = false; if (!dead&&!known(target.to)&&!known(target.height))timer = win.setTimeout(() => { if (!root.ownerDocument.hidden) load(false); else timer = win.setTimeout(()=>load(false),4000); }, ticket===generation?4000:0); }
    }
    baseline.onchange = variant.onchange = reset; kind.onchange = () => { fillInstances(); reset(); };
    api('/api/v1/bitmap/profiles').then(value => { if (dead) return; registry = value; showCatalogue(); fillInstances(); status.textContent = 'Select tracks to compare. Their index controls remain on the timeline.';updateAddress();load(); }).catch(error => { if (!dead) {status.textContent=error.message;onResolved?.('bitmap.gateway',{state:'unavailable'});} });
    return {teardown() { dead = true; generation++; win.clearTimeout(timer); timeline?.setBitmapComparison?.(null); }};
  }
  return {kindOf, instanceID, address, mountSettings, mountOverview,mountDelete};
})();
