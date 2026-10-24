#!/usr/bin/env python3
"""Exercise fresh Standard V1 stores through an owned compiled application.

The immutable fixture/archive is read-only; only normal run APIs create records.
This is acceptance evidence, not a matched live-throughput benchmark.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import time


def helpers():
    spec = importlib.util.spec_from_file_location('replay', Path(__file__).with_name('benchmark-index-replay.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


DISTRICT = 'bitmap-districts-standard-v1'
PARCEL = 'bitmap-parcels-standard-v1'
IDS = ('tx-locator', DISTRICT, PARCEL)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--fixture', type=Path, required=True)
    p.add_argument('--archive', type=Path)
    p.add_argument('--peer-source', action='store_true')
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--count', type=int, default=8)
    p.add_argument('--timeout', type=float, default=600)
    p.add_argument('--transaction-reference', type=Path)
    args = p.parse_args()
    h = helpers()
    assert bool(args.archive) != args.peer_source, 'Choose a local archive or peer source'
    binary, fixture, out = args.binary.resolve(strict=True), args.fixture.resolve(strict=True), args.out.resolve()
    _, headers, rows = h.validate_fixture(fixture, args.count)
    assert not out.exists(), 'Use a new owned acceptance directory'
    out.mkdir(parents=True)
    profile = out / 'profile'
    (profile / 'headers').mkdir(parents=True)
    shutil.copyfile(headers, profile / 'headers/headers.bin')
    settings = dict(network_disabled=not args.peer_source, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, share_cache=False, onboarded=True,
                    lan_discovery=False, graph_index=False, cache_blocks=False, privacy_mode=True,
                    ord_enabled=True)
    archive_before = None
    if args.archive:
        settings['archive_dir'] = str(args.archive.resolve(strict=True))
        archive_before = h.inventory(args.archive)
    h.write(profile / 'settings.json', settings)
    runtime = h.Runtime(binary, profile, out)
    result = dict(binary=str(binary), binary_sha256=h.sha(binary), profile=str(profile),
                  source='bitcoin_peers' if args.peer_source else 'verified_external_archive',
                  blocks=len(rows), transactions=sum(row['transactions'] for row in rows))
    try:
        runtime.start()
        status = runtime.call('/api/v1/index/status')
        definitions = {d['id']: d for d in status['definitions']}
        for identity, name in ((DISTRICT, 'Bitmap Districts Standard V1'), (PARCEL, 'Bitmap Parcels Standard V1')):
            assert definitions[identity]['name'] == name and definitions[identity]['buildable']
            assert definitions[identity]['start_height'] == 792435
        request = dict(in_hash=rows[0]['block_hash'], out_hash=rows[-1]['block_hash'],
                       consumers=[dict(index=identity, armed=True, live=False,
                                       mode='full' if identity == 'tx-locator' else 'lean') for identity in IDS])
        request.update({'in': rows[0]['height'], 'out': rows[-1]['height']})
        plan = runtime.call('/api/v1/index/run/plan', request)
        assert plan['ready'], plan
        h.write(out / 'plan.json', plan)
        started = time.perf_counter()
        runtime.call('/api/v1/index/run/start', {'request': request})
        with (out / 'progress.jsonl').open('w') as progress:
            while True:
                document = runtime.call('/api/v1/index/run')
                run = document['run']
                progress.write(json.dumps(dict(elapsed=time.perf_counter()-started, run=run))+'\n')
                progress.flush()
                if run['state'] == 'complete':
                    break
                assert run['state'] not in ('failed', 'error', 'complete_with_errors', 'cancelled', 'rebuild_required'), run
                assert time.perf_counter()-started < args.timeout, run
                time.sleep(.2)
        result.update(elapsed_seconds=time.perf_counter()-started, counts=run['counts'], run=run)
        status = runtime.call('/api/v1/index/status')
        h.write(out / 'status.json', status)
        for identity in IDS:
            instance = next(i for i in status['instances'] if i['definition'] == identity)
            assert instance['coverage'] == [{'from': rows[0]['height'], 'to': rows[-1]['height']}]
        district = runtime.call('/api/v1/bitmap/view?district=0')
        assert district['instance'] == DISTRICT and district['state'] == 'selected', district
        assert district['result']['winner_blocator'] == '953i0.792435', district
        assert len(district['profile']['rules']) == 6
        parcel = runtime.call('/api/v1/bitmap/view?kind=parcel&district=0&transaction=0')
        assert parcel['instance'] == PARCEL and len(parcel['profile']['rules']) == 7
        assert parcel['district_instance'] == DISTRICT
        h.write(out / 'district-zero.json', district)
        h.write(out / 'parcel-zero.json', parcel)
        before_projection = runtime.call('/api/v1/index/run')
        target_path = '/api/v1/bitmap/target-coverage?axis=blocks&from=0&to=7&pixels=8'
        targets = runtime.call(target_path)
        assert targets['instance'] == DISTRICT and targets['state'] == 'ready', targets
        zero = next(bin for bin in targets['bins'] if bin['from'] == 0)
        assert zero['to'] == 0 and zero['winners'] == 1 and zero['unknown'] == 0, targets
        assert all(bin['unknown'] == bin['targets']-bin['winners'] for bin in targets['bins'])
        assert targets['examined_winners'] <= targets['limits']['winners']
        assert targets['read_pages'] <= targets['limits']['pages']
        cached = runtime.call(target_path)
        assert cached['cache_hit'] and cached['read_pages'] == 0 and cached['examined_winners'] == 0
        assert cached['bins'] == targets['bins'] and cached['snapshot'] == targets['snapshot']
        transaction_targets = runtime.call('/api/v1/bitmap/target-coverage?axis=transactions&district=0&from=0&to=0&pixels=1')
        assert transaction_targets['instance'] == PARCEL and transaction_targets['district'] == 0
        assert len(transaction_targets['bins']) == 1 and transaction_targets['bins'][0]['winners'] == 0
        assert transaction_targets['bins'][0]['targets'] == 1 and transaction_targets['bins'][0]['unknown'] == 1
        changed = runtime.call(target_path+'&snapshot='+'0'*64)
        assert changed['state'] == 'changed' and all(bin['winners'] == 0 for bin in changed['bins'])
        assert runtime.call('/api/v1/index/run') == before_projection, 'Painting target coverage changed the run'
        h.write(out / 'target-blocks.json', targets)
        h.write(out / 'target-cached.json', cached)
        h.write(out / 'target-transactions.json', transaction_targets)
        h.write(out / 'target-changed.json', changed)
        result.update(target_zero_not_reveal_height=True, target_cache_hit_bounded=True,
                      parcel_missing_targets_unknown=True, projection_run_unchanged=True)
        if args.peer_source:
            h.write(out / 'network-after.json', runtime.call('/api/v1/network'))
        runtime.stop()
        commits = {}
        for identity in IDS:
            directory = profile / 'indexes' / (identity if identity == 'tx-locator' else 'bitmap-development-v1/'+identity)
            values = [h.read_json(file) for file in (directory / 'commits').rglob('*.json')]
            values.sort(key=lambda value: value.get('height', value.get('checkpoint', {}).get('height', -1)))
            assert len(values) == len(rows)
            previous = ''
            for value, row in zip(values, rows):
                cp = value['checkpoint'] if identity == 'tx-locator' else value
                assert cp['height'] == row['height'] and cp['block_hash'] == row['block_hash']
                assert cp.get('previous_commitment', cp.get('previous', '')) == previous
                previous = cp['commitment']
                if identity == 'tx-locator':
                    block = value['transactions']['block']
                    transactions = block.get("tx's", block.get('tx'))
                    assert block['tx count'] == row['transactions'] and len(transactions) == row['transactions']
                    assert all(list(tx) == [str(n)] for n, tx in enumerate(transactions))
                else:
                    assert cp['index'] == identity and cp['segment_start'] == 792435
                    assert cp['last_block']['block_evaluated'] and cp['last_block']['complete_scope']
                    assert 'winner_blocators' in cp['last_block'] and 'ordered_winner_ids' not in cp['last_block']
            commits[identity] = values
        if args.transaction_reference:
            reference = h.read_json(args.transaction_reference)
            assert commits['tx-locator'] == reference['tx-locator']['commits'], 'Transaction payload changed during design pass'
            for current, frozen in ((DISTRICT, 'bitmap-districts-canonical-v2'), (PARCEL, 'bitmap-parcels-canonical-v2')):
                previous = reference[frozen]['commits']
                assert [v['last_block']['winner_blocators'] for v in commits[current]] == [v['last_block']['winner_blocators'] for v in previous]
                assert all(v['commitment'] != old['commitment'] and v['profile_hash'] != old['profile_hash'] for v, old in zip(commits[current], previous)), 'New Standard generation must retain its distinct authenticated identity'
            result['transaction_payloads_equal_reference'] = True
            result['winner_blocators_equal_frozen_reference'] = True
            result['standard_commitments_distinct_from_frozen'] = True
        assert not h.inventory(profile / 'indexes/blocks') and not h.inventory(profile / 'blocks'), 'Raw Off retained bodies'
        assert not archive_before or h.inventory(args.archive) == archive_before, 'Source archive changed'
        h.write(out / 'commits.json', commits)
        result.update(result='PASS', raw_off=True, archive_unchanged=True,
                      standard_origin=792435, district_zero_reveal='953i0.792435')
        print('Design Standard V1 compiled acceptance PASS: '+result['binary_sha256']+'; '+str(len(rows))+' blocks, all transactions, exact origin/rules/blocators/producer; Raw Off.')
    except BaseException as error:
        result.update(result='FAIL', error=str(error))
        raise
    finally:
        runtime.stop()
        h.write(out / 'result.json', result)
        h.write(out / 'api-calls.json', runtime.calls)


if __name__ == '__main__':
    main()
