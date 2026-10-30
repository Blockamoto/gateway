(function(global){
  'use strict';
  const copy=value=>JSON.parse(JSON.stringify(value));
  const object=value=>!!value&&typeof value==='object'&&!Array.isArray(value);
  const keys=(value,allowed)=>object(value)&&Object.keys(value).every(key=>allowed.includes(key));
  const fields=['kind','block','txid','height','tx_index','output_count','coinbase','prev_txid','input_ref','fact_page','prev_vout','input_index','vout','value','fact_row','script'];
  const fifoFields=[...fields,'id','unbound','has_pointer','input_value','input_offset','pointer','outputs_value'];
  const projectionFields=['block','height','tx_index','vout','value','script','add'];
  const tableFields=fields.filter(name=>!['fact_page','fact_row'].includes(name));
  const arities={eq:2,not:1,present:1,add_money:2,sub_money:2,less_equal:2,outpoint:2,output_ref:3,destination:1,payment_destination:1,retains:2,bip30_exception:3,get:2,range_empty:0,range_subsidy:1,range_concat:2,range_split:2,range_value:1,range_at:2};
  const operations={let:['name','expr'],read:['name','table','key'],put:['table','key','expr'],delete:['table','key'],if:['expr','body','else'],require:['name','expr'],emit:['table','key','expr'],for_range:['name','expr','body'],for_events:['body']};
  const uint=text=>typeof text==='string'&&/^(0|[1-9][0-9]*)$/.test(text)&&(text.length<20||text.length===20&&text<='18446744073709551615');
  function valueSupported(value,depth=0){
    if(depth>32||!object(value))return false;
    const kind=value.kind;if(!['missing','text','uint','bool','bytes','object','ranges'].includes(kind)||!keys(value,kind==='missing'?['kind']:['kind',kind]))return false;
    if(kind==='missing')return true;
    if(kind==='text')return !Object.hasOwn(value,'text')||typeof value.text==='string';
    if(kind==='uint')return uint(value.uint);
    if(kind==='bool')return !Object.hasOwn(value,'bool')||typeof value.bool==='boolean';
    if(kind==='bytes')return !Object.hasOwn(value,'bytes')||typeof value.bytes==='string'&&/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value.bytes);
    if(kind==='ranges')return Array.isArray(value.ranges)&&value.ranges.every(r=>keys(r,['start','end'])&&Object.keys(r).length===2&&uint(r.start)&&uint(r.end)&&BigInt(r.start)<BigInt(r.end)&&BigInt(r.end)<=2099999997690000n);
    return object(value.object)&&Object.keys(value.object).length<=64&&Object.values(value.object).every(v=>valueSupported(v,depth+1));
  }
  function expressionSupported(expr,depth=0,allowedFields=fields){
    if(depth>32||!object(expr))return false;
    const modes=['literal','var','field','call','object'].filter(key=>Object.hasOwn(expr,key));
    if(modes.length!==1)return false;
    const mode=modes[0];if(!keys(expr,mode==='call'?['call','args']:[mode]))return false;
    if(mode==='literal')return valueSupported(expr.literal);
    if(mode==='var')return typeof expr.var==='string'&&!!expr.var;
    if(mode==='field')return allowedFields.includes(expr.field);
    if(mode==='call')return Object.hasOwn(arities,expr.call)&&Array.isArray(expr.args)&&expr.args.length===arities[expr.call]&&expr.args.every(arg=>expressionSupported(arg,depth+1,allowedFields));
    return object(expr.object)&&Object.keys(expr.object).length<=64&&Object.values(expr.object).every(arg=>expressionSupported(arg,depth+1,allowedFields));
  }
  function stepsSupported(steps,depth=0,scope=null){
    return depth<=32&&Array.isArray(steps)&&steps.length<=512&&steps.every(step=>{
      if(!Object.hasOwn(operations,step?.op)||scope?.output&&['read','put','delete'].includes(step.op)||scope?.generic&&step.op==='emit')return false;
      const allowed=operations[step.op];if(step.op==='for_events'&&depth!==0||!keys(step,['op',...allowed]))return false;
      if(Object.hasOwn(step,'name')&&(typeof step.name!=='string'||!step.name))return false;
      if(Object.hasOwn(step,'table')&&!(step.op==='emit'?(scope?.output?scope.tables:scope?.emitTables||['prevouts']):scope?.stateTables||['coins','counts']).includes(step.table))return false;
      if(scope?.fifo&&step.table==='history'&&step.op!=='put')return false;
      if(['let','read','for_range'].includes(step.op)&&!step.name)return false;
      if(['read','put','delete','emit'].includes(step.op)&&!expressionSupported(step.key,0,scope?.fields))return false;
      if(['let','put','if','require','emit','for_range'].includes(step.op)&&!expressionSupported(step.expr,0,scope?.fields))return false;
      if(['if','for_range','for_events'].includes(step.op)&&!stepsSupported(step.body,depth+1,scope))return false;
      if(Object.hasOwn(step,'else')&&!stepsSupported(step.else,depth+1,scope))return false;
      return true;
    });
  }
  function toGuided(definition){
    const d=definition;
    if(!keys(d,['format','version','id','name','source','origin','history','storage','dependencies','exports','tables','ordered'])||d.format!=='gateway-recipe'||d.version!==1||typeof d.id!=='string'||typeof d.name!=='string'||!['bitcoin.ordered','dependency.utxo','bitcoin.fifo'].includes(d.source)||!Number.isSafeInteger(d.origin)||d.origin<0)return null;
    const projection=d.source==='dependency.utxo'&&d.history==='projection'&&d.storage==='bitcoin.address.cow.v1'&&d.origin===0;
    const fifo=d.source==='bitcoin.fifo'&&d.history==='ordered'&&d.storage==='bitcoin.sats.cow.v1'&&d.origin===0;
    const sparse=d.history==='sparse'&&d.storage==='rows.json.v1';
    const generic=d.source==='bitcoin.ordered'&&d.history==='ordered'&&d.storage==='tables.cow.v1';
    if(d.source==='dependency.utxo'&&!projection||d.source==='bitcoin.fifo'&&!fifo||!generic&&!fifo&&!projection&&!sparse&&(d.origin!==0||d.history!=='ordered'||d.storage!=='bitcoin.utxo.cow.v2'))return null;
    if(projection){
      if(!Array.isArray(d.dependencies)||d.dependencies.length!==1)return null;
      const dep=d.dependencies[0],hash=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value),id=value=>typeof value==='string'&&/^[a-z0-9][a-z0-9_-]{0,63}$/.test(value);
      if(!keys(dep,['name','scope','instance','revision','semantic_hash','export','coverage','type'])||!id(dep.name)||!id(dep.instance)||dep.instance===d.id||!['','saved','builtin'].includes(dep.scope||'')||dep.scope==='builtin'&&dep.instance!=='utxo-state'||!hash(dep.revision)||!hash(dep.semantic_hash)||dep.export!=='coins'||dep.coverage!=='snapshot_delta'||dep.type!=='coin')return null;
    }else if(Object.hasOwn(d,'dependencies')&&(!Array.isArray(d.dependencies)||d.dependencies.length))return null;
    if(!Array.isArray(d.tables)||!d.tables.length||new Set(d.tables.map(t=>t.name)).size!==d.tables.length)return null;
    if(sparse){
      if(d.tables.length>64||!d.tables.every(t=>keys(t,['name','key','on_conflict'])&&typeof t.name==='string'&&!!t.name&&typeof t.key==='string'&&!!t.key&&(!Object.hasOwn(t,'on_conflict')||['','identical'].includes(t.on_conflict))))return null;
      if(Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||!d.exports.every(e=>keys(e,['name','kind','table','column','type'])&&typeof e.name==='string'&&!!e.name&&e.kind==='same_block'&&d.tables.some(t=>t.name===e.table)&&typeof e.column==='string'&&!!e.column&&['text','bool','integer','null'].includes(e.type))))return null;
    }else if(generic){
      if(Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length))return null;
      const utf8=value=>typeof value==='string'&&new TextEncoder().encode(value).length;
      if(d.tables.length>64||!d.tables.every(t=>keys(t,['name','key'])&&utf8(t.name)>0&&utf8(t.name)<=256&&!t.name.startsWith('__gateway_')&&utf8(t.key)>0&&utf8(t.key)<=256))return null;
    }else if(projection){
      if(Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length))return null;
      if(d.tables.length!==2||!d.tables.every(t=>keys(t,['name','key'])&&['members','excluded'].includes(t.name)&&t.key==='ref'))return null;
    }else if(fifo){
      if(Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length))return null;
      if(d.tables.length>2||!d.tables.some(t=>t.name==='outputs')||!d.tables.every(t=>keys(t,['name','key'])&&((t.name==='outputs'&&t.key==='outpoint')||(t.name==='history'&&t.key==='output'))))return null;
    }else{
      if(Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length))return null;
      if(d.tables.length!==2||!d.tables.every(t=>keys(t,['name','key'])&&((t.name==='coins'&&t.key==='outpoint')||(t.name==='counts'&&t.key==='txid'))))return null;
    }
    if(!keys(d.ordered,['steps'])||!stepsSupported(d.ordered.steps,0,{output:sparse||projection,fifo,generic,fields:projection?projectionFields:fifo?fifoFields:generic?tableFields:fields,tables:d.tables.map(t=>t.name),stateTables:fifo||generic?d.tables.map(t=>t.name):undefined,emitTables:fifo?['identities','lost','incarnations']:undefined})||d.ordered.steps.length!==1||d.ordered.steps[0].op!=='for_events')return null;
    return copy(d);
  }
  function fromGuided(definition){
    const result=toGuided(definition);
    if(!result)throw Error('The ordered draft contains an unsupported shape or incomplete typed value.');
    if(!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(result.id)||!result.name.trim())throw Error('Provide a valid recipe ID and name.');
    return result;
  }
  const missing=()=>({literal:{kind:'missing'}});
  function initialTables(){
    const literal=text=>({literal:{kind:'text',text}}),key=literal('total');
    return {format:'gateway-recipe',version:1,id:'my-output-count',name:'Output count from block 1',source:'bitcoin.ordered',origin:1,history:'ordered',storage:'tables.cow.v1',dependencies:[],tables:[{name:'counts',key:'name'}],ordered:{steps:[{op:'for_events',body:[{op:'if',expr:{call:'eq',args:[{field:'kind'},literal('output')]},body:[
      {op:'read',name:'old',table:'counts',key:copy(key)},
      {op:'let',name:'count',expr:{literal:{kind:'uint',uint:'0'}}},
      {op:'if',expr:{call:'present',args:[{var:'old'}]},body:[{op:'let',name:'count',expr:{var:'old'}}]},
      {op:'put',table:'counts',key:copy(key),expr:{call:'add_money',args:[{var:'count'},{literal:{kind:'uint',uint:'1'}}]}}
    ]}]}]}};
  }
  function instruction(op){
    const step={op};
    if(['let','read','for_range'].includes(op))step.name='value';
    if(['read','put','delete','emit'].includes(op)){step.table=op==='emit'?'prevouts':'coins';step.key={field:'txid'};}
    if(['let','put','if','require','emit','for_range'].includes(op))step.expr=op==='for_range'?{literal:{kind:'uint',uint:'0'}}:op==='if'||op==='require'?{literal:{kind:'bool',bool:true}}:missing();
    if(['if','for_range','for_events'].includes(op))step.body=[];
    if(op==='if')step.else=[];
    return step;
  }
  function mount(root,{onChange=()=>{},onApply=()=>{},getCatalogue=()=>null,readParent=async()=>{throw Error('Saved parent reader unavailable.');}}={}){
    const doc=root.ownerDocument;let model=null,sequence=0,editEpoch=0,parentEpoch=0,parentNote='';
    const el=(tag,text)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
    const label=(text,input)=>{const node=el('label',text);input.setAttribute('aria-label',text);node.append(input);return node;};
    const button=(text,action)=>{const node=el('button',text);node.type='button';node.addEventListener('click',action);return node;};
    const changed=()=>{editEpoch++;onChange();};
    const outputOnly=()=>['sparse','projection'].includes(model?.history);
    const generic=()=>model?.storage==='tables.cow.v1';
    const availableOperations=()=>Object.keys(operations).filter(op=>op!=='for_events'&&!(outputOnly()&&['read','put','delete'].includes(op))&&!(generic()&&op==='emit'));
    const fifo=()=>model?.source==='bitcoin.fifo';
    const tableChoices=op=>op==='emit'?(outputOnly()?model.tables.map(t=>t.name):fifo()?['identities','lost','incarnations']:['prevouts']):generic()?model.tables.map(t=>t.name):fifo()?model.tables.map(t=>t.name).filter(name=>op==='put'||name!=='history'):['coins','counts'];
    const newInstruction=op=>{const value=instruction(op);if((fifo()||generic())&&value.table){value.table=tableChoices(op)[0];value.key={field:op==='emit'?'id':'txid'};}if(op==='emit'&&outputOnly())value.table=model.tables[0].name;if(model?.history==='projection'&&value.key)value.key={call:'output_ref',args:[{field:'height'},{field:'tx_index'},{field:'vout'}]};return value;};
    function textInput(title,value,set){const input=el('input');input.type='text';input.value=value||'';input.addEventListener('input',()=>{set(input.value);changed();});return label(title,input);}
    function select(title,choices,value,set){const input=el('select');for(const choice of choices){const [key,name]=Array.isArray(choice)?choice:[choice,choice],option=el('option',name);option.value=key;input.append(option);}input.value=value;input.addEventListener('change',()=>{set(input.value);changed();});return label(title,input);}
    function lazy(title,render){const node=el('details'),summary=el('summary',title),body=el('div');body.className='ordered-editor-body';let drawn=false;node.append(summary,body);node.addEventListener('toggle',()=>{if(node.open&&!drawn){drawn=true;render(body);}});return node;}
    function members(host,mapping,renderChild){
      const rows=el('div'),actions=el('div');host.append(rows,actions);
      function paint(){rows.replaceChildren();for(const key of Object.keys(mapping)){
        const row=el('div');row.className='ordered-editor-member';
        const input=el('input');input.type='text';input.value=key;
        input.addEventListener('change',()=>{const next=input.value;if(!next||next!==key&&Object.hasOwn(mapping,next)){input.value=key;return;}const value=mapping[key];delete mapping[key];Object.defineProperty(mapping,next,{value,enumerable:true,writable:true,configurable:true});paint();changed();});
        const name=label('Object key',input);
        row.append(name,lazy('Value for '+key,body=>renderChild(body,mapping[key],next=>{mapping[key]=next;})),button('Remove object field',()=>{delete mapping[key];paint();changed();}));rows.append(row);
      }}
      actions.append(button('Add object field',()=>{let name='field';while(Object.hasOwn(mapping,name))name='field_'+(++sequence);Object.defineProperty(mapping,name,{value:renderChild===literalEditor?{kind:'missing'}:missing(),enumerable:true,writable:true,configurable:true});paint();changed();}));paint();
    }
    function literalEditor(host,value,set){
      function draw(){host.replaceChildren();host.append(select('Value type',['missing','text','uint','bool','bytes','object','ranges'],value.kind,kind=>{
        value={kind};if(kind==='text')value.text='';if(kind==='uint')value.uint='0';if(kind==='bool')value.bool=false;if(kind==='bytes')value.bytes='';if(kind==='object')value.object=Object.create(null);if(kind==='ranges')value.ranges=[];set(value);draw();
      }));
      if(value.kind==='text')host.append(textInput('Text',value.text||'',text=>{value.text=text;}));
      if(value.kind==='uint')host.append(textInput('Unsigned integer (exact decimal)',value.uint,text=>{value.uint=text;}));
      if(value.kind==='bytes')host.append(textInput('Bytes (base64)',value.bytes||'',text=>{value.bytes=text;}));
      if(value.kind==='bool')host.append(select('Boolean',['false','true'],String(!!value.bool),text=>{value.bool=text==='true';}));
      if(value.kind==='object')members(host,value.object,literalEditor);
      if(value.kind==='ranges'){
        const rows=el('div');host.append(rows);
        const paint=()=>{rows.replaceChildren();value.ranges.forEach((range,index)=>{const row=el('div');row.append(textInput('Interval start (exact decimal)',range.start,text=>{range.start=text;}),textInput('Interval end, exclusive (exact decimal)',range.end,text=>{range.end=text;}),button('Remove interval',()=>{value.ranges.splice(index,1);paint();changed();}));rows.append(row);});};
        host.append(button('Add interval',()=>{value.ranges.push({start:'0',end:'1'});paint();changed();}));paint();
      }
      }draw();
    }
    function expressionEditor(host,expr,set){
      function draw(){host.replaceChildren();const mode=['literal','var','field','call','object'].find(key=>Object.hasOwn(expr,key));
        host.append(select('Expression type',[['field','Source field'],['var','Variable'],['literal','Typed value'],['call','Function'],['object','Object fields']],mode,next=>{
          expr=next==='field'?{field:model?.history==='projection'?'height':'kind'}:next==='var'?{var:'value'}:next==='call'?{call:'eq',args:[missing(),missing()]}:next==='object'?{object:Object.create(null)}:missing();set(expr);draw();
        }));
        if(mode==='field'){
          const declared=getCatalogue()?.sources?.find(source=>source.id===model.source)?.fields;
          const available=Array.isArray(declared)?declared.filter(f=>Array.isArray(f.path)&&f.path.length===1).map(f=>f.path[0]):model?.history==='projection'?projectionFields:fifo()?fifoFields:fields;
          host.append(select('Source field',generic()?available.filter(name=>tableFields.includes(name)):available,expr.field,value=>{expr.field=value;}));
        }
        if(mode==='var')host.append(textInput('Variable name',expr.var,value=>{expr.var=value;}));
        if(mode==='literal'){const child=el('div');host.append(child);literalEditor(child,expr.literal,value=>{expr.literal=value;});}
        if(mode==='object')members(host,expr.object,expressionEditor);
        if(mode==='call'){
          const declared=getCatalogue()?.sources?.find(source=>source.id===model.source)?.functions;
          const available=Array.isArray(declared)?declared.map(fn=>fn.name).filter(name=>Object.hasOwn(arities,name)):Object.keys(arities);
          host.append(select('Function',available,expr.call,value=>{expr.call=value;expr.args=Array.from({length:arities[value]},missing);draw();}));
          expr.args.forEach((arg,index)=>host.append(lazy('Argument '+(index+1),body=>expressionEditor(body,arg,value=>{expr.args[index]=value;}))));
        }
      }draw();
    }
    function stepsEditor(host,steps){
      const rows=el('div'),actions=el('div');let selected='require';
      function paint(){rows.replaceChildren();steps.forEach((step,index)=>{
        const details=lazy((index+1)+'. '+step.op+(step.name?' '+step.name:''),body=>{
          const fields=el('div');fields.className='ordered-editor-fields';
          fields.append(select('Instruction',availableOperations(),step.op,op=>{steps[index]=newInstruction(op);paint();}));
          if(['let','read','for_range'].includes(step.op))fields.append(textInput('Bind variable',step.name,value=>{step.name=value;}));
          if(step.op==='require')fields.append(textInput('Assertion name',step.name,value=>{if(value)step.name=value;else delete step.name;}));
          if(['read','put','delete','emit'].includes(step.op))fields.append(select('Table',tableChoices(step.op),step.table,value=>{step.table=value;}));
          body.append(fields);
          if(step.key)body.append(lazy('State or emitted key',child=>expressionEditor(child,step.key,value=>{step.key=value;})));
          if(step.expr)body.append(lazy(step.op==='if'||step.op==='require'?'Condition':step.op==='for_range'?'Exclusive range end':'Value',child=>expressionEditor(child,step.expr,value=>{step.expr=value;})));
          if(step.body)body.append(lazy(step.op==='if'?'When true':'Body',child=>stepsEditor(child,step.body)));
          if(step.op==='if')body.append(lazy('When false',child=>{if(!step.else)step.else=[];stepsEditor(child,step.else);}));
          const controls=el('div');controls.className='actions';
          const up=button('Move earlier',()=>{if(index){[steps[index-1],steps[index]]=[steps[index],steps[index-1]];paint();changed();}}),down=button('Move later',()=>{if(index+1<steps.length){[steps[index+1],steps[index]]=[steps[index],steps[index+1]];paint();changed();}});up.disabled=index===0;down.disabled=index+1===steps.length;
          controls.append(up,down,button('Remove instruction',()=>{steps.splice(index,1);paint();changed();}));body.append(controls);
        });rows.append(details);
      });}
      actions.className='actions';actions.append(select('Add instruction type',availableOperations(),selected,value=>{selected=value;}),button('Add instruction',()=>{steps.push(newInstruction(selected));paint();changed();}));host.append(rows,actions);paint();
    }
    function declarationsEditor(host){
      const box=el('fieldset');host.append(box);box.append(el('legend','Output declarations'));
      for(const [index,table] of model.tables.entries()){
        const row=el('div');row.append(textInput('Output table name',table.name,value=>{table.name=value;}),textInput('Output key column',table.key,value=>{table.key=value;}),select('Duplicate output keys',['','identical'],table.on_conflict||'',value=>{if(value)table.on_conflict=value;else delete table.on_conflict;}));
        const remove=button('Remove output table',()=>{model.tables.splice(index,1);render();changed();});
        const references=steps=>steps.some(step=>step.table===table.name||references(step.body||[])||references(step.else||[]));
        remove.disabled=model.tables.length===1||references(model.ordered.steps)||(model.exports||[]).some(entry=>entry.table===table.name);remove.title='Remove its instructions and exports before removing a referenced table.';row.append(remove);box.append(row);
      }
      const add=button('Add output table',()=>{let name='outputs';while(model.tables.some(table=>table.name===name))name='outputs_'+(++sequence);model.tables.push({name,key:'ref'});render();changed();});add.disabled=model.tables.length>=64;box.append(add);
      for(const [index,entry] of (model.exports||[]).entries()){
        const row=el('div');row.append(textInput('Export name',entry.name,value=>{entry.name=value;}),select('Export table',model.tables.map(table=>table.name),entry.table,value=>{entry.table=value;}),textInput('Export column',entry.column,value=>{entry.column=value;}),select('Export type',['text','bool','integer','null'],entry.type,value=>{entry.type=value;}),button('Remove export',()=>{model.exports.splice(index,1);render();changed();}));box.append(row);
      }
      const addExport=button('Add same-block export',()=>{if(!model.exports)model.exports=[];let name='value';while(model.exports.some(entry=>entry.name===name))name='value_'+(++sequence);model.exports.push({name,kind:'same_block',table:model.tables[0].name,column:model.tables[0].key,type:'text'});render();changed();});addExport.disabled=(model.exports||[]).length>=64;box.append(addExport);
    }
    function fifoTablesEditor(host){
      const box=el('fieldset');box.append(el('legend','FIFO state tables'),el('p','Every source output is retained in current state. The optional history table requires a history write for each output event.'));
      const retained=model.tables.some(table=>table.name==='history'),references=steps=>steps.some(step=>step.table==='history'||references(step.body||[])||references(step.else||[]));
      const toggle=button(retained?'Remove retained-history table':'Add retained-history table',()=>{if(retained)model.tables=model.tables.filter(table=>table.name!=='history');else model.tables.push({name:'history',key:'output'});render();changed();});
      toggle.disabled=retained&&references(model.ordered.steps);box.append(toggle);host.append(box);
    }
    function stateTablesEditor(host){
      const box=el('fieldset');host.append(box);box.append(el('legend','State tables'));
      const references=(steps,name)=>steps.some(step=>step.table===name||references(step.body||[],name)||references(step.else||[],name));
      const rename=(steps,oldName,newName)=>{for(const step of steps){if(step.table===oldName)step.table=newName;rename(step.body||[],oldName,newName);rename(step.else||[],oldName,newName);}};
      for(const [index,table] of model.tables.entries()){
        const row=el('div'),name=el('input');row.className='recipe-fields ordered-editor-table';name.type='text';name.value=table.name;
        name.addEventListener('change',()=>{const next=name.value;if(!next||next.startsWith('__gateway_')||new TextEncoder().encode(next).length>256||model.tables.some(other=>other!==table&&other.name===next)){name.value=table.name;return;}rename(model.ordered.steps,table.name,next);table.name=next;render();changed();});
        const remove=button('Remove state table',()=>{model.tables.splice(index,1);render();changed();});remove.disabled=model.tables.length===1||references(model.ordered.steps,table.name);remove.title='Remove its instructions before removing a referenced table.';
        row.append(label('State table name',name),textInput('State key label',table.key,value=>{table.key=value;}),remove);box.append(row);
      }
      const add=button('Add state table',()=>{let name='state';while(model.tables.some(table=>table.name===name))name='state_'+(++sequence);model.tables.push({name,key:'key'});render();changed();});add.disabled=model.tables.length>=64;box.append(add);
    }
    function checkpointParentEditor(host){
      const dep=model.dependencies[0],box=el('fieldset'),row=el('div');box.append(el('legend','Checkpoint source'));host.append(box);
      const builtin=()=>getCatalogue()?.builtin_checkpoints?.find(value=>value.scope==='builtin'&&value.instance==='utxo-state'&&value.export==='coins');
      const pinBuiltin=()=>{const value=builtin();if(!value){parentNote='Built-in binding is unavailable; refresh the recipe catalogue.';render();return;}const name=dep.name;Object.assign(dep,copy(value),{name});parentNote='Pinned the installed built-in UTXO definition. Its checkpoint must already exist or be explicitly selected.';render();changed();};
      row.append(textInput('Dependency name',dep.name,value=>{dep.name=value;}),select('Checkpoint source scope',[['builtin','Built-in UTXO'],['saved','Saved Coin recipe']],dep.scope==='builtin'?'builtin':'saved',value=>{dep.scope=value;if(value==='builtin'){if(builtin()){pinBuiltin();return;}dep.instance='utxo-state';parentNote='Use the installed built-in binding before saving.';}render();}),textInput('Parent recipe ID',dep.instance,value=>{dep.instance=value;}));
      box.append(row,textInput('Pinned parent revision',dep.revision,value=>{dep.revision=value;}),textInput('Pinned parent semantic fingerprint',dep.semantic_hash,value=>{dep.semantic_hash=value;}));
      const actions=el('div'),native=button('Use built-in UTXO binding',pinBuiltin),saved=button('Read saved parent revision',async()=>{
        const captured=model,epoch=editEpoch,id=dep.instance,ticket=++parentEpoch;
        if(!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(id)){parentNote='Enter a saved parent recipe ID.';render();return;}
        saved.disabled=true;note.textContent='Reading the saved Coin definition…';
        try{const value=await readParent(id);if(ticket!==parentEpoch||captured!==model||epoch!==editEpoch||dep.instance!==id){if(ticket===parentEpoch&&captured===model)note.textContent='Draft changed; read the parent again.';return;}
          if(!value?.validated||value.recipe?.id!==id||value.recipe.source!=='bitcoin.ordered'||value.recipe.history!=='ordered'||value.recipe.storage!=='bitcoin.utxo.cow.v2'||!/^[a-f0-9]{64}$/.test(value.revision)||!/^[a-f0-9]{64}$/.test(value.semantic_hash))throw Error('Choose a validated ordered Coin recipe.');
          Object.assign(dep,{scope:'saved',revision:value.revision,semantic_hash:value.semantic_hash,export:'coins',coverage:'snapshot_delta',type:'coin'});parentNote='Pinned '+id+' at the returned immutable revision. Reading its definition starts no work.';render();changed();
        }catch(error){if(ticket===parentEpoch&&captured===model&&epoch===editEpoch){parentNote='Parent: '+error.message;render();}}
        finally{saved.disabled=false;}
      }),note=el('p',parentNote||'Uses an exact coins checkpoint and checked deltas. The child never starts a parent writer.');
      saved.disabled=dep.scope==='builtin';actions.append(native,saved);box.append(actions,note);
    }
    function render(){root.replaceChildren();if(!model)return;const legend=el('legend',model.history==='projection'?'Guided checkpoint draft':'Guided ordered draft'),header=el('div');header.className='recipe-fields';
      header.append(textInput('Ordered recipe ID',model.id,value=>{model.id=value;}),textInput('Ordered recipe name',model.name,value=>{model.name=value;}));
      root.append(legend,el('p',model.history==='projection'?'Derive current destination members and exclusion counts from the declared complete Coin snapshot, then apply checked deltas. Every source Coin must be accounted for. Source selection does not start its writer.':fifo()?'Process ordinary transactions in Bitcoin order, then allocate subsidy and their fees to coinbase outputs. Compose FIFO intervals, lost sats and inscription identities. The outputs table stores current state; the optional history table retains original output ranges.':generic()?'Process transaction events continuously from the declared origin. Read, write or delete exact typed values in your named tables. Expand instructions to compose counters, branches and keyed state.':model.history==='sparse'?'Project complete transaction events into per-block rows from the chosen origin. Empty evaluated blocks are retained; no UTXO prefix is required.':'Process transaction events in order from genesis. Coins are keyed by outpoint; counts by transaction ID. Expand an instruction to edit its expressions, branches or nested instructions.'),header);
      if(model.history==='sparse'){header.append(textInput('Origin height',String(model.origin),value=>{model.origin=/^\d+$/.test(value)?Number(value):value;}));declarationsEditor(root);}
      if(generic()){header.append(textInput('Origin height',String(model.origin),value=>{model.origin=/^\d+$/.test(value)?Number(value):value;}));stateTablesEditor(root);}
      if(model.history==='projection')checkpointParentEditor(root);
      if(fifo())fifoTablesEditor(root);
      stepsEditor(root,model.ordered.steps[0].body);root.append(button('Apply ordered changes',onApply));
    }
    return {load(definition){parentEpoch++;editEpoch++;parentNote='';model=toGuided(definition);root.hidden=!model;render();return !!model;},definition(){return fromGuided(model);}};
  }
  global.GatewayOrderedRecipeEditor={toGuided,fromGuided,expressionSupported,valueSupported,instruction,initialTables,mount};
})(typeof window==='undefined'?globalThis:window);
