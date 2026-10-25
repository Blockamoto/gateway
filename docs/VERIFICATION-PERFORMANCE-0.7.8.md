# Local 0.7.8 performance preview verification

This is a separate local portable preview on branch `v0.7.8`, based on reviewed
commit `cfde4c9b488b3c14456b9e54556d6b4e6a01daee`. No Gateway main push, merge,
release, updater promotion, installation or Gears modification was performed.
The portable `artifact-manifest.json` records its exact source revision and
executable SHA-256. Earlier reviewed artifacts remain separate.

## Implemented and reviewed behavior

- Genesis-aligned 336-block logical windows, bounded byte/record subflushes,
  overlapping acquisition/application and packed locator persistence. Immutable
  per-block commitments and every transaction position, including Coinbase,
  remain unchanged. Bitmap starts at 792435; no earlier padding is processed.
- Only durable progress becomes coverage. Staging is explicitly uncommitted.
  Pause/cancel, restart, source-authority changes, Raw Off, late consent changes,
  reorgs and finite partial batches preserve publication fences.
- Receipt/data/head failure injection covers retries before/after head rename,
  empty deduplicated retries, and directory acknowledgement. Old-binary rollback
  reconstruction remains possible; re-upgrade refreshes membership after a
  legacy append or same-length reorg, including stale packed files.
- Coordinator guards retain scalar admission/anchor data rather than decoded
  block graphs. Finalizer/GC tests cover retained heavy staged blocks and release
  of cancellation callbacks.
- Exact accepted two-level order: Blocks, Transactions, Addresses, Inscriptions,
  Bitmap. Saved-order migration preserves independent controls and IDs. UTXO and
  Transaction locators are under Transactions; Districts/Parcels are siblings.
- Immediate/pinnable keyboard-safe purpose help and separate genuine other-track
  dependency help. Exact Bitmap rules, origins, blocators and disabled Chains
  remain intact.
- Home feature indicators use actual providers, settings and selected-chain
  evidence, with scoped/unknown states. Arm is not availability. Known reveal
  content and a retained reveal inventory are separate. The checklist does not
  start indexing, resource fetches or tracing. Home uses the available pane width.
- Continuous invertible nonlinear scrollbar sizing/resizing, exact opposite-edge
  anchoring, desktop/narrow hit targets, pan, keyboard, wheel and pinch behavior.
- Bounded cached detail observation with explicit age and fresh run/jobs on the
  lightweight progress endpoint. Visible active work polls roughly once per
  second; idle/paused work polls at five seconds; hidden views pause. Requests
  coalesce and do not overlap. Control actions still refresh immediately. An
  observed active-to-terminal run/job transition requests one coalesced full
  detail refresh so lanes, totals and Home settle promptly, without full audits
  on every block commitment or repeated terminal polls.

## Correctness and recovery checks

The focused combined Go suite passed in 14.542 seconds and `go vet .` passed.
It includes the final packed storage/retry/reorg/downgrade-upgrade, bounded
buffers, memory lifetime, coordinator durable finish/pause/restart/authority,
Bitmap v2 origin/rules/blocator/execution fences, provider, progress-cache and
layout-migration tests. Earlier broader coordinator/resolved-ingestion tests
passed in 141.758 seconds. The 336-block synthetic comparison asserts byte-exact
immutable commit payloads and all 336 Coinbase positions, with head, active-table
and same-bucket append operations amortized from 336 to one. It is a persistence
correctness test, not a modern-block peer throughput measurement.

Production-asset browser checks cover accepted hierarchy/migration, independent
Arm controls, purpose/dependency help, narrow positioning and event isolation,
nonlinear scrollbar edge/anchor/pan gestures, workspace navigation and the Home
checklist. Home tests exercise all four aliases, 17 indicators, physical-owner
Inspect navigation, dynamic upgrades/downgrades, focused-button preservation,
settings and snapshot mismatches, no hidden resource/index requests and narrow
layout. Pure-model regressions cover Raw Off demand, explicit disconnects,
stale-chain evidence, sparse child links, direct script addresses, numbered-Sat
math, address checkpoint matching, offline reveal bodies, Lean locations and
Full content.

## Measurements and limits

The pinned fixture has 32 authentic consecutive blocks, 792435–792466,
48,444,377 serialized bytes and 64,767 transactions. Its header position/hash,
raw SHA-256, byte length and transaction-count prefix are checked independently.
The matched comparison uses the first eight blocks, 792435–792442, containing
17,782 transactions, three District winners and zero Parcel candidates/winners.
Parcel equality covers completed empty outcomes in this sample.

The measurement-only baseline preserves `cfde4c9` algorithms, original I/O order
and Sync calls. Its first two complete per-block payloads match an untouched
baseline run. The eight-block baseline took 784.115 seconds. The first optimized
candidate took 9.160 seconds. The final executable is checked separately below;
use its recorded sample rather than assuming timing is identical across builds.

The baseline recorded 17,464 shard appends, 33,094 data Syncs, 33,094 receipt
Syncs/receipt-write attempts, eight commit/head writes and 2,046 sparse pointer
writes, with zero failed Syncs. The optimized eight-block path recorded 256
bucket appends, 512 bucket Syncs (including initial empty receipts), 513 receipt
replacements, one active/head write and eight unchanged commit writes. Physical
observer scopes differ and are preserved in the evidence; file inventory is not
used to infer Sync counts. Source/decode/extraction work fell from 14 to eight
while durable output remained identical.

All 24 complete per-height payloads, final semantic heads, commitments, coverage
and transaction positions are compared. The sole permitted physical head-field
difference is the validated `locator_layout: packed-buckets-v1`, outside block
commitments. Fresh application profiles share an uncontrolled OS cache; machine
load and antivirus effects vary. These local samples do not establish a universal
speed multiplier or a Bitcoin-peer download throughput gain. No full modern
336-block network performance claim is made.

On the same paused disposable profile, candidate progress GETs had a median total
of 3.45 ms, full status 1.722 seconds and run 2.28 ms. Single-pass synchronous UI
rendering measured 12.95 ms collapsed/27.25 ms expanded. The earlier baseline
under concurrent disk load measured full status 5.574 seconds and double-pass
rendering 45.6/81.4 ms; changed load prevents a controlled full-status multiplier.
Details and reproduction commands are in `INDEX-REPLAY-PERFORMANCE.md`.

Final exact-binary replay, peer, offline-reveal and portable launch evidence is
recorded in the final validation section below and under `build/qa-078/`.

## Final exact-binary validation

Executable SHA-256:
`c1e8d577d26ae487f2a4e94d38ab9d8059aa7b1fb0ebbf2d05319a848af83346`.
The packaging build must reproduce this digest before delivery.

| Check on this executable | Result |
| --- | --- |
| Eight-block matched local replay | PASS; 6.262 s, 17,782 transactions, 159,289,344-byte OS peak working set |
| Strict comparison with the 784.115 s observed baseline | PASS; all 24 per-block payloads and final semantic heads/coverage/positions identical |
| 32-block local capacity check | PASS; 10.476 s, all 64,767 transactions, 185,905,152-byte OS peak working set |
| 32-block prefix comparison | First eight payloads match the eight-block run for every selected track |
| Separate fresh-profile peer acquisition | PASS; eight blocks, 180.151 s including waiting for suitable historical-block peers; all 24 payloads match local replay |
| Known reveal offline from retained Raw | PASS; `953i0.792435` returns exactly eight bytes `0.bitmap`; Home content scoped and inventory unbuilt |
| Actual compiled UI/browser | PASS; all six new assets HTTP 200, all four Home aliases/17 indicators, real help/Inspect/geometry/narrow layout; no JS errors, missing assets or index mutations |
| Final providers/progress/layout/assets Go checks | PASS, 5.945 s; `go vet .` PASS |

The local eight-block run recorded one locator flush, 256 bucket appends, 512
bucket Syncs, 513 receipt replacements, one active/head write, eight commit
writes and 17,782 locator records. Source/decode/extraction counts were eight.
The 32-block run likewise retained every transaction and completed one partial
logical-window flush; byte/record limit crossings are covered by fault/unit
tests rather than inferred from this sample. The larger run overlapped an
independent network check and is a capacity/correctness sample without a matched
32-block baseline.

The peer profile received only selected headers; fixture bodies and an archive
were not copied/configured. Networking was on, Core/mounts/Gateway peerhood were
off, and Raw remained Off. Two ordinary Bitcoin peers were connected at the
final observation. Peer availability caused waiting and varied across separate
checks; no baseline live-peer comparison or download multiplier is claimed.
Source timers exclude some coordinator waiting/retry intervals and are not a
replacement for the reported end-to-end wall time.

The compiled offline-reveal check created Raw through normal run APIs, detached
its owned source archive, restarted with network/Core disabled, and began with
no reveal inventory, transaction-locator index or Ord cache. Reveal bytes were
read from retained Raw with header/PoW/Merkle/parsing/witness checks. Run, Raw
head and source archive remained unchanged by the read-only content query.

Evidence directories under `build/qa-078/performance-336/` are
`delivery-local-8`, `comparison-delivery-8`, `delivery-local-32`,
`delivery-peer-8` and `offline-known-reveal-delivery`. Native browser evidence is
`build/reviewed-qa/final-home-compiled`. Source-asset fixtures separately verify
one terminal full refresh settles stale zero coverage to 11 blocks and Home
readiness, while repeated terminal polls and per-block progress cause no new
full audit. A failed-job boundary is tested independently. Initial missing-asset
evidence is preserved at `offline-known-reveal-final`; an embedded-reference
HTTP-handler regression now prevents the allowlist omission.

## Portable use

The separately named directory is `build/portable-v0.7.8-performance-336`; its ZIP
is `build/Gateway-v0.7.8-performance-336-portable-preview-windows-amd64.zip`.
`Start-Preview.cmd` starts its own fresh `preview-profile` and opens Gateway Home.
`Stop-Preview.cmd` stops that profile's verified runtime. `Preview.ps1 -Offline`
can create a new offline profile. ZIP contents exclude profiles, logs and tokens.
Updates are disabled at build time. Runtime state is local and separate from
installed Gateway profiles.

## Standard Bitmap I/O and target-query repair (2026-10-25)

The follow-up diagnosis and its measurement limits are recorded in
`INDEX-REPLAY-PERFORMANCE.md`. It found substantial page-open and page-write
delay, plus visible target queries that missed their cache on each new head.
Raw remained off throughout the read-only observation and disposable replays.

The final focused Go suite passed in 102.987 seconds: ordinal page integrity and
budget checks, concurrent Bitmap flush failures/cancellation/collisions,
target coverage, frozen Standard identity, admission publication fences and
exports. The collision regression first exposed a real Windows rename failure;
bounded temporary publication locks fixed it without suppressing I/O errors.
Failure and cancellation preserve the old head, drain every worker, and permit
fresh reopen/replay. Even a structurally tested Tx0 shared-page case retains its
native output; production parsing and eligibility were not changed.

The final dense two-block diagnostic retained both commitments, all four roots,
counts and unique page bytes. Repeated serial/concurrent measurements use
structural fixtures and have timing variation. They are not a speed guarantee
for the user's populated index. Every page Sync and existing commit/head fence
remains required.

All 19 focused browser cases and the help/category regression passed. Under
one-second head changes, target request starts fell from 13 in 13.02 seconds to
three in 12.37 seconds. The cap preserves immediate context invalidation,
single-flight requests, exact global versus sampled counts, trailing refresh,
and hidden-document cancellation. Spring geometry is unchanged.

Evidence directories are `build/qa-078/throughput-diagnosis` and
`build/qa-078/ui-poll-audit`. The portable candidate is
`build/portable-v0.7.8-bitmap-throughput`, with archive
`build/Gateway-v0.7.8-bitmap-throughput-portable-preview-windows-amd64.zip`.
An active-profile handoff must wait for a locator physical checkpoint to avoid
discarding substantial unpublished staging, then preserve the same profile,
track choices, run interval and latest human running/paused intent. No Raw or
Arm toggles are part of that handoff.
