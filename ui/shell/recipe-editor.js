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
    if (!keys(d,['format','version','id','name','source','origin','history','dependencies','exports','tables','steps']) || d.format!=='gateway-recipe' || d.version!==1 || d.source!=='bitcoin.reveal' || d.history!=='sparse' || !Array.isArray(d.dependencies) || d.dependencies.length>1 || (Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length!==1)) || (d.dependencies.length&&d.exports?.length) || !Array.isArray(d.tables) || d.tables.length!==1 || !Array.isArray(d.steps)) return null;
    const table=d.tables[0];
    if (!keys(table,['name','key']) || typeof table.name!=='string' || typeof table.key!=='string' || !table.name || !table.key) return null;
    const dep=d.dependencies[0], exp=d.exports?.[0];
    const supportedType=t=>['text','bool','integer','null'].includes(t);
    if (dep&&(!keys(dep,['name','instance','revision','semantic_hash','export','coverage','type'])||dep.coverage!=='same_block'||![dep.name,dep.instance,dep.revision,dep.semantic_hash,dep.export].every(x=>typeof x==='string'&&x)||!supportedType(dep.type))) return null;
    if (exp&&(!keys(exp,['name','kind','table','column','type'])||exp.kind!=='same_block'||exp.table!==table.name||typeof exp.name!=='string'||!exp.name||typeof exp.column!=='string'||!exp.column||!supportedType(exp.type))) return null;
    let write=d.steps[d.steps.length-1], lookupColumn='', filterEnd=d.steps.length-1;
    if(dep){
      if(d.steps.length<3)return null;
      filterEnd=d.steps.length-3;const binding=d.steps[filterEnd],guard=d.steps[filterEnd+1],keyExpr=write?.row?.[table.key];
      if(!keys(binding,['op','as','value'])||binding.op!=='let'||binding.as!=='dependency_value'||!keys(guard,['op','test'])||guard.op!=='require'||!same(guard.test,call('valid',variable('dependency_value')))||!path(keyExpr)||!same(binding.value,call('dependency.lookup',literal(dep.name),keyExpr)))return null;
      const candidates=Object.keys(write?.row||{}).filter(x=>x!==table.key&&same(write.row[x],variable('dependency_value')));
      if(candidates.length!==1)return null;lookupColumn=candidates[0];
    }
    const filters=[];let metadataCount=0;
    for(let i=0;i<filterEnd;i++){
      let mode='field',key='',filterPath='',step=d.steps[i],metadataVariable='';
      if(step?.op==='let'){
        metadataCount++;metadataVariable=metadataCount===1?'metadata_value':'metadata_value_'+metadataCount;
        const val=step.value;key=val?.args?.[0]?.args?.[1]?.literal;
        const expected=call('cbor.scalar',call('cbor.key',call('cbor.decode',field('metadata_hex')),literal(key)));
        if(!keys(step,['op','as','value'])||step.as!==metadataVariable||typeof key!=='string'||!key||!same(val,expected))return null;
        step=d.steps[++i];mode='metadata';
      }
      const test=step?.test,equals=test?.args?.[1],operand=equals?.args?.[0],value=equals?.args?.[1];
      if(!keys(step,['op','test'])||step.op!=='require'||!keys(test,['call','args'])||test.call!=='and'||!Array.isArray(test.args)||test.args.length!==2||!keys(value,['literal'])||!Object.hasOwn(value,'literal')||!scalar(value.literal))return null;
      if(mode==='metadata'){
        const v=variable(metadataVariable);if(!same(test,call('and',call('valid',v),call('eq',v,value))))return null;
      }else if(Array.isArray(operand?.field)&&operand.field.length===4&&same(operand.field.slice(0,3),['properties','attributes','traits'])&&typeof operand.field[3]==='string'&&operand.field[3]){
        mode='trait';key=operand.field[3];if(!same(test,call('and',call('present',operand),call('eq',operand,value))))return null;
      }else{
        filterPath=path(operand);if(!filterPath||!comparable(value.literal)||!same(test,call('and',call('present',operand),call('eq',operand,value))))return null;
      }
      filters.push({mode,path:filterPath,key,value:JSON.stringify(value.literal)});if(filters.length>32)return null;
    }
    if(!keys(write,['op','table','row'])||write.op!=='write'||write.table!==table.name||!keys(write.row,Object.keys(write.row||{})))return null;
    const cols=Object.keys(write.row),keyPath=path(write.row[table.key]);
    if(!keyPath||cols.length<1||cols.length>128||!cols.includes(table.key)||exp&&!cols.includes(exp.column))return null;
    const columns=[];for(const name of cols){if(name===table.key||name===lookupColumn)continue;const fieldPath=path(write.row[name]);if(!fieldPath)return null;columns.push({name,path:fieldPath});}
    if(typeof d.id!=='string'||typeof d.name!=='string'||!Number.isSafeInteger(d.origin)||d.origin<0)return null;
    const first=filters[0]||{mode:'field',path:'',key:'',value:''},column=columns[0]||{name:'',path:''};
    return {id:d.id,name:d.name,origin:String(d.origin),table:table.name,key:table.key,keyPath,filterMode:first.mode,filterPath:first.path,filterKey:first.key,filterValue:first.value,extraFilters:filters.slice(1),valueColumn:column.name,valuePath:column.path,extraColumns:columns.slice(1),exportName:exp?.name||'',exportColumn:exp?.column||'',exportType:exp?.type||'',dependencyName:dep?.name||'',dependencyInstance:dep?.instance||'',dependencyRevision:dep?.revision||'',dependencySemanticHash:dep?.semantic_hash||'',dependencyExport:dep?.export||'',dependencyType:dep?.type||'',lookupColumn};
  }
  function fromGuided(g) {
    const origin=Number(g.origin),mode=g.filterMode||'field',extras=g.extraFilters||[],extraColumns=g.extraColumns||[];
    if (!g.id.trim() || !g.name.trim()) throw new Error('ID and name are required.');
    if (!/^\d+$/.test(g.origin) || !Number.isSafeInteger(origin)) throw new Error('Origin must be a non-negative whole block height.');
    if (!g.table.trim() || !g.key.trim()) throw new Error('Table and key column are required.');
    if (!['field','metadata','trait'].includes(mode)) throw new Error('Choose a supported filter type.');
    if (!Array.isArray(extras)||extras.length>31||!Array.isArray(extraColumns)||extraColumns.length>126) throw new Error('Too many filters or output columns.');
    const firstFilter={mode,path:g.filterPath||'',key:g.filterKey||'',value:g.filterValue||''},subject=f=>f.mode==='field'?f.path:f.key;
    if(extras.length&&!subject(firstFilter)&&!firstFilter.value)throw new Error('Complete the first filter before adding more filters.');
    const filters=(subject(firstFilter)||firstFilter.value)?[firstFilter,...extras]:[];
    const columns=(g.valueColumn||g.valuePath)?[{name:g.valueColumn,path:g.valuePath},...extraColumns]:[];
    if(extraColumns.length&&!columns.length)throw new Error('Complete the first output column before adding more columns.');
    const row=Object.create(null);row[g.key]=fromPath(g.keyPath,'Key field');
    for(const col of columns){if(!col?.name||!col?.path)throw new Error('Provide both a value column and its field path, or leave both blank.');if(Object.hasOwn(row,col.name))throw new Error('Output column names must be distinct.');row[col.name]=fromPath(col.path,'Value field');}
    const depFields=['dependencyName','dependencyInstance','dependencyRevision','dependencySemanticHash','dependencyExport','dependencyType','lookupColumn'],hasDep=depFields.some(k=>!!g[k]);
    const exportFields=['exportName','exportColumn','exportType'],hasExport=exportFields.some(k=>!!g[k]);
    const validID=x=>/^[a-z0-9][a-z0-9_-]{0,63}$/.test(x||''),validHash=x=>/^[0-9a-f]{64}$/.test(x||''),validType=x=>['text','bool','integer','null'].includes(x);
    if(hasDep){
      if(!validID(g.dependencyName)||!validID(g.dependencyInstance)||g.dependencyInstance===g.id||!validID(g.dependencyExport)||!validHash(g.dependencyRevision)||!validHash(g.dependencySemanticHash)||!validType(g.dependencyType)||!g.lookupColumn||Object.hasOwn(row,g.lookupColumn))throw new Error('Dependency needs a different parent, alias, export, immutable revision, fingerprint, scalar type and distinct output column.');
      if(hasExport)throw new Error('Chained exports require raw JSON.');
    }
    if(Object.keys(row).length+(hasDep?1:0)>128)throw new Error('Too many output columns.');
    if(hasExport&&(!validID(g.exportName)||!g.exportColumn||!Object.hasOwn(row,g.exportColumn)||!validType(g.exportType)))throw new Error('Export needs a name, an existing output column and a scalar type.');
    const steps=[];let metadataCount=0;
    for(const f of filters){
      if(!f||!['field','metadata','trait'].includes(f.mode))throw new Error('Choose a supported filter type.');
      const target=subject(f);if(!target||!f.value)throw new Error('Provide both the filter field or key and JSON value.');
      let value;try{value=JSON.parse(f.value);}catch{throw new Error('Filter value must be valid JSON, such as "text", 5 or true.');}
      if(!guidableJSON(f.value))throw new Error('This JSON value cannot be represented safely in guided controls; use raw JSON.');
      if(f.mode!=='field'&&!scalar(value))throw new Error('Metadata and trait filters support text, boolean, null or safe integer values in guided controls.');
      if(f.mode==='field'&&!comparable(value))throw new Error('Field filters support text, boolean, null or safe integer values. Decimal comparisons are not supported.');
      let operand;
      if(f.mode==='metadata'){
        metadataCount++;const name=metadataCount===1?'metadata_value':'metadata_value_'+metadataCount;operand=variable(name);
        steps.push({op:'let',as:name,value:call('cbor.scalar',call('cbor.key',call('cbor.decode',field('metadata_hex')),literal(f.key)))});
        steps.push({op:'require',test:call('and',call('valid',operand),call('eq',operand,literal(value)))});
      }else{
        operand=f.mode==='trait'?field('properties','attributes','traits',f.key):fromPath(f.path,'Filter field');
        steps.push({op:'require',test:call('and',call('present',operand),call('eq',operand,literal(value)))});
      }
    }
    if(hasDep){steps.push({op:'let',as:'dependency_value',value:call('dependency.lookup',literal(g.dependencyName),row[g.key])});steps.push({op:'require',test:call('valid',variable('dependency_value'))});row[g.lookupColumn]=variable('dependency_value');}
    steps.push({op:'write',table:g.table,row});
    const dependencies=hasDep?[{name:g.dependencyName,instance:g.dependencyInstance,revision:g.dependencyRevision,semantic_hash:g.dependencySemanticHash,export:g.dependencyExport,coverage:'same_block',type:g.dependencyType}]:[];
    const result={format:'gateway-recipe',version:1,id:g.id,name:g.name,source:'bitcoin.reveal',origin,history:'sparse',dependencies,tables:[{name:g.table,key:g.key}],steps};
    if(hasExport)result.exports=[{name:g.exportName,kind:'same_block',table:g.table,column:g.exportColumn,type:g.exportType}];return result;
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
    const q=id=>root.querySelector('[data-recipe="'+id+'"]'), names=['id','name','origin','table','key','keyPath','filterMode','filterPath','filterKey','filterValue','valueColumn','valuePath','exportName','exportColumn','exportType','dependencyName','dependencyInstance','dependencyRevision','dependencySemanticHash','dependencyExport','dependencyType','lookupColumn'];
    const raw=q('raw'), status=q('status'), guided=q('guided'), saveNote=q('save-note');
    let model=initial(), revision=0, base=null, pendingGuided=false, busy=false, importing=false, importEpoch=0, requestEpoch=0, parentEpoch=0, parentSnapshot=null, extraFilters=[], extraColumns=[];
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
    function guidedChanged(){cancelImport();revision++;pendingGuided=true;status.textContent='Apply guided changes before validating or saving.';}
    function labelled(text,input){const label=root.ownerDocument.createElement('label');label.textContent=text;label.append(input);return label;}
    function renderExtras(){
      const doc=root.ownerDocument,filterNodes=extraFilters.map((f,index)=>{
        const row=doc.createElement('div');row.className='recipe-fields';const mode=doc.createElement('select');mode.setAttribute('aria-label','Additional filter type '+(index+1));
        for(const [value,text] of [['field','Reveal field'],['metadata','Metadata key'],['trait','Property trait']]){const o=doc.createElement('option');o.value=value;o.textContent=text;mode.append(o);}mode.value=f.mode;
        const fieldInput=doc.createElement('input'),keyInput=doc.createElement('input'),valueInput=doc.createElement('input');fieldInput.value=f.path;fieldInput.setAttribute('list','recipe-fields');keyInput.value=f.key;valueInput.value=f.value;valueInput.placeholder='JSON value';
        const sync=()=>{fieldInput.disabled=mode.value!=='field';keyInput.disabled=mode.value==='field';};sync();
        for(const [input,key] of [[mode,'mode'],[fieldInput,'path'],[keyInput,'key'],[valueInput,'value']])input.addEventListener('input',()=>{f[key]=input.value;sync();guidedChanged();});
        const remove=doc.createElement('button');remove.type='button';remove.textContent='Remove filter';remove.setAttribute('aria-label','Remove additional filter '+(index+1));remove.addEventListener('click',()=>{extraFilters.splice(index,1);guidedChanged();renderExtras();});
        row.append(labelled('Filter type',mode),labelled('Field path',fieldInput),labelled('Metadata key or trait',keyInput),labelled('Equals JSON value',valueInput),remove);return row;
      });
      const columnNodes=extraColumns.map((c,index)=>{const row=doc.createElement('div');row.className='recipe-fields';const name=doc.createElement('input'),fieldInput=doc.createElement('input');name.value=c.name;fieldInput.value=c.path;fieldInput.setAttribute('list','recipe-fields');
        for(const [input,key] of [[name,'name'],[fieldInput,'path']])input.addEventListener('input',()=>{c[key]=input.value;guidedChanged();});
        const remove=doc.createElement('button');remove.type='button';remove.textContent='Remove column';remove.setAttribute('aria-label','Remove additional column '+(index+1));remove.addEventListener('click',()=>{extraColumns.splice(index,1);guidedChanged();renderExtras();});row.append(labelled('Value column',name),labelled('Value field path',fieldInput),remove);return row;
      });
      q('extraFilters').replaceChildren(...filterNodes);q('extraColumns').replaceChildren(...columnNodes);
    }
    q('addFilter').addEventListener('click',()=>{if(extraFilters.length>=31){status.textContent='At most 32 guided filters are supported.';return;}extraFilters.push({mode:'field',path:'',key:'',value:''});guidedChanged();renderExtras();});
    q('addColumn').addEventListener('click',()=>{if(extraColumns.length>=126){status.textContent='At most 127 guided value columns are supported.';return;}extraColumns.push({name:'',path:''});guidedChanged();renderExtras();});
    function showModel() { pendingGuided=false; raw.value=JSON.stringify(model,null,2); showGuided(); }
    function filterControls() { const mode=q('filterMode').value; q('filterPath').disabled=mode!=='field'; q('filterKey').disabled=mode==='field'; }
    function showGuided() {
      const g=guidableJSON(raw.value) ? toGuided(model) : null; guided.disabled=!g;
      if (g) { for (const name of names) q(name).value=g[name]; extraFilters=g.extraFilters.map(f=>({...f}));extraColumns=g.extraColumns.map(c=>({...c}));filterControls();renderExtras(); }
      status.textContent=g ? 'Guided controls represent this draft. Apply guided changes to update JSON.' : 'This JSON cannot be represented safely by the guided controls. Its original text is preserved.';
    }
    for (const name of names) q(name).addEventListener('input',()=>{guidedChanged();if(name==='filterMode')filterControls();});
    raw.addEventListener('input',()=>{ cancelImport(); revision++; pendingGuided=false; try { const d=JSON.parse(raw.value); if (!d || typeof d!=='object' || Array.isArray(d)) throw new Error('A recipe must be an object.'); model=d; showGuided(); } catch(e) { guided.disabled=true; status.textContent='JSON: '+e.message; } });
    q('apply').addEventListener('click',()=>{ cancelImport(); revision++; try { const g={}; for (const name of names) g[name]=q(name).value;g.extraFilters=extraFilters.map(f=>({...f}));g.extraColumns=extraColumns.map(c=>({...c}));model=fromGuided(g); showModel(); status.textContent='Guided changes applied to the JSON draft.'; } catch(e) { status.textContent=e.message; } });
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
      try { await request('/validate',{recipe_json:raw.value}); if (started===revision) status.textContent='Validated by the recipe compiler. Finite and Live runs require a reviewed plan; Out caps either choice. Same-block reveal dependencies require a reviewed plan and matching parent coverage; saved-only parents are not started automatically. Transaction recipe dependencies remain unavailable.'; }
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
        if (started===revision) { base={id:value.id,revision:result.revision,semanticHash:result.semantic_hash}; status.textContent='Draft saved and validated. Finite and Live runs require a reviewed plan; Out caps either choice. Same-block reveal dependencies require a reviewed plan and matching parent coverage; saved-only parents are not started automatically. Transaction recipe dependencies remain unavailable.'; }
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
        status.textContent=result.validated===false ? (result.note||'Saved draft is not validated by the current interpreter.') : 'Saved draft loaded. Finite and Live runs require a reviewed plan; Out caps either choice. Same-block reveal dependencies require a reviewed plan and matching parent coverage; saved-only parents are not started automatically. Transaction recipe dependencies remain unavailable.';
      } catch(e) { if (started===revision) status.textContent='Load: '+e.message; }
      finally { setBusy(false); }
    });
    // Parent discovery is read-only and never changes the draft. An explicit
    // Use action pins a fetched immutable identity; late fetches are fenced.
    const parentStatus=q('parentStatus'), parentChoice=q('parentChoice'), parentExportChoice=q('parentExportChoice');
    const option=(value,title)=>{const node=root.ownerDocument.createElement('option');node.value=value;node.textContent=title;return node;};
    function clearParent(){parentSnapshot=null;parentExportChoice.replaceChildren(option('','Choose export'));}
    q('parentRefresh').addEventListener('click',async()=>{
      const ticket=++parentEpoch,started=revision;clearParent();parentStatus.textContent='Reading saved parents…';
      try{const items=await request('');if(ticket!==parentEpoch)return;if(started!==revision){parentStatus.textContent='Draft changed; refresh the parent list.';return;}if(!Array.isArray(items))throw new Error('Invalid parent list.');const previous=parentChoice.value;parentChoice.replaceChildren(option('','Choose parent'),...items.filter(x=>x?.validated&&x.recipe?.id).map(x=>option(x.recipe.id,x.recipe.name+' ('+x.recipe.id+')')));parentChoice.value=items.some(x=>x?.validated&&x.recipe?.id===previous)?previous:'';parentStatus.textContent='Choose a parent to inspect its saved exports.';}
      catch(e){if(ticket===parentEpoch&&started===revision)parentStatus.textContent='Parent list: '+e.message;}
    });
    parentChoice.addEventListener('change',async()=>{
      const id=parentChoice.value,ticket=++parentEpoch,started=revision;clearParent();if(!id){parentStatus.textContent='Choose a saved parent.';return;}parentStatus.textContent='Reading immutable parent definition…';
      try{const v=await request('?id='+encodeURIComponent(id));if(ticket!==parentEpoch||parentChoice.value!==id)return;if(started!==revision){parentStatus.textContent='Draft changed; select the parent again.';return;}
        const d=v?.recipe;if(!v?.validated||d?.id!==id||d.source!=='bitcoin.reveal'||d.history!=='sparse'||(d.dependencies!=null&&(!Array.isArray(d.dependencies)||d.dependencies.length))||!Array.isArray(d.exports)||!d.exports.length||!(/^[0-9a-f]{64}$/.test(v.revision||'')&&/^[0-9a-f]{64}$/.test(v.semantic_hash||'')))throw new Error('No validated independent same-block exports are available.');
        const exports=d.exports.filter(e=>e?.kind==='same_block'&&['text','bool','integer','null'].includes(e.type)&&typeof e.name==='string'&&e.name);if(!exports.length)throw new Error('No supported same-block exports are available.');
        parentSnapshot={id,revision:v.revision,semanticHash:v.semantic_hash,exports};parentExportChoice.replaceChildren(option('','Choose export'),...exports.map(e=>option(e.name,e.name+' · '+e.type+' · '+e.table+'.'+e.column)));parentStatus.textContent='Choose an export, then use it to pin this exact revision. Parent coverage is checked during a reviewed run; saved-only parents are not started automatically.';
      }catch(e){if(ticket===parentEpoch&&started===revision&&parentChoice.value===id)parentStatus.textContent='Parent: '+e.message;}
    });
    q('parentUse').addEventListener('click',()=>{
      const parent=parentSnapshot,exported=parent?.exports.find(e=>e.name===parentExportChoice.value);
      if(!parent||parentChoice.value!==parent.id||!exported){parentStatus.textContent='Choose a current parent and export first.';return;}
      if(parent.id===q('id').value){parentStatus.textContent='A recipe cannot depend on itself.';return;}
      cancelImport();revision++;pendingGuided=true;q('dependencyName').value=q('dependencyName').value||'parent';q('dependencyInstance').value=parent.id;q('dependencyRevision').value=parent.revision;q('dependencySemanticHash').value=parent.semanticHash;q('dependencyExport').value=exported.name;q('dependencyType').value=exported.type;parentStatus.textContent='Pinned the selected parent revision. Enter a lookup output column, then apply guided changes. A reviewed run can use matching parent coverage; saved-only parents are not started automatically.';status.textContent='Apply guided changes before validating or saving.';
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
