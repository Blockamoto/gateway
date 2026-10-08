'use strict';
// Executes both shipped Indexes scripts at event/API boundaries. Not layout QA.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const {documentFor}=require('./index-dom-fixture');
const html=fs.readFileSync('ui/shell/indexes.html','utf8');
const {doc,nodes,tabs,panes}=documentFor(html);
const definition={id:'bitmap',name:'Bitmap',buildable:true,theory:'Base districts',start_height:792435,dependencies:['blocks'],rules:['First valid claim wins'],version:1,network:'mainnet'};
let job=null,calls=[],instances=[],live=[],jobs=[],refreshTimer;
const definitions=[definition,{...definition,id:'inscriptions',name:'Inscriptions',start_height:0,arbitrary_start:true},{...definition,id:'address-history',buildable:false}];
const snapshot=()=>({definitions,instances,providers:[],job,live,jobs});
let queryPending=null;
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
 return {ok:true,json:async()=>result};
};
const context=vm.createContext({document:doc,window:{},location:{search:'?embedded=1'},URLSearchParams,fetch,setInterval:fn=>{refreshTimer=fn;},console});
vm.runInContext(fs.readFileSync('ui/shell/index-cards.js','utf8'),context);
vm.runInContext(html.match(/<script nonce="__INDEX_NONCE__">([\s\S]*?)<\/script>/)[1],context);
const flush=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await flush();assert.equal(nodes.get('index-cards').children.length,2);assert.equal(nodes.get('capability-cards').children.length,1);
 for(const id of ['index','query-index','peer-index'])assert.equal(nodes.get(id).tagName,'input','index selection belongs to cards, not dropdowns');
 assert(panes.every(p=>p.hidden),'initial catalogue does not display a form maze');
 assert.equal(nodes.get('build').disabled,true);
 const card=nodes.get('index-cards').children[0],on=card.querySelectorAll('input')[0];
 on.checked=true;await on.emit('change');
 assert.equal(on.checked,true,'fresh On reflects saved permission without a checkpoint');
 assert.equal(calls.filter(c=>c.url.endsWith('/live')).length,1,'On saves the permission through the backend');
 assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,0,'On does not begin an unbounded scan');
 assert(panes.every(p=>p.hidden),'On does not just open a selected card');
 await card.querySelector('button').emit('click');
 assert(panes.filter(p=>p.dataset.pane==='build').every(p=>!p.hidden));
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
 // Incoming records for an old selection must not appear under the new card.
 let resolveQuery;queryPending=new Promise(r=>{resolveQuery=r;});
 await tabs.find(t=>t.dataset.view==='results').emit('click');await flush();
 await nodes.get('index-cards').children[1].querySelector('button').emit('click');
 resolveQuery({ok:true,json:async()=>({rows:[{id:'WRONG-CARD'}],checkpoint:{height:1}})});await flush();queryPending=null;
 assert.equal(nodes.get('query-results').children.length,0,'late old-index data ignored after card change');
 assert.equal(nodes.get('index').value,'inscriptions');
 assert.equal(nodes.get('job-summary').textContent,'No index job has been started.','another index job must not appear under Inscriptions');
 assert.equal(nodes.get('pause').disabled,true,'inscriptions cannot pause Bitmap from its empty job controls');
 await nodes.get('plan-form').emit('submit');assert.equal(nodes.get('build').disabled,false,'another active index permits a queued reviewed plan');assert.equal(nodes.get('build').textContent,'Queue reviewed plan');
 const before=calls.filter(c=>c.body).length;
 jobs=[{id:'queued-inscriptions',queue_managed:true,index:'inscriptions',state:'queued',queue_position:1,waiting_reason:'Waiting for Bitmap',from:10,to:20,height:9}];await refreshTimer();await flush();
 assert(nodes.get('job-summary').textContent.includes('Waiting for Bitmap'));assert.equal(calls.filter(c=>c.body).length,before,'observing a queued job is read-only');
 const queueButton=nodes.get('job-queue').querySelectorAll('button').find(x=>x.textContent==='Later');await queueButton.emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/queue')).at(-1).body?.action,'down','queue order uses the saved job identity');

 jobs=[{id:'yielded-inscriptions',queue_managed:true,index:'inscriptions',state:'yielded',from:10,to:20,height:15}];await refreshTimer();await flush();assert.equal(nodes.get('resume').disabled,true,'Live handoff is maintained by policy, not a resumable queue request');
 assert(!calls.some(c=>/\/peers$|\/peer-preview|\/publish/.test(c.url)),'local use does not inspect peers or publish');
 assert.equal(typeof refreshTimer,'function');
 console.log('Indexes shipped-script interactions PASS: cards, no index dropdowns, reviewed plan, stale-plan rejection, build/stop/resume, bounded table, stale-response guard, no implicit peer/publication work.');
})().catch(e=>{console.error(e);process.exitCode=1;});
