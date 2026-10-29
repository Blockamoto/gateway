# Gateway 0.7.9: Bitmap Parcels

Gateway remains sparse by default. Browsing does not authorize a scan or imply
complete coverage. Preserve existing profiles, saved experimental discovery data,
and versioned identities. The revised workflow publishes only verified Parcel
records and coverage; unknown or incomplete proof blocks progress without being
classified as invalid. No provisional Parcel scan, result, count, badge or export
is part of the user-facing index.

Ord parent relationships use the actual inscription and sat movement. Missing
chain, location, spend, value, witness or satpoint evidence remains unresolved.
Bitmap Parcel selection is separate from Ord provenance, uses the rules at
Blockamoto/bitmap main revision `335958311eb1c8ce4d14c48bc5c21d23c7a4b134`,
and binds decisions to the selected District interpretation and chain. Only
qualifying verified children compete for positions.

A reviewed finite run selects Bitmap Districts and exactly one proof route:
Raw blocks, TX locators, or resolved TXIO. These are alternatives, not a mandate
to build all three. A locator alone is not transaction bytes or proof. The
backend rechecks evidence within the admitted route and finite indexing scope.
There is no per-run validation-operation quota.
Dependencies can build from genesis while Parcel waits, and compatible saved
coverage and checkpoints are reused. Paired District and Parcel progress follows
committed validated completion; independently ready work may continue. Parcel
Live-follow is not enabled.

The dependency setup, genesis selection, compact start chooser and timeline
behavior are specified in INDEXING-WORKFLOW-PROPOSAL-0.7.9.md. The public
updater, releases, Render, main and paused 0.8 are outside this work. Full-chain
acceptance and Bitcoin consensus validation are not implied by focused tests.
