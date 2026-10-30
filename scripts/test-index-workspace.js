'use strict';
// Executes the shipped Indexes controller and pure models at event/API boundaries.
// The timeline DOM is covered separately in Chromium; this fixture isolates its callbacks.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const {Node,documentFor}=require('./index-dom-fixture');
const placements=[];Node.prototype.before=function(...nodes){for(const node of nodes)placements.push({target:this,node});};
const html=fs.readFileSync('ui/shell/indexes.html','utf8');
const {doc,nodes,tabs,panes}=documentFor(html);
const definition={id:'bitmap',name:'Bitmap',buildable:true,theory:'Base districts',start_height:792435,dependencies:['blocks'],rules:['First valid claim wins'],version:1,network:'mainnet'};
let job=null,calls=[],instances=[],live=[],jobs=[],refreshTimer,refreshDelay,parcelPrereqs;
const definitions=[{...definition,id:'headers',name:'Bitcoin headers',buildable:false},definition,{...definition,id:'inscriptions',name:'Inscriptions',start_height:0,arbitrary_start:true},{...definition,id:'address-history',buildable:false,locked:true,lock_reason:'Locked during Headers and Blocks testing.'}];
const snapshot=()=>({definitions,instances,providers:[],job,live,jobs,parcel_prerequisites:parcelPrereqs});
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
let timelineOptions,selectedTimeline='',timelineSnapshot={},navigation=[],explorerFocus=[],draftChanges=[],exportMount=null,exportIndex='',exportUpdates=[];const windowListeners=new Map();
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
const window={addEventListener(type,handler){const listeners=windowListeners.get(type)||[];listeners.push(handler);windowListeners.set(type,listeners);},GatewayWorkspaceResources:{mount(){return {classify(){return null},overview(){},suspend(){},teardown(){}}}},GatewayUTXOPanel:{mount(){return {render(){},health(){}}}},GatewayInscriptionViewport:{coordinate:record=>record?.reveal_coordinate||'',mount(){return {available(){},setKnown(){},open(){},teardown(){}}}},GatewaySemanticTimeline:{mount(){return {clear(){},open(){},setMarkers(){}}}},GatewayTimelineExplorer:{mount(){return {focus:value=>explorerFocus.push(value),health(){},setTab(){}}}},GatewayIndexWorkspace:{mountLayout(){},mountRange(){return {update(){},sync(){}}}},GatewayIndexTimeline:{mount(options){timelineOptions=options;return {
 render(value){timelineSnapshot=value}, health(){}, acceptResource(){},cancelPendingNavigation(){},setConsumerOptions(id,options){draftChanges.push({id,options})},getRunDraft(){return {consumers:[]}},
 select(id){selectedTimeline=id;options.workspace.hidden=!id||!!definitions.find(d=>d.id===id)?.locked},
 model(id){return window.GatewayIndexCards.model(definitions.find(d=>d.id===id)||{id},timelineSnapshot)}
}}}};
// The shared export browser test owns actual requests/downloads. This boundary
// checks that the controller mounts it once, supplies current status and clears
// its Inscriptions context when selection or resource navigation changes.
window.GatewayIndexExport={mount(options){assert.equal(exportMount,null);exportMount=options;return {update(value){exportUpdates.push(value);if(Object.hasOwn(value,'index')){exportIndex=value.index;options.root.hidden=exportIndex!=='inscriptions';}},teardown(){options.root.hidden=true;}};}};
const context=vm.createContext({document:doc,window,location:{search:'?embedded=1',origin:'http://fixture.local'},parent:{postMessage:(value,origin)=>navigation.push({value,origin})},URLSearchParams,fetch,setTimeout:(fn,delay)=>{refreshTimer=fn;refreshDelay=delay;return 1;},clearTimeout:()=>{refreshTimer=null;},console});
vm.runInContext(fs.readFileSync('ui/shell/index-cards.js','utf8'),context);
vm.runInContext(fs.readFileSync('ui/shell/bitmap-workspace.js','utf8'),context);
vm.runInContext(fs.readFileSync('ui/shell/index-transport.js','utf8'),context);
vm.runInContext(html.match(/<script nonce="__INDEX_NONCE__">([\s\S]*?)<\/script>/)[1],context);
const flush=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await flush();assert.equal(timelineSnapshot.definitions.length,4);assert.equal(selectedTimeline,'headers','Headers is the foundational default selection');
 assert.equal(exportMount.root,nodes.get('index-export-panel'),'Shared export component uses its owned settings panel');assert.equal(typeof exportMount.timeline.setConsumerOptions,'function','Explicit locator staging uses the existing timeline boundary');assert.equal(exportMount.getStatus().definitions.length,4,'Export receives the current status observation');assert.equal(nodes.get('index-export-panel').hidden,true,'Default Headers selection does not expose an unrelated export');
 assert(placements.some(({target,node})=>target===drawer&&node===exportMount.root),'Export panel moves before the advanced details instead of remaining inside its collapsed form');assert.equal(drawer.open,false,'Export placement does not force the advanced form open');
 assert.equal(nodes.has('index-cards'),false);assert.equal(nodes.has('capability-cards'),false);
 for(const id of ['index','query-index','peer-index'])assert.equal(nodes.get(id).tagName,'input','Track owns contextual index selection');
 assert.equal(calls.filter(c=>c.body).length,0,'Default selection is read only');
 timelineOptions.select('bitmap','advanced');assert.equal(nodes.get('index').value,'bitmap');
 assert.equal(exportIndex,'','Legacy Bitmap selection does not mount the active Inscriptions export');
 nodes.get('mode').value='lean';await nodes.get('mode').emit('change');assert.equal(draftChanges.at(-1).id,'bitmap');assert.equal(draftChanges.at(-1).options.mode,'lean');
 assert.equal(calls.filter(c=>c.body).length,0,'Advanced edits only stage the shared transport');
 await tabs.find(t=>t.dataset.view==='results').emit('click');await flush();assert.equal(nodes.get('query-results').querySelectorAll('tr').length,2,'Read-only bounded records remain available');
 assert(calls.some(c=>c.url.includes('/query?index=bitmap&limit=50')));
 let resolveQuery;queryPending=new Promise(r=>resolveQuery=r);vm.runInContext('recordKey=""; loadRecords()',context);await flush();timelineOptions.select('inscriptions','advanced');
 resolveQuery({ok:true,json:async()=>({rows:[{id:'WRONG-CONTEXT'}],checkpoint:{height:1}})});await flush();queryPending=null;
 assert.equal(nodes.get('query-results').children.length,0,'Late prior-index response cannot replace another context');
 assert.equal(exportIndex,'inscriptions');assert.equal(nodes.get('index-export-panel').hidden,false,'Selecting Inscriptions exposes its shared export settings');assert(exportUpdates.some(value=>value.status),'Selected status observations reach the export component');
 assert.equal(drawer.open,false,'Inscriptions export is separate from the closed advanced settings');
 const workspaceVisibility=visible=>{for(const handler of windowListeners.get('message')||[])handler({source:context.parent,origin:'http://fixture.local',data:{type:'gateway:workspace-visibility',visible}});};workspaceVisibility(false);assert.equal(exportIndex,'','Hidden workspace invalidates the export context');workspaceVisibility(true);await flush();assert.equal(exportIndex,'inscriptions','Returning to the same selected Inscriptions index restores export controls');assert.equal(nodes.get('index-export-panel').hidden,false);assert(!calls.some(c=>/\/export\//.test(c.url)),'Visibility restoration does not prepare an export');
 timelineOptions.onFocus({index:'inscriptions',height:12,immediate:false});assert.equal(exportIndex,'','Ruler or lane focus leaving settings invalidates the export context');timelineOptions.select('inscriptions','advanced');assert.equal(exportIndex,'inscriptions','Returning after timeline focus needs a fresh export context');
 vm.runInContext('resourceIntent()',context);assert.equal(exportIndex,'','Resource navigation clears the prior export context');assert.equal(nodes.get('index-export-panel').hidden,true);timelineOptions.select('inscriptions','advanced');assert.equal(exportIndex,'inscriptions','Returning to Inscriptions restores its export context without requesting an export');
 timelineOptions.openEntity('inscriptions',12);assert.equal(explorerFocus.at(-1).height,12);const count=explorerFocus.length;timelineOptions.openEntity('inscriptions',-1);assert.equal(explorerFocus.length,count,'Invalid coordinate causes no source lookup');
 const staged=draftChanges.length;timelineOptions.select('address-history','catalog');assert.equal(nodes.get('index-workspace').hidden,true,'Locked index cannot expose its settings');timelineOptions.buildRange('address-history',1,2);assert.equal(draftChanges.length,staged);
 assert.equal(exportIndex,'','Selecting a different or locked index clears export context');assert(!calls.some(c=>/\/export\//.test(c.url)),'Mounting, context selection and status updates never prepare or download exports');
 assert.equal(calls.filter(c=>c.body).length,0,'Selecting contexts, reading records and resource navigation starts no work');
 assert(!calls.some(c=>/\/peers$|\/peer-preview|\/publish|\/build|\/queue|\/live$/.test(c.url)));
 definitions.push({...definition,id:'bitmap-parcels-standard-v1-replay',name:'Bitmap Parcels',kind:'parcel',buildable:true});await vm.runInContext('refresh()',context);timelineOptions.select('bitmap-parcels-standard-v1-replay','advanced');assert.equal(nodes.get('parcel-evidence-controls').hidden,false);assert.equal(nodes.get('parcel-route-locators').disabled,true,'Unsupported route remains disabled');assert.equal(nodes.get('parcel-route-links').disabled,true,'Unsupported route remains disabled');parcelPrereqs={routes:[{id:'tx_locators',execution_available:true},{id:'txio_links',execution_available:true}]};await vm.runInContext('refresh()',context);assert.equal(nodes.get('parcel-route-locators').disabled,false);assert.equal(nodes.get('parcel-route-links').disabled,false);assert.equal(nodes.get('parcel-evidence-route').value,'');assert.equal(nodes.has('parcel-raw-scan-budget'),false,'No operation quota control exists');for(const route of ['retained-raw','tx-locators','txio-links','']){nodes.get('parcel-evidence-route').value=route;await nodes.get('parcel-evidence-route').emit('change');assert.equal(draftChanges.at(-1).options.parcel_evidence_route,route);assert.equal(Object.hasOwn(draftChanges.at(-1).options,'parcel_raw_scan_budget'),false,'Route choice needs no quota');if(route==='txio-links'){assert.match(nodes.get('parcel-route-description').textContent,/audit.*does not establish/i);assert.match(nodes.get('parcel-route-description').textContent,/may fetch missing source blocks/i);assert.match(nodes.get('parcel-route-description').textContent,/may repeat the audit/i);assert.match(nodes.get('parcel-route-description').textContent,/forward scan/i);}}

 const inscriptionDefinition=definitions.find(d=>d.id==='inscriptions');
 inscriptionDefinition.executable_recipe_variants=['inscriptions-lean.json','inscriptions-full.json','inscriptions-conventional-ids.json','inscriptions-saved-conventional-ids.json'].map(file=>({name:file,definition:JSON.parse(fs.readFileSync('recipes/builtins/'+file,'utf8'))}));
 inscriptionDefinition.executable_recipe=inscriptionDefinition.executable_recipe_variants[0].definition;
 await vm.runInContext('refresh()',context);timelineOptions.select('inscriptions','advanced');
 assert.equal(nodes.get('executable-recipe-variants-label').hidden,false);const variants=nodes.get('executable-recipe-variants');assert.equal(variants.children.length,4);
 variants.value='1';variants.onchange();assert.equal(JSON.parse(nodes.get('executable-recipe-json').textContent).id,'inscriptions-full');
 const writesBeforeVariant=calls.filter(c=>c.body).length;
 await vm.runInContext('refresh()',context);assert.equal(variants.value,'1','Observation preserves the selected recipe layer');assert.equal(variants.children.length,4);
 assert.equal(calls.filter(c=>c.body).length,writesBeforeVariant,'Viewing a recipe layer performs no save or run');
 assert.equal(typeof refreshTimer,'function');assert.equal(refreshDelay,5000,'Idle observation uses a completion-scheduled five-second timeout');console.log('Indexes controller PASS: foundational selection, contextual staged options, bounded records, stale-response rejection, locked settings, resource navigation and no competing execution.');
})().catch(e=>{console.error(e);process.exitCode=1;});
