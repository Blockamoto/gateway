#!/usr/bin/env python3
"""Verify modern shared fanout with Bitmap ahead of TX in a fresh owned profile."""
import argparse
import importlib.util
import shutil
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('gateway_replay', ROOT / 'scripts/benchmark-index-replay.py')
replay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(replay)
replay.IDS = ('tx-locator', 'bitmap-districts-standard-v1', 'bitmap-parcels-standard-v1')


def run(runtime, rows, ids, output, timeout):
    request = dict(
        **{'in': rows[0]['height'], 'out': rows[-1]['height']},
        in_hash=rows[0]['block_hash'], out_hash=rows[-1]['block_hash'],
        consumers=[dict(index=identity, armed=True, live=False,
                        mode='full' if identity == 'tx-locator' else 'lean') for identity in ids])
    plan = runtime.call('/api/v1/index/run/plan', request)
    replay.write(output.with_suffix('.plan.json'), plan)
    assert plan['ready'], plan
    before = replay.process_metrics(runtime.process)
    started = time.perf_counter()
    document = runtime.call('/api/v1/index/run/start', dict(request=request))
    run_id = document['run']['id']
    with output.with_suffix('.progress.jsonl').open('w', encoding='utf-8') as progress:
        while True:
            document = runtime.call('/api/v1/index/run')
            current = document.get('run')
            assert current and current['id'] == run_id and not document.get('error'), document
            progress.write(replay.canonical(dict(elapsed=time.perf_counter() - started, run=current)).decode() + '\n')
            progress.flush()
            if current['state'] == 'complete':
                break
            assert current['state'] not in ('complete_with_errors', 'failed', 'cancelled', 'rebuild_required'), current
            assert time.perf_counter() - started < timeout, current
            time.sleep(.2)
    after = replay.process_metrics(runtime.process)
    result = dict(elapsed_seconds=time.perf_counter() - started, run=current,
                  os_io_delta={key: after['io'][key] - before['io'][key]
                               for key in after.get('io', {}) if key in before.get('io', {})})
    replay.write(output.with_suffix('.json'), result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--compare-semantics', type=Path)
    parser.add_argument('--count', type=int, default=32)
    parser.add_argument('--lead', type=int, default=16)
    parser.add_argument('--timeout', type=int, default=600)
    args = parser.parse_args()
    fixture, binary, output = args.fixture.resolve(strict=True), args.binary.resolve(strict=True), args.out.resolve()
    manifest, headers, rows = replay.validate_fixture(fixture, args.count)
    assert 0 < args.lead < len(rows)
    assert not output.exists(), 'Use a new output directory; existing profiles are never reused'
    output.mkdir(parents=True)
    archive, profile = output / 'archive', output / 'profile'
    (profile / 'headers').mkdir(parents=True)
    shutil.copyfile(headers, profile / 'headers/headers.bin')
    for row in rows:
        target = archive / 'blocks' / row['block_hash'][:2] / row['block_hash']
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(fixture / row['filename'], target.with_suffix('.blk'))
        replay.write(target.with_suffix('.json'), dict(schema=1, verifier_version=2, hash=row['block_hash'],
                     height=row['height'], sha256=row['sha256'], bytes=row['bytes']))
    archive_before = replay.inventory(archive)
    replay.write(profile / 'settings.json', dict(network_disabled=True, headers_paused=True, core_disabled=True,
                 core_mount_disabled=True, serve_data=False, share_cache=False, onboarded=True,
                 lan_discovery=False, graph_index=False, cache_blocks=False, privacy_mode=True, archive_dir=str(archive)))
    runtime = replay.Runtime(binary, profile, output)
    report = dict(binary_sha256=replay.sha(binary), fixture_manifest_sha256=replay.sha(fixture / 'manifest.json'),
                  blocks=len(rows), transactions=sum(row['transactions'] for row in rows), bitmap_lead=args.lead,
                  source='verified external archive; network/Core/Raw disabled',
                  note='One fresh-profile diagnostic; OS I/O operations are not physical fsync counts.')
    try:
        runtime.start()
        report['seed'] = run(runtime, rows[:args.lead], replay.IDS[1:], output / 'seed', args.timeout)
        report['skewed'] = run(runtime, rows, replay.IDS, output / 'skewed', args.timeout)
        counts = report['skewed']['run']['counts']
        for key in ('source_reads', 'decoded_blocks', 'inscription_extractions'):
            assert counts[key] == len(rows), (key, counts)
        status_started = time.perf_counter()
        status = runtime.call('/api/v1/index/status')
        report['status_seconds'] = time.perf_counter() - status_started
        replay.write(output / 'status.json', status)
        for identity in replay.IDS:
            instance = next(item for item in status['instances'] if item['definition'] == identity)
            assert instance['coverage'] == [{'from': rows[0]['height'], 'to': rows[-1]['height']}], instance
        runtime.stop()
        semantics = replay.store_semantics(profile, rows)
        replay.write(output / 'semantics.json', semantics)
        if args.compare_semantics:
            assert semantics == replay.read_json(args.compare_semantics), 'Native durable payloads or commitments changed'
            report['native_semantics_equal'] = True
        assert replay.inventory(archive) == archive_before
        report.update(result='PASS', archive_unchanged=True)
        replay.write(output / 'result.json', report)
        print('Modern skewed fanout PASS: %d bodies/%d transactions, one source/decode/extraction per body; native coverage and commitments verified.' % (len(rows), report['transactions']), flush=True)
    except BaseException as error:
        report.update(result='FAIL', error=str(error))
        replay.write(output / 'result.json', report)
        raise
    finally:
        runtime.stop()
        replay.write(output / 'api-calls.json', runtime.calls)


if __name__ == '__main__':
    main()
