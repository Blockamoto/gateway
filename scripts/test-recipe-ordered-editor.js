'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs');
require('../ui/shell/recipe-ordered-editor.js');
const editor=globalThis.GatewayOrderedRecipeEditor,original=JSON.parse(fs.readFileSync('recipes/builtins/utxo-state.json','utf8'));
const guided=editor.toGuided(original);assert(guided,'the actual executable UTXO document has a guided representation');
assert.deepEqual(editor.fromGuided(guided),original,'all instructions, expressions, diagnostic names and table declarations round-trip');
guided.name='Changed';assert.notEqual(original.name,guided.name,'the editor owns its draft');
for(const mutate of [d=>d.ordered.steps[0].body[0].unsupported=true,d=>d.dependencies.push({name:'unsupported'}),d=>d.storage='other',d=>d.tables.push({name:'other',key:'id'}),d=>d.ordered.steps[0].body.push({op:'for_events',body:[]}),d=>d.ordered.steps[0].body.push({op:'__proto__'})]){
 const d=structuredClone(original);mutate(d);const text=JSON.stringify(d);assert.equal(editor.toGuided(d),null);assert.equal(JSON.stringify(d),text,'unsupported forms are never dropped or rewritten');
}
for(const uint of ['0','9007199254740993','18446744073709551615'])assert(editor.valueSupported({kind:'uint',uint}));
for(const uint of [9007199254740993,'-1','01','1.5','18446744073709551616'])assert.equal(editor.valueSupported({kind:'uint',uint}),false);
assert.equal(editor.valueSupported({kind:'missing',missing:'extra'}),false);
assert(editor.valueSupported({kind:'object',object:JSON.parse('{"__proto__":{"kind":"text","text":"literal"},"Title":{"kind":"uint","uint":"9007199254740993"}}')}));
class Node{
 constructor(tag){this.tagName=tag;this.value='';this.textContent='';this.children=[];this.events={};this.attributes={};this.hidden=false;this.disabled=false;this.open=false;this.ownerDocument=doc;}
 append(...nodes){this.children.push(...nodes);}replaceChildren(...nodes){this.children=nodes;}setAttribute(key,value){this.attributes[key]=value;}addEventListener(type,fn){this.events[type]=fn;}
}
const doc={createElement:tag=>new Node(tag)},root=new Node('fieldset');
function find(node,predicate){if(predicate(node))return node;for(const child of node.children){const value=find(child,predicate);if(value)return value;}return null;}
const control=(name,within=root)=>{const value=find(within,node=>node.attributes['aria-label']===name);assert(value,'control '+name);return value;};
const button=(name,within=root)=>{const value=find(within,node=>node.tagName==='button'&&node.textContent===name);assert(value,'button '+name);return value;};
function expand(name,within=root){const value=find(within,node=>node.tagName==='details'&&node.children[0]?.textContent===name);assert(value,'expand '+name);value.open=true;value.events.toggle();return value;}
let changes=0,applied=0;
const mounted=editor.mount(root,{onChange:()=>changes++,onApply:()=>applied++});
assert(mounted.load(original));assert.equal(root.hidden,false);assert.deepEqual(mounted.definition(),original,'viewing collapsed controls preserves the full program');
const empty=structuredClone(original);empty.id='my-guided-coins';empty.name='My guided Coins';empty.ordered.steps[0].body=[];mounted.load(empty);
const add=control('Add instruction type');add.value='let';add.events.change();button('Add instruction').events.click();
let step=expand('1. let value');let name=control('Bind variable',step);name.value='exact_limit';name.events.input();
let value=expand('Value',step),kind=control('Value type',value);kind.value='uint';kind.events.change();
let decimal=control('Unsigned integer (exact decimal)',value);decimal.value='9007199254740993';decimal.events.input();
assert.equal(control('Expression type',value).value,'literal','typed literals retain the expression type selector');
assert.deepEqual(mounted.definition().ordered.steps[0].body,[{op:'let',name:'exact_limit',expr:{literal:{kind:'uint',uint:'9007199254740993'}}}]);
button('Apply ordered changes').events.click();assert.equal(applied,1);assert(changes>=4);
// Change a typed value into a source field, then compose a condition and a
// nested write through controls rather than entering JSON.
let expression=control('Expression type',value);expression.value='field';expression.events.change();let source=control('Source field',value);source.value='height';source.events.change();
add.value='if';add.events.change();button('Add instruction').events.click();
step=expand('2. if');const branch=expand('When true',step),nestedAdd=control('Add instruction type',branch);nestedAdd.value='delete';nestedAdd.events.change();button('Add instruction',branch).events.click();
let nested=expand('1. delete',branch),table=control('Table',nested);table.value='counts';table.events.change();
const key=expand('State or emitted key',nested);source=control('Source field',key);source.value='txid';source.events.change();
let result=mounted.definition();assert.equal(result.ordered.steps[0].body[0].expr.field,'height');assert.deepEqual(result.ordered.steps[0].body[1].body,[{op:'delete',table:'counts',key:{field:'txid'}}]);
// Reorder and remove use the currently displayed list, preserving subtrees.
button('Move earlier',step.children[1].children.at(-1)).events.click();assert.equal(mounted.definition().ordered.steps[0].body[0].op,'if');
step=expand('1. if');button('Remove instruction',step.children[1].children.at(-1)).events.click();assert.equal(mounted.definition().ordered.steps[0].body.length,1);
for(const file of ['address-history.json','address-history-payment-legacy.json']){const actual=JSON.parse(fs.readFileSync('recipes/builtins/'+file,'utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(actual)),actual,'actual address-history composition round-trips');}
const sparse=JSON.parse(fs.readFileSync('recipes/output-destinations.json','utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(sparse)),sparse,'stateless output projections and exports round-trip');
for(const file of ['output-values.json','output-scripts.json','output-values-scripts.json']){const actual=JSON.parse(fs.readFileSync('recipes/builtins/'+file,'utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(actual)),actual,'actual independent retained output layer round-trips');}
const inputs=JSON.parse(fs.readFileSync('recipes/builtins/txio-inputs.json','utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(inputs)),inputs,'actual spending input counter and coordinate composition round-trips');
assert(mounted.load(sparse));let origin=control('Origin height');origin.value='100';origin.events.input();assert.equal(mounted.definition().origin,100);
button('Add output table').events.click();assert.equal(mounted.definition().tables.length,2);button('Add same-block export').events.click();assert.equal(mounted.definition().exports.length,2);
const sparseAdd=control('Add instruction type');assert(!sparseAdd.children.some(option=>['read','put','delete'].includes(option.value)),'stateless controls offer output instructions only');
sparseAdd.value='emit';sparseAdd.events.change();button('Add instruction').events.click();let added=expand('2. emit');assert.equal(control('Table',added).value,'outputs');
mounted.load({...original,storage:'unsupported'});assert.equal(root.hidden,true);assert.throws(()=>mounted.definition(),/unsupported shape/);
const stateful=JSON.parse(fs.readFileSync('recipes/output-count-state.json','utf8'));
assert.deepEqual(editor.initialTables(),stateful,'the guided starter is the actual executable example');
assert.deepEqual(editor.fromGuided(editor.toGuided(stateful)),stateful);assert(mounted.load(stateful));
origin=control('Origin height');origin.value='10';origin.events.input();assert.equal(mounted.definition().origin,10);
let tableName=control('State table name');tableName.value='totals';tableName.events.change();
assert.equal(mounted.definition().tables[0].name,'totals');assert.equal(mounted.definition().ordered.steps[0].body[0].body[0].table,'totals','renaming preserves instruction references');
assert.equal(button('Remove state table').disabled,true,'a referenced state table cannot disappear');
button('Add state table').events.click();assert.equal(mounted.definition().tables.length,2);
const genericAdd=control('Add instruction type');assert(genericAdd.children.some(option=>option.value==='read'));assert(!genericAdd.children.some(option=>option.value==='emit'));
genericAdd.value='put';genericAdd.events.change();button('Add instruction').events.click();let statePut=expand('2. put');assert.deepEqual(control('Table',statePut).children.map(option=>option.value),['totals','state']);
const genericKey=expand('State or emitted key',statePut);assert(!control('Source field',genericKey).children.some(option=>['fact_page','fact_row'].includes(option.value)));
for(const mutate of [d=>d.tables[0].name='__gateway_reserved',d=>d.tables.push({name:'counts',key:'id'}),d=>d.tables[0].on_conflict='identical',d=>d.ordered.steps[0].body.push({op:'emit',table:'counts',key:{field:'txid'},expr:{field:'value'}}),d=>d.ordered.steps[0].body.push({op:'let',name:'physical',expr:{field:'fact_page'}})]){const d=structuredClone(stateful);mutate(d);assert.equal(editor.toGuided(d),null);}
for(const file of ['sat-state.json','sat-ranges.json']){const actual=JSON.parse(fs.readFileSync('recipes/builtins/'+file,'utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(actual)),actual,'actual FIFO current/history programs round-trip');assert(mounted.load(actual));const eventLoop=actual.ordered.steps[0].body;assert(eventLoop.some(step=>step.expr?.args?.[1]?.literal?.text==='inscription'));}
mounted.load(JSON.parse(fs.readFileSync('recipes/builtins/sat-state.json','utf8')));button('Add retained-history table').events.click();assert(mounted.definition().tables.some(table=>table.name==='history'));button('Remove retained-history table').events.click();assert.equal(mounted.definition().tables.length,1);
assert(editor.valueSupported({kind:'ranges',ranges:[{start:'900719925474099',end:'900719925474100'}]}));
for(const ranges of [null,[{start:'01',end:'2'}],[{start:'2',end:'2'}],[{start:'0',end:'2099999997690001'}],[{start:0,end:'2'}]])assert.equal(editor.valueSupported({kind:'ranges',ranges}),false);
const current=JSON.parse(fs.readFileSync('recipes/builtins/address-state.json','utf8'));
for(const file of ['address-state.json','address-state-payment-legacy.json']){const actual=JSON.parse(fs.readFileSync('recipes/builtins/'+file,'utf8'));assert.deepEqual(editor.fromGuided(editor.toGuided(actual)),actual,'actual current-address dependency composition round-trips');}
for(const mutate of [d=>d.dependencies[0].coverage='same_block',d=>d.dependencies[0].scope='unknown',d=>d.dependencies[0].revision='invalid',d=>d.ordered.steps[0].body.push({op:'read',name:'x',table:'coins',key:{field:'height'}}),d=>d.ordered.steps[0].body[0].expr={field:'txid'}]){
 const value=structuredClone(current);mutate(value);const originalText=JSON.stringify(value);assert.equal(editor.toGuided(value),null);assert.equal(JSON.stringify(value),originalText,'unsupported checkpoint forms preserve raw text');
}
;(async()=>{
 const checkpointRoot=new Node('fieldset'),binding=structuredClone(current.dependencies[0]);let respond;
 const parentValue={validated:true,revision:'f'.repeat(64),semantic_hash:'b'.repeat(64),recipe:{...original,id:'guided-source-coins'}};
 const checkpoint=editor.mount(checkpointRoot,{getCatalogue:()=>({builtin_checkpoints:[binding]}),readParent:id=>{assert.equal(id,'guided-source-coins');return new Promise(resolve=>{respond=resolve;});}});
 assert(checkpoint.load(current));assert.deepEqual(checkpoint.definition(),current);
 const scope=control('Checkpoint source scope',checkpointRoot);scope.value='saved';scope.events.change();
 let parentID=control('Parent recipe ID',checkpointRoot);parentID.value='guided-source-coins';parentID.events.input();
 let click=button('Read saved parent revision',checkpointRoot).events.click();respond(parentValue);await click;
 let definition=checkpoint.definition();assert.equal(definition.dependencies[0].scope,'saved');assert.equal(definition.dependencies[0].revision,parentValue.revision);assert.equal(definition.dependencies[0].semantic_hash,parentValue.semantic_hash);
 click=button('Read saved parent revision',checkpointRoot).events.click();const alias=control('Dependency name',checkpointRoot);alias.value='renamed_source';alias.events.input();respond({...parentValue,revision:'c'.repeat(64)});await click;
 assert.equal(checkpoint.definition().dependencies[0].revision,parentValue.revision,'a changed draft fences a late source response');
 button('Use built-in UTXO binding',checkpointRoot).events.click();definition=checkpoint.definition();assert.equal(definition.dependencies[0].name,'renamed_source');assert.equal(definition.dependencies[0].scope,'builtin');assert.equal(definition.dependencies[0].revision,binding.revision);
 const add=control('Add instruction type',checkpointRoot);assert(!add.children.some(option=>['read','put','delete'].includes(option.value)),'checkpoint controls emit source-bound deltas rather than state writes');
 const first=expand('1. let destination',checkpointRoot),value=expand('Value',first),mode=control('Expression type',value);mode.value='field';mode.events.change();const field=control('Source field',value);assert(field.children.some(option=>option.value==='add'));assert(!field.children.some(option=>option.value==='txid'));
 assert.equal(find(checkpointRoot,node=>node.tagName==='button'&&node.textContent==='Add output table'),null,'physical checkpoint tables remain fixed');
 console.log('Ordered recipe editor PASS: actual built-ins and checkpoint dependencies round-trip, exact uints, guided source pinning, stale responses, typed expressions and nested instructions.');
})().catch(error=>{console.error(error);process.exitCode=1;});
