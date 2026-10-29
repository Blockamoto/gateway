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
tree. The Parcel selection wrapped around those blocks is synthetic, not a
claim that the Rug Me child is a Bitmap Parcel. Other movement, scheduling and
selection fixtures are synthetic. Browser checks exercise the actual UI assets
against isolated fixture APIs, including the Bitmap tab, address badges and
relationship states.

The broad native Go run was not a pass: its 15-minute aggregate timeout expired
in the existing locator integration fixture, and one graph-readiness test
failed in that run. That graph test passed when rerun individually. The
focused integration group, Go vet and UI checks passed. The Windows race detector was
not run because a supported C compiler was unavailable. No complete mainnet
Raw corpus, full-chain Parcel build, installer, updater, or Live Parcel run was
tested. Review the limits in [indexing](INDEXING.md) before interpreting
coverage or replayability.
