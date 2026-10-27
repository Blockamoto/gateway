# Reproducing index replay measurements

`scripts/benchmark-index-replay.py` runs compiled applications in new, owned profiles. It never opens an existing profile or controls a running preview. Core, networking, discovery, serving, legacy graph indexing and Raw retention are disabled. A shared external archive supplies identical pinned Bitcoin bytes; the application still verifies those bytes against the copied selected headers.

The workload selects Full Transaction Index, canonical Bitmap Districts and canonical Bitmap Parcels together, from the Bitmap origin at 792435 through the selected fixture end. No synthetic saved index, checksummed checkpoint or interpreted result is injected. Every repetition gets a fresh profile. Existing output directories are refused.

## Fixture and commands

The fixture directory contains `manifest.json`, `headers.bin`, and raw block files. The manifest records `header_sha256` and a consecutive `blocks` array with `height`, `block_hash`, `filename`, `bytes`, `sha256`, and optionally `transactions`. The harness independently checks each raw header's Bitcoin hash, its exact position in `headers.bin`, raw-file SHA-256, byte length and transaction-count prefix before starting a process. The first block must be 792435. Fixture paths cannot escape the fixture directory.

Run a small smoke first, then a representative paired replay:

```powershell
python scripts/benchmark-index-replay.py --binary build/portable-v0.7.8-reviewed/GatewayClient.exe --label baseline-smoke --fixture build/qa-078/performance-336/fixture --count 2 --out build/qa-078/performance-336/smoke

python scripts/benchmark-index-replay.py --baseline BASELINE.exe --candidate CANDIDATE.exe --fixture FIXTURE --count 32 --repetitions 3 --out NEW_OUTPUT_DIRECTORY
```

Paired repetitions alternate baseline/candidate order. If the candidate is still being built, run each binary separately with the same fixture and count, then compare saved artifacts without reopening either profile:

```powershell
python scripts/benchmark-index-replay.py --binary BASELINE.exe --label baseline --fixture FIXTURE --count 8 --out BASELINE_OUTPUT
python scripts/benchmark-index-replay.py --binary CANDIDATE.exe --label candidate --fixture FIXTURE --count 8 --out CANDIDATE_OUTPUT
python scripts/compare-index-replays.py --baseline BASELINE_OUTPUT --candidate CANDIDATE_OUTPUT --out NEW_COMPARISON_DIRECTORY
```

The default timeout is one hour. `--count 0` uses the full manifest. The benchmark records the executable and fixture SHA-256, platform, Python version, block list, selected modes, startup time, complete progress snapshots, API request timings, process memory, available process I/O counters and final durable evidence. Profiles and logs remain inside the requested output directory for review. Shutdown and failure cleanup target only the process that the harness created.

## What elapsed time and counters mean

Elapsed replay time starts immediately before `run/start` and ends when a 200 ms `run` poll observes durable completion. It includes start admission and polling resolution. Startup/header loading, fixture validation/copying, final status reads and evidence inspection are recorded separately. Local archive replay measures indexing and local source work; it does not measure Bitcoin peer download performance.

The operating-system file cache is not flushed. Profiles are fresh, but repeated fixture reads can be warm. Use multiple alternating repetitions for a performance comparison; a single sample is a diagnostic, not a statistical guarantee. Concurrent applications, antivirus and filesystem behavior can affect results.

| Field | Meaning and limits |
| --- | --- |
| `source_milliseconds` | Legacy inclusive source-call time. It includes source substages; do not add it to them. Prefetch and shared work can overlap wall time. |
| `source_acquire_milliseconds` | Local/provider acquisition and shared-flight waiting. This is not a network-only timer. |
| `source_decode_milliseconds` | Timed body decoding/verification within acquisition. |
| `source_raw_retention_milliseconds` | Raw retention work within the source policy. Raw is Off in this replay. |
| `source_incidental_milliseconds` | Optional incidental derivation/publication within acquisition. |
| `extraction_milliseconds` | Explicit shared inscription extraction that was needed outside an already supplied extraction. Zero/omitted does not mean the block had no inscriptions. |
| `apply_milliseconds` | Consumer application time. It can include storage work also reported by the flush timer. |
| `storage_flush_milliseconds` | Actual timed `FlushLocatorBatch` calls. Automatic boundary/byte-limit flushes can occur inside Apply; final partial-run flushes occur outside Apply. It can overlap Apply depending on the call site, so do not add the timers blindly. |
| `progress_milliseconds`, `reconcile_milliseconds` | Coordinator progress publication and reconciliation work. |
| `source_reads`, `decoded_blocks`, `inscription_extractions` | Existing logical coordinator counters. They do not by themselves count network downloads, every low-level parse, physical files, or fsync calls. |
| `locator_*` write counters | Actual successful physical operations reported by the candidate writer observer, separate from logical blocks and transactions. Missing baseline fields are unavailable, never an inferred zero. |
| `os_process_io_delta` | Available OS read/write operation and byte counters for the whole owned process. A write operation is not an fsync, nor necessarily a locator write. |
| `memory.rss_bytes` | Maximum sampled working set while replay ran, at approximately 50 ms intervals. Very short peaks can be missed. |
| `memory.process_peak_rss_bytes` | OS process peak when available; includes startup and is not a run-only allocation total. Unsupported metrics are null or absent. |

The measurement-only baseline produced by `scripts/build-index-benchmark-observer.py` preserves the pinned baseline's algorithm, write order and original `Sync` calls. Its optional `GET /api/v1/index/bench-observer` reports atomic counters. The replay harness takes before/after snapshots around the run, keeps exact counter names, and requires `failed_syncs == 0`. Receipt/active-write attempts can be treated as completed only after the run succeeds with no failed syncs. These whole-process instrumentation counters and the candidate store counters have different scopes; retain their names and describe the scope when comparing them. File counts and final file sizes are reported as inventory, never used to infer fsync counts.

## Correctness before a speed claim

Each result must have all three durable consumers complete at the pinned final height/hash, with exact requested coverage and no retained Raw files. The harness reads every durable commit after the owned process exits. It checks the full Transaction Index has every actual transaction position and the raw block's denominator, checks consecutive predecessor commitments, and checks canonical profile/origin and completed Bitmap outcomes. It compares complete per-block commit payload digests and final heads across binaries. Those payloads bind candidate/outcome roots and selected results. Durable per-height output counts must agree. Coordinator source/decode/extraction/outcome/chain work counts and cache hits are reported as deltas and may decrease when redundant work is removed; they are not substituted for output counts. Physical-layout inventories may differ after a storage optimization.

An assertion failure saves the partial metrics and leaves the profile available for review. `--analyze-only` with the same fixture/count/output repeats post-exit evidence inspection without replaying blocks; record any harness correction and preserve the original failure artifact. Do not erase a genuine application failure or compare runs with different block lists, profile modes or source policies.

## Status API and browser rendering

Measure live status separately with:

```powershell
node scripts/profile-index-status.js --url http://127.0.0.1:PORT --out OUTPUT --samples 8 --browser true
```

This tool sends sequential read-only `index/run` and `index/status` GETs; add `--progress true` to measure `index/progress`. It measures response waiting, body reading, uncompressed UTF-8 payload bytes and JSON parsing separately. Its optional fresh browser times synchronous `timeline.render` and the health update using the already received snapshot in collapsed and expanded states. The old baseline rendered twice per refresh; the candidate updates health without rendering and renders once. Timers exclude server fetch, JSON parsing, layout/paint completion and other inspector refresh work. It records run state, before/after run documents, all browser POSTs and JavaScript errors. No run controls are used. A changed run document may reflect another user's activity; it is not attributed to this observer.

In the paused baseline sample on 2026-10-24, collected while a separate owned replay was writing to the same machine, eight status GETs had a median total of 5.574 seconds for an 83 KB payload, while run GETs had a median of 4.43 ms. JSON parsing was under 1 ms median. Synchronous timeline-plus-health rendering was 45.6 ms collapsed and 81.4 ms expanded. The result is under `build/qa-078/performance-336/status-paused-baseline`. This establishes the measured response cost under that system load, not an unloaded idle baseline, a proven cause, or an active-run throughput penalty.

The same disposable paused profile was then opened with the candidate without changing its run or saved data. Eight candidate GETs had median totals of 1.722 seconds for full status, 3.45 ms for lightweight progress and 2.28 ms for run. Synchronous single-pass rendering was 12.95 ms collapsed and 27.25 ms expanded. System load differed, so the full-status before/after timings are not a controlled speed multiplier. The candidate's paired full/progress measurements establish why recurring UI observation uses the lightweight endpoint. Details are cached, bounded and refreshed at 30-second intervals with explicit observation age; run/jobs and durable progress are refreshed on each poll. Initial/manual/control refreshes still request full details. Evidence: `status-paused-candidate-rerun/result.json`. The profiler's earlier visibility-wait failure is preserved separately; it did not indicate an application or indexing failure.

Saved scopes also differ legitimately: Raw bodies, a sequential run checkpoint, and individually viewed transaction facts are independent. The selected-track inspector labels committed run coverage and viewed block facts separately; their ranges can overlap and the total counts their union once. Staged blocks/records/bytes appear as explicitly uncommitted progress, while the durable checkpoint and filled coverage remain authoritative.

## Observed eight-block local replay (2026-10-24)

The fixture subset was Bitcoin heights 792435–792442, with 17,782 transactions, three District candidates/winners and zero Parcel candidates/winners. Parcel equality therefore covers completed empty outcomes here; this replay does not benchmark nonempty Parcel parent-proof work. This is a single local archive replay per binary on this machine, with fresh profiles and an uncontrolled OS cache. It is not live Bitcoin throughput and does not establish a statistical speedup guarantee.

| Measurement | Pinned baseline with measurement-only observer | Candidate |
| --- | ---: | ---: |
| Replay elapsed | 784.115 s | 9.160 s |
| Source call total | 45.391 s | 1.063 s |
| Consumer Apply | 736.148 s | 1.088 s |
| Explicit locator flush timer | unavailable | 6.843 s |
| Coordinator source loads/extractions | 14 / 14 | 8 / 8 |
| OS process peak working set | 213,172,224 bytes | 193,126,400 bytes |

The elapsed ratio for these two samples is 85.61. The candidate reports one locator batch flush, 256 shard appends, 512 shard syncs, 513 receipt writes, one active-table write, one head write and eight commit writes. The baseline observer reports 33,094 locator data syncs, 33,094 receipt syncs, 33,094 receipt-write attempts, 17,464 shard appends, 17,472 locator write opens, eight head writes, eight full commit writes and 2,046 sparse-pointer writes, with zero failed syncs. Their observer scopes are retained in the result; these are measured operations, not estimates from file inventories.

All 24 durable block-commit payloads match exactly (eight for each selected index), as do final commitments, coverage and all Full Transaction Index positions including Coinbase. The only allowed head-field difference is the explicitly validated physical `locator_layout: packed-buckets-v1` marker outside block commitments. The observed baseline's first two payloads for all three indexes also match the untouched delivered baseline's two-block smoke. Coordinator work-count deltas are reported without redefining output counts.

Review `build/qa-078/performance-336/comparison-8/result.json`, the two replay directories, and their `semantics.json`, progress and runtime logs. An initial schema-spelling harness correction and a Windows long-path analysis correction are preserved in the owned artifacts; completed indexing was not repeated to hide either issue.

The final delivery executable (`c1e8d577d26ae487f2a4e94d38ab9d8059aa7b1fb0ebbf2d05319a848af83346`) was replayed again after all UI/recovery changes: 6.262 seconds for the same eight blocks, with strict baseline equality and unchanged physical counts above; OS process peak working set was 159,289,344 bytes. A separate 32-block local check retained all 64,767 transactions in 10.476 seconds with a 185,905,152-byte peak; its first eight payloads match the comparison sample. That larger run has no matched baseline and overlapped a network check.

For a separate single-binary peer acceptance check, add `--peer-source --binary EXE` rather than a baseline/candidate pair. This copies selected headers only, configures no archive, keeps Core/mounts/Raw off, and lets ordinary Bitcoin peers provide the bodies. The final eight-block check took 180.151 seconds including source waiting; every durable payload matched the local run. Earlier checks varied with peer availability. No live baseline or network speed multiplier is established. Inspect `delivery-local-8`, `comparison-delivery-8`, `delivery-local-32`, `delivery-peer-8` and `VERIFICATION-PERFORMANCE-0.7.8.md` for final evidence.

## Standard Bitmap storage and observation diagnosis (2026-10-25)

A read-only live observation covered about 109 seconds: the two Bitmap tracks
advanced five blocks, the locator staged three, and no locator flush occurred.
The inclusive source counter added 10.864 seconds and Apply added 89.326 seconds.
The download queue held 37 ready bodies, about 63 MB. A separate 43.5-second
process sample recorded 23 CPU-seconds, about 102 MB read and 95 MB written.
These overlapping observations identify substantial application/storage work;
they are not a controlled replay or a per-track timing breakdown.

The six immutable District commits selected by the saved observation contain
109–1,689 candidates per block. At the last observed block the index held
531,397 candidates and 458,312 winners. Normal application updates touched radix
paths and leaves; it does not scan all those saved winners on every block.
Scattered targets can touch more leaves than a dense fresh-state fixture.

Raw retention governs future local reuse. Required block verification,
inscription extraction and Bitmap evaluation still run with Raw off. Retained
bodies can help later reads, including lagging consumers and Parcel dependency
lookups. No Raw setting was changed during this diagnosis, and the ready queue
does not support attributing the observed Apply cost to missing downloads.

`TestBitmapPerformanceDiagnostic` isolates two normal Standard District Apply
calls with 1,000 synthetic claims each, including 500 duplicate targets in the
second block. It uses TempDir only, with no application worker or network. Its
trace regions exclude page census and cleanup. It models real writer paths,
not authenticated Bitcoin history, a populated 458,000-winner index, or network
throughput. Run it with fresh output files, for example:

```powershell
go test -run '^TestBitmapPerformanceDiagnostic$' -count=1 -v -trace apply.trace -cpuprofile apply.cpu.pprof .
go tool trace -pprof=syscall apply.trace > apply.syscall.pprof
go tool pprof -top apply.syscall.pprof
```

The first trace attributes substantial delay to both file opens and writes:
Apply accounts for 4.67 seconds of syscall delay, with 2.69 seconds under
CreateFile, 0.75 seconds under file Sync and 0.57 seconds under rename. Stack
totals overlap; do not sum them as separate elapsed stages. Windows CPU-profile
`runtime.cgocall` samples include WinAPI waits and are not pure computation.

Page reads now use one checked descriptor and a Stat-sized bounded buffer, with
one extra byte to detect growth. Checksum, JSON, structure and extent checks
remain mandatory. The first one-descriptor prototype used a growing ReadAll
buffer; tracing showed extra read calls, so the final reader preallocates its
bounded extent.

The four Bitmap state trees flush with fixed concurrency, keeping separate
mutable caches. All workers finish successfully before their roots are assigned
and the existing commit/head publication begins. Per-file Sync, atomic page
publication and required directory acknowledgements remain. A temporary shared
set of 256 publication locks prevents concurrent identical-page read/rename
races; it is released after all four workers finish. No page format, canonical
encoding, profile identity, block commitment or saved prefix is changed.

Three alternating structural pairs compared the one-descriptor serial writer
with the concurrent writer. Median summed Apply time was 2.668 versus 2.211
seconds; one concurrent pair was slower. Both commitments, all four roots,
candidate/winner counts and every unique page count/byte total matched in all
six runs. Traced Sync and rename counts were each 479 in the original pilot,
one-descriptor pilot and concurrent pilot. These fixture observations do not
establish a live throughput multiplier or a guaranteed memory bound.

Visible target coverage previously missed its cache on every new committed
District head. An isolated browser fixture measured 13 reads over 13.02 seconds
with one-second head updates. A minimum five-second interval for the same view
reduced this to three reads over 12.37 seconds. Geometry, profile and detected
chain replacement invalidate immediately; a trailing refresh observes the last
head, and hidden documents cancel pending observation. The UI labels retained
results with their previous snapshot. This measures request reduction, not an
indexing speedup.

Evidence is retained under `build/qa-078/throughput-diagnosis` and
`build/qa-078/ui-poll-audit`. The real 32-block Standard replay uses the existing
owned-profile harness through a diagnostic wrapper that explicitly selects the
current Standard IDs. Historical harness defaults and earlier measurements are
preserved. Native-output equality is checked separately from timing.

## Populated radix lookup cache (2026-10-26)

The ordinal page cache used to discard all 512 entries on reaching its limit.
The current writer instead evicts the least recently used page while retaining
that same bound. Frequently visited branch pages can stay cached across scattered
lookups. The cache is private to a tree; page checks, encodings, roots and
durable publication are unchanged.

`BenchmarkOrdinalTreeScatteredReads` builds 50,000 synthetic numeric-key
entries in a fresh temporary tree, then times 5,000 deterministic scattered
lookups with a new reader. Fixture creation and writes are outside the timer.
On this Windows machine, the prior implementation took 54.511 seconds and the
candidate took 24.521 seconds. These are single samples under uncontrolled
concurrent machine load, with a synthetic tree smaller than the observed Bitmap
index. They diagnose this read pattern and do not establish live or whole-run
indexing throughput, nor prove the reported stall resolved. No live profile was
opened. The baseline command requested two samples; its owned process was
stopped after the first sample to avoid rebuilding the large fixture a second
time. The candidate ran once. Commands (using the same Go toolchain) were:

```powershell
$env:GO111MODULE='off'; $env:CGO_ENABLED='0'
go test -run '^$' -bench '^BenchmarkOrdinalTreeScatteredReads$' -benchtime=1x -count=2 . # prior cache; stopped after first sample
go test -run '^$' -bench '^BenchmarkOrdinalTreeScatteredReads$' -benchtime=1x -count=1 . # LRU cache
```

The focused cache test verifies the 512-page bound, retention of a hot page,
and eviction of the least recently used page. The existing checked-page
reopen/integrity test also passed with the new cache.

## Populated District Apply pilot (2026-10-27)

An isolated structural pilot uses the normal Standard District writer to
create one synthetic block with 20,000 distinct winners. This block is not
authenticated Bitcoin history and does not model the real sequence of
hundreds of thousands of claims. The source fixture is kept separate from
the two writable copies used for the timed next-block Apply. That second
synthetic block contains 500 previously claimed targets and 500 new targets,
interleaved in deterministic order. Neither stage opens a user profile or
uses a network.

The seed's normal Apply took 74.607 seconds after 0.780 seconds of fixture
setup; its process took 80.267 seconds overall and reached its first test
output after 4.063 seconds. Peak process working set was 219,570,176 bytes.
The reopened committed head contains 20,000 candidates and winners, with
4,044 immutable page files and 31,341,488 logical page bytes. Other machine
work was active; this is one local sample, not a baseline guarantee.

The first next-block pair used the old clear-all ordinal cache followed by
the current bounded LRU. Apply took 33.462 and 86.813 seconds respectively;
whole-process durations were 40.597 and 92.137 seconds, with sampled OS peak
working sets of 91,000,832 and 96,804,864 bytes. Both published exactly the
same commitment, all four roots, 21,000 candidates, 20,500 winners and
4,736 page files / 36,686,648 logical bytes. These fixed-order single samples
ran under uncontrolled machine load, and their copy preflight checked the
same head and checksummed root pages rather than every copied file. Pages
accessed during Apply retained their normal integrity checks. The pair does
not establish a cache regression or improvement; in particular, the earlier
scattered-read microbenchmark cannot be used as a writer-throughput claim.

A second pair reversed the order after the detached-evidence cache fix, using
fresh test binaries and profile copies. Current LRU Apply took 43.532 seconds
and the old cache took 60.594 seconds. All four samples across both pairs had
the same committed outputs and page census. In this reverse pair, sampled
whole-process CPU time was 4.359 and 4.563 seconds, and sampled read-operation
deltas were 2,217 and 2,287 respectively; process I/O includes startup and
census, can miss a short tail between samples, and is not a Sync count. Sampled
mean host CPU load was 98.57% and 99.16%, respectively, with a 100% sampled
peak in both runs. A separate system snapshot showed heavy Windows Defender
activity alongside other active processes. The contrasting order-dependent
times and heavy load leave the speed effect unresolved. No further inference
about live indexing or the 458,000-winner state follows from these samples.

The reverse baseline overlaid the old detached-evidence helper as well as the
old ordinal tree and test because the corrected helper uses newer cache fields.
This synthetic District fixture does not exercise the detached parent-proof
path. Reverse baseline/current test binary SHA-256 values were
`59923d1d18e9fdd770ea6ba7bf75497452a4ba784c298388dda64c1ec9586dad` and
`d0ea5dde8fe79f9271030cabaffd0fe407f52d064eda4160feabde8ee1a5bfca`;
the corrected current ordinal source SHA-256 was
`ea519a5a20a0b6d07176d3652f9d8fecdd9dc5a3096b12ac532f09134e9cac65`.
The reverse receipt is `build/bitmap-populated-pilot/pairs-reverse-result.json`.

The seed was not scaled to the reported 458,000-winner live state. A simple
linear extrapolation of the seed's page count and bytes gives about 92,600
files and 718 MB, and its seed Apply time gives about 28.5 minutes; tree shape,
additional commits, machine load, memory and witness/occurrence limits make
those estimates unreliable. A 458,000-claim single synthetic block also
exceeds the existing occurrence budget. The real populated profile and its
ongoing work were not accessed by this pilot.

Evidence is retained in `build/bitmap-populated-pilot`: `seed-process.json`,
`seed/pilot-summary.json`, the source overlays, copied profiles and the first
`pairs-result.json`. The seed input commitment was
`50d940c5f5118d4304c7e2ad3ca215f3dd4ab7f63ebcfc98c822dadf4d9ac9ab`.
The first pair used baseline/current test binary SHA-256 values
`38e80ee235a2191ce42f8ff7a018fa468f429868a638b2409c2ae33ac5a1fca1` and
`b6d048d59b2c56686fa6b137906f084afffd9b28671609544eeee21872206fb9`;
the old/current ordinal source SHA-256 values were
`ee69b12b3c2a31d5147aeba1f8e252fec566ffeed27696776821b0c951203805` and
`fc0443689255d22f617690e11ec26fda4595563c15ca88efeaab9cff57c0ec11`.

To reproduce in a **new** owned pilot directory, use the Windows Go toolchain.
The output must be a new direct `build/bitmap-pilot-*` child. Initialization
fails if it exists; runners require its marker, reject reparse points and
existing outputs, and terminate only their owned process above 1.5 GiB sampled
working set or 240 seconds. These guards protect against accidental reuse,
not hostile concurrent path changes. Do not point it at existing profiles.

```powershell
$ErrorActionPreference = 'Stop'
$env:GO111MODULE='off'; $env:CGO_ENABLED='0'
$out = 'build/bitmap-pilot-choose-a-new-name'
python scripts/bitmap_pilot_paths.py --init $out
if ($LASTEXITCODE -ne 0) { throw 'Pilot initialization failed' }
if (Test-Path -LiteralPath "$out/pilot.test.exe") { throw 'Pilot binary already exists' }
go test -c -o "$out/pilot.test.exe" .
if ($LASTEXITCODE -ne 0) { throw 'Pilot build failed' }
python scripts/benchmark-bitmap-populated-seed.py --out $out
if ($LASTEXITCODE -ne 0) { throw 'Pilot seed failed' }
```

For the old-cache comparison, create a Go overlay with the `efc5b13` versions
of `ordinal_state_tree.go`, its matching test file and the detached-evidence
helper. The helper overlay is needed to compile against the corrected current
source; this District-only fixture does not exercise parent-proof evidence.
Both binaries use the same remaining source and the tracked
`TestBitmapPopulatedPilotApply` fixture. Each run uses separate owned copies
and compares committed results. The original first pair preceded the detached
evidence correction and used only the ordinal source/test overlays.

```powershell
$env:BITMAP_PILOT_OUT = (Resolve-Path -LiteralPath $out).Path
@'
import json, os, pathlib, subprocess
root = pathlib.Path.cwd()
out = pathlib.Path(os.environ['BITMAP_PILOT_OUT'])
mapping = {}
for name in ['ordinal_state_tree.go', 'ordinal_state_tree_test.go', 'bitmap_development_evidence.go']:
    data = subprocess.run(['git', 'show', 'efc5b13:' + name], check=True, stdout=subprocess.PIPE).stdout
    target = out / ('old-' + name)
    with target.open('xb') as f:
        f.write(data)
    mapping[str(root / name)] = str(target)
with (out / 'baseline-overlay.json').open('x', encoding='utf-8') as f:
    f.write(json.dumps({'Replace': mapping}, indent=2) + '\n')
'@ | python -
if ($LASTEXITCODE -ne 0) { throw 'Overlay preparation failed' }
if (Test-Path -LiteralPath "$out/baseline-apply.test.exe") { throw 'Baseline binary already exists' }
go test -c -overlay "$out/baseline-overlay.json" -o "$out/baseline-apply.test.exe" .
if ($LASTEXITCODE -ne 0) { throw 'Baseline build failed' }
if (Test-Path -LiteralPath "$out/current-apply.test.exe") { throw 'Candidate binary already exists' }
go test -c -o "$out/current-apply.test.exe" .
if ($LASTEXITCODE -ne 0) { throw 'Candidate build failed' }
python scripts/benchmark-bitmap-populated-pairs.py --out $out
if ($LASTEXITCODE -ne 0) { throw 'Pilot comparison failed' }
```

The first and reverse pairs use distinct profile and receipt names. Each pair
refuses existing destinations before copying; retain a partial run and reproduce
in a fresh workspace instead of resuming. For reverse order, build the same two
configurations as `reverse-baseline-v2.test.exe` and `reverse-current-v2.test.exe`
in a new workspace and create `v2-old-bitmap_development_evidence.go` exclusively
from the old helper. Run `python scripts/benchmark-bitmap-populated-pairs.py
--out $out --reverse`.

The Python preflight checks the summary commitment string and four checksummed
summary root pages, but does not independently bind those roots to the commit;
the normal Go reader authenticates the committed head and roots during Apply.
Run `python scripts/test_bitmap_pilot_guards.py -v` for the lightweight path,
collision and reparse-point tests; they do not run the benchmark.

## Hosted populated District Apply (2026-10-27)

[GitHub Actions run](https://github.com/Blockamoto/gateway/actions/runs/40448722444),
tooling commit `f2c83a5c2e47d76db89c706ac3b2e5752490451d`, candidate source
`6c33011a9cb3878d6246031f23938ca11ec5ba81`, old-cache baseline overlay source
`efc5b13c8a43d43b91e6423707e81ccf74e9f2db`. The run used Windows Server 2025,
image `windows-2025-vs2026` version `20261021.278.1`, and Go 1.27.1. The
baseline overlays the old ordinal cache and matching evidence helper on the same
candidate; it does not represent a complete older release.

The 27-block seed contained 531,397 synthetic candidates and 458,312 winners.
It took 833.714 seconds summed Apply and 837.652 seconds process time, with
251,695,104 bytes peak working set. It used 793,125 immutable page files and
3,786,035,576 logical page bytes. The first checkpoint above 80,000 winners
projected 187,605 final files, underpredicting actual file count by 4.23 times.
Free disk fell from 235,329,609,728 to 202,227,335,168 bytes across the seed and
four copies. The runner had much more free space than GitHub's nominal 14 GB
specification, so this does not establish suitability for a 14 GB runner. All
guards passed, the 2 GiB process working-set cap was respected and free disk
remained above the 3 GiB reserve.

Four separate copies ran serially in baseline/current/current/baseline order:

| Variant | Apply times, seconds | Whole-process times, seconds |
| --- | --- | --- |
| Baseline | 3.829, 3.911 | 212.353, 234.841 |
| Current | 3.577, 3.676 | 209.585, 211.107 |

The current samples show no regression in this bounded synthetic comparison;
the 6.3% median difference is descriptive, with two observations per variant,
no predefined threshold and uncontrolled OS cache. Whole-process time includes
file preflight and page census and is not Apply time. Sampled host CPU means in
run order were 29.28%, 28.81%, 28.95% and 53.53%.

All four outputs had 532,397 candidates, 458,812 winners, 796,471 page files /
3,804,803,129 logical page bytes, identical four roots and commitment
`699538d2ee351bf79b33b504a3fe442e9230a5e8d495cd5e6bed6f0fb1eada1f`. Each
checked 16 input-derived first-winner blocators. Checked roots and accessed
pages do not establish full-store integrity. The timed block had 500 duplicate
and 500 new targets. History and targets were synthetic rather than authenticated
Bitcoin data; the workload omitted UI polling and network activity. The result
does not prove real-profile throughput or that the observed stall is resolved.

The current and baseline test executables had SHA-256 values
`6559c7d03a94ba282dd269cc13601b9d4a86863be6f04625a93d774a933061a4` and
`2bacbe030735c2348a941c79a2ced27e61f3aefb0f988fcdba98df298d8e6a82`; fixture
SHA-256 was `cb11bdc81800f9f7a06bc013d82724841610091855bbaa8f88f2690591229aeb`.
The original artifact ZIP is retained under `build/hosted-bitmap-evidence-40448722444`, SHA-256
`bf84343b4a1f5ad669fb1a0ab155750a8edd11d9bcee11e04b25d319ffc1b3cc`, matching
GitHub's artifact digest. That directory's `extracted` folder contains the complete compact receipt and
logs; `job-122006108805.log` is the separately retrieved job log.
No retries or remote changes were performed while monitoring.
