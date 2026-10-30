(function(global){
  'use strict';
  const copy=value=>JSON.parse(JSON.stringify(value));
  const object=value=>!!value&&typeof value==='object'&&!Array.isArray(value);
  const keys=(value,allowed)=>object(value)&&Object.keys(value).every(key=>allowed.includes(key));
  const fields=['kind','block','txid','height','tx_index','output_count','coinbase','prev_txid','input_ref','fact_page','prev_vout','input_index','vout','value','fact_row','script'];
  const arities={eq:2,not:1,present:1,add_money:2,sub_money:2,less_equal:2,outpoint:2,destination:1,retains:2,bip30_exception:3,get:2};
  const operations={let:['name','expr'],read:['name','table','key'],put:['table','key','expr'],delete:['table','key'],if:['expr','body','else'],require:['name','expr'],emit:['table','key','expr'],for_range:['name','expr','body'],for_events:['body']};
  const uint=text=>typeof text==='string'&&/^(0|[1-9][0-9]*)$/.test(text)&&(text.length<20||text.length===20&&text<='18446744073709551615');
  function valueSupported(value,depth=0){
    if(depth>32||!object(value))return false;
    const kind=value.kind;if(!['missing','text','uint','bool','bytes','object'].includes(kind)||!keys(value,kind==='missing'?['kind']:['kind',kind]))return false;
    if(kind==='missing')return true;
    if(kind==='text')return !Object.hasOwn(value,'text')||typeof value.text==='string';
    if(kind==='uint')return uint(value.uint);
    if(kind==='bool')return !Object.hasOwn(value,'bool')||typeof value.bool==='boolean';
    if(kind==='bytes')return !Object.hasOwn(value,'bytes')||typeof value.bytes==='string'&&/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value.bytes);
    return object(value.object)&&Object.keys(value.object).length<=64&&Object.values(value.object).every(v=>valueSupported(v,depth+1));
  }
  function expressionSupported(expr,depth=0){
    if(depth>32||!object(expr))return false;
    const modes=['literal','var','field','call','object'].filter(key=>Object.hasOwn(expr,key));
    if(modes.length!==1)return false;
    const mode=modes[0];if(!keys(expr,mode==='call'?['call','args']:[mode]))return false;
    if(mode==='literal')return valueSupported(expr.literal);
    if(mode==='var')return typeof expr.var==='string'&&!!expr.var;
    if(mode==='field')return fields.includes(expr.field);
    if(mode==='call')return Object.hasOwn(arities,expr.call)&&Array.isArray(expr.args)&&expr.args.length===arities[expr.call]&&expr.args.every(arg=>expressionSupported(arg,depth+1));
    return object(expr.object)&&Object.keys(expr.object).length<=64&&Object.values(expr.object).every(arg=>expressionSupported(arg,depth+1));
  }
  function stepsSupported(steps,depth=0){
    return depth<=32&&Array.isArray(steps)&&steps.length<=512&&steps.every(step=>{
      if(!Object.hasOwn(operations,step?.op))return false;
      const allowed=operations[step.op];if(step.op==='for_events'&&depth!==0||!keys(step,['op',...allowed]))return false;
      if(Object.hasOwn(step,'name')&&(typeof step.name!=='string'||!step.name))return false;
      if(Object.hasOwn(step,'table')&&!(step.op==='emit'?['prevouts']:['coins','counts']).includes(step.table))return false;
      if(['let','read','for_range'].includes(step.op)&&!step.name)return false;
      if(['read','put','delete','emit'].includes(step.op)&&!expressionSupported(step.key))return false;
      if(['let','put','if','require','emit','for_range'].includes(step.op)&&!expressionSupported(step.expr))return false;
      if(['if','for_range','for_events'].includes(step.op)&&!stepsSupported(step.body,depth+1))return false;
      if(Object.hasOwn(step,'else')&&!stepsSupported(step.else,depth+1))return false;
      return true;
    });
  }
  function toGuided(definition){
    const d=definition;
    if(!keys(d,['format','version','id','name','source','origin','history','storage','dependencies','exports','tables','ordered'])||d.format!=='gateway-recipe'||d.version!==1||typeof d.id!=='string'||typeof d.name!=='string'||d.source!=='bitcoin.ordered'||d.origin!==0||d.history!=='ordered'||d.storage!=='bitcoin.utxo.cow.v2')return null;
    if(Object.hasOwn(d,'dependencies')&&(!Array.isArray(d.dependencies)||d.dependencies.length)||Object.hasOwn(d,'exports')&&(!Array.isArray(d.exports)||d.exports.length))return null;
    if(!Array.isArray(d.tables)||d.tables.length!==2||!d.tables.every(t=>keys(t,['name','key'])&&((t.name==='coins'&&t.key==='outpoint')||(t.name==='counts'&&t.key==='txid')))||new Set(d.tables.map(t=>t.name)).size!==2)return null;
    if(!keys(d.ordered,['steps'])||!stepsSupported(d.ordered.steps)||d.ordered.steps.length!==1||d.ordered.steps[0].op!=='for_events')return null;
    return copy(d);
  }
  function fromGuided(definition){
    const result=toGuided(definition);
    if(!result)throw Error('The ordered draft contains an unsupported shape or incomplete typed value.');
    if(!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(result.id)||!result.name.trim())throw Error('Provide a valid recipe ID and name.');
    return result;
  }
  const missing=()=>({literal:{kind:'missing'}});
  function instruction(op){
    const step={op};
    if(['let','read','for_range'].includes(op))step.name='value';
    if(['read','put','delete','emit'].includes(op)){step.table=op==='emit'?'prevouts':'coins';step.key={field:'txid'};}
    if(['let','put','if','require','emit','for_range'].includes(op))step.expr=op==='for_range'?{literal:{kind:'uint',uint:'0'}}:op==='if'||op==='require'?{literal:{kind:'bool',bool:true}}:missing();
    if(['if','for_range','for_events'].includes(op))step.body=[];
    if(op==='if')step.else=[];
    return step;
  }
  function mount(root,{onChange=()=>{},onApply=()=>{},getCatalogue=()=>null}={}){
    const doc=root.ownerDocument;let model=null,sequence=0;
    const el=(tag,text)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
    const label=(text,input)=>{const node=el('label',text);input.setAttribute('aria-label',text);node.append(input);return node;};
    const button=(text,action)=>{const node=el('button',text);node.type='button';node.addEventListener('click',action);return node;};
    const changed=()=>onChange();
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
      function draw(){host.replaceChildren();host.append(select('Value type',['missing','text','uint','bool','bytes','object'],value.kind,kind=>{
        value={kind};if(kind==='text')value.text='';if(kind==='uint')value.uint='0';if(kind==='bool')value.bool=false;if(kind==='bytes')value.bytes='';if(kind==='object')value.object=Object.create(null);set(value);draw();
      }));
      if(value.kind==='text')host.append(textInput('Text',value.text||'',text=>{value.text=text;}));
      if(value.kind==='uint')host.append(textInput('Unsigned integer (exact decimal)',value.uint,text=>{value.uint=text;}));
      if(value.kind==='bytes')host.append(textInput('Bytes (base64)',value.bytes||'',text=>{value.bytes=text;}));
      if(value.kind==='bool')host.append(select('Boolean',['false','true'],String(!!value.bool),text=>{value.bool=text==='true';}));
      if(value.kind==='object')members(host,value.object,literalEditor);
      }draw();
    }
    function expressionEditor(host,expr,set){
      function draw(){host.replaceChildren();const mode=['literal','var','field','call','object'].find(key=>Object.hasOwn(expr,key));
        host.append(select('Expression type',[['field','Source field'],['var','Variable'],['literal','Typed value'],['call','Function'],['object','Object fields']],mode,next=>{
          expr=next==='field'?{field:'kind'}:next==='var'?{var:'value'}:next==='call'?{call:'eq',args:[missing(),missing()]}:next==='object'?{object:Object.create(null)}:missing();set(expr);draw();
        }));
        if(mode==='field'){
          const declared=getCatalogue()?.sources?.find(source=>source.id==='bitcoin.ordered')?.fields;
          const available=Array.isArray(declared)?declared.filter(f=>Array.isArray(f.path)&&f.path.length===1).map(f=>f.path[0]):fields;
          host.append(select('Source field',available,expr.field,value=>{expr.field=value;}));
        }
        if(mode==='var')host.append(textInput('Variable name',expr.var,value=>{expr.var=value;}));
        if(mode==='literal'){const child=el('div');host.append(child);literalEditor(child,expr.literal,value=>{expr.literal=value;});}
        if(mode==='object')members(host,expr.object,expressionEditor);
        if(mode==='call'){
          const declared=getCatalogue()?.sources?.find(source=>source.id==='bitcoin.ordered')?.functions;
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
          fields.append(select('Instruction',Object.keys(operations).filter(op=>op!=='for_events'),step.op,op=>{steps[index]=instruction(op);paint();}));
          if(['let','read','for_range'].includes(step.op))fields.append(textInput('Bind variable',step.name,value=>{step.name=value;}));
          if(step.op==='require')fields.append(textInput('Assertion name',step.name,value=>{if(value)step.name=value;else delete step.name;}));
          if(['read','put','delete','emit'].includes(step.op))fields.append(select('Table',step.op==='emit'?['prevouts']:['coins','counts'],step.table,value=>{step.table=value;}));
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
      actions.className='actions';actions.append(select('Add instruction type',Object.keys(operations).filter(op=>op!=='for_events'),selected,value=>{selected=value;}),button('Add instruction',()=>{steps.push(instruction(selected));paint();changed();}));host.append(rows,actions);paint();
    }
    function render(){root.replaceChildren();if(!model)return;const legend=el('legend','Guided ordered draft'),header=el('div');header.className='recipe-fields';
      header.append(textInput('Ordered recipe ID',model.id,value=>{model.id=value;}),textInput('Ordered recipe name',model.name,value=>{model.name=value;}));
      root.append(legend,el('p','Process transaction events in order from genesis. Coins are keyed by outpoint; counts by transaction ID. Expand an instruction to edit its expressions, branches or nested instructions.'),header);
      stepsEditor(root,model.ordered.steps[0].body);root.append(button('Apply ordered changes',onApply));
    }
    return {load(definition){model=toGuided(definition);root.hidden=!model;render();return !!model;},definition(){return fromGuided(model);}};
  }
  global.GatewayOrderedRecipeEditor={toGuided,fromGuided,expressionSupported,valueSupported,instruction,mount};
})(typeof window==='undefined'?globalThis:window);
