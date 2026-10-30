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
mounted.load({...original,storage:'unsupported'});assert.equal(root.hidden,true);assert.throws(()=>mounted.definition(),/unsupported shape/);
console.log('Ordered recipe editor PASS: actual built-in round-trip, owned drafts, exact uints, unsupported preservation, typed expressions, nested instructions and reordering.');
