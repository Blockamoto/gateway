'use strict';
const status=document.getElementById('status');
chrome.runtime.sendMessage({type:'gatewayStatus'}).then(r=>{status.textContent=r?.ok?'Connected to Gateway '+r.version:(r?.error||'Connection unavailable. Finish browser setup in Gateway.');}).catch(e=>status.textContent=e.message);
document.getElementById('open').onclick=async()=>{const r=await chrome.runtime.sendMessage({type:'openGateway',address:document.getElementById('address').value||'.gateway'});if(r?.valid&&r.web_url)chrome.tabs.create({url:r.web_url});else status.textContent=r?.error||'Could not open address';};
