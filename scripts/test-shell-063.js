'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const read = p => fs.readFileSync(path.join(root,p),'utf8');
const html = read('ui/shell/index.html');
const app = read('ui/shell/app.js');
const workspace = read('ui/shell/workspace.js');

const topbar = html.match(/<header class="topbar">([\s\S]*?)<\/header>/)?.[1] || '';
assert.doesNotMatch(html, /<aside class="sidebar"/);
const routes = [...topbar.matchAll(/<[^>]*class="[^"]*\bnav-item\b[^>]*data-route="([^"]+)"/g)].map(m=>m[1]);
assert.deepEqual(routes, ['.gateway','indexes.gateway','peers.gateway','settings.gateway']);
assert.match(topbar, /class="brand nav-item"[^>]*data-route="\.gateway"/, 'Gateway mark owns Home navigation');
assert.match(html, /id="address-form"/, 'one persistent address bar remains in the shell');
assert.equal((html.match(/id="address-form"/g)||[]).length, 1, 'shell must have exactly one address bar');
assert.match(html, /id="settings-button"[^>]*data-route="settings\.gateway"|data-route="settings\.gateway"[^>]*id="settings-button"/, 'settings stays secondary in the top bar');
for (const route of ['bitcoin.gateway','satline.gateway','ord.gateway','storage.gateway','activity.gateway','settings.gateway','sync.gateway']) {
  assert.match(app, new RegExp(`['"]${route.replace('.', '\\.') }['"]`), `${route} remains resolvable without being a primary tab`);
}
assert.match(workspace, /id="home-state"/, 'Home renders a dedicated quiet status surface');
assert.match(workspace, /ix\.live\|\|\[\]/, 'Home reads persisted Live-index state');
assert.match(workspace, /Nothing is being maintained yet\./, 'Home has a quiet no-maintenance state');
assert.match(workspace, /data-route="indexes\.gateway"/, 'Home doorway to Indexes remains explicit');
assert.match(workspace, /data-route="peers\.gateway"/, 'Home doorway to Peers remains explicit');
assert.doesNotMatch(workspace.slice(workspace.indexOf('async function home'), workspace.indexOf('async function connections')), /Explore Bitcoin|FOLLOW A SAT|OPEN AN INSCRIPTION/, 'Home does not duplicate resolver domains as dashboard tiles');
console.log('Gateway testing shell contract: PASS (root Home + two primary workspaces + one address bar + secondary settings + legacy resolver views retained).');
