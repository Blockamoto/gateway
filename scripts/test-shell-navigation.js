'use strict';

// Real browser DOM regression for the shell router. All HTTP traffic is fulfilled
// in memory: this never opens or changes a running Gateway node. All referenced
// shell scripts are real; embedded module pages and APIs are test doubles.
// Run: node scripts/test-shell-navigation.js
// Reproduce a checked-in regression: node scripts/test-shell-navigation.js --revision HEAD
// Requires Playwright and an installed Chromium browser (bundled Chromium, Edge or
// Chrome), or GATEWAY_TEST_BROWSER pointing to a browser executable. No downloads.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {execFileSync} = require('node:child_process');
const root = path.resolve(__dirname, '..');
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--revision')) {
  throw Error('Usage: node scripts/test-shell-navigation.js [--revision <git-ref>]');
}
const revision = args[1];
const read = name => revision
  ? execFileSync('git', ['show', `${revision}:${name}`], {cwd:root, encoding:'utf8', maxBuffer:2 << 20})
  : fs.readFileSync(path.join(root, name), 'utf8');
// Read once so concurrent source edits cannot mix versions within a test run.
const html = read('ui/shell/index.html');
const scripts = new Map([...html.matchAll(/<script\b[^>]*\bsrc="(\/shell\/[^"?]+\.js)"/g)]
  .map(match => [match[1], read('ui' + match[1])]));
assert.ok(scripts.has('/shell/app.js'), 'real shell app.js must be referenced by the HTML');

function playwright() {
  try { return require('playwright'); } catch (error) {
    const bundled = path.join(os.homedir(), '.cache', 'codex-runtimes', 'codex-primary-runtime', 'dependencies', 'node', 'node_modules', 'playwright');
    if (fs.existsSync(bundled)) return require(bundled);
    throw Error('Playwright is required. Make an existing installation available through NODE_PATH. ' + error.message);
  }
}

function browserPath(chromium) {
  const candidates = [process.env.GATEWAY_TEST_BROWSER, chromium.executablePath()];
  if (process.platform === 'win32') {
    for (const base of [process.env['ProgramFiles(x86)'], process.env.ProgramFiles, process.env.LOCALAPPDATA].filter(Boolean)) {
      candidates.push(path.join(base, 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
      candidates.push(path.join(base, 'Google', 'Chrome', 'Application', 'chrome.exe'));
    }
  }
  const found = candidates.find(candidate => candidate && fs.existsSync(candidate));
  if (!found) throw Error('No installed Chromium browser found. Set GATEWAY_TEST_BROWSER; this test does not download browsers.');
  return found;
}

const origin = 'http://gateway-shell.test';
const fixtures = {
  '/api/v1/settings': {settings:{bitcoin_peers:[], satline_enabled:true, ord_enabled:true}, core:{connected:false}, core_store:{}},
  '/api/v1/browser/status': {browser:{native_active:false}},
  '/api/v1/system/status': {},
  '/api/v1/peers': {connections:[], network:{}, core_peers:[]},
  '/api/v1/migration': {job:{}, archive_root:'local fixture archive'},
  '/api/v1/graph/status': {},
  '/api/v1/jobs': {jobs:[]},
  '/api/v1/index/status': {job:{}, instances:[]},
  '/api/v1/network': {enabled:true, connected:0, gateway_connected:0, headers:{header_height:0, header_state:'waiting'}, discovery:{mode:'bootstrap'}},
  '/api/v1/setup': {show_automatically:false, settings:{}, progress:{stage:0}},
  '/api/v1/discovery': {rows:[], next_offset:-1},
  '/api/v1/data-coverage': {headers:{header_height:0}, cache:{}, transaction_locations:{}, spender_knowledge:{}, mounted:{}, mounted_preparation:{}, core:{connected:false}, serving:{}},
};

async function harness(browser, address = '.gateway', failStatus = false) {
  const context = await browser.newContext({serviceWorkers:'block'});
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  const errors = [], unexpected = [], requests = [];
  page.on('pageerror', error => errors.push(error.message));
  await context.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url());
    requests.push({method:request.method(), path:url.pathname});
    const fulfill = (body, contentType, status = 200) => route.fulfill({status, contentType, body});
    if (url.origin !== origin) {
      unexpected.push(request.url());
      return route.abort();
    }
    if (url.pathname === '/') return fulfill(html.replace('__GATEWAY_BOOTSTRAP__', JSON.stringify({version:'test', address, activation:''})), 'text/html');
    if (scripts.has(url.pathname)) return fulfill(scripts.get(url.pathname), 'application/javascript');
    if (url.pathname.endsWith('.css')) return fulfill('', 'text/css');
    if (url.pathname === '/shell/gateway.ico') return fulfill('', 'image/x-icon');
    if (url.pathname === '/shell/gateway-mark.svg') return fulfill('<svg xmlns="http://www.w3.org/2000/svg"/>', 'image/svg+xml');
    if (['/satline', '/indexes'].includes(url.pathname)) return fulfill('<!doctype html><title>Module fixture</title><h1>Embedded module fixture</h1>', 'text/html');
    if (request.method() === 'GET' && Object.hasOwn(fixtures, url.pathname)) {
      if (failStatus && url.pathname === '/api/v1/settings') return fulfill(JSON.stringify({error:'Fixture services unavailable'}), 'application/json', 503);
      return fulfill(JSON.stringify(fixtures[url.pathname]), 'application/json');
    }
    unexpected.push(`${request.method()} ${url.pathname}`);
    return fulfill(JSON.stringify({error:'Unexpected test request'}), 'application/json', 500);
  });
  await page.goto(origin, {waitUntil:'load'});
  await page.waitForFunction(() => document.querySelector('#content')?.children.length || !document.querySelector('#notice')?.hidden);
  return {context, page, errors, unexpected, requests};
}

async function healthy(page, label) {
  const state = await page.evaluate(() => ({
    children:document.querySelector('#content').children.length,
    text:document.querySelector('#content').textContent.trim(),
    frames:document.querySelector('#content').querySelectorAll('iframe').length,
    notice:document.querySelector('#notice').hidden ? '' : document.querySelector('#notice').textContent,
  }));
  assert.ok(state.children > 0 && (state.text.length > 15 || state.frames > 0), `${label}: content is blank; notice=${JSON.stringify(state.notice)}`);
  assert.equal(state.notice, '', `${label}: unexpected shell error`);
}

async function checkRoute(page, address, selector) {
  await page.locator(selector).first().waitFor();
  await healthy(page, address);
  assert.equal(await page.locator('#address').inputValue(), address);
  const selected = page.locator('.nav-item.active');
  assert.equal(await selected.count(), 1, `${address}: one active destination`);
  assert.equal(await selected.getAttribute('data-route'), address);
  assert.equal(await selected.getAttribute('aria-current'), 'page');
  if (!['satline.gateway', 'indexes.gateway'].includes(address)) {
    assert.equal(await page.locator('#content').evaluate(node => node.classList.contains('embed-host')), false, `${address}: stale embedded layout`);
  }
}

async function run() {
  const {chromium} = playwright();
  const browser = await chromium.launch({headless:true, executablePath:browserPath(chromium)});
  try {
    const h = await harness(browser);
    try {
      await healthy(h.page, 'initial Home');
      await h.page.locator('#home-current').getByText('No index job started', {exact:true}).waitFor();
      await checkRoute(h.page, '.gateway', '#home-current .current-work');
      const routes = [
        ['bitcoin.gateway', '#content h1'], ['satline.gateway', '#satline-module'],
        ['ord.gateway', '#ord-form'], ['indexes.gateway', 'iframe.indexes-frame'],
        ['discovery.gateway', '#discovery-page [data-seek-status]'],
        ['sync.gateway', '#content .coverage-row'], ['peers.gateway', '#peer-data .panel'],
        ['storage.gateway', '#storage-body .grid'], ['activity.gateway', '#jobs-list .panel'],
        ['settings.gateway', '#settings-form'], ['.gateway', '#home-current .current-work'],
      ];
      for (const [address, selector] of routes) {
        const nav = h.page.locator(`.nav-item[data-route="${address}"]`);
        assert.equal(await nav.count(), 1, `missing shell destination ${address}`);
        // Click the nested icon to exercise the actual delegated closest() path.
        await nav.locator('span').first().click();
        await checkRoute(h.page, address, selector);
        assert.equal(new URL(h.page.url()).searchParams.get('resolve'), address);
        if (address === 'indexes.gateway') {
          assert.equal(await h.page.locator('iframe.indexes-frame').getAttribute('src'), '/indexes?embedded=1');
          assert.equal(await h.page.locator('iframe.indexes-frame').getAttribute('title'), 'Index workspace');
          assert.equal(new URL(h.page.url()).pathname, '/', 'Indexes must preserve the shell');
        }
      }
      // Address-bar entry and browser back/forward are separate router entry points.
      await h.page.locator('#address').fill('indexes.gateway');
      await h.page.locator('#address-form').evaluate(form => form.requestSubmit());
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      await h.page.goBack();
      await checkRoute(h.page, '.gateway', '#home-current .current-work');
      await h.page.goForward();
      await checkRoute(h.page, 'indexes.gateway', 'iframe.indexes-frame');
      // A plain external/legacy nav anchor must not crash active-state traversal.
      await h.page.evaluate(() => {
        const anchor = document.createElement('a');
        anchor.className = 'nav-item'; anchor.href = '/legacy'; anchor.textContent = 'Legacy fixture';
        document.querySelector('nav').append(anchor);
      });
      await h.page.locator('.brand span').last().click();
      await checkRoute(h.page, '.gateway', '#home-current .current-work');
      // An untrusted origin must not drive the management shell through postMessage.
      await h.page.evaluate(() => window.dispatchEvent(new MessageEvent('message', {origin:'https://untrusted.invalid', data:{type:'gateway:navigate', address:'indexes.gateway'}})));
      assert.equal(await h.page.locator('#address').inputValue(), '.gateway');
      assert.deepEqual(h.errors, [], 'uncaught browser exceptions');
      assert.deepEqual(h.unexpected, [], 'unexpected network/API requests');
      assert.ok(h.requests.every(request => request.method === 'GET'), 'navigation unexpectedly mutated state');
    } finally { await h.context.close(); }

    const deep = await harness(browser, 'indexes.gateway');
    try {
      await checkRoute(deep.page, 'indexes.gateway', 'iframe.indexes-frame');
      assert.deepEqual(deep.errors, []);
      assert.deepEqual(deep.unexpected, []);
    } finally { await deep.context.close(); }

    const degraded = await harness(browser, '.gateway', true);
    try {
      await degraded.page.locator('#home-current').getByText('Workspace status unavailable', {exact:true}).waitFor();
      await healthy(degraded.page, 'Home with unavailable services');
      await degraded.page.locator('.nav-item[data-route="bitcoin.gateway"]').click();
      await checkRoute(degraded.page, 'bitcoin.gateway', '#content h1');
      assert.deepEqual(degraded.errors, []);
      assert.deepEqual(degraded.unexpected, []);
    } finally { await degraded.context.close(); }
    console.log(`Shell navigation: PASS (${revision || 'working tree'}; real DOM/all ${scripts.size} shell scripts, initial/degraded Home, 11 destinations, Indexes deep link, nested clicks, address entry, history, plain anchor, origin guard; mocked embedded pages/API boundaries).`);
  } finally { await browser.close(); }
}

run().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
