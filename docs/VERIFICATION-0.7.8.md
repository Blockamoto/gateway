# Gateway 0.7.8 portable preview verification

This is local development acceptance, not release or updater promotion.
The implementation branch is `v0.7.8`, created after fetching Gateway
`origin/main` at `7e1d1d9b731b689586309df8f287316a614e1934`.
The Bitmap source is the new `Blockamoto/bitmap` repository:

- Canonical rules: main `335958311eb1c8ce4d14c48bc5c21d23c7a4b134`,
  README blob `cbd10d75feac8636dd553c10ab52995e38e55514`.
- Chain draft: `577bae5e5810cb2389f1e02f322438df567f0089`,
  README blob `5a67f8b37a12bd0ce9968c67f8c8ab8d40488a27`.

The newer timeline/shared acquisition/outcome-retention implementation was
adapted from the read-only `timeline-bitmap-development` checkout. Its saved
profiles and uncommitted work were preserved. Source commits were not rebased
or replayed onto main. The saved project's stale local main was not the base.

## Deterministic and compiled checks

Go 1.27.1, Windows amd64, `GO111MODULE=off`, `CGO_ENABLED=0`:

- Canonical District/Parcel fixtures cover exact ASCII bodies regardless of
  declared charset; parsed MIME parameters/case; grammar and bounds; first
  qualifying reveal; restart/reorg; selected-parent proof, wrong/missing parent,
  exact retained preblock Sat state, and collected evidence remaining unresolved.
- Development chain vectors cover exact provisional bytes, slot activation and
  retirement, previous-final chaining, independent District/Parcel histories,
  simultaneous versus retained-outcome catch-up, empty versus missing results,
  unknown codec refusal, restart, Full/Pruned history and selected-branch recovery.
- Actual worker catch-up of both chains performs six steps with zero source
  reads, decodes or inscription extractions. Missing producer history does not
  initiate raw replay or advance a chain checkpoint.
- Bitmap legacy inspection and execution fences, timeline/shared runtime,
  raw coverage and deletion tests were exercised. Internal and packaging package
  tests and `go vet . ./internal/... ./packaging/...` pass.
- The actual portable executable passes `test-bitmap-standard-native-api.py`:
  pinned fixed definitions, unchanged legacy hashes, denied authoring/comparison
  and legacy/custom execution, cancellable source gaps, and no fabricated result.
- `test-bitmap-standard-browser.js`, index controller/timeline/transport helpers,
  and Browser Companion checks pass. The real application's browser check uses
  no mocked routes and checks read-only metadata, desktop/narrow layout,
  disabled updates, and absence of hidden indexing starts.

The initial broad Bitmap command exceeded its aggregate 300-second limit.
The 900-second run completed, revealing stale fixture assumptions about legacy
names/classification and the old default view; those fixtures were corrected
and the failed groups rerun. A large raw-coverage test's fixed 30-second wait was
brittle under Windows filesystem contention. Its unchanged isolated run passed;
the helper now has a two-minute absolute limit and a 30-second no-progress
limit, with exact 5,000-record, restart, deletion and reorg assertions unchanged.

## Live Bitcoin smoke

The isolated preview profile has ordinary Bitcoin networking enabled, with Core
access, serving and application updates disabled. It received fresh headers from
real Bitcoin peers and reached height 972831, then observed height 972832.
A request for block 972831 fetched 1,635,654 serialized bytes over Bitcoin P2P
and parsed 3,830 transactions. The application verified the P2P checksum,
header hash/chain, proof of work, Merkle root and witness commitment. Full Bitcoin
consensus validation is not claimed.

The same block was opened in the actual browser workspace. A separately explicit
canonical District run over heights 0 through 1 completed using the real runtime:
two source reads, two decodes, two inscription extractions, two committed evaluated
blocks. The checkpoint at 1 is
`00000000839a8e6886ab5951d76f411475428afc90947ee320161bbf18eb6048`.
This is a bounded smoke check, not a completed mainnet Bitmap index.

## Limits

The chain codec is deliberately developmental; unresolved upstream encoding
choices and fixed vectors are documented in [the codec guide](BITMAP-CHAIN-DEV-V1.md).
Same-block parent creation/transfers and unavailable/collected exact preblock
Sat evidence remain explicit parcel dependency gaps. A first-valid-0 origin change
requires explicit chain rebuilding. No full mainnet index, complete Go/race suite,
installer flow or released update delivery is claimed by this local preview.

Use `scripts/build-portable-preview.py` to reproduce the Windows portable ZIP.
Its manifest records source revision, dirty state and file hashes. It contains no
installer or update helper; update actions are disabled at build time. Local QA
profiles, logs, screenshots and runtime tokens stay outside versioned source and
the ZIP. See [launch/stop instructions](TESTING-0.7.8.md).
