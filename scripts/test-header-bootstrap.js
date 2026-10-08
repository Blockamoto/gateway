'use strict';
// Exercise the actual packaged baseline and restart path without Core, peers,
// browser integration, an updater feed, or an existing user profile.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {createHash} = require('node:crypto');

const options = {};
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i], value = process.argv[i + 1];
  if (!['--binary', '--out', '--timeout-ms'].includes(name) || !value) {
    throw Error('Usage: node scripts/test-header-bootstrap.js --binary PATH --out DIR [--timeout-ms 600000]');
  }
  options[name.slice(2)] = value;
}
if (!options.binary || !options.out) throw Error('--binary and --out are required');
const binary = path.resolve(options.binary), out = path.resolve(options.out);
const timeout = Number(options['timeout-ms'] || 600000);
assert(Number.isSafeInteger(timeout) && timeout > 0, 'timeout must be positive milliseconds');
const expectedVersion = fs.readFileSync(path.join(__dirname, '..', 'VERSION.txt'), 'utf8').trim();
const sidecar = path.join(path.dirname(binary), 'bootstrap', 'headers-mainnet.bin');
const metadataPath = path.join(path.dirname(binary), 'bootstrap', 'headers-mainnet.json');
fs.mkdirSync(out, {recursive: true});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const sha = bytes => createHash('sha256').update(bytes).digest();
const roundMS = start => Math.round(performance.now() - start);

async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of fs.createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}

function diskTip(file) {
  const size = fs.statSync(file).size;
  assert(size >= 80 && size % 80 === 0, 'header file has complete records');
  const fd = fs.openSync(file, 'r'), last = Buffer.alloc(80);
  try { assert.equal(fs.readSync(fd, last, 0, 80, size - 80), 80); }
  finally { fs.closeSync(fd); }
  return {count: size / 80, bytes: size, tip_hash: Buffer.from(sha(sha(last))).reverse().toString('hex')};
}

function loopbackURL(record, child) {
  assert.equal(record.pid, child.pid, 'runtime must belong to the process this script started');
  const url = new URL(record.url);
  assert.equal(url.protocol, 'http:');
  assert.equal(url.hostname, '127.0.0.1');
  assert(url.port && !url.username && !url.password && url.pathname === '/' && !url.search && !url.hash, 'local runtime URL only');
  return url.origin;
}

async function jsonAt(base, route, init = {}) {
  // Every request originates from the validated loopback runtime record.
  const response = await fetch(base + route, {redirect: 'error', signal: AbortSignal.timeout(3000), ...init});
  assert(response.ok, route + ': HTTP ' + response.status);
  return response.json();
}

function childEnvironment(fixture) {
  const env = {...process.env};
  for (const name of Object.keys(env)) {
    if (/^(?:GATEWAY_|GH_TOKEN$|GITHUB_TOKEN$|HTTPS?_PROXY$|ALL_PROXY$)/i.test(name)) delete env[name];
  }
  // Profile registration and platform data discovery remain inside this fixture.
  env.LOCALAPPDATA = path.join(fixture, 'local-app-data');
  env.APPDATA = path.join(fixture, 'app-data');
  fs.mkdirSync(env.LOCALAPPDATA, {recursive: true});
  fs.mkdirSync(env.APPDATA, {recursive: true});
  return env;
}

async function run() {
  const started = performance.now();
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'gateway-header-bootstrap-'));
  const profile = path.join(fixture, 'profile');
  const headers = path.join(profile, 'headers', 'headers.bin');
  const runtimePath = path.join(profile, 'runtime.json');
  fs.mkdirSync(profile);
  const result = {status: 'running', started_at: new Date().toISOString(), binary, version: expectedVersion,
    sidecar, fixture, profile, profile_retained: true, runs: [],
    policy: {network_disabled: true, core_disabled: true, core_mount_disabled: true, headers_paused: false, loopback_requests_only: true}};
  const saveResult = () => fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  let current = null, interrupted = false, failure;
  const onSignal = () => { interrupted = true; };
  process.on('SIGINT', onSignal);
  process.on('SIGTERM', onSignal);

  function ensureRunning(state) {
    if (interrupted) throw Error('Header bootstrap acceptance interrupted');
    if (state.spawnError) throw state.spawnError;
    if (state.exited) throw Error('Fixture process exited unexpectedly: ' + JSON.stringify(state.exit));
  }

  async function waitFor(fn, label, state, limit = timeout) {
    const end = performance.now() + limit;
    while (performance.now() < end) {
      ensureRunning(state);
      const value = await fn();
      if (value) return value;
      await delay(250);
    }
    throw Error('Timed out waiting for ' + label);
  }

  async function stop(state) {
    if (!state || state.exited) return;
    const stopStart = performance.now();
    let quitError;
    try {
      const record = JSON.parse(fs.readFileSync(runtimePath, 'utf8'));
      const base = loopbackURL(record, state.child);
      assert.equal(typeof record.token, 'string');
      assert(record.token.length > 0);
      await jsonAt(base, '/api/v1/runtime/quit', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-Gateway-Token': record.token}, body: '{}'});
      const end = performance.now() + 20000;
      while (!state.exited && performance.now() < end) await delay(100);
      assert(state.exited, 'fixture did not quit gracefully');
      assert.equal(state.exit.code, 0, 'graceful runtime exit code');
    } catch (error) { quitError = error; }
    if (!state.exited) {
      state.child.kill(); // Only this script's child handle can be terminated.
      const end = performance.now() + 10000;
      while (!state.exited && performance.now() < end) await delay(100);
      if (!state.exited) throw Error('Could not stop task-owned PID ' + state.child.pid);
      state.run.forced_stop = true;
    }
    state.run.stop_ms = roundMS(stopStart);
    state.run.exit = state.exit;
    if (quitError) throw quitError;
  }

  try {
    result.binary_sha256 = await digest(binary);
    const meta = JSON.parse(fs.readFileSync(metadataPath, 'utf8'));
    assert.equal(meta.schema, 1);
    assert.equal(meta.network, 'mainnet');
    assert(Number.isSafeInteger(meta.count) && meta.count > 1, 'actual nonempty release baseline required');
    result.expected = {...diskTip(sidecar), sha256: await digest(sidecar)};
    assert.equal(result.expected.count, meta.count);
    assert.equal(result.expected.sha256, meta.sha256);
    assert.equal(result.expected.tip_hash, meta.tip_hash);
    fs.writeFileSync(path.join(profile, 'settings.json'), JSON.stringify({
      network_disabled: true, headers_paused: false, core_disabled: true, core_mount_disabled: true,
      bitcoin_data_dir: path.join(fixture, 'unused-core'), bitcoin_blocks_dir: path.join(fixture, 'unused-core', 'blocks'),
      serve_data: false, share_cache: false, onboarded: true, lan_discovery: false, satline_enabled: false, ord_enabled: false
    }));
    const env = childEnvironment(fixture);
    saveResult();
    for (const phase of ['import', 'restart']) {
      const runStart = performance.now();
      const run = {phase, log: path.join(out, phase + '.log')};
      result.runs.push(run);
      const fd = fs.openSync(run.log, 'w');
      let child;
      try {
        child = spawn(binary, ['-data', profile, '-background', '-no-open', '-no-tray', '-http', '127.0.0.1:0'],
          {cwd: path.dirname(binary), env, windowsHide: true, stdio: ['ignore', fd, fd]});
      } finally { fs.closeSync(fd); }
      const state = {child, run, exited: false};
      current = state;
      child.once('error', error => { state.spawnError = error; state.exited = true; });
      child.once('exit', (code, signal) => { state.exited = true; state.exit = {code, signal}; });
      run.pid = child.pid;
      const base = await waitFor(async () => {
        if (!fs.existsSync(runtimePath)) return false;
        let record;
        try { record = JSON.parse(fs.readFileSync(runtimePath, 'utf8')); } catch { return false; }
        if (record.pid !== child.pid) return false;
        const base = loopbackURL(record, child);
        let ping;
        try { ping = await jsonAt(base, '/api/v1/runtime/ping'); } catch { return false; }
        assert.equal(ping.pid, child.pid);
        assert.equal(ping.version, expectedVersion);
        return base;
      }, 'isolated runtime startup', state, 30000);
      run.startup_ms = roundMS(runStart);
      let progressAt = 0;
      const status = await waitFor(async () => {
        const status = await jsonAt(base, '/api/v1/status');
        run.last_status = status;
        if (status.header_bootstrap_error) throw Error('Baseline validation: ' + status.header_bootstrap_error);
        if (performance.now() - progressAt >= 10000) {
          console.log(phase + ': ' + status.header_count + '/' + meta.count + ' headers (' + status.header_bootstrap_state + ')');
          progressAt = performance.now();
          saveResult();
        }
        const expectedState = phase === 'import' ? 'ready' : 'skipped';
        return status.header_count === meta.count && status.header_bootstrap_state === expectedState ? status : false;
      }, 'complete ' + phase + ' baseline', state);
      run.ready_ms = roundMS(runStart);
      assert.equal(status.header_height, meta.count - 1);
      assert.equal(status.tip_hash, meta.tip_hash);
      const network = await jsonAt(base, '/api/v1/network');
      assert.equal(network.enabled, false);
      assert.equal(network.core_enabled, false);
      assert.equal(network.core_mount_enabled, false);
      assert.equal(network.headers_user_paused, false);
      assert.equal(network.connected, 0);
      assert.equal(network.gateway_connected, 0);
      assert.equal(network.bootstrap_running, false);
      run.network = {enabled: network.enabled, connected: network.connected, gateway_connected: network.gateway_connected,
        core_enabled: network.core_enabled, core_mount_enabled: network.core_mount_enabled};
      run.headers = {...diskTip(headers), sha256: await digest(headers)};
      assert.deepEqual(run.headers, result.expected, phase + ' on-disk baseline exactly matches the release sidecar');
      await stop(state);
      current = null;
      assert.equal(fs.existsSync(runtimePath), false, 'graceful quit removes its runtime record');
      assert.equal(await digest(headers), meta.sha256, 'shutdown preserves the full baseline');
      run.total_ms = roundMS(runStart);
      saveResult();
    }
    assert.deepEqual(result.runs[1].headers, result.runs[0].headers, 'restart retains identical count, digest and selected tip');
    assert.equal(await digest(binary), result.binary_sha256, 'tested executable was not replaced during acceptance');
    assert.equal(await digest(sidecar), result.expected.sha256, 'release sidecar was not modified');
    result.status = 'passed';
  } catch (error) {
    failure = error;
    result.status = 'failed';
    result.error = error.stack || String(error);
  } finally {
    if (current) {
      try { await stop(current); }
      catch (error) { result.cleanup_error = error.message; failure ||= error; result.status = 'failed'; }
    }
    process.off('SIGINT', onSignal);
    process.off('SIGTERM', onSignal);
    result.total_ms = roundMS(started);
    result.finished_at = new Date().toISOString();
    if (!failure) {
      // The exact freshly-created fixture is checked before recursive cleanup.
      const resolved = fs.realpathSync(fixture), temp = fs.realpathSync(os.tmpdir());
      assert.equal(path.dirname(resolved).toLowerCase(), temp.toLowerCase());
      assert(path.basename(resolved).startsWith('gateway-header-bootstrap-'));
      fs.rmSync(resolved, {recursive: true});
      result.profile_retained = false;
    }
    saveResult();
  }
  if (failure) throw failure;
  console.log('Header bootstrap/restart: PASS (' + result.expected.count + ' headers, import ' + result.runs[0].ready_ms + 'ms, restart ' + result.runs[1].ready_ms + 'ms).');
  console.log(path.join(out, 'result.json'));
}

run().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
