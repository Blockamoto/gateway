#!/usr/bin/env python3
"""Check the compiled canonical Bitmap API in disposable offline profiles.

The optional previous binary creates its own authentic registry and paused run;
this fixture never synthesizes checksummed registry, run or claim-store files.
"""
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
import urllib.parse
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
FIXED = {
    'bitmap-districts': ('district', '3e957003ceefbeebd84a6cff74e483956d51b456229d556c6cbdcc7d414c99da'),
    'bitmap-parcels': ('parcel', '19422536eee0541785a3bb8f15aad6f16ec4c09145b7f622aeb891f179df5af6'),
    'bitmap-chain': ('chain', 'd11609b8a55a55f6902fbdf158ed5331d956d49df4d254b47a4f3300f70f2211'),
}
GENESIS = bytes.fromhex(
    '01000000' + '00' * 32 +
    '3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a29ab5f49ffff001d1dac2b7c'
    '010100000001' + '00' * 32 +
    'ffffffff4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f7220'
    '6f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73ffffffff0100f2052a01000000'
    '434104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e'
    '51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac00000000')
GENESIS_HASH = '000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def prepare_profile(output):
    profile = Path(tempfile.mkdtemp(prefix='native-bitmap-standard-', dir=output))
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, share_cache=False,
                    onboarded=True, lan_discovery=False, graph_index=False,
                    cache_blocks=False, privacy_mode=True)
    (profile / 'settings.json').write_text(json.dumps(settings), encoding='utf-8')
    (profile / 'headers').mkdir()
    (profile / 'headers' / 'headers.bin').write_bytes(GENESIS[:80])
    return profile


class Runtime:
    def __init__(self, binary, profile, log_path, calls):
        self.binary, self.profile, self.log_path, self.calls = binary, profile, log_path, calls
        self.process, self.endpoint, self.log = None, None, None

    def start(self):
        try:
            self.log = self.log_path.open('ab')
            self.process = subprocess.Popen(
                [str(self.binary), '-data', str(self.profile), '-background', '-no-tray', '-http', '127.0.0.1:0'],
                cwd=self.binary.parent, stdout=self.log, stderr=self.log,
                creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                assert self.process.poll() is None, 'Owned runtime exited before readiness'
                try:
                    info = json.loads((self.profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                    if info['pid'] != self.process.pid:
                        raise ValueError('Waiting for this process')
                    candidate = info['url']
                    assert candidate.startswith('http://127.0.0.1:')
                    with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                        self.endpoint = candidate
                    return self
                except (OSError, ValueError, urllib.error.URLError):
                    time.sleep(0.1)
            raise AssertionError('Owned compiled runtime did not become ready')
        except BaseException:
            self.stop()
            raise

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=10)
        if self.log is not None:
            self.log.close()
            self.log = None
        assert self.process is None or self.process.poll() is not None

    def call(self, path, body=None, expected=200):
        request = urllib.request.Request(self.endpoint + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json', 'Origin': self.endpoint})
        try:
            response = urllib.request.urlopen(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw, status = response.read(), response.status
        value = json.loads(raw)
        self.calls.append(dict(path=path, method='GET' if body is None else 'POST', status=status))
        allowed = (expected,) if isinstance(expected, int) else expected
        assert status in allowed, (path, status, value)
        return value

    def denied(self, path, body=None, expected=(400, 403, 409)):
        value = self.call(path, body, expected)
        reason = value.get('error', '')
        assert reason and any(text in reason.lower() for text in ('unavailable', 'locked', 'not available', 'not an available', 'cannot execute', 'methodology', 'fixed canonical')), (path, value)
        return value

    def wait_run(self, states, run_id=None):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            document = self.call('/api/v1/index/run')
            run = document.get('run')
            if run and (run_id is None or run['id'] == run_id) and run['state'] in states:
                return document
            assert not document.get('error'), document
            time.sleep(0.1)
        raise AssertionError(('Run did not reach expected state', states, document))


def selection(identity, live=False):
    return dict(index=identity, armed=True, live=live)


def instance_query(instance):
    return urllib.parse.urlencode(dict(kind=instance['kind'], instance=instance['id'],
                                      expected_profile=instance['profile_hash']))


def preserved_files(profile):
    paths = [profile / 'indexes' / 'bitmap-profiles-v2', profile / 'indexes' / 'bitmap-development-v1']
    return {str(file.relative_to(profile)): sha(file.read_bytes()) for path in paths if path.exists()
            for file in path.rglob('*') if file.is_file()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    binary, output = args.binary.resolve(strict=True), args.out.resolve()
    output.mkdir(parents=True, exist_ok=True)
    assert len(GENESIS) == 285
    checks, calls, runtime, profile = [], [], None, None
    fixed = {'bitmap-districts-standard-v1': 'district', 'bitmap-parcels-standard-v1': 'parcel'}
    previous = {'bitmap-districts-canonical-v2': 'district', 'bitmap-parcels-canonical-v2': 'parcel',
                'bitmap-districts-canonical': 'district', 'bitmap-parcels-canonical': 'parcel'}
    chains = {'bitmap-district-chain-dev-v1': 'chain', 'bitmap-parcel-chain-dev-v1': 'chain'}
    try:
        profile = prepare_profile(output)
        runtime = Runtime(binary, profile, output / 'runtime.log', calls).start()
        initial = runtime.call('/api/v1/bitmap/profiles')
        assert initial['read_only']
        assert all(initial['capabilities'][key] is False for key in ('custom_profiles', 'comparisons', 'custom_execution'))
        instances = {i['id']: i for i in initial['instances']}
        assert set(instances) == set(fixed) | set(previous) | set(chains) | set(FIXED)
        for identity, kind in fixed.items():
            item = instances[identity]
            assert item['kind'] == kind and item['execution_available'] and not item.get('rules_editable')
            assert item['label'] == ('Bitmap Districts Standard V1' if kind == 'district' else 'Bitmap Parcels Standard V1')
            assert len(item['profile_hash']) == 64
            pinned = '335958311eb1c8ce4d14c48bc5c21d23c7a4b134'
            assert item['profile']['reference_revision'] == pinned
            assert pinned in item['profile']['reference_url']
            assert item['profile']['canonical_standard'] is True
            assert item['start_height'] == 792435
            assert item['profile']['fingerprint_schema'] == 'gateway-bitmap-standard-profile-v3'
            assert len(item['profile']['rules']) == (6 if kind == 'district' else 7)
            assert item['profile']['implementation_limits']
            view = runtime.call('/api/v1/bitmap/view?' + instance_query(item))
            assert view['state'] == 'not_built' and view['snapshot'] is None and view['profile_hash'] == item['profile_hash']
        for identity, (_, fingerprint) in FIXED.items():
            assert instances[identity]['profile_hash'] == fingerprint
            assert instances[identity]['execution_available'] is False
            assert instances[identity]['classification'] == 'legacy'
        assert instances['bitmap-districts-canonical']['profile_hash'] == 'eee99fd265545a86f71373406a3b302a30ffe11dfbdd4f06eafc2b1780ee961f'
        assert instances['bitmap-parcels-canonical']['profile_hash'] == 'ced973c270616540a3580022dfc3a035fb4e9eb26c45f0d3795208be12565307'
        assert instances['bitmap-districts-canonical-v2']['profile_hash'] == 'ab1154b7a3d6ea6256a191e6bb07e6d0199067af95dcc4cfd0245e1f48591b2c'
        assert instances['bitmap-parcels-canonical-v2']['profile_hash'] == '99a8ff74f55c4de451f9b26b446ae71e8e820d91a1fa8f6aaa65dd6a8535e051'
        for identity in previous | chains:
            assert not instances[identity]['execution_available'] and not instances[identity].get('rules_editable')
            assert instances[identity]['classification'] == 'legacy'
            if identity in chains:
                assert 'methodology is being developed' in instances[identity]['execution_unavailable_reason']
        assert runtime.call('/api/v1/bitmap/view?kind=district')['instance'] == 'bitmap-districts-standard-v1'
        assert runtime.call('/api/v1/bitmap/view?kind=parcel')['instance'] == 'bitmap-parcels-standard-v1'
        assert set(initial['chain_metadata']) == set(chains)
        checks.append('current District/Parcel identities start at 792435 with six/seven published rules and separate implementation limits; previous hashes remain unchanged and chains are inspect-only')
        status = runtime.call('/api/v1/index/status')
        definitions = {i['id']: i for i in status['definitions']}
        assert not status['jobs'] and not status['run'].get('run')
        for identity in fixed:
            assert definitions[identity]['buildable'] and not definitions[identity].get('locked')
            assert definitions[identity]['metadata'] and definitions[identity]['source_url']
        for identity in FIXED | previous | chains:
            assert definitions[identity]['locked']
        before = preserved_files(profile)
        for action in ('create', 'save_draft', 'update', 'rename'):
            runtime.denied('/api/v1/bitmap/profiles', dict(action=action, kind='district', rules={}), 403)
        runtime.denied('/api/v1/bitmap/profiles/validate', dict(kind='district', rules={}), 403)
        runtime.denied('/api/v1/bitmap/compare?baseline=bitmap-districts&variant=bitmap-parcels', expected=403)
        custom = 'bitmap-districts-' + '1' * 32
        for identity in list(FIXED | previous | chains) + [custom]:
            request = dict(consumers=[selection(identity)])
            runtime.denied('/api/v1/index/run/prepare', request)
            runtime.denied('/api/v1/index/run/start', dict(request=request))
            runtime.denied('/api/v1/index/build', dict(index=identity, retention='pruned'))
            runtime.denied('/api/v1/index/live', dict(index=identity, action='enable'))
        assert preserved_files(profile) == before
        checks.append('creation/editing/drafts/validation/comparison routes refuse work; all chain, previous-generation and custom admission routes refuse execution without mutating evidence')
        request = dict(consumers=[selection('bitmap-districts-standard-v1')])
        runtime.call('/api/v1/index/run/prepare', request)
        plan = runtime.call('/api/v1/index/run/plan', request)
        consumer = plan['consumers'][0]
        assert consumer['origin_height'] == 792435 and consumer['next_required_height'] == 792435
        assert consumer['range_mode'] == 'complete_prefix' and consumer['state'] == 'complete'
        started = runtime.call('/api/v1/index/run/start', dict(request=request))['run']
        current = runtime.wait_run({'complete'}, started['id'])['run']
        assert current['consumers'][0]['index'] == 'bitmap-districts-standard-v1'
        assert current['counts']['source_reads'] == 0 and current['counts']['inscription_extractions'] == 0
        assert preserved_files(profile) == before
        assert runtime.call('/api/v1/bitmap/view?kind=district')['state'] == 'not_built'
        skipped = dict(request={'in': 792436, 'out': 792436, 'consumers': [selection('bitmap-districts-standard-v1')]})
        blocked = runtime.call('/api/v1/index/run/start', skipped, expected=409)
        assert not blocked['plan']['ready'] and blocked['plan']['consumers'][0]['origin_height'] == 792435
        checks.append('offline genesis-only authority admits a current District track without evaluating any pre-origin block; planning exposes origin 792435 and refuses an arbitrary skipped prefix')
        receipt = dict(result='PASS', binary=str(binary), binary_sha256=sha(binary.read_bytes()),
            checks=checks, calls=calls, limitations=['Offline compiled route/admission checks with a genuine genesis header; no external provider is contacted.',
            'This fixture does not validate live indexing or source-gap retries beyond the genuine genesis-only authority.',
            'Canonical witnesses, blocator-only persistence, parent evaluation receipts and disabled-chain admission are checked by focused Go tests.'])
        (output / 'result.json').write_text(json.dumps(receipt, indent=2) + chr(10), encoding='utf-8')
        print('Compiled Bitmap Standard V1 API PASS: origin 792435, six/seven published rules, disabled chains, both previous canonical generations unchanged and denied custom execution.')
    finally:
        if runtime is not None:
            runtime.stop()
        if profile is not None:
            assert profile.parent.resolve() == output and profile.name.startswith('native-bitmap-standard-') and not profile.is_symlink()
            shutil.rmtree(profile)


if __name__ == '__main__':
    main()
