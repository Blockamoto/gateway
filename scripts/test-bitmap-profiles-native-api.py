#!/usr/bin/env python3
"""Exercise compiled Bitmap management routes in an owned disposable profile."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    binary, output = args.binary.resolve(), args.out.resolve()
    output.mkdir(parents=True, exist_ok=True)
    profile = Path(tempfile.mkdtemp(prefix='native-bitmap-api-', dir=output))
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, share_cache=False,
                    onboarded=True, lan_discovery=False, graph_index=False,
                    cache_blocks=False, privacy_mode=True)
    (profile / 'settings.json').write_text(json.dumps(settings), encoding='utf-8')
    process = None
    passed = False
    checks = []
    log = (output / 'runtime.log').open('wb')
    try:
        process = subprocess.Popen([str(binary), '-data', str(profile), '-background',
                                    '-no-tray', '-http', '127.0.0.1:0'],
                                   cwd=binary.parent, stdout=log, stderr=log,
                                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        endpoint = None
        for _ in range(600):
            if process.poll() is not None:
                raise AssertionError('Disposable runtime exited before readiness')
            try:
                runtime = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                assert runtime['pid'] == process.pid
                candidate = runtime['url']
                with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                    endpoint = candidate
                break
            except (OSError, ValueError, urllib.error.URLError):
                time.sleep(0.1)
        assert endpoint, 'Compiled runtime did not become ready'

        def call(path, body=None, expected=200):
            request = urllib.request.Request(endpoint + path,
                data=None if body is None else json.dumps(body).encode(),
                headers={'Content-Type': 'application/json', 'Origin': endpoint})
            try:
                response = urllib.request.urlopen(request, timeout=30)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw, status, kind = response.read(), response.status, response.headers.get('Content-Type', '')
            assert status == expected, (path, status, raw[:400])
            return json.loads(raw) if 'application/json' in kind else raw

        initial = call('/api/v1/bitmap/profiles')
        assert len(initial['registry']['district']) == 12 and len(initial['registry']['parcel']) == 13
        assert len(initial['instances']) == 3
        for asset in ('bitmap-workspace.js', 'bitmap-workspace.css'):
            assert len(call('/shell/' + asset)) > 500
        checks.append('compiled profile routes and both embedded workspace assets')
        before = call('/api/v1/index/status')
        assert not before['jobs'] and before['run'].get('run') is None

        made = call('/api/v1/bitmap/profiles', dict(action='create', kind='district',
                    label='Native reference', rules=initial['reference']['district'], linked_parcel=True))
        track, parcel, chain = made['instance'], made['linked_parcel'], made['chain']
        assert parcel['district_instance'] == track['id'] == chain['district_instance']
        assert parcel['district_profile_hash'] == track['profile_hash']
        assert len(call('/api/v1/bitmap/profiles')['instances']) == 6
        checks.append('atomic linked District, Chain and Parcel creation')
        status = call('/api/v1/index/status')
        definitions = {item['id']: item for item in status['definitions']}
        for item in (track, parcel, chain):
            definition = definitions[item['id']]
            assert definition['name'] == item['label'] and definition['buildable']
        assert track['id'] in definitions[parcel['id']]['dependencies']
        assert definitions[chain['id']]['dependencies'] == [track['id']]
        assert not status['jobs'] and status['run'].get('run') is None
        checks.append('real status exposes all unbuilt custom tracks and their exact dependencies without work')
        for item in (track, parcel, chain):
            view = call('/api/v1/bitmap/view?kind=' + item['kind'] + '&instance=' + item['id'] + '&expected_profile=' + item['profile_hash'])
            assert view['state'] == 'not_built'
        checks.append('instance/profile-bound unknown reads without jobs')
        renamed = call('/api/v1/bitmap/profiles', dict(action='rename', id=track['id'], label='Renamed reference'))
        assert renamed['instance']['profile_hash'] == track['profile_hash']
        checks.append('rename preserves executable rules identity')
        rules = dict(initial['reference']['district'], text=0)
        variant = call('/api/v1/bitmap/profiles', dict(action='create', kind='district', label='Media comparison', rules=rules))['instance']
        assert variant['profile_hash'] != track['profile_hash']
        result = call('/api/v1/bitmap/compare?baseline=' + track['id'] + '&variant=' + variant['id'])
        assert result['state'] == 'unavailable' and result['compared_blocks'] == 0
        checks.append('distinct alternative profile and honest unavailable comparison with Chain Off')

        owned = profile / 'indexes' / 'bitmap-development-v1' / track['id']
        owned.mkdir(parents=True)
        (owned / 'fixture.txt').write_text('Disposable owned deletion fixture', encoding='utf-8')
        review = call('/api/v1/index/delete?index=' + track['id'])
        assert review['ready'] and review['exists']
        (owned / 'head.json').write_text('{}', encoding='utf-8')
        call('/api/v1/index/delete', dict(index=track['id'], review_id=review['review_id']), 409)
        review = call('/api/v1/index/delete?index=' + track['id'])
        result = call('/api/v1/index/delete', dict(index=track['id'], review_id=review['review_id']))
        assert result['deleted'] and not result.get('cleanup_pending') and not owned.exists()
        assert any(item['id'] == track['id'] for item in call('/api/v1/bitmap/profiles')['instances'])
        checks.append('stale deletion refused, confirmed owned-data deletion, definition preserved')
        after = call('/api/v1/index/status')
        assert not after['jobs'] and after['run'].get('run') is None
        checks.append('management and inspection never launched an index job')
        layout = {'root': ['group:bitmap', 'group:txio', 'group:blocks'],
                  'group:txio': ['txio-sat-ranges', 'txio-links'],
                  'district:' + track['id']: [chain['id']]}
        saved = call('/api/v1/index/layout', dict(order=layout))
        assert saved['order'] == layout and saved['revision'] > 0
        process.terminate()
        process.wait(timeout=10)
        previous_endpoint = endpoint
        process = subprocess.Popen([str(binary), '-data', str(profile), '-background',
                                    '-no-tray', '-http', '127.0.0.1:0'],
                                   cwd=binary.parent, stdout=log, stderr=log,
                                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        endpoint = None
        for _ in range(600):
            if process.poll() is not None:
                raise AssertionError('Disposable runtime exited during restart')
            try:
                runtime = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                if runtime['pid'] != process.pid:
                    raise ValueError('Waiting for restarted runtime')
                candidate = runtime['url']
                with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                    endpoint = candidate
                break
            except (OSError, ValueError, urllib.error.URLError):
                time.sleep(0.1)
        assert endpoint and endpoint != previous_endpoint, 'Restart must exercise a new browser origin'
        restored = call('/api/v1/index/layout')
        assert restored == saved
        assert len(call('/api/v1/bitmap/profiles')['instances']) == 8
        assert call('/api/v1/index/status')['run'].get('run') is None
        checks.append('compiled restart preserves profiles and parent-bound layout across local ports without work')
        receipt = dict(result='PASS', binary=str(binary), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                       checks=checks, limitations=['No evaluated claim or network-source fixture in this management-route suite.'])
        (output / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8')
        passed = True
        print('Compiled Bitmap management API PASS: creation, identities, unknown reads, assets and reviewed deletion.')
    except Exception as error:
        (output / 'failure.json').write_text(json.dumps(dict(error=str(error), checks=checks), indent=2) + '\n', encoding='utf-8')
        raise
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
        log.close()
        if passed:
            assert profile.resolve().parent == output and profile.name.startswith('native-bitmap-api-') and not profile.is_symlink()
            shutil.rmtree(profile)


if __name__ == '__main__':
    main()
