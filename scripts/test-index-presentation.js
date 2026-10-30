'use strict';
const assert=require('node:assert/strict');
require('../ui/shell/index-presentation.js');
const {flatten,mount}=globalThis.GatewayIndexPresentation;
const nodes=[{id:'c',kind:'category',name:'C'},{id:'p',kind:'track',name:'P',parent:'c',index:'blocks'},{id:'child',kind:'track',name:'Child',parent:'p',index:'missing'},{id:'next',kind:'category',name:'Next'}];
assert.deepEqual(flatten(nodes).map(x=>[x.node.id,x.depth]),[['c',0],['p',1],['child',2],['next',0]]);
assert.deepEqual(flatten(nodes,{p:false}).map(x=>x.node.id),['c','p','next']);
class Node {
 constructor(tag){this.tagName=tag;this.children=[];this.value='';this.hidden=false;this.disabled=false;this.textContent='';this.style={};this.dataset={};this.attributes={};this.events={};}
 append(...values){this.children.push(...values);}
 replaceChildren(...values){this.children=values;}
 setAttribute(k,v){this.attributes[k]=String(v);}
 addEventListener(k,v){this.events[k]=v;}
}
const doc={createElement:t=>new Node(t)}, controls=new Node('div'),container=new Node('div');
(async()=>{
 const calls=[],painted=[];let saveWait=null,saved={revision:4,nodes:structuredClone(nodes)};
 const api=async(action,body)=>{calls.push({action,body});if(body){assert.equal(action,'layout/hierarchy');if(saveWait)await saveWait;if(body.expected_revision!==saved.revision)throw Error('stale');saved={revision:saved.revision+1,nodes:structuredClone(body.nodes)};}return structuredClone(saved);};
 const p=mount({document:doc,controls,container,api,definitionFor:id=>id==='blocks'?{id:'blocks',name:'Bitcoin blocks'}:null,drawLane:(canvas,d)=>painted.push(d.id),select:()=>{throw Error('not invoked');}});
 await Promise.resolve();await Promise.resolve();
 assert.equal(calls.length,1);assert.equal(calls[0].body,undefined,'initial load is read only');
 assert.equal(container.children.length,4);assert.equal(container.children[2].children[1].children[0].textContent,'Unavailable reference: missing');
 assert(painted.includes('blocks'),'known reference uses actual track renderer');
 const detail=controls.children[0],fields=detail.children[2],actions=detail.children[3],status=detail.children[4];
 const name=fields.children[0].children[0],kind=fields.children[1].children[0],parent=fields.children[2].children[0],index=fields.children[3].children[0];
 assert.match(detail.children[1].textContent,/0\.7\.9 cannot read/);
 name.value='Nested';kind.value='track';kind.events.change();parent.value='p';index.value='future-index';await actions.children[0].events.click();
 const save=calls.at(-1);assert.equal(save.action,'layout/hierarchy');assert.equal(save.body.expected_revision,4);assert.deepEqual(save.body.nodes.at(-1),{id:save.body.nodes.at(-1).id,kind:'track',name:'Nested',parent:'p',index:'future-index'});assert(!('dependencies' in save.body.nodes.at(-1)));
 assert.equal(container.children.length,5);assert.match(status.textContent,/does not start indexing/);
 const toggle=container.children[1].children[0].children[0];toggle.events.click();assert.deepEqual(container.children.map(x=>x.dataset.presentationId),['c','p','next']);
 p.draw();p.refresh();assert.equal(calls.length,2,'redraw does not save or start a run');
 let finishSave;saveWait=new Promise(resolve=>finishSave=resolve);name.value='Earlier';kind.value='category';kind.events.change();parent.value='';const pending=actions.children[0].events.click();
 name.value='Newer entry';name.events.input();finishSave();await pending;saveWait=null;
 assert.equal(name.value,'Newer entry','late save cannot erase newer form edits');assert.match(status.textContent,/newer form edits remain/);

 saved.revision++;name.value='Unsaved';kind.value='category';kind.events.change();parent.value='';await actions.children[0].events.click();assert.match(status.textContent,/stale/);assert.equal(name.value,'Unsaved','conflict preserves form');
 console.log('Presentation timeline PASS: nested lanes, unknown placeholders, coverage rendering, explicit writes and conflict preservation.');
})().catch(error=>{console.error(error);process.exitCode=1;});
