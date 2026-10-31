#!/usr/bin/env python3
"""Offline process/API smoke for a portable preview in a NEW disposable folder.

Uses authenticated mainnet genesis/block-1 headers and the 215-byte block-1
fixture already exercised by the Go parser tests. Does not control a browser.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
from urllib.parse import urlencode, urlparse
from urllib.request import Request, urlopen


def sha256d(data):
    return hashlib.sha256(hashlib.sha256(data).digest()).digest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--preview', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    preview, out = args.preview.resolve(), args.out.resolve()
    if out.exists():
        parser.error('--out must be a new disposable folder; existing data is preserved')
    out.mkdir(parents=True)
    profile = out / 'profile'
    (profile / 'headers').mkdir(parents=True)
    genesis = bytes.fromhex('01000000' + '00' * 32 +
                           '3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a' +
                           '29ab5f49ffff001d1dac2b7c')
    block = bytes.fromhex('01000000' + '6fe28c0ab6f1b372c1a6a246ae63f74f931e8365e15a089c68d6190000000000' +
                          '982051fd1e4ba744bbbe680e1fee14677ba1a3c3540bf7b1cdb606e857233e0e' +
                          '61bc6649ffff001d01e36299' + '010100000001' + '00' * 32 +
                          'ffffffff0704ffff001d0104ffffffff0100f2052a010000004341' +
                          '0496b538e853519c726a2c91e61ec11600ae1390813a627c66fb8be7947be63c52da7589379515d4e0a604f8141781e62294721166bf621e73a82cbf2342c858ee' +
                          'ac00000000')
    assert len(genesis) == 80 and len(block) == 215
    block_hash = sha256d(block[:80])[::-1].hex()
    assert block_hash == '00000000839a8e6886ab5951d76f411475428afc90947ee320161bbf18eb6048'
    (profile / 'headers' / 'headers.bin').write_bytes(genesis + block[:80])
    sources = profile / 'indexes' / 'sources'
    sources.mkdir(parents=True)
    (sources / (block_hash + '.block')).write_bytes(block)
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, serve_gateway_data=False,
                    share_cache=False, onboarded=True, lan_discovery=False, graph_index=False,
                    cache_blocks=False, privacy_mode=True, satline_use_peers=False,
                    bitcoin_data_dir=str(out / 'unused-core'))
    (profile / 'settings.json').write_text(json.dumps(settings), encoding='utf-8')
    process = None
    runtime = None
    logs = []
    evidence = dict(kind='offline-process-api-smoke', visible_browser_acceptance=False,
                    fixture=dict(height=1, block_hash=block_hash, bytes=len(block)), checks=[])

    def api(path, value=None):
        request = Request(runtime['url'] + path,
                          data=None if value is None else json.dumps(value).encode(),
                          headers={'Content-Type': 'application/json', 'X-Gateway-Token': runtime['token']})
        with urlopen(request, timeout=30) as response:
            return json.load(response)

    def start():
        nonlocal process, runtime
        log = (out / ('process-' + str(len(logs)) + '.log')).open('wb')
        logs.append(log)
        process = subprocess.Popen([str(preview / 'GatewayClient.exe'), '-data', str(profile),
                                    '-background', '-no-tray', '-http', '127.0.0.1:0'],
                                   cwd=preview, stdout=log, stderr=log,
                                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            assert process.poll() is None, 'preview exited during startup'
            try:
                candidate = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                assert candidate['pid'] == process.pid
                assert urlparse(candidate['url']).hostname == '127.0.0.1'
                runtime = candidate
                assert api('/api/v1/runtime/ping')['pid'] == process.pid
                return
            except (OSError, ValueError, AssertionError, KeyError):
                time.sleep(.1)
        raise TimeoutError('portable startup timeout')

    def stop():
        if process is not None and process.poll() is None:
            api('/api/v1/runtime/quit', {})
            process.wait(timeout=30)

    def save(name, recipe):
        recipe = copy.deepcopy(recipe)
        recipe.update(id=name, name=name, origin=1)
        validated = api('/api/v1/index/recipes/validate', dict(recipe=recipe))
        assert validated['validated'] and validated['runnable'], validated
        saved = api('/api/v1/index/recipes', dict(recipe=recipe))
        assert saved['semantic_hash'] == validated['semantic_hash']
        return saved

    def run(names, expected_reads, headers_only=False):
        request = dict(**{'in': 1}, out=1, consumers=[dict(index=name, armed=True) for name in names])
        plan = api('/api/v1/index/run/plan', request)
        assert plan['ready'] and not plan.get('requires_genesis'), plan
        api('/api/v1/index/run/start', dict(request=request, plan_id=plan['plan_id']))
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            result = api('/api/v1/index/run')
            state = result['run']['state']
            if state not in ('running', 'preparing', 'stopping'):
                assert state == 'complete', result
                counts = result['run']['counts']
                assert counts.get('source_reads', 0) == expected_reads, counts
                if headers_only:
                    assert counts.get('decoded_blocks', 0) == 0, counts
                evidence['checks'].append(dict(run=names, counts=counts, state=state))
                return
            time.sleep(.1)
        raise TimeoutError('portable recipe run timeout')

    def output(saved):
        params = dict(id=saved['recipe']['id'], revision=saved['revision'],
                      semantic_hash=saved['semantic_hash'], height=1, limit=20)
        result = api('/api/v1/index/recipes/output?' + urlencode(params))
        assert result['state'] == 'covered', result
        return result

    try:
        start()
        assert api('/api/v1/updates/status')['state'] == 'disabled'
        assert len(api('/api/v1/index/recipes/catalogue')['sources']) == 8
        saved = {}
        for name, file in [('qa-headers', 'headers'), ('qa-raw', 'raw-blocks'), ('qa-fees', 'transaction-fees')]:
            saved[name] = save(name, json.loads((preview / 'recipes' / 'builtins' / (file + '.json')).read_text(encoding='utf-8')))
        run(['qa-headers'], 1, headers_only=True)
        run(['qa-raw', 'qa-fees'], 1)
        run(['qa-raw', 'qa-fees'], 0)
        for item in saved.values():
            output(item)
        assert output(saved['qa-fees'])['state_entry_count'] == '2'
        stop()
        start()
        run(['qa-raw', 'qa-fees'], 0)
        for item in saved.values():
            output(item)
        for native in ['utxo-state', 'sat-state', 'txo-spender']:
            assert not (profile / 'indexes' / native / 'head.json').exists(), native
        evidence.update(passed=True, source_manifest=json.loads((preview / 'artifact-manifest.json').read_text(encoding='utf-8')),
                        saved=[dict(id=v['recipe']['id'], revision=v['revision'], semantic_hash=v['semantic_hash']) for v in saved.values()])
    finally:
        stop()
        for log in logs:
            log.close()
        (out / 'smoke-results.json').write_text(json.dumps(evidence, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(dict(passed=evidence['passed'], profile=str(profile), results=str(out / 'smoke-results.json'))))


if __name__ == '__main__':
    main()
