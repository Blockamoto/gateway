# Approved local 0.7.8 design pass

Baseline: local branch `v0.7.8`, performance-preview commit
`666fd37543e50676f422bd2b85dbeca9f317e781`. Implement and validate a separately
named local portable test copy. Do not push a branch, merge main, publish a
release/update, install Gateway or modify Gears/global navigation. Preserve the
existing performance work, verification, sparse consent and disabled Chains.
Leave the previous review preview usable while preparing the next copy.

## Track presentation

Keep the exact parent/child hierarchy and order: Blocks, Transactions, Addresses,
standalone Inscriptions, Bitmap. Replace detached collapsed child-name lists
with compact child rows: every label aligns with its own mini timeline lane.
Each child independently expands into its normal lane, controls and useful
status while its siblings can remain compact. Parent disclosure remains
coherent. This reveals the same track, without another hierarchy/index level.
Keep lane browsing, selection, independent Arm/Live and saved layout semantics.

Use one coherent shared header layout for parents, standalone and child rows.
Titles have their own space, help/dependency icons a fixed area at the right,
and expanded actions a separate consistent baseline. Validate the actual
approximately 202-pixel sidebar and narrow layouts. Context-short titles and
version badges are acceptable if full names remain accessible; titles must not
sit behind controls or be clipped by fixed row heights. Remove conflicting
Bitmap-specific shared-header overrides rather than adding another override.

Purpose help appears immediately on real pointer hover and keyboard focus,
supports click/tap pinning and dismissal, and remains above clipping layers.
Test actual hit areas and elementFromPoint, including compact and expanded
Bitmap rows. A question mark belongs on every track/group. Genuine other-track
dependency icons belong only on Current address outputs (matching complete
UTXO) and Parcels (matching Districts, conditionally exact retained Sat context)
and the relevant parent summaries. Own history, temporary block sources and
optional locators are context help, not additional mandatory indexes. Keep the
published Bitmap rules verbatim and separate.

Arm ON is clearly red like Record; OFF is muted. Preserve accessible pressed
state and distinct Live semantics. Disabled Chain retains a chain-link glyph,
with UI/API/job fences intact. Reorder grips sit directly on the track
background without circular/background button treatment in normal, hover or
drag states, while retaining usable hit areas and keyboard focus indication.
Help, expansion and dragging must not arm/start or select another item.

## One Bitmap parent target band

One target-coverage band belongs on the Bitmap parent row, available with
compact or expanded children. At the block axis it projects known selected
District claims onto their numbered blocks: `0.bitmap`, revealed at 792435,
appears at target block 0. Inside a block's transaction axis the same parent
band projects known Parcel claims onto transaction positions in that referenced
District block, one potential position per transaction. Child lanes retain
their normal reveal-block indexing progress from 792435. Do not duplicate
target bands inside Districts and Parcels or add an arming mode/index.

This is a view over committed selected results. Missing claims, incomplete
history, pending provenance, stale/reorg evidence and gaps remain unknown.
Query only the visible target range, aggregate at pixel-appropriate granularity
and cache against authenticated heads and chain context. Painting must not
enumerate every winner on every poll, scan/reindex, fetch blocks, build a
dependency or enable Chains. Clear the band when the hierarchy axis is not
supported, rather than inventing a mapping.

## New active Standard V1 identities

Use active public IDs and names consistently:

- `bitmap-districts-standard-v1`: Bitmap Districts Standard V1.
- `bitmap-parcels-standard-v1`: Bitmap Parcels Standard V1.

Saved paths/checkpoints and Parcel producer bindings authenticate exact IDs and
profiles. Create fresh Standard V1 stores; do not rename directories, alias or
rewrite saved checkpoints, silently normalize old profiles or retarget old
runs. Preserve both earlier canonical generations and frozen Chain producer
bindings under their original readable identities. Internal serialization
schema/version remains distinct from the Standard V1 product name. Preserve
the exact six/seven published rules, start 792435, blocator-only new references
and checked Parcel provenance. A new product name does not imply an upstream
standard change.

## Validation and delivery

Use meaningful focused Go/browser/compiled-runtime tests for compact and
independently expanded lanes, label alignment, no clipped controls, real
hover/focus/pinning, transparent grips/dragging, red Arm, chain-link glyph,
unchanged independent controls and no hidden indexing. Test target-vs-reveal
positions, Parcel transaction coordinates, stale/pending/unknown states,
bounded reads and cache invalidation. Verify frozen old identities/read-only
execution fences and fresh Standard V1 origin/provenance. Preserve lightweight
active polling and one coalesced terminal detail refresh.

Build a separately named updates-disabled portable directory/ZIP, verify its
exact executable and embedded UI, exercise live Bitcoin access, and leave its
fresh test profile running with a usable browser URL. Record revision, checks,
limits, ZIP and launch/stop paths. Installed/unrelated profiles remain untouched;
verify ownership and resolved targets before any disposable-test cleanup.
