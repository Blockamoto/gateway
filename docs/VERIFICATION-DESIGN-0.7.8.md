# Local 0.7.8 design preview verification

This pass starts from the local performance preview at
`666fd37543e50676f422bd2b85dbeca9f317e781` on branch `v0.7.8`. It is a separately
named local test copy, with updates disabled. No branch push, main merge,
release/update publication, installation, Gears work or global navigation
change is included. The previous performance preview remains separate and
usable. The exact final source revision and executable digest are recorded in
the portable manifest.

## Delivered design

The hierarchy remains Blocks, Transactions, Addresses, standalone Inscriptions,
Bitmap. Every compact child has its own aligned label and timeline lane.
Independent disclosure reveals the same track's normal lane, action baseline
and status, without expanding its siblings or adding another hierarchy level.
Parent disclosure expands/compacts its children coherently. Saved order,
selection/navigation and separate Arm/Live consent remain intact.

One shared 202-pixel header geometry owns title space, right-side purpose and
dependency controls, and the expanded action baseline. Conflicting Bitmap
header overrides were removed. Arm ON uses red record styling; OFF is muted.
Disabled Chains retain a chain-link glyph and execution fences. Reorder grips
are transparent in normal, hover and drag states, with keyboard focus outlines.

Purpose help is available on every group/track. Other-track dependency icons
remain restricted to matching UTXO for Current Addresses and matching Districts
plus conditional exact earlier Sat evidence for Parcels, with parent summaries.
Published Bitmap rules remain verbatim and separate. Real pointer hit tests
cover Bitmap parent purpose/dependencies, District purpose and Parcel
purpose/dependencies, compact and expanded, at the actual 202-pixel header and
narrow iframe sizes. Long cards choose a side with available space and scroll
within it, keeping their own trigger unobstructed.

## Parent target band

`GET /api/v1/bitmap/target-coverage` accepts a visible `from`/`to`, `pixels`
(1–1024) and `axis=blocks|transactions`. Transactions additionally require
`district`. Only the active Standard instance for the axis is accepted.

The single parent band maps known selected Districts to their numbered Bitcoin
blocks, and known selected Parcels to transaction positions inside the referenced
District block. Child lanes retain reveal-history indexing progress from 792435.
For example, `0.bitmap` at reveal `953i0.792435` marks target block 0. Unsupported
deeper axes clear the band. Unmarked positions remain unknown; the band makes
no invalid/unclaimed/current-ownership claim. `query_complete` means the bounded
read of saved selected results completed, not that every target's claim history
is complete or that a missing winner is absent forever.

The view radix-prunes each visible pixel range and validates selected winner
membership, completed reveal outcomes, source anchors and Parcel receipts. It
examines at most four winners per bin, at most 4096 overall, with at most 256
range/evidence pages, 16 MiB range/evidence bytes, 128 source anchors and a
two-second query deadline. Small requests retain enough fixed page allowance
for deep winner/outcome paths. Counters exclude fixed snapshot authentication.
Quota truncation leaves the remainder explicitly unknown.

The server caches at most 16 immutable summaries by authenticated head, profile,
selected-chain context, view and limits, and rechecks current metadata before a
cache hit. Complete and deterministic quota results may be cached; transient
I/O, missing/corrupt evidence, anchor failures and deadlines are retryable with
the same head. Frontend requests are coalesced/debounced, visible-range only,
include fresh durable run-head observations, and revalidate metadata after five
seconds. Failure clears old positives and uses bounded retry. Painting starts
no index, scan, reindex, dependency, Chain or block fetch.

## Standard V1 identity transition

New active IDs/names are:

- `bitmap-districts-standard-v1` — Bitmap Districts Standard V1.
- `bitmap-parcels-standard-v1` — Bitmap Parcels Standard V1.

These use fresh stores. Both older canonical generations retain their original
factories, hashes, origins, producer bindings and explicit readable IDs, with
execution disabled. No directory/checkpoint rewrite, alias/resume or silent
profile normalization occurs. Copied old checkpoints fail authentication under
new IDs. Restored old runs retain old IDs, cannot resume execution and remain
cancellable without creating Standard state.

Standard V1 product naming is independent of internal profile generation
`gateway-bitmap-standard-profile-v3`. Checkpoint schema 1 and blocator receipt
schema 2 are unchanged. The exact six/seven pinned published rules, start
792435, own-envelope parsing, blocator-only references and checked creation-time
Parcel provenance are preserved. New Parcels require the exact new District
instance/profile/revision; old producer coverage cannot satisfy that binding.

## Focused validation

- Identity/provenance/release/layout suite: PASS, 44.141 s, including all four
  frozen profile hash vectors, wrong-origin/copy rejection and safe old runs.
- Combined Standard, frozen-generation, disabled-Chain, batching guard, progress,
  provider, layout and embedded-asset checks: PASS, 28.153 s. `go vet .` PASS.
- Eight target-band tests: PASS, 154.438 s. A normal-writer 130-height deep radix
  setup accounts for 142.26 s of that time; it is not query latency. Tests cover
  per-pixel quotas, unrelated-branch pruning, cache isolation/repair retry,
  stale/reorg/snapshot changes, pending/empty unknown states and read-only HTTP.
- Nonempty Parcel correctness uses an explicitly synthetic structural retained
  Sat pre-context, followed by normal Sat/District/Parcel appends and checked
  receipt creation. It marks transaction 1 of District block 792435, with 0/2/3
  unknown. No winner/receipt is injected, and this is not a mainnet consensus or
  performance claim.
- Production-asset browser checks cover exact hierarchy, aligned compact rows,
  independent disclosure, fixed title/icon areas, real pointer/focus/pinning and
  dismissal, transparent grips/dragging, red Arm, chain-link glyph, narrow layout,
  cache refresh/retry, hierarchy-axis changes and unchanged independent controls.
- The existing lightweight active/idle/hidden polling, request coalescing and one
  terminal full-detail refresh tests remain passing, together with Home, Sats,
  UTXO, workspace, gestures, rules, scrollbar and shell navigation regressions.

## Exact executable and data checks

The final executable's compiled UI, fresh Standard indexing, frozen V2 reads,
peer access and portable manifest/ZIP equality are checked separately. Evidence
is under `build/qa-078/design-pass` and `build/reviewed-qa/`.

The authentic acceptance fixture is Bitcoin blocks 792435–792442: 17,782 actual
transactions and selected District targets 0, 3 and 7. All transactions including
Coinbase are retained. New Standard outcome blocators agree with frozen V2 on
this fixture; their generation-bound commitments intentionally differ. The full
transaction commit payloads remain byte-equivalent to the previous preview.
All 24 new per-height records must match between local and peer-fed runs.

This real eight-block sample has no Parcel candidates/winners; nonempty Parcel
projection is validated by the separate normal-writer synthetic fixture above.
Local replay timing is not peer throughput. Concurrent QA, OS cache, peer
availability and machine load vary; no new universal/live speed multiplier or
full modern 336-block performance claim is made. The existing 336-block batching,
bounded staging and durable publication guarantees remain covered by focused
regressions and physical-operation counters.

## Final exact-binary evidence

Executable SHA-256:
`fac9b7667065f622f0ec7a7b47e999e4bfbd746e2e56d0c47aa2aee7c5d2b9a2`.
The separately packaged executable must reproduce this digest.

| Check | Result |
| --- | --- |
| Fresh Standard V1 local archive run | PASS; eight blocks, 17,782 transactions, 6.308 s |
| Fresh Standard V1 Bitcoin-peer run | PASS; eight blocks, 17,782 transactions, 13.499 s |
| Local/peer durable equivalence | All 24 complete per-height records identical |
| Prior-preview transaction equivalence | All eight full transaction commit payloads identical |
| Frozen-generation claim comparison | Winner blocators match V2; new Standard profile/commitment identities remain distinct |
| Actual frozen V2 profile opened by this executable | Explicit reads authenticate; all saved evidence files/heads unchanged; execution locked; no Standard stores created implicitly |
| Actual compiled desktop/narrow UI | PASS; real Bitmap parent/District/Parcel pointer targets, compact/expanded rows, 202px headers, red Arm/OFF, chain links and transparent grips |
| Actual saved-result parent band | PASS; target winners 0/3/7, child reveal coverage 792435–792442; expanded District and compact Parcel visible together |

Both eight-block runs recorded one locator flush, 256 bucket appends, 512 bucket
Syncs, 513 receipt replacements, one active/head write, eight commit writes and
17,782 locator records. Source/decode/extraction counts were eight. The peer
profile received only selected headers; no source body/archive was copied or
configured, Core/mounts were disabled, and Raw stayed Off. Timings are acceptance
samples under varying machine/network load, not a controlled speed multiplier.

Compiled UI tests use the actual server and embedded assets without route/data
mocks. An isolated fresh profile explicitly toggles District Arm ON/OFF through
exactly two prepare calls to verify styling; no run starts. Disclosure/help
causes no hidden selection or consent changes. The saved-result parent-band
test sends no index POST or block-body fetch and preserves run/jobs/heads. Both
owned runtimes stop cleanly. Real iframe tests exposed a long help card covering
its own trigger; the corrected chosen-side placement passes a 390×460 iframe
case with scroll, pinning and Escape. The initial failure is retained.

Evidence paths:

- `build/qa-078/design-pass/sealed-standard-8`
- `build/qa-078/design-pass/sealed-peer-8`
- `build/qa-078/design-pass/sealed-legacy-v2`
- `build/qa-078/design-pass/sealed-data-equivalence.json`
- `build/reviewed-qa/design-pass-compiled-popover-fixed`
- `build/reviewed-qa/design-pass-actual-targets-framed`

## Portable use

The new directory is `build/portable-v0.7.8-design-pass`; the archive is
`build/Gateway-v0.7.8-design-pass-portable-preview-windows-amd64.zip`.
`Start-Preview.cmd` opens Gateway Home using its own `preview-profile`.
`Stop-Preview.cmd` verifies and stops only that profile's runtime. The ZIP uses
an explicit file allowlist and excludes profiles, logs and runtime tokens.
Installed and unrelated profiles are untouched.
