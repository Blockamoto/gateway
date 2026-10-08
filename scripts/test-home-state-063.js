'use strict';
// Functional view tests, not browser/DOM acceptance. The real workspace script
// runs against an in-memory API and minimal view boundary, without networking.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const sandbox = {window:{}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../ui/shell/workspace.js'),'utf8'), sandbox);

async function run() {
  const state = {innerHTML:''}, root = {innerHTML:'', querySelector: selector => {
    assert.equal(selector, '#home-state'); return state;
  }};
  let active = true, next, fail = false;
  const requested = [];
  const ix = {live:[],job:{}}, settings = {settings:{}}, network = {enabled:false,connected:0,gateway_connected:0,headers:{header_state:'current',header_height:100}};
  const api = async (url, data) => {
    assert.equal(data, undefined, 'Home must not mutate settings or start work');
    requested.push(url);
    if (fail) throw Error('fixture unavailable <script>');
    const reply = {'/api/v1/index/status':ix,'/api/v1/settings':settings,'/api/v1/network':network}[url];
    assert.ok(reply, 'unexpected API request: '+url);
    return reply;
  };
  await sandbox.window.GatewayWorkspace.home({root,api,heading:()=>'',isCurrent:()=>active,schedule:fn=>{next=fn;}});
  assert.match(state.innerHTML, /Headers offline/);
  assert.match(state.innerHTML, /Nothing is being maintained yet/);
  assert.doesNotMatch(state.innerHTML, /home-dot ok/);
  assert.equal(typeof next, 'function', 'Home must keep its displayed state fresh');

  network.enabled = true;
  await next();
  assert.match(state.innerHTML, /Header freshness unknown/, 'stale current flag without a peer is not freshness');
  network.connected = 1;
  await next();
  assert.match(state.innerHTML, /Headers caught up/);
  settings.settings.headers_paused = true;
  await next();
  assert.match(state.innerHTML, /Headers paused/);
  settings.settings.headers_paused = false;
  network.headers.error = 'fixture failure';
  await next();
  assert.match(state.innerHTML, /Header freshness unknown/);
  delete network.headers.error;

  ix.live = [{index:'bitmap',enabled:true,state:'synced',tip_fresh:false,lag_known:false,lag:0}];
  await next();
  assert.match(state.innerHTML, /Bitmap freshness unknown/);
  ix.live[0] = {index:'bitmap',enabled:true,state:'catching_up',tip_fresh:true,lag_known:true,lag:4};
  await next();
  assert.match(state.innerHTML, /Bitmap catching up · 4 blocks behind/);
  ix.job = {id:'fixture',index:'bitmap',state:'waiting',height:96,error:'source <unavailable>'};
  ix.errors = ['policy <error>'];
  await next();
  assert.match(state.innerHTML, /source &lt;unavailable&gt;/);
  assert.match(state.innerHTML, /policy &lt;error&gt;/);

  fail = true;
  await next();
  assert.match(state.innerHTML, /Status unavailable/);
  assert.match(state.innerHTML, /fixture unavailable &lt;script&gt;/);
  fail = false;
  await next();
  assert.doesNotMatch(state.innerHTML, /Status unavailable/, 'refresh must recover without navigating away');
  const old = state.innerHTML;
  active = false;
  const last = next; next = null;
  await last();
  assert.equal(next, null, 'a departed view must not continue scheduling itself');
  assert.equal(state.innerHTML, old, 'a departed view must not repaint another route');
  assert.equal(new Set(requested).size, 3);
  console.log('Home state: PASS (refresh, offline/paused/error freshness, Live lag, escaping, recovery, route cancellation; mocked view/API boundary).');
}
run().catch(error => { console.error(error); process.exitCode = 1; });
