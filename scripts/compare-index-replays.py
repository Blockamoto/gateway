#!/usr/bin/env python3
"""Compare completed replay artifacts without reopening either profile."""
import argparse
import json
from pathlib import Path
import statistics


def load(directory):
    value = json.loads((directory / 'result.json').read_text(encoding='utf-8-sig'))
    assert value.get('result') == 'PASS', ('Replay did not pass', directory)
    samples = [sample for repetition in value['repetitions'] for sample in repetition['samples']]
    assert samples and all(sample['result'] == 'PASS' for sample in samples)
    for sample in samples:
        # This approved store-layout marker is outside every block commitment.
        # Keep full raw heads in artifacts; no other head or payload field is ignored.
        head = sample['semantics']['tx-locator']['head']
        layout = head.pop('locator_layout', None)
        assert layout in (None, 'packed-buckets-v1'), 'Unknown physical locator layout'
        sample.setdefault('storage_layouts', {}).setdefault('tx-locator', layout or 'legacy-shards-v1')
    return value, samples


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    baseline, before = load(args.baseline)
    candidate, after = load(args.candidate)
    for key in ('fixture_manifest_sha256', 'header_sha256', 'selected_blocks', 'selected_indexes'):
        assert baseline[key] == candidate[key], ('Different workload', key)
    reference = before[0]['semantics']
    for sample in before + after:
        assert sample['semantics'] == reference, ('Durable per-block payload or head mismatch', sample['label'])
        assert not sample['raw_retained_files']
    logical = ('source_reads', 'decoded_blocks', 'inscription_extractions', 'outcome_reads', 'chain_steps')
    work_counts_equal = {key: len({sample['counts'].get(key, 0) for sample in before + after}) == 1 for key in logical}
    medians = {name: statistics.median(sample['elapsed_seconds'] for sample in samples) for name, samples in [('baseline', before), ('candidate', after)]}
    result = dict(result='PASS', baseline_artifact=str(args.baseline.resolve()), candidate_artifact=str(args.candidate.resolve()), fixture_manifest_sha256=baseline['fixture_manifest_sha256'], selected_blocks=baseline['selected_blocks'], selected_indexes=baseline['selected_indexes'], semantics_equal=True, durable_counts_equal=True, coordinator_work_counts_equal=work_counts_equal, coordinator_work_delta={key: after[0]['counts'].get(key, 0)-before[0]['counts'].get(key, 0) for key in logical}, raw_off_verified=True, median_elapsed_seconds=medians, median_elapsed_speedup=medians['baseline'] / medians['candidate'], baseline_samples=before, candidate_samples=after, limitations=['Local replay; no peer-download throughput claim.', 'Fresh application profiles, uncontrolled shared OS cache.', 'Small sample counts do not establish a statistical performance guarantee.', 'OS write counters include all process I/O; actual locator syncs require the measurement observer or run instrumentation.'])
    args.out.mkdir(parents=True, exist_ok=False)
    (args.out / 'result.json').write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print('Replay comparison PASS: identical durable payloads, outcomes, coverage and output counts; coordinator work deltas reported; elapsed median %.3fs -> %.3fs (%.2fx).' % (medians['baseline'], medians['candidate'], result['median_elapsed_speedup']))


if __name__ == '__main__':
    main()
