# Gateway 0.7.9 development verification

This is a local development preview, not a published release. The baseline is
`origin/main` at `6b95dd893fae0057058f820f4816ca85e3d8a635`; the merge also
preserves the prior local 0.7.9 history. Bitmap rules are pinned to
`Blockamoto/bitmap@335958311eb1c8ce4d14c48bc5c21d23c7a4b134` and Ord parsing
and updater behavior to `ordinals/ord@b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414`.

Focused Windows Go tests cover parent encodings, multiple declarations,
satpoint movement and fees, same-transaction ordering, proof and scan
checkpoints, selected-chain changes, first eligible Parcel selection, pending
and invalid candidates, receipt reopening, explicit consent and concurrent
worker scheduling. The real Ord handbook Rug Me parent and child were checked
using verified mainnet blocks 818117, 818174 and 818182, obtained from
`mempool.space`; these local optional fixtures are not committed to the source
tree. A modified synthetic claim on the real block is rejected because it does
not have an authenticated complete extraction; the Rug Me child is not claimed
to be a Bitmap Parcel. Other movement, scheduling and selection fixtures are
synthetic. Browser checks exercise the actual UI assets
against isolated fixture APIs, including the Bitmap tab, address badges and
relationship states.

The broad native Go run was not a pass: its 15-minute aggregate timeout expired
in the existing locator integration fixture, and one graph-readiness test
failed in that run. That graph test passed when rerun individually. The
focused integration group, Go vet and UI checks passed. The Windows race detector was
not run because a supported C compiler was unavailable. No complete mainnet
Raw corpus, full-chain Parcel Live build, installer, or updater was tested. Review the limits in [indexing](INDEXING.md) before interpreting
coverage or replayability.

The current Parcel workflow requires one reviewed proof route and an explicit
indexing scope, without a per-run proof-operation quota. Experimental discovery data is retained but is not used for
new processing or user-facing results. Raw, TX ID Locator and IO Link are
alternative evidence routes; coverage alone does not establish a parent proof.
The IO Link route has synthetic audit fixtures, but a full mainnet audit and its
throughput have not been tested. A locally authenticated negative evaluation is
not an independently replayable proof, and an independent audit action is not
yet available.

The coordinated sweep and selected-chain tests use synthetic selected-header
fixtures. Do not infer full-chain acceptance from these focused checks.

The revised validation-only workflow has focused coverage for all three
configured route prefixes, the paired publication gate, blocked proof and
preservation of old experimental records, genesis continuation, Current UTXO
Arm and Live, and browser setup, chooser and timeline interactions. The broader
`TestIndexRun`, `TestTimelineRun` and `Test076AddressFinalReview` group passed.
The route-prefix fixtures are synthetic; they are not a full-chain Parcel run.
Missing exact source bytes trigger a bounded-backoff retry in the active run;
other missing evidence remains pending until relevant committed coverage changes.
Focused Live checks cover dependency admission, evidence lag and recovery,
pause/resume, finite Out, reorganization and a reorganization at head
publication. They use synthetic headers and an injected evidence prefix, not a
full-history proof-route build. A separate TXIO test checks suffix-only reads
after an authenticated completed audit and rejects reorgs or tampering.

The quota removal was checked with synthetic operation counts beyond the former
10,000,000 ceiling, coordinated validation past an old one-unit grant,
pause/resume, retained legacy metadata, and missing or changed evidence. These
checks do not establish full-chain throughput. Existing bounded proof-receipt
size and movement-hop limits are separate from the removed per-run quota.
