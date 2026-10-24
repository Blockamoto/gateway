'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),crypto=require('node:crypto');
const scope={window:{}};vm.createContext(scope);vm.runInContext(fs.readFileSync(path.join(__dirname,'../ui/shell/transaction-flow-layout.js'),'utf8'),scope);
const flow=scope.window.GatewayTransactionFlow,fixtures=JSON.parse(fs.readFileSync(path.join(__dirname,'transaction-flow-reference.json'),'utf8'));
const numeric=value=>value==null?null:(value.match(/-?\d+(?:\.\d+)?(?:e[+-]?\d+)?/g)||[]).map(Number).map(n=>Math.round(n*1e7)/1e7);
const round=value=>Math.round(value*1e7)/1e7;
const projection=result=>({inputs:result.inputs.map(r=>({path:numeric(r.path),thickness:round(r.thickness),zero:r.zeroValue,marker:numeric(r.markerPath),connector:numeric(r.connectorPath)})),outputs:result.outputs.map(r=>({path:numeric(r.path),thickness:round(r.thickness),zero:r.zeroValue,marker:numeric(r.markerPath),connector:numeric(r.connectorPath)})),middle:{path:numeric(result.middle.path),thickness:round(result.middle.thickness)},hasLine:result.hasLine});
const digest=value=>crypto.createHash('sha256').update(JSON.stringify(value)).digest('hex');
for(const {scenario,options,digest:expected}of fixtures.cases){
 const values=scenario.count?Array.from({length:scenario.count},(_,i)=>i%11===0?0:(i+1)*37):null;
 const data={inputs:(values||scenario.inputs).map((value,index)=>({index,value,coinbase:scenario.coinbase===true,outpoint:scenario.coinbase?'coinbase':'fixture:'+index})),outputs:(values||scenario.outputs).map((value_sats,n)=>({n,value_sats})),fee:scenario.count?0:scenario.fee};
 const result=flow.layout(data,options);
 assert.equal(digest(projection(result)),expected,'Pinned official reference geometry: '+scenario.name+' '+JSON.stringify(options));
 for(const row of [...result.inputs,...result.outputs])for(const n of numeric(row.path))assert(Number.isFinite(n),'No nonfinite paths');
 assert.equal(result.inputCount,data.inputs.length);assert.equal(result.outputCount,data.outputs.length);
 if(scenario.count===260){assert.equal(result.inputs.length,251);assert.equal(result.inputs.at(-1).rest,10);assert.equal(result.outputs.at(-1).rest,10);}
 if(scenario.name==='unknown')assert.equal(result.inputs[1].value,null,'Residual visual width never becomes an established input value');
}
for(const value of [-1,1.2,Infinity,NaN,Number.MAX_SAFE_INTEGER+1])assert.throws(()=>flow.layout({inputs:[{value}],outputs:[],fee:0}),/integer sats/);
assert.throws(()=>flow.layout({inputs:[],outputs:[{value_sats:Number.MAX_SAFE_INTEGER},{value_sats:1}],fee:0}),/exact integer/);
assert.throws(()=>flow.layout({inputs:[{value:Number.MAX_SAFE_INTEGER},{value:1}],outputs:[],fee:0}),/exact integer/);
assert.throws(()=>flow.layout({inputs:[{index:1,value:1}],outputs:[],fee:0}),/original transaction order/);
const inconsistent=flow.layout({inputs:[{value:100},{value:null}],outputs:[{n:0,value_sats:50}],fee:null});
assert.equal(inconsistent.inputs[1].weight,0,'An unknown value never produces a negative residual band');
class Node {
 constructor(tag){this.tagName=tag;this.attributes={};this.children=[];this.listeners={};this.textContent='';this.style={};}
 setAttribute(key,value){this.attributes[key]=value;}
 removeAttribute(key){delete this.attributes[key];}
 append(...children){for(const child of children){child.parent=this;this.children.push(child);}}
 replaceChildren(...children){this.children=[];this.append(...children);}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(child=>child!==this);}
 focus(){this.focused=true;}
 addEventListener(type,callback){(this.listeners[type]??=[]).push(callback);}
 set innerHTML(value){throw Error('Renderer must never insert API data through innerHTML');}
 fire(type,event={}){for(const callback of this.listeners[type]||[])callback({defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},...event});}
}
const doc={createElementNS(ns,tag){assert.equal(ns,'http://www.w3.org/2000/svg');return new Node(tag);}};
const flatten=node=>[node,...node.children.flatMap(flatten)],calls=[];
const verified={id:'fixturei0',coordinate:'1i0.840000.bitcoin',state:'verified_reveal_output',output_index:0};
const data={inputs:[{value:null,outpoint:'<script>alert(1)</script>'}],outputs:[{n:0,value_sats:0},{n:1,value_sats:100}],fee:0,inscription_context:{entries:[verified,{...verified,state:'historical_context_required',output_index:1},{...verified,state:'fee_to_coinbase',output_index:1}]}};
const svg=flow.render(doc,data,{onInput:payload=>calls.push(['input',payload.index]),onOutput:payload=>calls.push(['output',payload.n]),onInscription:entry=>calls.push(['inscription',entry.id])});
assert.equal(svg.attributes.viewBox,'0 0 1200 610','Initial reference viewport frames max24 strands; full bounds are separate for deliberate Fit');
const longSvg=flow.render(doc,{inputs:Array.from({length:100},()=>({value:10})),outputs:Array.from({length:100},(_,n)=>({n,value_sats:10})),fee:0});
assert.equal(longSvg.attributes.viewBox,'0 0 1200 610');assert(longSvg.flowLayout.bounds.height>610,'All later endpoints remain available to pan/explicit Fit');
const all=flatten(svg),output=all.find(node=>node.attributes['data-flow-side']==='output'&&node.attributes['data-flow-index']==='0');
output.fire('click');output.fire('keydown',{key:'Enter'});assert.deepEqual(calls,[['output',0],['output',0]],'Zero output is keyboard and pointer selectable');
const badges=all.filter(node=>node.attributes['data-flow-inscription-output']!==undefined);assert.equal(badges.length,1,'Only verified reveal output placement gets an output badge');badges[0].fire('click');assert.deepEqual(calls.at(-1),['inscription','fixturei0']);
const input=all.find(node=>node.attributes['data-flow-side']==='input');input.fire('focus');
let tip=flatten(svg).find(node=>node.attributes.role==='tooltip');
assert.equal(tip.attributes['aria-hidden'],'false','Keyboard focus shows details immediately');
assert(flatten(tip).some(node=>node.tagName==='text'&&node.textContent.includes('<script>')),'Untrusted labels remain literal text nodes');
assert(flatten(tip).some(node=>node.textContent==='Value unresolved'),'Unknown input stays unresolved');
input.fire('blur');assert.equal(tip.attributes['aria-hidden'],'true');
output.fire('pointerenter');tip=flatten(svg).find(node=>node.attributes.role==='tooltip');
assert(flatten(tip).some(node=>node.textContent==='0.00000000 BTC · 0 sats'),'A zero output immediately shows BTC and sats');
output.fire('pointerleave');assert.equal(tip.attributes['aria-hidden'],'true');
const described=flow.render(doc,{inputs:[{value:null}],outputs:[{n:0,value_sats:123456789,address:'bc1qhistorical'},{n:1,value_sats:1,type:'nonstandard'}],fee:null});
const describedOutputs=flatten(described).filter(node=>node.attributes['data-flow-side']==='output');
describedOutputs[0].fire('pointerenter');let texts=flatten(described).filter(node=>node.tagName==='text').map(node=>node.textContent);
assert(texts.includes('Output #0')&&texts.includes('1.23456789 BTC · 123,456,789 sats')&&texts.includes('Historical destination: bc1qhistorical'));
describedOutputs[1].fire('focus');texts=flatten(described).filter(node=>node.tagName==='text').map(node=>node.textContent);assert(texts.includes('Script type: nonstandard'),'Script type is shown when no address exists');
assert(!describedOutputs.some(node=>node.attributes.class.includes('unknown-value')),'Known outputs are solid while unrelated input values remain unknown');
const narrowTip=flow.render(doc,{inputs:[{value:null,outpoint:'a'.repeat(64)+':123'}],outputs:[{n:0,value_sats:1}],fee:null},{width:280});
const narrowInput=flatten(narrowTip).find(node=>node.attributes['data-flow-side']==='input');narrowInput.fire('focus');
let narrowDetails=flatten(narrowTip).find(node=>node.attributes.role==='tooltip'),narrowBox=narrowDetails.children[0];
assert(Number(narrowBox.attributes.width)<=264);assert(narrowDetails.children.slice(1).every(node=>node.textContent.length*7+24<=Number(narrowBox.attributes.width)),'Narrow details wrap inside their background');
narrowTip.viewBox={baseVal:{x:40,y:10,width:140,height:305}};narrowInput.fire('focus');narrowDetails=flatten(narrowTip).find(node=>node.attributes.role==='tooltip');assert(Number(narrowDetails.children[0].attributes.width)<=132,'Zoomed details fit the live viewport');
assert(!all.some(node=>['script','foreignObject','iframe','image'].includes(node.tagName)),'Flow metadata never activates inscription content');
const more=flow.render(doc,{inputs:Array.from({length:3},()=>({value:10})),outputs:[{n:0,value_sats:25}],fee:5},{lineLimit:1,onInput:()=>calls.push(['group'])});
const rest=flatten(more).find(node=>node.attributes['data-flow-rest']);const before=calls.length;rest.fire('click');rest.fire('keydown',{key:'Enter'});assert.equal(calls.length,before,'An aggregated group cannot navigate as an individual input');
assert.notEqual(svg.children[0].children[0].attributes.id,more.children[0].children[0].attributes.id,'Concurrent SVG gradients have unique IDs');
const multi=flow.render(doc,{inputs:[{value:100}],outputs:[{n:0,value_sats:100}],fee:0,inscription_context:{entries:Array.from({length:25},(_,index)=>({...verified,id:'fixturei'+index,coordinate:'1i'+index+'.840000.bitcoin'}))}},{onInscription:entry=>calls.push(['picked',entry.id])});
const countBadge=flatten(multi).find(node=>node.attributes['data-flow-inscription-output']==='0');const beforePicker=calls.length;countBadge.fire('click');assert.equal(calls.length,beforePicker,'A count badge never silently opens only the first inscription');
let choices=flatten(multi).filter(node=>node.attributes['data-flow-inscription-choice']!==undefined);assert.equal(choices.length,8,'The picker DOM stays bounded');choices[0].fire('keydown',{key:'End'});choices=flatten(multi).filter(node=>node.attributes['data-flow-inscription-choice']!==undefined);assert.equal(choices.at(-1).attributes['data-flow-inscription-choice'],'24','Keyboard reaches the last verified occurrence');choices.at(-1).fire('keydown',{key:'Enter'});assert.deepEqual(calls.at(-1),['picked','fixturei24']);assert.equal(flatten(multi).filter(node=>node.attributes['data-flow-inscription-menu']!==undefined).length,0);
const later=flow.render(doc,{inputs:[{value:1000}],outputs:Array.from({length:100},(_,n)=>({n,value_sats:10})),fee:0,inscription_context:{entries:[{...verified,output_index:70},{...verified,id:'fixturei1',output_index:70}]}});flatten(later).find(node=>node.attributes['data-flow-inscription-output']==='70').fire('click');const laterMenu=flatten(later).find(node=>node.attributes['data-flow-inscription-menu']==='70');assert(Number(laterMenu.children[0].attributes.y)>610,'A panned later output keeps its picker near that output, beyond the initial24-strand frame');
console.log('Transaction flow PASS: '+fixtures.cases.length+' pinned Mempool geometry vectors; exact ordering/fee/zero/unknown/coinbase/grouped IO; honest metadata, safe DOM, keyboard hooks and verified-only placement badges.');
