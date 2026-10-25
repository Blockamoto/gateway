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
