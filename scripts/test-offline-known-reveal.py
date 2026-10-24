#!/usr/bin/env python3
"""Validate retained-Raw known-reveal content in a fresh compiled QA runtime.

Uses genuine pinned Bitcoin bytes and an existing read-only archive receipt.
Only normal Raw run APIs create index data. No Inscriptions inventory, locator
index, Ord adapter, Core, network provider, or forged index state is supplied.
"""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import time
import urllib.request


def load_replay_helpers():
    source = Path(__file__).with_name('benchmark-index-replay.py')
    spec = importlib.util.spec_from_file_location('gateway_replay_helpers', source)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--node', default='node')
    args = parser.parse_args()
    helper = load_replay_helpers()
    binary, fixture, archive = (value.resolve(strict=True) for value in (args.binary, args.fixture, args.archive))
    output = args.out.resolve()
    assert not output.exists(), 'Use a new owned output directory; existing profiles are never modified'
    manifest, headers, rows = helper.validate_fixture(fixture, 1)
    row = rows[0]
    assert row['height'] == 792435
    known = '953i0.792435'
    expected_body = b'0.bitmap'
    raw_source = archive / 'blocks' / row['block_hash'][:2] / (row['block_hash'] + '.blk')
    receipt_source = raw_source.with_suffix('.json')
    assert helper.sha(raw_source) == row['sha256']
    receipt = helper.read_json(receipt_source)
    assert receipt == dict(schema=1, verifier_version=2, hash=row['block_hash'], height=row['height'], sha256=row['sha256'], bytes=row['bytes'])
    source_digests = {str(path): helper.sha(path) for path in (raw_source, receipt_source)}
    output.mkdir(parents=True)
    profile = output / 'profile'
    profile.mkdir()
    (profile / 'headers').mkdir()
    shutil.copyfile(headers, profile / 'headers/headers.bin')
    owned_archive = output / 'source-archive'
    owned_raw = owned_archive / 'blocks' / row['block_hash'][:2] / raw_source.name
    owned_raw.parent.mkdir(parents=True)
    shutil.copyfile(raw_source, owned_raw)
    shutil.copyfile(receipt_source, owned_raw.with_suffix('.json'))
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True, core_mount_disabled=True,
                    serve_data=False, serve_gateway_data=False, share_cache=False, onboarded=True,
                    lan_discovery=False, graph_index=False, cache_blocks=False, privacy_mode=True,
                    ord_enabled=True, archive_dir=str(owned_archive))
    helper.write(profile / 'settings.json', settings)
    result = dict(binary=str(binary), binary_sha256=helper.sha(binary), fixture_manifest_sha256=helper.sha(fixture / 'manifest.json'),
                  header_sha256=manifest['header_sha256'], block=row, known_reveal=known,
                  profile=str(profile), source_archive=str(archive), mocked_routes=False)
    setup_dir, offline_dir = output / 'setup', output / 'offline'
    setup_dir.mkdir()
    offline_dir.mkdir()
    runtime = helper.Runtime(binary, profile, setup_dir)
    try:
        runtime.start()
        request = {'in': row['height'], 'out': row['height'], 'in_hash': row['block_hash'], 'out_hash': row['block_hash'],
                   'consumers': [dict(index='blocks', armed=True, live=False, mode='full')]}
        plan = runtime.call('/api/v1/index/run/plan', request)
        helper.write(setup_dir / 'plan.json', plan)
        assert plan['ready'], plan
        document = runtime.call('/api/v1/index/run/start', {'request': request})
        run_id = document['run']['id']
        deadline = time.monotonic() + 120
        while True:
            document = runtime.call('/api/v1/index/run')
            run = document.get('run')
            assert run and run['id'] == run_id and not document.get('error'), document
            if run['state'] == 'complete':
                break
            assert run['state'] not in ('failed', 'cancelled', 'complete_with_errors', 'rebuild_required'), run
            assert time.monotonic() < deadline, run
            time.sleep(.1)
        assert {p['index'] for p in run['consumers']} == {'blocks'}
        assert run['consumers'][0]['height'] == row['height']
        assert run['counts'].get('inscription_extractions', 0) == 0, 'Raw-only setup unexpectedly built inscription extraction'
        helper.write(setup_dir / 'completed-run.json', document)
        runtime.call('/api/v1/index/run/prepare', {'consumers': []})
    finally:
        runtime.stop()
        helper.write(setup_dir / 'api-calls.json', runtime.calls)
    assert not (profile / 'indexes/inscriptions').exists(), 'Raw setup created an Inscriptions inventory'
    assert not (profile / 'indexes/tx-locator').exists(), 'Raw setup created a Transaction Locator index'
    assert not list((profile / 'ord').glob('*')), 'Known content was cached before the offline check'
    # Detach only this test's owned copy, and remove its provider configuration
    # while the process is stopped. The shared archive remains read-only.
    detached = output / 'source-archive-detached'
    assert owned_archive.resolve().is_relative_to(output) and detached.resolve().is_relative_to(output)
    owned_archive.rename(detached)
    settings['archive_dir'] = ''
    helper.write(profile / 'settings.json', settings)
    runtime = helper.Runtime(binary, profile, offline_dir)
    try:
        runtime.start()
        public_settings = runtime.call('/api/v1/settings')['settings']
        assert public_settings['ord_enabled'] and public_settings['network_disabled'] and public_settings['core_disabled'] and public_settings['core_mount_disabled']
        assert not public_settings.get('archive_dir') and not public_settings.get('ord_url')
        status = runtime.call('/api/v1/index/status')
        helper.write(offline_dir / 'status-initial.json', status)
        coverage_deadline = time.monotonic() + 30
        while True:
            raw_track = next(track for track in status['timeline']['tracks'] if track['id'] == 'blocks')
            retained = raw_track.get('retained_coverage', {})
            if retained.get('complete') and any(span['from'] <= row['height'] <= span['to'] for span in retained.get('ranges', [])):
                break
            assert time.monotonic() < coverage_deadline, ('Retained Raw coverage did not become ready', retained)
            time.sleep(.1)
            status = runtime.call('/api/v1/index/status')
        helper.write(offline_dir / 'status-before-resolve.json', status)
        assert not any(i['definition'] == 'inscriptions' and i.get('coverage') for i in status['instances'])
        assert not any(p['id'] == 'blocks' and p.get('on_demand') for p in status['providers']), 'A network provider is still available'
        assert not any(p['id'] == 'blocks' and p['provider'] in ('bitcoin_core', 'core_mount') and p.get('queryable') for p in status['providers'])
        raw_head = helper.sha(profile / 'indexes/blocks/head.json')
        saved_run = helper.sha(profile / 'indexes/run.json')
        asset_request = urllib.request.Request(runtime.endpoint + '/shell/explorer-features.js')
        with urllib.request.urlopen(asset_request, timeout=30) as response:
            asset = response.read()
        asset_path = offline_dir / 'compiled-explorer-features.js'
        asset_path.write_bytes(asset)
        script = "const fs=require('fs'),vm=require('vm');const m={exports:{}};vm.runInNewContext(fs.readFileSync(process.argv[1],'utf8'),{module:m});process.stdout.write(JSON.stringify(m.exports.project(JSON.parse(fs.readFileSync(process.argv[2],'utf8')))));"
        projection = json.loads(subprocess.check_output([args.node, '-e', script, str(asset_path), str(offline_dir / 'status-before-resolve.json')], text=True))
        helper.write(offline_dir / 'home-feature-projection.json', projection)
        content = next(value for value in projection if value['id'] == 'inscription-content')
        inventory = next(value for value in projection if value['id'] == 'inscription-locations')
        assert content['state'] == 'scoped' and inventory['state'] == 'needs-index', (content, inventory)
        assert not list((profile / 'ord').glob('*')), 'Status/model check resolved content implicitly'
        record = runtime.call('/api/v1/ord/resolve', {'id': known})
        helper.write(offline_dir / 'resolved-reveal.json', record)
        assert record['reveal_coordinate'] == known and record['block_hash'] == row['block_hash'] and record['height'] == row['height'] and record['tx_index'] == 953
        assert record['provider'] == 'bitcoin_on_demand_witness' and record['resolution_state'] == 'resolved' and record['interpretation'] == 'envelope_extracted'
        assert record['evidence'] in ('header_anchored', 'consensus_validated')
        assert record['content_sha256'] == hashlib.sha256(expected_body).hexdigest() and record['content_size'] == len(expected_body)
        assert record.get('canonical_number') is None and record.get('sat_number') is None
        raw_url = record['raw_url']
        request = urllib.request.Request(raw_url, headers={'Origin': runtime.endpoint})
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read()
        assert body == expected_body
        (offline_dir / 'reveal-body.bin').write_bytes(body)
        navigation = runtime.call('/api/v1/navigate', {'address': '792435.bitcoin'})
        block = navigation['resource']['block']
        assert block['source_network'] == 'index_retained', block.get('evidence')
        checks = block['verification']
        for key in ('header_hash', 'proof_of_work', 'merkle_root', 'transactions_parsed', 'witness_commitment'):
            assert checks[key], (key, checks)
        helper.write(offline_dir / 'retained-block-evidence.json', {key: block.get(key) for key in ('height', 'hash', 'source_network', 'source_peer', 'verification', 'verification_state', 'evidence')})
        assert not (profile / 'indexes/inscriptions').exists() and not (profile / 'indexes/tx-locator').exists()
        assert helper.sha(profile / 'indexes/blocks/head.json') == raw_head, 'Known reveal lookup rewrote Raw head'
        assert helper.sha(profile / 'indexes/run.json') == saved_run, 'Known reveal lookup started or changed a run'
        result.update(result='PASS', home_content=content, home_inventory=inventory, content_body_hex=body.hex(),
                      content_sha256=record['content_sha256'], content_provider=record['provider'], byte_source=block['source_network'],
                      no_inscriptions_inventory=True, no_transaction_locator_index=True, no_preexisting_ord_cache=True,
                      archive_detached_before_restart=True, network_and_core_disabled=True,
                      raw_head_unchanged=True, run_unchanged=True, model_asset_sha256=hashlib.sha256(asset).hexdigest())
    except BaseException as error:
        result.update(result='FAIL', error=str(error))
        raise
    finally:
        runtime.stop()
        helper.write(offline_dir / 'api-calls.json', runtime.calls)
        result['source_archive_unchanged'] = all(helper.sha(Path(path)) == digest for path, digest in source_digests.items())
        helper.write(output / 'result.json', result)
    assert result['source_archive_unchanged']
    print('Offline compiled known reveal PASS: Home content scoped; no Inscriptions inventory; '+known+' -> exact 0.bitmap from retained Raw; archive detached, Core/network off; no hidden index/run changes.')


if __name__ == '__main__':
    main()
