(function (global) {
  'use strict';
  // Draft validation and registry writes never start an index run.
  const initial = () => ({format:'gateway-recipe',version:1,id:'my-reveals',name:'My reveals',source:'bitcoin.reveal',origin:0,history:'sparse',dependencies:[],tables:[{name:'reveals',key:'id'}],steps:[{op:'write',table:'reveals',row:{id:{field:['ref']}}}]});
  const keys = (o, allowed) => o && typeof o === 'object' && !Array.isArray(o) && Object.keys(o).every(k => allowed.includes(k));
  const path = e => keys(e,['field']) && Array.isArray(e.field) && e.field.length && e.field.every(x => typeof x === 'string' && x && !x.includes('.')) ? e.field.join('.') : null;
  const fromPath = (s, label) => {
    const pieces=s.split('.');
    if (!s || pieces.some(x=>!x)) throw new Error(label+' needs a dot-separated field path.');
    return {field:pieces};
  };
  const call=(name,...args)=>({call:name,args});
  const literal=value=>({literal:value});
  const field=(...parts)=>({field:parts});
  const variable=name=>({variable:name});
  const same=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
  const scalar=value=>value===null || typeof value==='string' || typeof value==='boolean' || typeof value==='number' && Number.isSafeInteger(value);
  const comparable=scalar;
  function toGuided(d) {
    if (!keys(d,['format','version','id','name','source','origin','history','dependencies','tables','steps']) || d.format!=='gateway-recipe' || d.version!==1 || d.source!=='bitcoin.reveal' || d.history!=='sparse' || !Array.isArray(d.dependencies) || d.dependencies.length || !Array.isArray(d.tables) || d.tables.length!==1 || !Array.isArray(d.steps)) return null;
    const table=d.tables[0];
    if (!keys(table,['name','key']) || typeof table.name!=='string' || typeof table.key!=='string' || !table.name || !table.key) return null;
    let filterMode='field',filterPath='',filterKey='',filterValue='',write;
    if (d.steps.length===1) write=d.steps[0];
    else {
      let filter;
      if (d.steps.length===3) {
        const letStep=d.steps[0],val=letStep?.value;
        const key=val?.args?.[0]?.args?.[1]?.literal;
        const expected=call('cbor.scalar',call('cbor.key',call('cbor.decode',field('metadata_hex')),literal(key)));
        if (!keys(letStep,['op','as','value']) || letStep.op!=='let' || letStep.as!=='metadata_value' || typeof key!=='string' || !key || !same(val,expected)) return null;
        filterMode='metadata';filterKey=key;filter=d.steps[1];write=d.steps[2];
      } else if(d.steps.length===2) {filter=d.steps[0];write=d.steps[1];}
      else return null;
      const test=filter?.test, guard=test?.args?.[0], equals=test?.args?.[1], operand=equals?.args?.[0], value=equals?.args?.[1];
      if (!keys(filter,['op','test']) || filter.op!=='require' || !keys(test,['call','args']) || test.call!=='and' || !Array.isArray(test.args) || test.args.length!==2 || !keys(value,['literal']) || !Object.hasOwn(value,'literal')) return null;
      if (filterMode==='metadata') {
        if (!scalar(value.literal) || !same(test,call('and',call('valid',variable('metadata_value')),call('eq',variable('metadata_value'),value)))) return null;
      } else if (Array.isArray(operand?.field) && operand.field.length===4 && same(operand.field.slice(0,3),['properties','attributes','traits']) && typeof operand.field[3]==='string' && operand.field[3]) {
        filterMode='trait';filterKey=operand.field[3];
        if (!scalar(value.literal) || !same(test,call('and',call('present',operand),call('eq',operand,value)))) return null;
      } else {
        filterPath=path(operand);
        if (!comparable(value.literal) || !filterPath || !same(test,call('and',call('present',operand),call('eq',operand,value)))) return null;
      }
      filterValue=JSON.stringify(value.literal);
    }
    if (!keys(write,['op','table','row']) || write.op!=='write' || write.table!==table.name || !keys(write.row,Object.keys(write.row||{}))) return null;
    const cols=Object.keys(write.row), keyPath=path(write.row[table.key]);
    if (!keyPath || cols.length<1 || cols.length>2 || !cols.includes(table.key)) return null;
    const extra=cols.find(x=>x!==table.key), valuePath=extra ? path(write.row[extra]) : '';
    if (extra && !valuePath) return null;
    if (typeof d.id!=='string' || typeof d.name!=='string' || !Number.isSafeInteger(d.origin) || d.origin<0) return null;
    return {id:d.id,name:d.name,origin:String(d.origin),table:table.name,key:table.key,keyPath,filterMode,filterPath,filterKey,filterValue,valueColumn:extra||'',valuePath};
  }
  function fromGuided(g) {
    const origin=Number(g.origin),mode=g.filterMode||'field';
    if (!g.id.trim() || !g.name.trim()) throw new Error('ID and name are required.');
    if (!/^\d+$/.test(g.origin) || !Number.isSafeInteger(origin)) throw new Error('Origin must be a non-negative whole block height.');
    if (!g.table.trim() || !g.key.trim()) throw new Error('Table and key column are required.');
    if (!['field','metadata','trait'].includes(mode)) throw new Error('Choose a supported filter type.');
    if (!!g.valueColumn !== !!g.valuePath) throw new Error('Provide both a value column and its field path, or leave both blank.');
    if (g.valueColumn && g.valueColumn===g.key) throw new Error('Value and key columns must differ.');
    const row=Object.create(null); row[g.key]=fromPath(g.keyPath,'Key field');
    if (g.valueColumn) row[g.valueColumn]=fromPath(g.valuePath,'Value field');
    const steps=[], subject=mode==='field'?g.filterPath:g.filterKey;
    if (subject || g.filterValue) {
      if (!subject || !g.filterValue) throw new Error('Provide both the filter field or key and JSON value.');
      let value; try { value=JSON.parse(g.filterValue); } catch { throw new Error('Filter value must be valid JSON, such as "text", 5 or true.'); }
      if (!guidableJSON(g.filterValue)) throw new Error('This JSON value cannot be represented safely in guided controls; use raw JSON.');
      if (mode!=='field' && !scalar(value)) throw new Error('Metadata and trait filters support text, boolean, null or safe integer values in guided controls.');
      if (mode==='field' && !comparable(value)) throw new Error('Field filters support text, boolean, null or safe integer values. Decimal comparisons are not supported.');
      let operand;
      if (mode==='metadata') {
        operand=variable('metadata_value');
        steps.push({op:'let',as:'metadata_value',value:call('cbor.scalar',call('cbor.key',call('cbor.decode',field('metadata_hex')),literal(g.filterKey)))});
        steps.push({op:'require',test:call('and',call('valid',operand),call('eq',operand,literal(value)))});
      } else {
        operand=mode==='trait'?field('properties','attributes','traits',g.filterKey):fromPath(g.filterPath,'Filter field');
        steps.push({op:'require',test:call('and',call('present',operand),call('eq',operand,literal(value)))});
      }
    }
    steps.push({op:'write',table:g.table,row});
    return {format:'gateway-recipe',version:1,id:g.id,name:g.name,source:'bitcoin.reveal',origin,history:'sparse',dependencies:[],tables:[{name:g.table,key:g.key}],steps};
  }
  async function defaultRequest(action, body) {
    const options=body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)};
    const response=await fetch('/api/v1/index/recipes'+action,options), value=await response.json();
    if (!response.ok) throw new Error(value.error||'Recipe request failed ('+response.status+').');
    return value;
  }
  // Guided edits use JavaScript values, so refuse any document where parsing
  // would collapse duplicate keys or change a numeric token on re-encoding.
  // Raw validation, saving and export preserve the original text instead.
  function guidableJSON(text) {
    let i=0;
    const ws=()=>{while (/\s/.test(text[i]||'') && i<text.length) i++;};
    const string=()=>{const start=i++; while(i<text.length){if(text[i]==='\\'){i+=2;continue;}if(text[i++]==='"')return JSON.parse(text.slice(start,i));}throw new Error('string');};
    function value(depth) {
      if(depth>128) throw new Error('depth'); ws(); const c=text[i];
      if(c==='"'){string();return;}
      if(c==='{'){i++;const seen=new Set();ws();if(text[i]==='}'){i++;return;}while(true){ws();const key=string();if(seen.has(key))throw new Error('duplicate');seen.add(key);ws();if(text[i++]!==':')throw new Error('object');value(depth+1);ws();const end=text[i++];if(end==='}')return;if(end!==',')throw new Error('object');}}
      if(c==='['){i++;ws();if(text[i]===']'){i++;return;}while(true){value(depth+1);ws();const end=text[i++];if(end===']')return;if(end!==',')throw new Error('array');}}
      const number=/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(i));
      if(number){const token=number[0],n=Number(token);if(!Number.isFinite(n)||Number.isInteger(n)&&!Number.isSafeInteger(n)||JSON.stringify(n)!==token)throw new Error('number');i+=token.length;return;}
      for(const keyword of ['true','false','null'])if(text.startsWith(keyword,i)){i+=keyword.length;return;}
      throw new Error('value');
    }
    try {value(0);ws();return i===text.length;} catch {return false;}
  }
  function cataloguePaths(catalogue) {
    if (catalogue?.version!==1 || !Array.isArray(catalogue.sources)) return [];
    const source=catalogue.sources.find(x=>x?.id==='bitcoin.reveal');
    if (!Array.isArray(source?.fields)) return [];
    return [...new Set(source.fields.filter(f=>!f.dynamic_tail && Array.isArray(f.path) && f.path.length && f.path.every(p=>typeof p==='string' && p && !p.includes('.'))).map(f=>f.path.join('.')))];
  }
  function mount(root, {request=defaultRequest}={}) {
    if (!root) return;
    const q=id=>root.querySelector('[data-recipe="'+id+'"]'), names=['id','name','origin','table','key','keyPath','filterMode','filterPath','filterKey','filterValue','valueColumn','valuePath'];
    const raw=q('raw'), status=q('status'), guided=q('guided'), saveNote=q('save-note');
    let model=initial(), revision=0, base=null, pendingGuided=false, busy=false, importing=false, importEpoch=0, requestEpoch=0;
    const workButtons=['validate','save','load','refresh'];
    function paintBusy() { for (const name of workButtons) q(name).disabled=busy||importing; }
    function setBusy(value) { busy=value; paintBusy(); }
    function cancelImport() { importEpoch++; importing=false; paintBusy(); }
    function draft() {
      if (importing) throw new Error('Wait for the import to finish.');
      if (pendingGuided) throw new Error('Apply guided changes before validating or saving.');
      const value=JSON.parse(raw.value);
      if (!value || typeof value!=='object' || Array.isArray(value)) throw new Error('A recipe must be an object.');
      return value;
    }
    function showModel() { pendingGuided=false; raw.value=JSON.stringify(model,null,2); showGuided(); }
    function filterControls() { const mode=q('filterMode').value; q('filterPath').disabled=mode!=='field'; q('filterKey').disabled=mode==='field'; }
    function showGuided() {
      const g=guidableJSON(raw.value) ? toGuided(model) : null; guided.disabled=!g;
      if (g) { for (const name of names) q(name).value=g[name]; filterControls(); }
      status.textContent=g ? 'Guided controls represent this draft. Apply guided changes to update JSON.' : 'This JSON cannot be represented safely by the guided controls. Its original text is preserved.';
    }
    for (const name of names) q(name).addEventListener('input',()=>{ cancelImport(); revision++; pendingGuided=true; if(name==='filterMode') filterControls(); status.textContent='Apply guided changes before validating or saving.'; });
    raw.addEventListener('input',()=>{ cancelImport(); revision++; pendingGuided=false; try { const d=JSON.parse(raw.value); if (!d || typeof d!=='object' || Array.isArray(d)) throw new Error('A recipe must be an object.'); model=d; showGuided(); } catch(e) { guided.disabled=true; status.textContent='JSON: '+e.message; } });
    q('apply').addEventListener('click',()=>{ cancelImport(); revision++; try { const g={}; for (const name of names) g[name]=q(name).value; model=fromGuided(g); showModel(); status.textContent='Guided changes applied to the JSON draft.'; } catch(e) { status.textContent=e.message; } });
    q('reset').addEventListener('click',()=>{ cancelImport(); revision++; base=null; model=initial(); showModel(); });
    q('import').addEventListener('change',async event=>{
      const file=event.target.files?.[0]; if (!file) return; const started=++revision, epoch=++importEpoch;
      if (file.size>1024*1024) { importing=false; paintBusy(); status.textContent='Import is limited to 1 MiB.'; event.target.value=''; return; }
      importing=true; paintBusy(); status.textContent='Reading recipe JSON…';
      try { const bytes=await file.arrayBuffer(); if (started!==revision || epoch!==importEpoch) return; const text=new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(bytes); const d=JSON.parse(text); if (!d || typeof d!=='object' || Array.isArray(d)) throw new Error('A recipe must be an object.'); model=d; pendingGuided=false; raw.value=text; showGuided(); }
      catch(e) { if (started===revision && epoch===importEpoch) status.textContent='Import: '+e.message; }
      finally { if (epoch===importEpoch) { importing=false; paintBusy(); event.target.value=''; } }
    });
    q('validate').addEventListener('click',async()=>{
      if (busy || importing) return; let value; try { value=draft(); } catch(e) { status.textContent=e.message; return; }
      const started=revision; ++requestEpoch; setBusy(true);
      try { await request('/validate',{recipe_json:raw.value}); if (started===revision) status.textContent='Validated by the recipe compiler. Finite runs require a reviewed plan and an explicit Out; Live and dependencies are unavailable.'; }
      catch(e) { if (started===revision) status.textContent='Validation: '+e.message; }
      finally { setBusy(false); }
    });
    q('save').addEventListener('click',async()=>{
      if (busy || importing) return; let value; try { value=draft(); } catch(e) { status.textContent=e.message; return; }
      const started=revision, body={recipe_json:raw.value}; ++requestEpoch; saveNote.textContent='';
      if (base && value.id===base.id) { body.previous_revision=base.revision; body.previous_semantic_hash=base.semanticHash; }
      setBusy(true);
      try {
        const result=await request('',body);
        if (started===revision) { base={id:value.id,revision:result.revision,semanticHash:result.semantic_hash}; status.textContent='Draft saved and validated. Finite runs require a reviewed plan and an explicit Out; Live and dependencies are unavailable.'; }
        else { if (base?.id===value.id) base=null; saveNote.textContent='An earlier version of '+value.id+' was saved. Export newer edits, then reload the saved draft before saving this ID again.'; }
      } catch(e) { if (started===revision) status.textContent='Save: '+e.message; }
      finally { setBusy(false); }
    });
    q('refresh').addEventListener('click',async()=>{
      if (busy || importing) return; const epoch=++requestEpoch, started=revision;
      try {
        const items=await request(''); if (epoch!==requestEpoch || started!==revision) return;
        const select=q('saved'),doc=root.ownerDocument, options=[];
        const first=doc.createElement('option');first.value='';first.textContent='Choose a draft';options.push(first);
        for (const item of items) {const option=doc.createElement('option');option.value=item.recipe.id;option.textContent=item.recipe.name+' ('+item.recipe.id+')';options.push(option);}
        select.replaceChildren(...options); status.textContent=items.length ? 'Select a saved draft to load.' : 'No saved drafts.';
      } catch(e) { if (epoch===requestEpoch && started===revision) status.textContent='List: '+e.message; }
    });
    q('saved').addEventListener('change',()=>{ cancelImport(); revision++; });
    q('load').addEventListener('click',async()=>{
      if (busy || importing) return; const id=q('saved').value; if (!id) { status.textContent='Choose a saved draft first.'; return; }
      const started=revision; ++requestEpoch; setBusy(true);
      try {
        const result=await request('?id='+encodeURIComponent(id));
        if (started!==revision) return;
        if (typeof result.recipe_json!=='string') throw new Error('Saved recipe text is unavailable.');
        const loaded=JSON.parse(result.recipe_json); model=loaded; base={id:loaded.id,revision:result.revision,semanticHash:result.semantic_hash}; revision++; pendingGuided=false; raw.value=result.recipe_json; showGuided(); saveNote.textContent='';
        status.textContent=result.validated===false ? (result.note||'Saved draft is not validated by the current interpreter.') : 'Saved draft loaded. Finite runs require a reviewed plan and an explicit Out; Live and dependencies are unavailable.';
      } catch(e) { if (started===revision) status.textContent='Load: '+e.message; }
      finally { setBusy(false); }
    });
    q('export').addEventListener('click',()=>{
      let d; try { d=draft(); } catch(e) { status.textContent='Cannot export: '+e.message; return; }
      const url=URL.createObjectURL(new Blob([raw.value],{type:'application/json'})), link=document.createElement('a');
      link.href=url; link.download='gateway-recipe.json'; link.click(); setTimeout(()=>URL.revokeObjectURL(url),0);
    });
    showModel();
    // Suggestions are independent of draft state. If the catalogue is absent
    // or invalid, retain the static HTML options; no saved text is changed.
    Promise.resolve().then(()=>request('/catalogue')).then(catalogue=>{
      const paths=cataloguePaths(catalogue); if (!paths.length) return;
      const doc=root.ownerDocument, options=paths.map(path=>{const option=doc.createElement('option');option.value=path;return option;});
      q('field-list').replaceChildren(...options);
    }).catch(()=>{});
  }
  global.GatewayRecipeEditor={initial,toGuided,fromGuided,guidableJSON,cataloguePaths,mount};
})(typeof window === 'undefined' ? globalThis : window);
