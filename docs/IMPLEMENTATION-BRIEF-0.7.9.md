# Gateway 0.7.9: Parcels

Gateway remains sparse by default: browsing a block or inscription does not
authorize a chain scan or imply complete index coverage. Build on the released
0.7.8 Districts behavior and preserve existing profiles and index identities.

Ord parent relationships are evaluated per declared parent using the actual
inscription and the movement of its sat into an input of the child reveal.
Missing chain, location, spend, value, or satpoint evidence is unresolved, not
invalid. Keep rejected declarations separate from verified relationships and
deduplicate children by inscription identity. Explorer results describe known
coverage; declarations alone cannot establish a complete child list.

Bitmap Parcel selection is a separate decision from Ord provenance. Use the
rules at Blockamoto/bitmap main revision
`335958311eb1c8ce4d14c48bc5c21d23c7a4b134`, and bind each decision to the
selected District interpretation and verified chain. Only qualifying verified
children may compete for a position; unresolved or invalid declarations do not
reserve it. District and Parcel coverage, verified child counts and selected
Parcel counts are distinct.

Parcel work may run with other armed consumers when the evidence needed for
each decision is ready. Full retained blocks, complete transaction locators,
or resolved IO Links are alternative routes to locating evidence, with verified
transaction bytes or retained Values facts supplying the amounts required for
sat movement. A locator is not itself transaction bytes or proof. Report exact
gaps and use the existing bounded planning and consent workflow; do not silently
scan the chain. Commit only authenticated coverage, recover safely after restart
and selected-chain changes, and reuse shared block decoding where possible.

Show Bitmap addresses only when backed by saved recognized Bitmap evidence.
Preserve block-level Bitmap navigation and the existing inscription viewer.
Keep legacy saved records readable and do not merge locator and TXIO stores.
This work does not change the public updater, publish a release, or resume 0.8.

Implementation scope: the executable prerequisite route is complete retained
Raw inventory from genesis to the finite requested end, with explicit read
budget and resumable scan checkpoints. Locators and IO Links accelerate
individual lookups but are not independent full admission routes yet. Positive
detached receipts are limited to 128 blocks, 64 MiB and 128 movement hops;
larger or ambiguous cases remain pending. Long negative checks are recorded as
profile-authenticated local evaluations, not independently portable proofs;
independent replay needs the retained Raw interval and is not automatic.
Parcel Live-follow is not enabled. These boundaries do not imply global child
enumeration, full-chain acceptance or Bitcoin consensus validation.
