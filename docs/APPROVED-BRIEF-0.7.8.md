# Gateway 0.7.8 approved implementation brief

This is the final approved scope, superseding the earlier read-only hold and the
previous local preview acceptance. Work stays on the local Gateway `v0.7.8`
branch. Do not push or merge Gateway main, publish a release/update, install the
application, alter existing user profiles, or replace an existing running preview.
Build and leave a separately named portable preview running after live verification.

## Canonical Bitmap

Districts and Parcels are fixed canonical sibling tracks under Bitmap. Both begin
at height **792435** in planning, status, and timeline coverage. Keep legacy
checkpoints and evidence readable under their original identities; do not relabel
old genesis-based state as the new protocol profile. Disable custom authoring,
editing, comparison execution, and legacy execution. Preserve Bitcoin content
navigation depth and its two-stage focus behavior.

Display the published main-branch District rules verbatim as six ordered entries,
and Parcel rules verbatim as seven ordered entries. Put implementation limits
outside those rules. Ownership is not an additional canonical rule. New Bitmap
persistence identifies inscriptions and outcomes using blocators and Bitcoin block
anchors; do not persist duplicate conventional inscription IDs or outcome ID arrays.
A Parcel child relationship must be evaluated from actual parent/spend/Sat evidence,
never inferred from a parent declaration alone. Offer arming the normal existing
Transaction Locators track alongside Bitmap; do not create a private child index.

Pinned canonical source: `Blockamoto/bitmap` main
`335958311eb1c8ce4d14c48bc5c21d23c7a4b134`, README blob
`cbd10d75feac8636dd553c10ab52995e38e55514`.

## Terminology and standards repository

Use lowercase `blocator` and `blocators` in active code, UI, and documentation,
without changing positional grammar. Rename private
`Blockamoto/bitcoin-positional-system` to `Blockamoto/blocators` with privacy,
settings, history, and permissions preserved. Put standards content changes on a
separate review branch and leave main unchanged. Preserve authenticated historical
bytes and frozen profile identifiers when cosmetic edits would alter identity;
explain their legacy spelling explicitly.

## Chains disabled

Both chain controls are greyed out for this version. Arm, Live, Start, Resume,
restored automatic work, and final publication must refuse chain execution.
Saved evidence remains readable. Do not present the old provisional codec as an
active methodology or introduce a replacement algorithm. Future chain retention,
reproduction choices, and index/outcome hashes remain outside scope.

## Shared complete-block ingestion

Every operation obtaining a verified complete block, including navigation,
generic resolution, internal/dependency fetches, and local/cache sources, offers
its authenticated facts to every already-armed capable track. Raw retention is
independent. Do not auto-arm, backfill gaps, or claim contiguous coverage from
isolated blocks. Stateful indexes may consume only their exact next block with
matching prior state and existing consent/publication guards.

Wire sparse positive TXIO facts, independent Values and Scripts, Historical
Outputs, and Lean/Full inscription occurrences to truthful coverage, query, and
GC ownership. Explicit shared runs selecting only Values/Scripts with TXIO links
off must work without arming links. Document and test capability metadata for
all fifteen UI lanes. Historical Sat ranges retain their existing explicit
retention option; this is separate from future chain-retention work.

## Explorer behavior

- Grey relationship actions when usable evidence is absent. Immediate hover and
  keyboard-focus help names the missing index or coverage. A saved positional
  TXIO link or original prevout plus usable transaction location is sufficient.
  Positive saved native spend evidence and configured Core RPC are valid routes
  when exact creation identity is established. Missing spend data is not proof
  of unspentness. Never imply historical Gateway-peer hints or Core txindex
  through ordinary Bitcoin P2P.
- Keep the existing flow diagram. Show immediate index/BTC/sats/address or actual
  script-type details. Render known outputs before unrelated inputs resolve;
  stripes mean unresolved values. Derive supported addresses locally from scripts
  without an Address index. Never infer a fee from unresolved inputs.
- Put transaction count in the transaction-list heading. Expose readable coinbase
  input byte runs and exact hex without miner/pool attribution. General OP_RETURN
  decoding and attribution remain future work.
- An unresolved output-local Satpoint still shows its blocator, offset, known
  amount, script/type/address, and output links. Global Sat identity remains
  unresolved and no hidden lineage starts. A proven identity opens its normal
  `.sat` page; mathematical numbered-Sat properties remain available without an
  index.
- On a browsed block, the Bitmap tab looks up that district's valid inscription,
  rather than assuming its reveal occurs in the browsed block. Reuse the existing
  inscription detail for both tab and direct Bitmap navigation. Preserve an
  explicitly selected Bitmap tab across block changes, clear stale inscriptions,
  and explain unavailable index coverage.

## Acceptance

Run meaningful backend and browser checks for the changed behavior, build a
separate portable executable/ZIP and profile, verify live Bitcoin acquisition
with that final executable, and leave it running with exact URL/start/stop paths.
Record what was actually checked and any limits. Broad inscription/recursion
redesign, miner attribution, future chain retention/reproduction, and new
index/outcome hashes are excluded.
