#!/usr/bin/env python3
"""Check one compiled client's source/retention policy in an owned profile.

Requires the externally authenticated contiguous real-block fixture used by
the paired Raw benchmark. It never opens or changes a user's preview profile.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    binary, fixture, output = args.binary.resolve(), args.fixture.resolve(), args.out.resolve()
    output.mkdir(parents=True, exist_ok=False)
    manifest = json.loads((fixture / 'manifest.json').read_text())
    assert digest(fixture / 'headers.bin') == manifest['header_sha256']
    assert len(manifest['blocks']) >= 4
    profile = Path(tempfile.mkdtemp(prefix='owned-source-policy-', dir=output))
    archive = output / 'external-archive'
    for row in manifest['blocks']:
        raw = fixture / row['filename']
        assert raw.stat().st_size == row['bytes'] and digest(raw) == row['sha256']
        target = archive / 'blocks' / row['block_hash'][:2] / row['block_hash']
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(raw, target.with_suffix('.blk'))
        target.with_suffix('.json').write_text(json.dumps(dict(schema=1, verifier_version=2,
            hash=row['block_hash'], height=row['height'], sha256=row['sha256'], bytes=row['bytes'])))
    archive_before = {str(p.relative_to(archive)): digest(p) for p in archive.rglob('*') if p.is_file()}
    (profile / 'headers').mkdir()
    shutil.copyfile(fixture / 'headers.bin', profile / 'headers/headers.bin')
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, share_cache=False,
                    onboarded=True, lan_discovery=False, graph_index=True,
                    cache_blocks=True, privacy_mode=True, archive_dir=str(archive))
    (profile / 'settings.json').write_text(json.dumps(settings))
    process, endpoint, runtime = None, None, None
    checks = []
    log = (output / 'runtime.log').open('wb')

    def call(path, body=None, expected=200, authenticated=False):
        headers = {'Content-Type': 'application/json', 'Origin': endpoint}
        if authenticated:
            headers['X-Gateway-Token'] = runtime['token']
        request = urllib.request.Request(endpoint + path,
            data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            response = urllib.request.urlopen(request, timeout=90)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw, status = response.read(), response.status
        assert status == expected, (path, status, raw[:400])
        return json.loads(raw)

    def start():
        nonlocal process, endpoint, runtime
        process = subprocess.Popen([str(binary), '-data', str(profile), '-background',
            '-no-tray', '-http', '127.0.0.1:0'], cwd=binary.parent, stdout=log, stderr=log,
            creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        endpoint = None
        for _ in range(600):
            assert process.poll() is None, 'Owned test runtime exited before readiness'
            try:
                runtime = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                if runtime['pid'] != process.pid:
                    raise ValueError('Waiting for new process')
                candidate = runtime['url']
                with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                    endpoint = candidate
                return
            except (OSError, ValueError, urllib.error.URLError):
                time.sleep(0.1)
        raise AssertionError('Owned test runtime did not become ready')

    def stop():
        call('/api/v1/runtime/quit', {}, authenticated=True)
        process.wait(timeout=20)
        assert process.returncode == 0

    def prepare(*ids):
        call('/api/v1/index/run/prepare', {'consumers': [dict(index=id, armed=True, live=False) for id in ids]})

    def opened(row):
        result = call('/api/v1/index/block-map?block=' + str(row['height']))
        # The compiled parser checks the fixture's selected header and body;
        # metadata is sufficient here, not a trusted test-created receipt alone.
        assert result['hash'] == row['block_hash'], result.keys()
        assert result['verification']['header_chain_match'] and result['transaction_count'] > 0
        return result

    def segment_files():
        root = profile / 'indexes/blocks/segments-v3'
        return {str(p.relative_to(root)): digest(p) for p in root.rglob('*') if p.is_file()}

    def coverage(expected, row=None):
        route = '/api/v1/index/raw-coverage'
        if row is not None:
            route += '?from=' + str(row['height']) + '&to=' + str(row['height'])
        for _ in range(600):
            value = call(route)
            if value.get('complete'):
                assert value['scope'] == 'gateway_retained_raw' and value['state'] == 'complete'
                assert value['total_blocks'] == value['observed_blocks'] == expected, value
                if row is not None:
                    assert value['ranges'] == [{'from': row['height'], 'to': row['height']}]
                    assert value['visible']['complete'] and value['visible']['total_blocks'] == expected
                return value
            time.sleep(0.1)
        raise AssertionError('Retained Raw coverage did not complete metadata recovery')

    def assert_no_prefix():
        for id in ('blocks', 'tx-locator', 'inscriptions'):
            assert not (profile / 'indexes' / id / 'head.json').exists(), id
        status = call('/api/v1/index/status')
        assert not status['jobs'] and status['run'].get('run') is None

    def assert_no_legacy_knowledge():
        path = profile / 'cache-index.json'
        if path.exists():
            saved = json.loads(path.read_text())
            assert all(not saved.get(key) for key in ('tx', 'spends', 'private_tx', 'private_spends'))

    try:
        start()
        first, second, third, fourth = manifest['blocks'][:4]
        opened(first)
        coverage(0)
        assert not (profile / 'indexes/blocks').exists(), 'Empty coverage inspection created an optional Raw store'
        assert not segment_files()
        assert not (profile / 'indexes/browse').exists()
        assert_no_prefix()
        assert_no_legacy_knowledge()
        checks.append('Unconfigured timeline ignores legacy cache/graph preferences: viewed real block creates no optional body or singleton index')
        prepare('blocks')
        opened(first)
        coverage(1, first)
        files = segment_files()
        assert any(name.startswith('blk') for name in files) and any(name.startswith('catalog') for name in files)
        assert not (profile / 'indexes/tx-locator').exists() and not (profile / 'indexes/browse').exists()
        assert_no_prefix()
        # Independently inspect the sparse membership and verify exact stored bytes.
        catalog = profile / 'indexes/blocks/segments-v3' / ('catalog%08d.dat' % (first['height'] // 4096))
        with catalog.open('rb') as stream:
            stream.seek((first['height'] % 4096) * 128)
            row = stream.read(128)
        assert row[:4] == b'GWC3'
        number, offset, length = struct.unpack_from('<IQI', row, 76)
        body = profile / 'indexes/blocks/segments-v3' / ('blk%08d.dat' % number)
        with body.open('rb') as stream:
            stream.seek(offset + 52)
            retained = stream.read(length)
        assert len(retained) == first['bytes'] and hashlib.sha256(retained).hexdigest() == first['sha256']
        checks.append('Raw On on-demand stores exact Bitcoin bytes plus sparse body membership without sequential progress or Locator')
        stop()
        assert_no_legacy_knowledge()
        saved = json.loads((profile / 'settings.json').read_text())
        saved['archive_dir'] = ''
        (profile / 'settings.json').write_text(json.dumps(saved))
        start()
        opened(first)
        coverage(1, first)
        assert segment_files() == files
        assert_no_prefix()
        checks.append('Real process restart reuses exact retained body with peers, Core and external archive disabled; repeat read adds no files')
        prepare()
        settings = call('/api/v1/settings')['settings']
        settings['archive_dir'] = str(archive)
        call('/api/v1/settings', settings)
        opened(second)
        coverage(1, first)
        assert segment_files() == files
        assert_no_prefix()
        checks.append('Raw Off permits temporary real block reads and preserves existing bodies without retaining the next block')
        prepare('tx-locator', 'inscriptions')
        result = opened(third)
        assert {item['index'] for item in result['indexing']} == {'tx-locator', 'inscriptions'}
        assert all(item['state'] == 'indexed' for item in result['indexing']), result['indexing']
        for id in ('tx-locator', 'inscriptions'):
            head = json.loads((profile / 'indexes/browse' / id / 'head.json').read_text())
            assert set(head['blocks']) == {str(third['height'])}
        assert segment_files() == files
        assert_no_prefix()
        checks.append('Locator and Inscriptions Arm create only explicitly viewed sparse facts; no Raw retention or prefix build')
        browse_before = {str(p.relative_to(profile)): digest(p) for p in (profile / 'indexes/browse').rglob('*') if p.is_file()}
        prepare()
        opened(fourth)
        assert browse_before == {str(p.relative_to(profile)): digest(p) for p in (profile / 'indexes/browse').rglob('*') if p.is_file()}
        assert segment_files() == files
        assert_no_prefix()
        checks.append('Disarming both optional tracks prevents new singleton writes while preserving saved facts')
        stop()
        assert_no_legacy_knowledge()
        assert archive_before == {str(p.relative_to(archive)): digest(p) for p in archive.rglob('*') if p.is_file()}
        receipt = dict(result='PASS', binary_sha256=digest(binary), fixture_manifest_sha256=digest(fixture / 'manifest.json'),
            checks=checks, process_restart_performed=True, retained_body_exact=True,
            external_archive_unchanged=True, retained_coverage_exact=True, empty_coverage_inspection_no_store=True, profile=str(profile),
            limitations=['Deterministic local real-block sources; peer scheduler behavior is tested separately.'])
        (output / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print('Compiled unified block policy PASS; exact retained bytes and real process restart verified.')
    except Exception as error:
        (output / 'failure.json').write_text(json.dumps(dict(error=str(error), checks=checks), indent=2) + '\n')
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


if __name__ == '__main__':
    main()
