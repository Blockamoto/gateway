'use strict';
importScripts('search.js');
const HOST = 'com.gateway.client';
const VERSION = '0.7.8';
let namespaces=null,handshake=null;
async function native(action,address='') {
  try {
    const result = await chrome.runtime.sendNativeMessage(HOST,{action,address,version:VERSION});
    if (action==='status') {
      namespaces=result?.ok===true&&Array.isArray(result.namespaces)?result.namespaces.filter(n=>/^\.[a-z][a-z0-9-]*$/.test(typeof n==='string'?n:n?.suffix)):null;
      if(namespaces)try{await chrome.storage.local.set({gatewayNamespaces:namespaces});}catch{}
    }
    return result;
  }
  catch(e) { if(action==='status')namespaces=null;return {valid:false,ok:false,error:'Gateway connection: '+String(e.message||e)}; }
}
async function refreshNamespaces() {
  if (handshake) return handshake;
  handshake=(async()=>{
    await native('status');
    return namespaces;
  })();
  try { return await handshake; } finally { handshake=null; }
}
async function validateAddress(address) {
  const candidate=GatewaySearch.resourceCandidate(address);
  if (!candidate) return {valid:false};
  const registered=namespaces||await refreshNamespaces();
  // A previously cached list cannot authenticate the currently selected client.
  if (!registered||!GatewaySearch.matchesNamespace(candidate,registered)) return {valid:false};
  const result=await native('validate',candidate);
  const canonical=['home.gateway','gateway.gateway'].includes(candidate)?'.gateway':candidate;
  if (!result?.valid||result.address!==canonical||!safeDestination(result.web_url,result.address)) return {valid:false};
  return result;
}
function safeDestination(value,address) {
  let u;try {u=new URL(value);}catch{return false;}
  if (u.protocol!=='http:'||u.username||u.password||u.hash||u.pathname!=='/'||!u.searchParams.get('activation')||[...u.searchParams.keys()].some(key=>key!=='activation')) return false;
  if (['127.0.0.1','localhost'].includes(u.hostname)) return Boolean(u.port);
  return !u.port&&(u.hostname===address||address==='.gateway'&&u.hostname==='home.gateway');
}
chrome.runtime.onInstalled.addListener(()=>refreshNamespaces());
chrome.runtime.onStartup.addListener(()=>{namespaces=null;return refreshNamespaces();});
chrome.runtime.onMessage.addListener((message,sender,sendResponse)=>{
  if(!message||!message.type)return;
  const ownPage=sender.url&&sender.url.startsWith(chrome.runtime.getURL(''));
  if(message.type==='validateGatewayAddress') {
    const candidate=GatewaySearch.extractCandidate(sender.url||'');
    if (!candidate||candidate!==GatewaySearch.oneToken(message.address)) {sendResponse({valid:false});return;}
    validateAddress(candidate).then(sendResponse).catch(() => sendResponse({valid:false}));return true;
  }
  if(message.type==='recordRecovery'){sendResponse({ok:true});return;}
  if(ownPage&&message.type==='gatewayStatus'){native('status').then(sendResponse);return true;}
  if(ownPage&&message.type==='openGateway'){native('open',String(message.address||'.gateway')).then(sendResponse);return true;}
});

const pendingNavigation=new Map();
// Only an existing exact-resource handoff is invalidated here; ordinary URLs
// are never retained, classified by the native host, or added to a history log.
chrome.webNavigation?.onBeforeNavigate.addListener(details=>{
  if(details.frameId!==0)return;
  const pending=pendingNavigation.get(details.tabId);
  if(pending&&pending.url!==details.url)pendingNavigation.delete(details.tabId);
});
chrome.webNavigation?.onBeforeNavigate.addListener(details=>{
  if(details.frameId!==0||!Number.isInteger(details.tabId)||details.tabId<0)return;
  const address=GatewaySearch.directCandidate(details.url);if(!address)return;
  const pending={url:details.url,started:Date.now()};pendingNavigation.delete(details.tabId);pendingNavigation.set(details.tabId,pending);
  while(pendingNavigation.size>128)pendingNavigation.delete(pendingNavigation.keys().next().value);
  validateAddress(address).then(async result=>{
    if(!result?.valid||pendingNavigation.get(details.tabId)!==pending||Date.now()-pending.started>15000)return;
    await chrome.tabs.update(details.tabId,{url:result.web_url});
  }).catch(()=>{}).finally(()=>{if(pendingNavigation.get(details.tabId)===pending)pendingNavigation.delete(details.tabId);});
},{url:['.bitcoin','.gateway','.bitmap','.sat'].map(hostSuffix=>({schemes:['http','https'],hostSuffix}))});
chrome.tabs.onRemoved?.addListener(tabId=>pendingNavigation.delete(tabId));
