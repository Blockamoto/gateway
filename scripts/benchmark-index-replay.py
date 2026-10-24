#!/usr/bin/env python3
"""Replay identical pinned Bitcoin blocks in fresh, owned compiled profiles.

No existing profile or running preview is opened. Network, Core, serving and Raw
retention are off; the application verifies local archive bytes against headers.
"""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import statistics
import struct
import subprocess
import threading
import time
import urllib.error
import urllib.request

IDS = ('tx-locator', 'bitmap-districts-canonical-v2', 'bitmap-parcels-canonical-v2')


def io_path(path):
    path = Path(path)
    if os.name == 'nt' and not str(path).startswith('\\\\?\\'):
        return Path('\\\\?\\' + str(path.resolve()))
    return path


def sha(path):
    with io_path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def write(path, value):
    io_path(path).write_text(json.dumps(value, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


def read_json(path):
    return json.loads(io_path(path).read_text(encoding='utf-8-sig'))


def interval_count(rows):
    return sum(r['to'] - r['from'] + 1 for r in rows)


def validate_fixture(fixture, count):
    manifest = read_json(fixture / 'manifest.json')
    headers = fixture / manifest.get('header_file', 'headers.bin')
    assert headers.resolve().is_relative_to(fixture), 'Header path escaped fixture'
    assert sha(headers) == manifest['header_sha256'], 'Header digest mismatch'
    rows = manifest['blocks'][:count or None]
    assert rows and rows[0]['height'] == 792435, 'Canonical replay must begin at 792435'
    with headers.open('rb') as header_stream:
        for offset, row in enumerate(rows):
            assert row['height'] == 792435 + offset, 'Fixture must be consecutive'
            source = (fixture / row['filename']).resolve()
            assert source.is_relative_to(fixture) and source.is_file(), 'Invalid fixture path'
            assert source.stat().st_size == row['bytes'] and sha(source) == row['sha256']
            with source.open('rb') as stream:
                prefix = stream.read(89)
            header = prefix[:80]
            assert hashlib.sha256(hashlib.sha256(header).digest()).digest()[::-1].hex() == row['block_hash']
            header_stream.seek(row['height'] * 80)
            assert header_stream.read(80) == header, 'Fixture body differs from selected header'
            marker = prefix[80]
            transactions = marker if marker < 253 else struct.unpack_from({253: '<H', 254: '<I', 255: '<Q'}[marker], prefix, 81)[0]
            assert row.get('transactions', transactions) == transactions
            row['transactions'] = transactions
    return manifest, headers, rows


def process_metrics(process):
    """OS process I/O is not a locator fsync count. Unsupported values stay absent."""
    if os.name == 'nt':
        from ctypes import wintypes

        class Memory(ctypes.Structure):
            _fields_ = [('cb', wintypes.DWORD), ('PageFaultCount', wintypes.DWORD)] + [(n, ctypes.c_size_t) for n in ('PeakWorkingSetSize', 'WorkingSetSize', 'QuotaPeakPagedPoolUsage', 'QuotaPagedPoolUsage', 'QuotaPeakNonPagedPoolUsage', 'QuotaNonPagedPoolUsage', 'PagefileUsage', 'PeakPagefileUsage')]

        class IO(ctypes.Structure):
            _fields_ = [(n, ctypes.c_ulonglong) for n in ('ReadOperationCount', 'WriteOperationCount', 'OtherOperationCount', 'ReadTransferCount', 'WriteTransferCount', 'OtherTransferCount')]

        handle = wintypes.HANDLE(int(process._handle))
        memory, io = Memory(), IO()
        memory.cb = ctypes.sizeof(memory)
        result = {}
        if ctypes.windll.psapi.GetProcessMemoryInfo(handle, ctypes.byref(memory), memory.cb):
            result.update(rss_bytes=memory.WorkingSetSize, process_peak_rss_bytes=memory.PeakWorkingSetSize)
        if ctypes.windll.kernel32.GetProcessIoCounters(handle, ctypes.byref(io)):
            result['io'] = {n: getattr(io, n) for n, _ in IO._fields_}
        return result
    try:
        fields = dict(line.split(':', 1) for line in Path('/proc/%d/status' % process.pid).read_text().splitlines() if ':' in line)
        return {key: int(fields[name].split()[0]) * 1024 for key, name in [('rss_bytes', 'VmRSS'), ('process_peak_rss_bytes', 'VmHWM')] if name in fields}
    except (OSError, ValueError):
        return {}


class Runtime:
    def __init__(self, binary, profile, output):
        self.binary, self.profile, self.output = binary, profile, output
        self.process = self.log = None
        self.endpoint = self.runtime = None
        self.calls = []
        self.samples = []
        self.stop_sampling = threading.Event()

    def start(self):
        started = time.perf_counter()
        self.log = (self.output / 'runtime.log').open('wb')
        self.process = subprocess.Popen([str(self.binary), '-data', str(self.profile), '-background', '-no-tray', '-http', '127.0.0.1:0'], cwd=self.binary.parent, stdout=self.log, stderr=self.log, creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            assert self.process.poll() is None, 'Owned runtime exited before readiness'
            try:
                self.runtime = read_json(self.profile / 'runtime.json')
                assert self.runtime['pid'] == self.process.pid
                self.endpoint = self.runtime['url'].rstrip('/')
                assert self.endpoint.startswith('http://127.0.0.1:')
                self.call('/api/v1/runtime/ping', timeout=1)
                self.startup_seconds = time.perf_counter() - started
                return
            except (OSError, ValueError, AssertionError, urllib.error.URLError):
                time.sleep(.1)
        raise AssertionError('Owned runtime did not become ready')

    def call(self, path, body=None, timeout=90, token=False):
        headers = {'Content-Type': 'application/json', 'Origin': self.endpoint}
        if token:
            headers['X-Gateway-Token'] = self.runtime['token']
        request = urllib.request.Request(self.endpoint + path, data=None if body is None else canonical(body), headers=headers)
        started = time.perf_counter()
        try:
            response = urllib.request.urlopen(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw, status = response.read(), response.status
        elapsed = time.perf_counter() - started
        self.calls.append(dict(path=path, method='GET' if body is None else 'POST', status=status, seconds=elapsed, bytes=len(raw)))
        assert status == 200, (path, status, raw[:600])
        return json.loads(raw)

    def sample_memory(self):
        while not self.stop_sampling.wait(.05):
            if self.process.poll() is not None:
                break
            self.samples.append(process_metrics(self.process))

    def stop(self):
        self.stop_sampling.set()
        if self.process is not None and self.process.poll() is None:
            try:
                self.call('/api/v1/runtime/quit', {}, timeout=10, token=True)
                self.process.wait(timeout=20)
            except Exception:
                self.process.terminate()
                try:
                    self.process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait(timeout=10)
        if self.log is not None:
            self.log.close()


def inventory(root):
    root = io_path(root)
    return {str(file.relative_to(root)).replace('\\', '/'): dict(bytes=file.stat().st_size, sha256=sha(file)) for file in sorted(root.rglob('*')) if file.is_file()}


def observer_snapshot(runtime):
    try:
        return runtime.call('/api/v1/index/bench-observer')
    except AssertionError:
        if runtime.calls[-1]['status'] == 404:
            return None
        raise


def store_semantics(profile, rows):
    expected = {r['height']: r for r in rows}
    result = {}
    for identity in IDS:
        directory = profile / 'indexes' / ('tx-locator' if identity == 'tx-locator' else 'bitmap-development-v1/' + identity)
        assert (directory / 'head.json').is_file(), ('Missing durable head', identity)
        commits = [read_json(file) for file in (directory / 'commits').rglob('*.json')]
        commits.sort(key=lambda value: value.get('height', value.get('checkpoint', {}).get('height', -1)))
        assert len(commits) == len(rows), (identity, 'Commit count', len(commits), len(rows))
        previous = ''
        for value in commits:
            checkpoint = value['checkpoint'] if identity == 'tx-locator' else value
            height = checkpoint['height']
            assert height in expected and checkpoint['block_hash'] == expected[height]['block_hash']
            assert checkpoint.get('previous_commitment', checkpoint.get('previous', '')) == previous
            previous = checkpoint['commitment']
            if identity == 'tx-locator':
                block = value['transactions']['block']
                assert block['tx count'] == expected[height]['transactions']
                transactions = block.get("tx's", block.get('tx'))
                assert isinstance(transactions, list) and len(transactions) == expected[height]['transactions'], 'Full Transaction Index was not retained'
                assert all(list(entry) == [str(n)] for n, entry in enumerate(transactions)), 'Transaction positions changed'
            else:
                assert checkpoint['profile']['canonical_standard'] and checkpoint['segment_start'] == 792435
                assert checkpoint['last_block']['block_evaluated'] and checkpoint['last_block']['complete_scope']
        head = read_json(directory / 'head.json')
        assert head['commitment'] == previous
        # Complete commit payloads bind every block, transaction, outcome and B-tree root.
        result[identity] = dict(head=head, commits=commits, commit_payload_sha256=hashlib.sha256(canonical(commits)).hexdigest())
    return result


def finalize_saved_run(result, owned, rows):
    """Read durable evidence after process exit; safe to repeat without replay."""
    profile = Path(result['profile'])
    semantic = store_semantics(profile, rows)
    write(owned / 'semantics.json', semantic)
    result['storage_layouts'] = {}
    for key, value in semantic.items():
        if key == 'tx-locator':
            layout = value['head'].get('locator_layout')
            assert layout in (None, 'packed-buckets-v1'), 'Unknown Transaction Index physical layout'
            result['storage_layouts'][key] = layout or 'legacy-shards-v1'
    result['semantics'] = {key: dict(commit_payload_sha256=value['commit_payload_sha256'], head={k: v for k, v in value['head'].items() if not (key == 'tx-locator' and k == 'locator_layout')}, commit_count=len(value['commits'])) for key, value in semantic.items()}
    result['files'] = {identity: inventory(profile / 'indexes' / ('tx-locator' if identity == 'tx-locator' else 'bitmap-development-v1/' + identity)) for identity in IDS}
    result['raw_retained_files'] = inventory(profile / 'indexes/blocks')
    assert not result['raw_retained_files'], 'Raw Off unexpectedly retained source bodies or progress'
    result['result'] = 'PASS'
    result.pop('error', None)
    write(owned / 'result.json', result)
    return result


def run_one(binary, label, repetition, output, archive, headers, rows, timeout, peer_source=False):
    owned = output / ('%02d-%s' % (repetition, label))
    owned.mkdir()
    profile = owned / 'profile'
    profile.mkdir()
    (profile / 'headers').mkdir()
    shutil.copyfile(headers, profile / 'headers/headers.bin')
    settings = dict(network_disabled=not peer_source, headers_paused=True, core_disabled=True, core_mount_disabled=True, serve_data=False, share_cache=False, onboarded=True, lan_discovery=False, graph_index=False, cache_blocks=False, privacy_mode=True)
    if not peer_source:
        settings['archive_dir'] = str(archive)
    write(profile / 'settings.json', settings)
    runtime = Runtime(binary, profile, owned)
    result = dict(label=label, repetition=repetition, binary=str(binary), binary_sha256=sha(binary), profile=str(profile), blocks=len(rows), transactions=sum(r['transactions'] for r in rows), source='bitcoin_peers' if peer_source else 'verified_external_archive', network_enabled=peer_source)
    try:
        runtime.start()
        if peer_source:
            write(owned / 'network-before.json', runtime.call('/api/v1/network'))
        assert not runtime.call('/api/v1/index/run').get('run'), 'Fresh profile already has a run'
        request = {'in': rows[0]['height'], 'out': rows[-1]['height'], 'in_hash': rows[0]['block_hash'], 'out_hash': rows[-1]['block_hash'], 'consumers': [dict(index=identity, armed=True, live=False, mode='full' if identity == 'tx-locator' else 'lean') for identity in IDS]}
        plan = runtime.call('/api/v1/index/run/plan', request)
        write(owned / 'plan.json', plan)
        assert plan['ready'], plan
        before_observer = observer_snapshot(runtime)
        before_io = process_metrics(runtime.process)
        thread = threading.Thread(target=runtime.sample_memory, daemon=True)
        thread.start()
        started = time.perf_counter()
        document = runtime.call('/api/v1/index/run/start', dict(request=request))
        run_id = document['run']['id']
        deadline = time.monotonic() + timeout
        with (owned / 'progress.jsonl').open('w', encoding='utf-8') as progress:
            while True:
                document = runtime.call('/api/v1/index/run')
                run = document.get('run')
                assert run and run['id'] == run_id and not document.get('error'), document
                progress.write(json.dumps(dict(elapsed_seconds=time.perf_counter() - started, run=run)) + '\n')
                progress.flush()
                if run['state'] == 'complete':
                    break
                assert run['state'] not in ('complete_with_errors', 'failed', 'cancelled', 'error', 'rebuild_required'), run
                assert time.monotonic() < deadline, ('Replay deadline exceeded', run)
                time.sleep(.2)
        result['elapsed_seconds'] = time.perf_counter() - started
        after_io = process_metrics(runtime.process)
        after_observer = observer_snapshot(runtime) if before_observer is not None else None
        result['measurement_only_observer'] = None if before_observer is None else dict(before=before_observer, after=after_observer, delta={key: after_observer[key] - value for key, value in before_observer.items() if type(value) is int and type(after_observer.get(key)) is int})
        if result['measurement_only_observer'] is not None:
            assert result['measurement_only_observer']['delta'].get('failed_syncs', 0) == 0
        runtime.stop_sampling.set()
        thread.join(timeout=2)
        result['counts'] = run['counts']
        result['startup_seconds'] = runtime.startup_seconds
        result['transactions_per_second'] = result['transactions'] / result['elapsed_seconds']
        result['blocks_per_second'] = len(rows) / result['elapsed_seconds']
        result['memory'] = {key: max((sample.get(key, 0) for sample in runtime.samples + [after_io]), default=0) or None for key in ('rss_bytes', 'process_peak_rss_bytes')}
        result['os_process_io_delta'] = {key: after_io['io'][key] - before_io['io'][key] for key in after_io.get('io', {}) if key in before_io.get('io', {})}
        result['run'] = run
        if peer_source:
            write(owned / 'network-after.json', runtime.call('/api/v1/network'))
        assert {p['index'] for p in run['consumers']} == set(IDS)
        for consumer in run['consumers']:
            assert consumer['height'] == rows[-1]['height'] and consumer['block_hash'] == rows[-1]['block_hash'] and consumer['state'] == 'complete', consumer
        # Status cost is sampled after timing the replay, never charged to source/apply.
        status_times = []
        for _ in range(3):
            status = runtime.call('/api/v1/index/status')
            status_times.append(runtime.calls[-1])
        result['post_run_status_samples'] = status_times
        write(owned / 'status.json', status)
        for identity in IDS:
            instance = next(i for i in status['instances'] if i['definition'] == identity)
            assert instance['coverage'] == [{'from': rows[0]['height'], 'to': rows[-1]['height']}], instance
        runtime.stop()
        finalize_saved_run(result, owned, rows)
        write(owned / 'api-calls.json', runtime.calls)
        print('%s repetition%d: %.3fs, %.1f tx/s' % (label, repetition, result['elapsed_seconds'], result['transactions_per_second']), flush=True)
        return result
    except BaseException as error:
        result.update(result='FAIL', error=str(error), calls=runtime.calls)
        write(owned / 'result.json', result)
        raise
    finally:
        runtime.stop()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline', type=Path)
    parser.add_argument('--candidate', type=Path)
    parser.add_argument('--binary', type=Path, help='Single-binary smoke run; no speedup is claimed')
    parser.add_argument('--label', default='smoke')
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--count', type=int, default=0, help='First N fixture blocks; 0 means all')
    parser.add_argument('--repetitions', type=int, default=1)
    parser.add_argument('--timeout', type=float, default=3600)
    parser.add_argument('--analyze-only', action='store_true', help='Recheck an existing owned output after process exit; never start a process')
    parser.add_argument('--peer-source', action='store_true', help='Separate single-binary network acceptance: copy headers only, fetch bodies from Bitcoin peers; no comparative speed claim')
    args = parser.parse_args()
    assert args.count >= 0 and args.repetitions > 0
    assert not args.peer_source or args.binary and not args.analyze_only and args.repetitions == 1, 'Peer acceptance requires one --binary run'
    assert args.label.replace('-', '').replace('_', '').isalnum(), 'Invalid label'
    assert args.analyze_only or bool(args.binary) != bool(args.baseline or args.candidate), 'Choose --binary or a baseline/candidate pair'
    if not args.binary and not args.analyze_only:
        assert args.baseline and args.candidate, 'Both comparison binaries are required'
    fixture, output = args.fixture.resolve(strict=True), args.out.resolve()
    manifest, headers, rows = validate_fixture(fixture, args.count)
    if args.analyze_only:
        report = read_json(output / 'manifest.json')
        assert report['fixture_manifest_sha256'] == sha(fixture / 'manifest.json') and report['selected_blocks'] == rows
        samples = []
        for owned in sorted(output.iterdir()):
            if not owned.is_dir() or not (owned / 'result.json').is_file():
                continue
            result = read_json(owned / 'result.json')
            assert result.get('run', {}).get('state') == 'complete', 'Analysis cannot repair an incomplete application run'
            if result.get('result') != 'PASS':
                write(owned / 'initial-analysis-failure.json', result)
            observer = result.get('measurement_only_observer')
            if observer:
                observer['delta'] = {key: observer['after'][key] - value for key, value in observer['before'].items() if type(value) is int and type(observer['after'].get(key)) is int}
            samples.append(finalize_saved_run(result, owned, rows))
        assert samples, 'No completed owned samples to analyze'
        report['repetitions'] = [dict(repetition=repetition, samples=[s for s in samples if s['repetition'] == repetition]) for repetition in sorted({s['repetition'] for s in samples})]
        archive = output / 'immutable-archive'
        assert len(inventory(archive)) == 2 * len(rows)
        for row in rows:
            raw = archive / 'blocks' / row['block_hash'][:2] / (row['block_hash'] + '.blk')
            assert sha(raw) == row['sha256']
            assert read_json(raw.with_suffix('.json')) == dict(schema=1, verifier_version=2, hash=row['block_hash'], height=row['height'], sha256=row['sha256'], bytes=row['bytes'])
        report.update(result='PASS', archive_unchanged=True, post_exit_analysis_repeated=True)
        write(output / 'result.json', report)
        print('Saved replay evidence PASS; no process was started or existing index changed.', flush=True)
        return
    assert not output.exists(), 'Output must be a new directory; profiles are never reused'
    output.mkdir(parents=True)
    archive = output / 'immutable-archive'
    for row in ([] if args.peer_source else rows):
        target = archive / 'blocks' / row['block_hash'][:2] / row['block_hash']
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(fixture / row['filename'], target.with_suffix('.blk'))
        write(target.with_suffix('.json'), dict(schema=1, verifier_version=2, hash=row['block_hash'], height=row['height'], sha256=row['sha256'], bytes=row['bytes']))
    archive_before = inventory(archive)
    binaries = [(args.label, args.binary.resolve(strict=True))] if args.binary else [('baseline', args.baseline.resolve(strict=True)), ('candidate', args.candidate.resolve(strict=True))]
    report = dict(schema=1, fixture_manifest_sha256=sha(fixture / 'manifest.json'), header_sha256=manifest['header_sha256'], selected_blocks=rows, selected_indexes=IDS, fixture_bytes=sum(r['bytes'] for r in rows), source_mode='bitcoin_peers' if args.peer_source else 'verified_external_archive', body_fixture_copied=not args.peer_source, platform=platform.platform(), python=platform.python_version(), process_count=os.cpu_count(), timing_scope='run/start request until durable complete observed by 200ms run polling; startup and post-run status inspection separate', cache_policy='fresh profile; only selected headers copied, no source bodies or archive' if args.peer_source else 'fresh application profiles; shared operating-system file cache is not flushed', repetitions=[])
    write(output / 'manifest.json', report)
    for repetition in range(args.repetitions):
        order = binaries if repetition % 2 == 0 else list(reversed(binaries))
        samples = [run_one(binary, label, repetition, output, archive, headers, rows, args.timeout, args.peer_source) for label, binary in order]
        if len(samples) == 2:
            baseline, candidate = (next(s for s in samples if s['label'] == label) for label in ('baseline', 'candidate'))
            assert baseline['semantics'] == candidate['semantics'], 'Durable commitments or full commit payloads differ'
            semantic_equal = {key: baseline['semantics'][key] == candidate['semantics'][key] for key in IDS}
            logical = ('source_reads', 'decoded_blocks', 'inscription_extractions', 'outcome_reads', 'chain_steps')
            logical_equal = {key: baseline['counts'].get(key, 0) == candidate['counts'].get(key, 0) for key in logical}
            # Cache hits are implementation work, not a semantic result and may decrease.
            report['repetitions'].append(dict(repetition=repetition, order=[s['label'] for s in samples], samples=samples, semantics_equal=semantic_equal, durable_counts_equal=True, coordinator_work_counts_equal=logical_equal, coordinator_work_delta={key: candidate['counts'].get(key, 0)-baseline['counts'].get(key, 0) for key in logical}, physical_store_bytes_equal={key: baseline['files'][key] == candidate['files'][key] for key in IDS}, elapsed_speedup=baseline['elapsed_seconds'] / candidate['elapsed_seconds']))
        else:
            report['repetitions'].append(dict(repetition=repetition, samples=samples))
        write(output / 'result.json', report)
    assert inventory(archive) == archive_before, 'Replay archive was changed'
    report['archive_unchanged'] = True
    report['result'] = 'PASS'
    if not args.binary:
        report['median_elapsed_seconds'] = {label: statistics.median(sample['elapsed_seconds'] for r in report['repetitions'] for sample in r['samples'] if sample['label'] == label) for label in ('baseline', 'candidate')}
        report['median_elapsed_speedup'] = report['median_elapsed_seconds']['baseline'] / report['median_elapsed_seconds']['candidate']
    write(output / 'result.json', report)
    print(('Peer acceptance PASS: verified peer-supplied blocks, exact durable payloads and Raw Off.' if args.peer_source else 'Replay benchmark PASS: exact per-block durable payloads, outcome commitments and coverage; archive unchanged.'), flush=True)


if __name__ == '__main__':
    main()
