'use strict';
// The browser only talks to its local Gateway. Signature verification, downloads
// and process replacement belong to the updater, never to a publisher web page.
window.GatewayUpdates = (() => {
 const states=new Set(['current','not_configured','disabled_offline','checking','available','downloading','staged','applying','error']);
 const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const size=n=>!Number.isFinite(n)||n<0?'Unknown':n<1024?n+' B':n<1048576?(n/1024).toFixed(1)+' KiB':(n/1048576).toFixed(1)+' MiB';
 function normalize(value,currentVersion){
  const v=value?.update||value||{};
  return {...v,state:states.has(v.state)?v.state:'unknown',current_version:v.current_version||currentVersion,configured:v.configured===true,auto_check:v.auto_check===true,auto_install:v.auto_check===true&&v.auto_install===true,can_apply:v.can_apply===true};
 }
 // A late status response must not replace the state returned by a newer action.
 // Keeping this controller DOM-free also lets the concurrency contract be tested.
 function createController(api,onChange=()=>{},currentVersion=''){
  let status=normalize({},currentVersion),pending='',clientError='',generation=0,disposed=false;
  const value=()=>({...status,pending,client_error:clientError});
  const emit=()=>{if(!disposed)onChange(value())};
  async function refresh(){
   if(disposed||pending)return;
   const ticket=++generation;
   try{const result=await api('/api/v1/updates/status');if(disposed||ticket!==generation)return;status=normalize(result,currentVersion);clientError='';emit()}
   catch(e){if(disposed||ticket!==generation)return;clientError=e.message||'Update status could not be read.';emit()}
  }
  async function act(action,data={}){
   if(disposed||pending)return false;
   const ticket=++generation;pending=action;clientError='';emit();
   try{
    const result=await api('/api/v1/updates/'+action,data);
    if(disposed||ticket!==generation)return false;
    if(result?.state||result?.update?.state)status=normalize(result,currentVersion);
    pending='';emit();await refresh();return true;
   }catch(e){if(disposed||ticket!==generation)return false;pending='';clientError=e.message||'The update request failed.';emit();return false}
  }
  return {refresh,act,value,dispose:()=>{disposed=true;++generation}};
 }
 function view(v){
  const state=v.pending==='check'?'checking':v.pending==='download'?'downloading':v.pending==='apply'?'applying':v.state;
  const version=v.available_version?'v'+v.available_version:'the update';
  const descriptions={
   unknown:['Reading update status','Gateway is checking the local updater.'],
   not_configured:['Choose a trusted publisher','Set a publisher address and independently verified public key below.'],
   disabled_offline:['Updates paused while offline','Enable outbound networking in Data providers before checking or downloading updates.'],
   current:[v.last_checked?'Gateway is up to date':'Ready to check for updates',v.publisher_version?'This Gateway is v'+v.current_version+'; the publisher offers v'+v.publisher_version+' for '+v.platform+'.':v.last_checked?'This Gateway is v'+v.current_version+'. The last check did not offer a newer signed release.':'Checks use the trusted publisher configured below.'],
   checking:['Checking for updates','The hosted update service may take about a minute to start. Gateway waits and retries once if needed.'],
   available:[version+' is available','This Gateway is v'+v.current_version+'. Download the newer signed package for '+(v.platform||'this platform')+'.'],
   downloading:['Downloading '+version,'Gateway verifies the package before it can be installed.'],
   staged:[version+' is ready to install','The downloaded package has been verified. Restart when you are ready.'],
   applying:['Restarting to install '+version,'Keep this window open. Gateway will return after the update completes.'],
   error:['Update needs attention','You can retry after addressing the error below.'],
  };
  const [title,description]=descriptions[state]||descriptions.unknown;
  return {state,title,description,error:v.client_error||v.error||'',busy:!!v.pending||['checking','downloading','applying'].includes(state)};
 }
 let controller=null,container=null,timer=null,pendingOpen=false,lastState=null,initialVersion='',wasApplying=false,reloading=false;
 function render(v){
  if(v.state==='applying')wasApplying=true;
  // The approved runtime keeps its management origin across restart. Once that
  // origin finishes installation with the new version, load its new bundled UI
  // too. While applying, ordinary API admissions are still closed.
  if(wasApplying&&initialVersion&&v.current_version&&v.current_version!==initialVersion&&!view(v).busy&&!reloading){reloading=true;window.location.reload();return;}
  lastState=v;const m=view(v),badge=document.getElementById('updates-status');
  if(badge){
   const label=m.state==='available'?'Update '+(v.available_version||'')+' available':m.state==='staged'?'Restart to update':m.state==='downloading'?'Downloading update':m.state==='applying'?'Restarting Gateway':m.error?'Updates need attention':'';
   badge.hidden=!label;badge.textContent=label;badge.classList.toggle('update-attention',!!m.error);badge.setAttribute('aria-label',label?label+'. Open update settings.':'Update settings');
  }
  if(!container?.isConnected)return;
  const $=id=>container.querySelector('#'+id);
  $('update-title').textContent=m.title;$('update-description').textContent=m.description;
  $('update-state').textContent=m.state.replaceAll('_',' ');$('update-state').classList.toggle('warn',!!m.error||m.state==='not_configured');
  $('update-error').hidden=!m.error;$('update-error').textContent=m.error;
  $('update-current').textContent=v.current_version?'v'+v.current_version:'Unknown';
  $('update-available').textContent=v.available_version?'v'+v.available_version:m.state==='current'&&v.publisher_version?'Already installed':'Not verified';
  $('update-publisher-version').textContent=v.publisher_version?'v'+v.publisher_version:'Not checked yet';
  $('update-platform').textContent=v.platform||'Unknown';
  const checked=v.last_checked?new Date(v.last_checked):null;
  $('update-checked').textContent=checked&&!Number.isNaN(checked.getTime())?checked.toLocaleString():'Not checked yet';
  $('update-trust').textContent=v.configured?'Trusted public key configured':'No trusted publisher configured';
  $('update-fingerprint').textContent=v.key_fingerprint||v.key_id||'';
  const config=$('update-config');
  if(!config.dataset.dirty){$('update-publisher').value=v.publisher_url||'';$('update-key').value=v.trusted_key||'';$('update-mode').value=v.auto_check?v.auto_install?'automatic':'notify':'manual';
   const channel=$('update-channel'),options=[{id:'manual',label:'Custom publisher'},...(v.channels||[])];channel.replaceChildren(...options.map(x=>{const option=document.createElement('option');option.value=x.id;option.textContent=x.label||x.id;return option}));channel.value=v.channel||'manual';$('update-channel-field').hidden=!(v.channels||[]).length;
  }
  $('update-access-state').textContent=v.access_token_configured?'A publisher access credential is stored. Leave blank to retain it.':'No publisher access credential is stored. Public delivery does not require one.';
  $('update-clear-access').disabled=!v.access_token_configured;
  const unavailable=!v.configured||m.state==='disabled_offline'||m.state==='unknown';
  $('update-check').disabled=m.busy||unavailable;
  $('update-check').textContent=m.state==='checking'?'Checking…':m.error?'Retry update check':'Check for updates';
  $('update-refresh').hidden=!v.client_error;$('update-refresh').disabled=!!v.pending;
  $('update-download').hidden=!['available','downloading'].includes(m.state)&&!(m.state==='error'&&v.available_version);
  $('update-download').disabled=m.busy||unavailable;
  $('update-download').textContent=m.state==='downloading'?'Downloading…':m.error?'Retry download':'Download update';
  $('update-apply').hidden=!['staged','applying'].includes(m.state);
  $('update-apply').disabled=m.busy||!v.can_apply;
  $('update-apply').textContent=m.state==='applying'?'Restarting…':'Update and restart';
  $('update-apply-help').hidden=m.state!=='staged'||v.can_apply;
  $('update-apply-help').textContent=v.apply_unavailable_reason||'This Gateway cannot replace its executable. Use a supported installed or portable release layout.';
  const downloading=m.state==='downloading',total=Number(v.total_bytes),downloaded=Number(v.downloaded_bytes);
  $('update-progress-area').hidden=!downloading;
  const progress=$('update-progress');
  if(total>0&&Number.isFinite(total)&&Number.isFinite(downloaded)){progress.max=total;progress.value=Math.max(0,Math.min(total,downloaded));$('update-progress-label').textContent=size(downloaded)+' of '+size(total)+' · '+Math.min(100,Math.max(0,Math.floor(downloaded/total*100)))+'%'}
  else{progress.removeAttribute('value');$('update-progress-label').textContent=downloaded>0?size(downloaded)+' downloaded':'Waiting for package data…'}
  $('update-notes').hidden=!v.release_notes;$('update-notes-text').textContent=v.release_notes||'';
  for(const id of ['update-publisher','update-key','update-channel','update-key-confirm','update-access-token'])$(id).disabled=m.busy;
  $('update-clear-access').disabled=m.busy||!v.access_token_configured;
  $('update-mode').disabled=!!v.pending||v.automatic_committed===true;
  $('update-save').disabled=!!v.pending||v.automatic_committed===true;$('update-save').textContent=v.pending==='config'?'Saving…':m.busy?'Save update preference':'Save publisher settings';
 }
 function schedule(){clearTimeout(timer);timer=setTimeout(async()=>{await controller.refresh();schedule()},container?.isConnected||view(lastState||{}).busy?3000:60000)}
 function start({api,currentVersion}){
  if(controller)return;
  initialVersion=currentVersion;
  controller=createController(api,render,currentVersion);
  document.addEventListener('click',e=>{if(e.target.closest('[data-updates-open]'))pendingOpen=true},true);
  document.addEventListener('visibilitychange',()=>{if(!document.hidden)controller.refresh()});
  controller.refresh().finally(schedule);
 }
 function mount(target){
  if(!controller)return;container=target;
  container.innerHTML=`<div class="section-head"><h2>Gateway updates</h2><span id="update-state" class="badge">Reading status</span></div>
   <div class="update-overview"><div><h3 id="update-title">Reading update status</h3><p id="update-description" class="muted"></p></div><dl class="update-versions"><dt>Current</dt><dd id="update-current"></dd><dt>Publisher release</dt><dd id="update-publisher-version"></dd><dt>Available</dt><dd id="update-available"></dd><dt>Platform</dt><dd id="update-platform" class="mono"></dd><dt>Last successful check</dt><dd id="update-checked"></dd></dl></div>
   <p id="update-error" class="update-error" role="alert" hidden></p>
   <div id="update-progress-area" class="update-progress-area" hidden><label for="update-progress" id="update-progress-label">Downloading…</label><progress id="update-progress"></progress></div>
   <div class="actions"><button id="update-check" type="button">Check for updates</button><button id="update-refresh" type="button" hidden>Retry status</button><button id="update-download" type="button" class="primary" hidden>Download update</button><button id="update-apply" type="button" class="primary" hidden>Update and restart</button></div>
   <p id="update-apply-help" class="hint small" hidden></p>
   <p class="small muted">Installation restarts Gateway. Your Bitcoin data, indexes and settings stay in your data directory. Gateway can roll back if the new version fails to start.</p>
   <details id="update-notes" class="update-notes" hidden><summary>Release notes</summary><pre id="update-notes-text"></pre></details>
   <form id="update-config" class="update-config"><h3>Trusted publisher</h3><p class="small muted">Your publisher supplies signed updates directly to Gateway. You do not need a GitHub account. Obtain its public key through a trusted channel outside the update feed.</p>
    <div class="field" id="update-channel-field" hidden><label for="update-channel">Release channel</label><select id="update-channel"><option value="manual">Custom publisher</option></select><small>Bundled channels carry the release publisher’s address and public key. Selecting a channel requires saving your choice.</small></div><div class="field"><label for="update-publisher">Publisher URL</label><input id="update-publisher" type="url" autocomplete="off" spellcheck="false" placeholder="https://updates.example.org"><small>Use HTTPS. HTTP is accepted only for a publisher on this computer.</small></div>
    <div class="field"><label for="update-key">Trusted Ed25519 public key (base64)</label><input id="update-key" class="mono" autocomplete="off" spellcheck="false"><small>A public key is safe to share. Never enter a private signing key or GitHub token here.</small></div>
    <p class="small muted"><span id="update-trust"></span><br><span id="update-fingerprint" class="mono"></span></p>
    <label class="check"><input id="update-key-confirm" type="checkbox"><span>I verified this publisher’s public key outside the update feed.</span></label>
    <div class="field"><label for="update-access-token">Publisher access token (optional)</label><input id="update-access-token" type="password" autocomplete="new-password"><small id="update-access-state"></small><small>Use a dedicated delivery credential from your publisher. Never enter a GitHub token or signing key.</small></div><label class="check"><input id="update-clear-access" type="checkbox"><span>Remove the stored publisher access token</span></label>
    <div class="field"><label for="update-mode">Update preference</label><select id="update-mode"><option value="notify">Check automatically; ask before installing</option><option value="automatic">Download and install automatically</option><option value="manual">Only check when I ask</option></select><small>Automatic checks run at startup and every six hours. Automatic installation downloads verified updates and restarts Gateway; your data and settings are preserved. You can change this choice here.</small></div>
    <p id="update-config-error" class="update-error" role="alert" hidden></p><button id="update-save" type="submit">Save publisher settings</button>
   </form><p class="small muted update-unsigned">Signed update metadata verifies delivery. Windows executable code signing is separate; the current test binaries remain unsigned.</p>`;
  const $=id=>container.querySelector('#'+id),form=$('update-config');
  const act=action=>{controller.act(action).finally(schedule)};
  $('update-check').onclick=()=>act('check');$('update-download').onclick=()=>act('download');$('update-apply').onclick=()=>act('apply');
  $('update-refresh').onclick=()=>controller.refresh().finally(schedule);
  form.addEventListener('input',()=>{form.dataset.dirty='1'});
  $('update-channel').onchange=()=>{form.dataset.dirty='1';const selected=controller.value().channels?.find(x=>x.id===$('update-channel').value);if(selected){$('update-publisher').value=selected.publisher_url;$('update-key').value=selected.trusted_key;$('update-key-confirm').checked=false}};
  form.onsubmit=async e=>{
   e.preventDefault();const old=controller.value(),preferencesOnly=view(old).busy,mode=$('update-mode').value,input={publisher_url:preferencesOnly?old.publisher_url:$('update-publisher').value.trim(),trusted_key:preferencesOnly?old.trusted_key:$('update-key').value.trim(),auto_check:mode!=='manual',auto_install:mode==='automatic'};
   if(!preferencesOnly&&$('update-access-token').value)input.access_token=$('update-access-token').value;
   if(!preferencesOnly&&$('update-clear-access').checked)input.clear_access_token=true;
   if(old.channels?.length&&!preferencesOnly)input.channel=$('update-channel').value||'manual';else if(preferencesOnly&&old.channel)input.channel=old.channel;const changed=input.publisher_url!==old.publisher_url||input.trusted_key!==old.trusted_key;
   let error='';
   if(!input.publisher_url||!input.trusted_key)error='Enter a publisher URL and its independently verified public key.';
   else if(changed&&!$('update-key-confirm').checked)error='Verify the public key through a trusted channel, then confirm it above.';
   $('update-config-error').hidden=!error;$('update-config-error').textContent=error;if(error)return;
   const target=container;
   if(await controller.act('config',input)){
    if(container===target&&target.isConnected){delete form.dataset.dirty;$('update-key-confirm').checked=false;$('update-access-token').value='';$('update-clear-access').checked=false;render(controller.value());}
   }
   schedule();
  };
  render(controller.value());controller.refresh().finally(schedule);
  if(pendingOpen){pendingOpen=false;target.closest('#settings-body')?.querySelector('.settings-sections [data-settings-section="updates"]')?.click();}
 }
 function unmount(){container=null;if(controller)schedule()}
 return {start,mount,unmount,createController,view};
})();
