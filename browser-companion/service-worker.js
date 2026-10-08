'use strict';
importScripts('search.js');
const HOST = 'com.gateway.client';
const VERSION = '0.7.0';
async function native(action,address='') {
  try {
    const result = await chrome.runtime.sendNativeMessage(HOST,{action,address,version:VERSION});
    if (result && Array.isArray(result.namespaces)) {
      await chrome.storage.local.set({gatewayNamespaces: result.namespaces});
    }
    return result;
  }
  catch(e) { return {valid:false,ok:false,error:'Gateway connection: '+String(e.message||e)}; }
}
chrome.runtime.onInstalled.addListener(()=>native('status'));
chrome.runtime.onStartup.addListener(()=>native('status'));
chrome.runtime.onMessage.addListener((message,sender,sendResponse)=>{
  if(!message||!message.type)return;
  const ownPage=sender.url&&sender.url.startsWith(chrome.runtime.getURL(''));
  if(message.type==='validateGatewayAddress') {
    (async () => {
      const address = String(message.address || '');
      const stored = await chrome.storage.local.get('gatewayNamespaces');
      const namespaces = stored.gatewayNamespaces || ['.bitcoin', '.gateway', '.bitmap'];
      // Unknown domain searches stay entirely in the browser. The native host
      // receives only an exact token in a locally registered namespace.
      if (!GatewaySearch.matchesNamespace(address, namespaces)) return {valid:false};
      return native('validate',address);
    })().then(sendResponse).catch(() => sendResponse({valid:false}));return true;
  }
  if(message.type==='recordRecovery'){sendResponse({ok:true});return;}
  if(ownPage&&message.type==='gatewayStatus'){native('status').then(sendResponse);return true;}
  if(ownPage&&message.type==='openGateway'){native('open',String(message.address||'.gateway')).then(sendResponse);return true;}
});
