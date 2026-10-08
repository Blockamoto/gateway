'use strict';
// Executes the shipped Indexes controller and pure models at event/API boundaries.
// The timeline DOM is covered separately in Chromium; this fixture isolates its callbacks.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const {Node,documentFor}=require('./index-dom-fixture');
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
let timelineOptions,selectedTimeline='',timelineSnapshot={},navigation=[];
const drawer=new Node('details');drawer.open=false;nodes.set('timeline-details',drawer);
const inspector=new Node('section');inspector.scrollTo=()=>{};nodes.set('timeline-inspector',inspector);
const window={GatewayIndexWorkspace:{mountLayout(){},mountRange(){return {update(){},sync(){}}}},GatewayIndexTimeline:{mount(options){timelineOptions=options;return {
 render(value){timelineSnapshot=value}, health(){},
 select(id){selectedTimeline=id;options.workspace.hidden=!id||!!definitions.find(d=>d.id===id)?.locked},
 model(id){return window.GatewayIndexCards.model(definitions.find(d=>d.id===id)||{id},timelineSnapshot)}
}}}};
const context=vm.createContext({document:doc,window,location:{search:'?embedded=1',origin:'http://fixture.local'},parent:{postMessage:(value,origin)=>navigation.push({value,origin})},URLSearchParams,fetch,setInterval:fn=>{refreshTimer=fn;},console});
vm.runInContext(fs.readFileSync('ui/shell/index-cards.js','utf8'),context);
vm.runInContext(html.match(/<script nonce="__INDEX_NONCE__">([\s\S]*?)<\/script>/)[1],context);
const flush=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await flush();assert.equal(timelineSnapshot.definitions.length,4);assert.equal(selectedTimeline,'headers','Headers is the foundational default selection');
 assert.equal(nodes.has('index-cards'),false,'Cards are fully replaced by the timeline root');
 assert.equal(nodes.has('capability-cards'),false,'Locked capabilities use the same timeline');
 for(const id of ['index','query-index','peer-index'])assert.equal(nodes.get(id).tagName,'input','index selection belongs to timeline, not dropdowns');
 assert.equal(nodes.get('build').disabled,true);assert.equal(drawer.open,false,'Details stay collapsed until requested');
 assert.equal(calls.filter(c=>c.body).length,0,'Default selection cannot mutate index policies');
 assert.equal(calls.filter(c=>c.url.includes('/query?')).length,0,'Default selection does not query records');
 timelineOptions.select('bitmap','build');
 assert(panes.filter(p=>p.dataset.pane==='build').every(p=>!p.hidden));
 assert.equal(calls.filter(c=>c.body).length,0,'Selecting a track does not start indexing');
 for(const view of ['providers','peers','build']){await tabs.find(t=>t.dataset.view===view).emit('click');assert(panes.every(p=>p.hidden===(p.dataset.pane!==view)));}
 nodes.get('retention').value='ephemeral';nodes.get('to').value='792436';
 await nodes.get('plan-form').emit('submit');assert.equal(nodes.get('build').disabled,false);
 nodes.get('to').value='792437';await nodes.get('to').emit('input');assert.equal(nodes.get('build').disabled,true);
 await nodes.get('build').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,0);
 await nodes.get('plan-form').emit('submit');await nodes.get('build').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,1);
 assert(calls.some(c=>c.url.endsWith('/query?index=bitmap&limit=50')),'build opens bounded rolling preview');
 assert.equal(nodes.get('query-results').querySelectorAll('tr').length,2,'table has header and one record');
 assert.equal(nodes.get('pause').disabled,false);await nodes.get('pause').emit('click');assert.equal(nodes.get('resume').disabled,false);
 await nodes.get('resume').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,2);assert.equal(nodes.get('build').disabled,true);
 // Incoming records for an old selection must not appear under the new track.
 let resolveQuery;queryPending=new Promise(r=>{resolveQuery=r;});
 await tabs.find(t=>t.dataset.view==='results').emit('click');await flush();
 timelineOptions.select('inscriptions','build');
 resolveQuery({ok:true,json:async()=>({rows:[{id:'WRONG-CARD'}],checkpoint:{height:1}})});await flush();queryPending=null;
 assert.equal(nodes.get('query-results').children.length,0,'late old-index data ignored after track change');
 assert.equal(nodes.get('index').value,'inscriptions');
 assert.equal(nodes.get('job-summary').textContent,'No index job has been started.','another index job must not appear under Inscriptions');
 assert.equal(nodes.get('pause').disabled,true,'inscriptions cannot pause Bitmap from its empty job controls');
 await nodes.get('plan-form').emit('submit');assert.equal(nodes.get('build').disabled,false,'another active index permits a queued reviewed plan');assert.equal(nodes.get('build').textContent,'Queue reviewed plan');
 const before=calls.filter(c=>c.body).length;
 jobs=[{id:'queued-inscriptions',queue_managed:true,index:'inscriptions',state:'queued',queue_position:1,waiting_reason:'Waiting for Bitmap',from:10,to:20,height:9}];await refreshTimer();await flush();
 assert(nodes.get('job-summary').textContent.includes('Waiting for Bitmap'));assert.equal(calls.filter(c=>c.body).length,before,'observing a queued job is read-only');
 const queueButton=nodes.get('job-queue').querySelectorAll('button').find(x=>x.textContent==='Later');await queueButton.emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/queue')).at(-1).body?.action,'down','queue order uses the saved job identity');

 jobs=[{id:'yielded-inscriptions',queue_managed:true,index:'inscriptions',state:'yielded',from:10,to:20,height:15}];await refreshTimer();await flush();assert.equal(nodes.get('resume').disabled,true,'Live handoff is maintained by policy, not a resumable queue request');
 // Timeline range selection only prepares a reviewable bounded plan, including
 // while a saved draft is live; the callback itself must never POST.
 const beforeRange=calls.filter(c=>c.body).length;
 nodes.get('initial-live').checked=true;timelineOptions.buildRange('inscriptions',10,20);
 assert.equal(nodes.get('from').value,'10');assert.equal(nodes.get('to').value,'20');
 assert.equal(nodes.get('initial-live').checked,false);assert.equal(nodes.get('to').disabled,false);
 assert.equal(nodes.get('build').disabled,true,'Changing the selected range invalidates a reviewed plan');
 assert.equal(drawer.open,true);assert.equal(nodes.get('plan-button').focused,true);
 assert.equal(calls.filter(c=>c.body).length,beforeRange,'Preparing a timeline range does not plan, start, or enable work');
 timelineOptions.buildRange('inscriptions',-1,20);assert.equal(nodes.get('from').value,'10','Invalid range cannot replace the draft');
 timelineOptions.openEntity('inscriptions',12);assert.deepEqual(navigation.map(n=>n.value.address),['12.bitcoin']);assert.equal(navigation[0].origin,'http://fixture.local');
 assert.equal(nodes.get('from').value,'10','Opening Explorer preserves the selected range');
 timelineOptions.openEntity('inscriptions',-1);assert.equal(navigation.length,1);
 const beforeLocked=calls.length;timelineOptions.select('address-history','catalog');
 assert.equal(selectedTimeline,'address-history');assert.equal(nodes.get('index-workspace').hidden,true);assert(panes.every(p=>p.hidden));
 timelineOptions.buildRange('address-history',1,2);timelineOptions.openEntity('address-history',1);
 assert.equal(calls.length,beforeLocked,'Locked selection and actions make no API request');assert.equal(navigation.length,1,'Locked selection cannot open an entity');
 timelineOptions.select('inscriptions','build');assert.equal(nodes.get('from').value,'10');assert.equal(nodes.get('to').value,'20','Switching through a locked track preserves the earlier draft');
 // The old track's in-flight mutation must not redirect a newly selected
 // inspector or discard its independently reviewed plan when it finishes.
 jobs=[];timelineOptions.select('bitmap','build');await nodes.get('plan-form').emit('submit');
 let releaseBuild;mutationGate={action:'build',wait:new Promise(resolve=>{releaseBuild=resolve})};
 const pendingBuild=nodes.get('build').emit('click');await flush();
 timelineOptions.select('inscriptions','build');nodes.get('to').value='30';await nodes.get('plan-form').emit('submit');
 const queriesBeforeRelease=calls.filter(c=>c.url.includes('/query?')).length;
 releaseBuild();await pendingBuild;mutationGate=null;
 assert.equal(nodes.get('index').value,'inscriptions');assert(panes.filter(p=>p.dataset.pane==='build').every(p=>!p.hidden),'Late build response preserves the newly selected details view');
 assert.equal(nodes.get('build').disabled,false,'Late build response preserves another index\'s reviewed plan');
 assert.equal(nodes.get('job-summary').textContent,'No index job has been started.','Late build response cannot paint another track\'s job');
 assert.equal(calls.filter(c=>c.url.includes('/query?')).length,queriesBeforeRelease,'Late mutation cannot implicitly load the new track\'s records');
 timelineOptions.select('bitmap','build');let releasePlan;
 mutationGate={action:'plan',wait:new Promise(resolve=>{releasePlan=resolve})};
 const pendingPlan=nodes.get('plan-form').emit('submit');await flush();
 timelineOptions.select('inscriptions','build');assert.equal(nodes.get('plan-button').disabled,false,'An old request does not disable the new track\'s plan review');
 releasePlan();await pendingPlan;mutationGate=null;
 assert.equal(nodes.get('plan').hidden,true);assert.equal(nodes.get('build').disabled,true,'A plan for another track cannot authorize a build');
 timelineOptions.select('bitmap','build');let rejectPlan;
 mutationGate={action:'plan',wait:new Promise((_,reject)=>{rejectPlan=reject})};
 const failedOldPlan=nodes.get('plan-form').emit('submit');await flush();timelineOptions.select('inscriptions','build');
 rejectPlan(new Error('Old Bitmap plan failed'));await failedOldPlan;mutationGate=null;
 assert.equal(nodes.get('message').hidden,true,'An old track\'s read-only failure cannot replace the new inspector\'s message');
 assert.equal(nodes.get('plan-button').disabled,false);
 assert(!calls.some(c=>/\/peers$|\/peer-preview|\/publish/.test(c.url)),'local use does not inspect peers or publish');
 assert.equal(typeof refreshTimer,'function');
 console.log('Indexes shipped controller PASS: default Headers selection, no implicit work, reviewed bounded timeline ranges, Explorer navigation, locked selections, per-index drafts, stale-plan rejection, build/pause/resume, queue controls, bounded records and stale-response guard.');
})().catch(e=>{console.error(e);process.exitCode=1;});
