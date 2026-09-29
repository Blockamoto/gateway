'use strict';
// Executes the shipped Indexes script with an event-capable DOM fixture.
// This verifies state transitions and API boundaries, not rendered layout.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const html=fs.readFileSync('ui/shell/indexes.html','utf8');
class Node {
 constructor(tag='div'){this.tagName=tag;this.children=[];this.dataset={};this.events={};this.value='';this.hidden=false;this.disabled=false;this.attributes={};this.classList={add:()=>{}};}
 append(...nodes){this.children.push(...nodes)}
 replaceChildren(...nodes){this.children=[...nodes]}
 addEventListener(name,fn){this.events[name]=fn}
 setAttribute(k,v){this.attributes[k]=v}
 removeAttribute(k){delete this.attributes[k]}
 closest(){return this.parent || (this.parent=new Node())}
 focus(){this.focused=true}
 querySelector(tag){return this.children.find(n=>n.tagName===tag)||new Node(tag)}
 async emit(name){if(this.events[name])await this.events[name]({preventDefault(){},currentTarget:this})}
}
const nodes=new Map(),tabs=[],panes=[];
for(const m of html.matchAll(/<([a-z]+)\b([^>]+)>/g)){
 const [,tag,attrs]=m,n=new Node(tag),id=attrs.match(/\bid="([^"]+)"/);if(id)nodes.set(id[1],n);
 for(const key of ['view','pane']){const v=attrs.match(new RegExp('data-'+key+'="([^"]+)"'));if(v){n.dataset[key]=v[1];(key==='view'?tabs:panes).push(n)}}
 n.hidden=/\bhidden\b/.test(attrs);n.disabled=/\bdisabled\b/.test(attrs);const val=attrs.match(/\bvalue="([^"]*)"/);if(val)n.value=val[1];
}
const doc={getElementById:id=>{assert(nodes.has(id),'missing actual HTML id '+id);return nodes.get(id)},createElement:tag=>new Node(tag),querySelectorAll:s=>s==='[data-view]'?tabs:panes,body:new Node(),hidden:false};
const definition={id:'bitmap',name:'Bitmap',buildable:true,theory:'Base districts',start_height:792435,dependencies:['blocks'],rules:['First valid claim wins'],version:1,network:'mainnet'};
let job=null,calls=[];
const status=()=>({definitions:[definition,{...definition,id:'address-history',buildable:false}],instances:[],providers:[],job});
const fetch=async(url,opts)=>{calls.push({url,body:opts?.body&&JSON.parse(opts.body)});let result=status();if(url.endsWith('/plan'))result={definition,from:792435,to:792436,dependencies:[],notes:[],retention:'ephemeral'};if(url.endsWith('/build'))result=job={id:'job1',index:'bitmap',state:'running',from:792435,to:792436,height:792434,retention:'ephemeral'};if(url.endsWith('/pause'))result=job={...job,state:'paused'};return{ok:true,json:async()=>result}};
const context=vm.createContext({document:doc,location:{search:'?embedded=1'},URLSearchParams,fetch,setInterval:()=>{},console});
vm.runInContext(html.match(/<script nonce="__INDEX_NONCE__">([\s\S]*?)<\/script>/)[1],context);
const flush=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await flush();assert.equal(nodes.get('index').children.length,1,'unsupported definitions excluded from build selector');
 assert.equal(nodes.get('build').disabled,true,'first load cannot build without plan');
 for(const view of ['results','providers','peers','build']){await tabs.find(t=>t.dataset.view===view).emit('click');assert(panes.every(p=>p.hidden===(p.dataset.pane!==view)));}
 nodes.get('retention').value='ephemeral';nodes.get('to').value='792436';
 await nodes.get('plan-form').emit('submit');assert.equal(nodes.get('build').disabled,false);
 nodes.get('to').value='792437';await nodes.get('to').emit('input');assert.equal(nodes.get('build').disabled,true,'range edit invalidates reviewed plan');
 await nodes.get('build').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,0,'even programmatic click cannot submit stale plan');
 await nodes.get('plan-form').emit('submit');await nodes.get('build').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,1);
 assert.equal(nodes.get('pause').disabled,false);await nodes.get('pause').emit('click');assert.equal(nodes.get('resume').disabled,false);
 await tabs.find(t=>t.dataset.view==='results').emit('click');await nodes.get('resume').emit('click');assert.equal(calls.filter(c=>c.url.endsWith('/build')).length,2,'resume restarts original request');assert(panes.filter(p=>p.dataset.pane==='build').every(p=>!p.hidden));assert.equal(nodes.get('build').disabled,true,'active resumed job cannot start twice');
 assert(!calls.some(c=>/\/peers|\/peer-preview|\/publish/.test(c.url)),'first load and local work do not inspect peers or publish');
 console.log('Indexes shipped-script interactions PASS: first load, four workspace views, exact-plan gating, edits, build, pause, resume, no implicit sharing/network inspection.');
})().catch(e=>{console.error(e);process.exitCode=1});
