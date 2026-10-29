'use strict';
// Deliberately small DOM boundary for executing shipped controllers. It does
// not establish browser layout, native events, CSP or assistive-tech support.
const assert = require('node:assert/strict');
class Node {
 constructor(tag='div') {this.tagName=tag;this.children=[];this.dataset={};this.events={};this.value='';this.checked=false;this.hidden=false;this.disabled=false;this.attributes={};this.classList={add(){}};this.textContent='';}
 append(...nodes){for(const n of nodes){n.parent=this;this.children.push(n);}}
 replaceChildren(...nodes){this.children=[];this.append(...nodes);}
 addEventListener(name,fn){this.events[name]=fn;}
 setAttribute(k,v){this.attributes[k]=String(v);}
 removeAttribute(k){delete this.attributes[k];}
 closest(){return this.parent||(this.parent=new Node());}
 focus(){this.focused=true;}
 querySelector(tag){return this.querySelectorAll(tag)[0] || new Node(tag);}
 querySelectorAll(tag){const out=[];for(const n of this.children){if(n.tagName===tag)out.push(n);out.push(...n.querySelectorAll(tag));}return out;}
 async emit(name){if(this.events[name])return this.events[name]({preventDefault(){},currentTarget:this});}
}
function documentFor(html) {
 const nodes=new Map(),tabs=[],panes=[];
 for(const m of html.matchAll(/<([a-z][a-z0-9]*)\b([^>]+)>/g)){
  const [,tag,attrs]=m,n=new Node(tag),id=attrs.match(/\bid="([^"]+)"/);if(id)nodes.set(id[1],n);
  for(const key of ['view','pane']){const v=attrs.match(new RegExp('data-'+key+'="([^"]+)"'));if(v){n.dataset[key]=v[1];(key==='view'?tabs:panes).push(n);}}
  n.hidden=/\bhidden\b/.test(attrs);n.disabled=/\bdisabled\b/.test(attrs);n.checked=/\bchecked\b/.test(attrs);
  const val=attrs.match(/\bvalue="([^"]*)"/);if(val)n.value=val[1];
 }
 const doc={getElementById:id=>{assert(nodes.has(id),'missing actual HTML id '+id);return nodes.get(id);},createElement:tag=>new Node(tag),querySelectorAll:s=>s==='[data-view]'?tabs:panes,body:new Node(),hidden:false};
 return {doc,nodes,tabs,panes};
}
module.exports={Node,documentFor};
