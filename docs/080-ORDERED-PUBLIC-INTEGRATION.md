# Public ordered integration

Status: the first ordered Coin/count contract is connected to public recipe
save, guided authoring, reviewed Start, finite/Live runs, restart, output queries,
rebuild and maintenance. The full release remains in progress: other built-ins,
general ordered contracts, broader dependencies and Chrome acceptance are open.
This is not a finished preview or an accepted scope reduction.

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

Native and custom physical facts have independent ownership. The prepared fact
cache now binds its physical root as well as retention choices and block anchor.
Private generations bypass that cache so a cohort does not retain another
whole-block fact-reference map per custom track. A mixed native/custom test
caught the old single-owner cache assumption before this public path passed.

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

The public coordinator tests passed in 13.753 seconds after the cache fix:
two different ordered programs and a transaction recipe share two source reads
and two additional owned-source verification decodes with a native UTXO build;
their saved physical UTXO bytes match. A missing prefix blocks without creating
storage, and a saved prefix is reused without a source read. A reviewed rebuild
preserves the prior generation; Live follows real block one, undoes a synthetic
local-header replacement, pauses and resumes after process recreation while
retaining the exact admitted historical definition. The broad recipe/saved-recipe/UTXO/address/run suite, including stale rebuild
review and the pinned metadata fixture, passed in 499.889 seconds. Editor,
ordered editor, saved tracks and workspace JavaScript checks and the production
build also passed.

Maintenance reuses native COW marking, retention and output-fact sweeping at
committed completion and every 128 processed blocks. It holds the custom work
slot and generation ownership, without taking native UTXO mutexes. Pins include
current Coins and retained undo before-images; missing owned pages stop cleanup.
Synthetic tests preserve a spent Coin through undo/reapply, remove old unowned
facts, leave native-root facts intact, and check cancellation/missing pins.

Rebuild is staged through Saved recipe tracks for a later reviewed Start and
requires an explicit range beginning at genesis. The plan binds mutable
generation metadata; changed publication metadata invalidates review. A durable
job intent archives the entire old generation, including damaged metadata and
its own facts, under the instance's `archives` directory. Resuming the same job
does not archive the replacement prefix. No recursive deletion or legacy data
migration is performed. Rebuild archive size is retained until explicit user
management, independently of the replacement's bounded recovery state.

Settings' executable UTXO recipe can be copied into a custom draft. Guided
controls preserve the full actual program and expose typed expressions, source
fields, variables, functions, literal/object values, branches, bounded ranges,
state reads/writes/deletes and emitted prevouts. Instructions can be added,
reordered and removed. The editor lazily expands nested controls, keeps uints
as exact decimal strings, requires Apply before validation/save, and preserves
unsupported raw forms. Viewing/copying does not save or start a run.

Remaining acceptance includes measured public-source overhead, visible Chrome
exercise, wider ordered contracts/dependencies and the complete built-in matrix.
Historical state queries here retain native recovery limits; full historical
DAG range snapshots/export remain separate open work in the engine ledger.
