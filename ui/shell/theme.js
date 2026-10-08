(function(){
 'use strict';
 const key='gateway-theme';let saved='',failure='',saving=false,loading=true;
 const valid=value=>value==='dark'||value==='light';
 function local(){try{const choice=localStorage.getItem(key)||localStorage.getItem('theme');return valid(choice)?choice:''}catch{return ''}}
 function apply(value){saved=valid(value)?value:'light';document.documentElement.dataset.theme=saved;document.documentElement.style.colorScheme=saved;}
 function synchronize(value,cache=true){apply(value);if(cache)try{localStorage.setItem(key,saved)}catch{};for(const frame of document.querySelectorAll('iframe'))try{frame.contentWindow.postMessage({type:'gateway:theme',theme:saved},location.origin)}catch{};controls()}
 async function api(theme){const response=await fetch('/api/v1/appearance',theme===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({theme})});const value=await response.json();if(!response.ok)throw Error(value.error||'Appearance could not be saved.');if(theme!==undefined&&!valid(value.theme))throw Error('Gateway returned an invalid appearance choice.');return value}
 const legacy=local();apply(legacy||'light');
 async function choose(theme){if(loading||saving||!valid(theme))return;saving=true;failure='';controls();try{const value=await api(theme);synchronize(value.theme)}catch(error){failure=error.message||String(error)}finally{saving=false;controls()}}
 const ready=(async()=>{try{const value=await api();if(valid(value.theme))synchronize(value.theme);else if(legacy){const migrated=await api(legacy);synchronize(migrated.theme)}else synchronize('light',false)}catch(error){failure='Appearance could not be loaded: '+(error.message||String(error))}finally{loading=false;controls()}})();
 window.GatewayTheme={ready,choose};
 window.addEventListener('storage',event=>{if(event.key===key||event.key===null){apply(local()||'light');controls()}});
 window.addEventListener('message',event=>{if(event.origin===location.origin&&event.source===parent&&event.data?.type==='gateway:theme'&&valid(event.data.theme)){apply(event.data.theme);controls()}});
 function controls(){
  const form=document.getElementById('settings-form');if(!form)return;
  let select=document.getElementById('theme-select');
  if(!select){
   const panel=document.createElement('section');panel.className='panel mb';panel.id='appearance-settings';
   const label=document.createElement('label');label.htmlFor='theme-select';label.textContent='Appearance';
   select=document.createElement('select');select.id='theme-select';select.setAttribute('aria-label','Appearance');
   for(const [value,text] of [['light','Warm off-white'],['dark','Dark']]){const option=document.createElement('option');option.value=value;option.textContent=text;select.append(option)}
   const error=document.createElement('p');error.id='theme-error';error.className='update-error';error.setAttribute('role','alert');
   select.addEventListener('change',()=>choose(select.value));panel.append(label,select,error);form.before(panel);
  }
  select.value=saved;select.disabled=saving||loading;const error=document.getElementById('theme-error');if(error.textContent!==failure)error.textContent=failure;error.hidden=!failure;
 }
 document.addEventListener('DOMContentLoaded',()=>{controls();new MutationObserver(controls).observe(document.body,{childList:true,subtree:true})});
})();
