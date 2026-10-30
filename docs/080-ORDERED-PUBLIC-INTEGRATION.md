# Public ordered integration

Status: in progress and public execution remains gated. Complete ordered
documents compile through the shared recipe/registry contract and native UTXO
loads that document. Private generation ownership, a selected-header/run-bound
consumer and historical state queries now have executable tests. Public
scheduler admission, guided ordered authoring, rebuild and maintenance still
need integration. This is not a finished preview or an accepted scope reduction.

The UTXO definition is now `recipes/builtins/utxo-state.json`, containing source,
origin, history, physical state contract, typed table declarations and the actual
ordered program. The old standalone program artifact is removed. Native and
custom copies compile through the same compiler/interpreter; IDs and names do
not affect execution identity. Canonical ordered JSON preserves typed integer
strings and source-key spelling. The existing native AST equivalence binding
and checkpoint/undo bytes remain unchanged.

The first declared physical contract is `bitcoin.utxo.cow.v2`: authenticated
Coin references keyed by outpoint plus per-transaction live counts. Individual
ordered reads/writes still derive the state. It does not dispatch an index by
ID. This first binding does not cover all ordered table types required for the
release. Additional native adapters and generic ordered state remain necessary.

Custom generations now reuse the existing native COW/fact/undo format below
`indexes/recipe-ordered-v1/<instance hash>/<semantic hash>/`. Each owns its facts
and recovery, binds its exact compiled definition/profile, and never publishes
into native UTXO or current-address state. An authenticated empty origin is
distinct from evaluated genesis; missing committed metadata fails closed.
Name-only changes reuse the semantic namespace; executable edits do not.
Private fact references are removed from transient cross-consumer prevout
relay, preserving authenticated value/script/creation data without creating
global pins into a private generation.

The coordinator prepares one owned ordered source per cohort. It verifies and
reparses the exact authenticated raw bytes to avoid trusting edited decoded
fields, and shares that result between ordered consumers. This currently costs
an extra decode, reported in source counters/time; it is not a one-decode claim.
No new storage engine/page-pack is introduced. Measure duplication and runtime
cost before choosing persistent cross-generation fact sharing.

Publication must hold selected-local-header, exact run/selection and saved
revision authority through an atomic generation CAS. Staging may happen outside
those locks. Do not acquire header/run/registry locks while holding native state
locks: query paths use the opposite order. A custom adapter should use a private
snapshot and an authority-first CAS publisher; a changed base must discard the
stage. Undo must preserve native per-transition crash recovery, including
intermediate disconnected ancestors, without calling them selected snapshots.
Queries reject disconnected heads and unavailable/incomplete generations. They
walk a committed tree after an exclusive physical-key cursor, with a bounded
wire response. Rows contain the logical table/key and exact typed value; uints
remain decimal strings. Ordering is coins then counts, each by physical key.
The token binds the immutable requested checkpoint, revision, instance, semantic
hash and query profile rather than the latest head. An unrelated append keeps a
retained historical token usable; changed prior selected headers reject it.
Only the native recovery window is queryable here (up to 288 transitions), with
an explicit unavailable response beyond it. This does not replace the separate
open requirement for historical DAG range snapshots/export.

Validation so far: the broad recipe/saved-recipe/UTXO/address/run suite passed in
318.923 seconds. Later private-consumer/catalogue/structural checks passed in
6.781 seconds; ordered historical pagination/range and existing sparse-output
checks passed in 12.031 seconds. Fixtures cover real genesis/block one, native
exact state/undo parity, private instance and semantic isolation, origin crash
recovery, stale CAS and missing metadata, finite/restart/pause/changed target,
synthetic local reorg recovery, raw-source ownership/no authority upgrade,
historical continuation across append and immediate selected-header rejection.
These checks do not establish full release acceptance or external consensus.

Required checks before enabling this capability include registry/guided round
trips, semantic edits creating new generations, genesis/prerequisite admission,
no hidden parent builds, finite/Live Out, pause/restart/resume, reorg/undo and
recovery limits, cancellation/publication races, selected-header query fences,
fact ownership/GC, source receipts, performance and visible Chrome acceptance.
Other built-ins, dependencies, historical range snapshots and complete release
acceptance remain tracked in the engine ledger.
