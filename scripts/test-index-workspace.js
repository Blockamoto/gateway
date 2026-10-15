'use strict';
// Executes the shipped Indexes controller and pure models at event/API boundaries.
// The timeline DOM is covered separately in Chromium; this fixture isolates its callbacks.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const {Node,documentFor}=require('./index-dom-fixture');
Node.prototype.before=function(){};
const html=fs.readFileSync('ui/shell/indexes.html','utf8');
const {doc,nodes,tabs,panes}=documentFor(html);
const definition={id:'bitmap',name:'Bitmap',buildable:true,theory:'Base districts',start_height:792435,dependencies:['blocks'],rules:['First valid claim wins'],version:1,network:'mainnet'};
let job=null,calls=[],instances=[],live=[],jobs=[],refreshTimer;
const definitions=[{...definition,id:'headers',name:'Bitcoin headers',buildable:false},definition,{...definition,id:'inscriptions',name:'Inscriptions',start_height:0,arbitrary_start:true},{...definition,id:'address-history',buildable:false,locked:true,lock_reason:'Locked during Headers and Blocks testing.'}];
const snapshot=()=>({definitions,instances,providers:[],job,live,jobs});
let queryPending=null,mutationGate=null;
const fetch=async(url,opts)=>{
 const body=opts?.body&&JSON.parse(opts.body);calls.push({url,body});let result=snapshot();
 if(url.endsWith('/plan'))result={definition:definitions.find(d=>d.id===body.index),from:792435,to:792436,dependencies:[],notes:[],retention:'ephemeral'};
 if(url.endsWith('/build'))result=job={id:'job1',index:body.index,state:'running',from:792435,to:792436,height:792434,retention:'ephemeral'};
 if(url.endsWith('/queue')){result={ok:true};}
 if(url.endsWith('/pause'))result=job={...job,state:'paused'};
 if(url.endsWith('/live')){const old=live.find(x=>x.index===body.index)||{index:body.index,enabled:false};result={...old,on:body.action!=='stop',stopped:body.action==='stop',enabled:body.action==='enable'?true:body.action==='disable'?false:old.enabled};live=[...live.filter(x=>x.index!==body.index),result];}
 if(url.includes('/query?')){
  if(queryPending)return queryPending;
  result={checkpoint:{height:792435},rows:[{schema:'bitmap-lean-v2',district:0,inscription:'testi0',reveal_height:792435}],total:1,total_known:true,inspected_blocks:1,chain_state:'selected_chain'};
 }
 if(mutationGate?.action===url.split('/').at(-1))await mutationGate.wait;
 return {ok:true,json:async()=>result};
};
let timelineOptions,selectedTimeline='',timelineSnapshot={},navigation=[],explorerFocus=[],draftChanges=[];
const drawer=new Node('details');drawer.open=false;nodes.set('timeline-details',drawer);
const inspector=new Node('section');inspector.scrollTo=()=>{};nodes.set('timeline-inspector',inspector);
nodes.set('timeline-inspector-panel',inspector);
// The timeline layout component creates this viewport before child mounting;
// the controller-only boundary supplies that owned node without relaxing the
// fixture's strict checks for IDs actually present in the source template.
nodes.set('workspace-viewport-body',new Node('div'));
// Child components own their browser DOM; this controller fixture supplies the
// mount boundary only. Their real layout and sandbox are tested in Chromium.
const timelineEditor=new Node('section');doc.querySelector=selector=>selector==='.timeline-editor'?timelineEditor:doc.querySelectorAll(selector)[0]||null;
nodes.get('mode').options=[...html.match(/<select id="mode">([\s\S]*?)<\/select>/)[1].matchAll(/<option value="([^"]+)">([^<]+)<\/option>/g)].map(match=>{const option=new Node('option');option.value=match[1];option.textContent=match[2];return option;});
doc.addEventListener=()=>{};
const window={addEventListener(){},GatewayWorkspaceResources:{mount(){return {classify(){return null},overview(){},suspend(){},teardown(){}}}},GatewayUTXOPanel:{mount(){return {render(){},health(){}}}},GatewayInscriptionViewport:{coordinate:record=>record?.reveal_coordinate||'',mount(){return {available(){},setKnown(){},open(){},teardown(){}}}},GatewaySemanticTimeline:{mount(){return {clear(){},open(){},setMarkers(){}}}},GatewayTimelineExplorer:{mount(){return {focus:value=>explorerFocus.push(value),health(){},setTab(){}}}},GatewayIndexWorkspace:{mountLayout(){},mountRange(){return {update(){},sync(){}}}},GatewayIndexTimeline:{mount(options){timelineOptions=options;return {
 render(value){timelineSnapshot=value}, health(){}, acceptResource(){},cancelPendingNavigation(){},setConsumerOptions(id,options){draftChanges.push({id,options})},getRunDraft(){return {consumers:[]}},
 select(id){selectedTimeline=id;options.workspace.hidden=!id||!!definitions.find(d=>d.id===id)?.locked},
 model(id){return window.GatewayIndexCards.model(definitions.find(d=>d.id===id)||{id},timelineSnapshot)}
}}}};
const context=vm.createContext({document:doc,window,location:{search:'?embedded=1',origin:'http://fixture.local'},parent:{postMessage:(value,origin)=>navigation.push({value,origin})},URLSearchParams,fetch,setInterval:fn=>{refreshTimer=fn;},console});
vm.runInContext(fs.readFileSync('ui/shell/index-cards.js','utf8'),context);
vm.runInContext(html.match(/<script nonce="__INDEX_NONCE__">([\s\S]*?)<\/script>/)[1],context);
const flush=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await flush();assert.equal(timelineSnapshot.definitions.length,4);assert.equal(selectedTimeline,'headers','Headers is the foundational default selection');
 assert.equal(nodes.has('index-cards'),false);assert.equal(nodes.has('capability-cards'),false);
 for(const id of ['index','query-index','peer-index'])assert.equal(nodes.get(id).tagName,'input','Track owns contextual index selection');
 assert.equal(calls.filter(c=>c.body).length,0,'Default selection is read only');
 timelineOptions.select('bitmap','advanced');assert.equal(nodes.get('index').value,'bitmap');
 nodes.get('mode').value='lean';await nodes.get('mode').emit('change');assert.equal(draftChanges.at(-1).id,'bitmap');assert.equal(draftChanges.at(-1).options.mode,'lean');
 assert.equal(calls.filter(c=>c.body).length,0,'Advanced edits only stage the shared transport');
 await tabs.find(t=>t.dataset.view==='results').emit('click');await flush();assert.equal(nodes.get('query-results').querySelectorAll('tr').length,2,'Read-only bounded records remain available');
 assert(calls.some(c=>c.url.includes('/query?index=bitmap&limit=50')));
 let resolveQuery;queryPending=new Promise(r=>resolveQuery=r);vm.runInContext('recordKey=""; loadRecords()',context);await flush();timelineOptions.select('inscriptions','advanced');
 resolveQuery({ok:true,json:async()=>({rows:[{id:'WRONG-CONTEXT'}],checkpoint:{height:1}})});await flush();queryPending=null;
 assert.equal(nodes.get('query-results').children.length,0,'Late prior-index response cannot replace another context');
 timelineOptions.openEntity('inscriptions',12);assert.equal(explorerFocus.at(-1).height,12);const count=explorerFocus.length;timelineOptions.openEntity('inscriptions',-1);assert.equal(explorerFocus.length,count,'Invalid coordinate causes no source lookup');
 const staged=draftChanges.length;timelineOptions.select('address-history','catalog');assert.equal(nodes.get('index-workspace').hidden,true,'Locked index cannot expose its settings');timelineOptions.buildRange('address-history',1,2);assert.equal(draftChanges.length,staged);
 assert.equal(calls.filter(c=>c.body).length,0,'Selecting contexts, reading records and resource navigation starts no work');
 assert(!calls.some(c=>/\/peers$|\/peer-preview|\/publish|\/build|\/queue|\/live$/.test(c.url)));
 assert.equal(typeof refreshTimer,'function');console.log('Indexes controller PASS: foundational selection, contextual staged options, bounded records, stale-response rejection, locked settings, resource navigation and no competing execution.');
})().catch(e=>{console.error(e);process.exitCode=1;});
