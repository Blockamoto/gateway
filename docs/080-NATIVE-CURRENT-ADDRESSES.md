# Current Addresses JSON and checkpoint dependency

This is an intermediate engine increment. The native path is integrated; public
custom checkpoint scheduling, queries, guided controls, rebuild and maintenance
are still being connected. It is not the complete 0.8.0 preview.

The current and legacy-payment documents in `recipes/builtins/address-state*.json`
declare an exact UTXO `coins` dependency with `snapshot_delta` coverage. Their
common typed program classifies each complete source coin and emits either a
member or an exclusion, preserving the exact source identity, script and sats.
Native full traversal and incremental undo/redo now run those documents through
the same event interpreter used by custom typed projections. The existing COW
reverse rows, counters, source recovery and native head publication remain.
The previous mutation loop is retained only as a differential test oracle.

Native schema 1 is explicitly bound to both actual documents, the typed source
contract and existing physical schema by bundle
`e2fb15cd1832f22c8238421351329541515d1ff653be9615133d0fb7ce6f03be`.
Unknown bundles require explicit migration. Existing native data and UTXO
semantic identities are unchanged.

The dependency resolver is read-only: it accepts only the pinned built-in UTXO
document or an exact saved `bitcoin.utxo.cow.v2` generation. It neither creates
nor starts a parent. An unbuilt origin cannot substitute for evaluated empty
genesis. Historical points must remain in the parent's checked recovery chain;
an unrelated append can preserve them, while a fork, lost head or expiry fails.

Custom storage uses the existing address COW codec under
`indexes/recipe-projection-v1/<instance hash>/<semantic>/`. Its owned marker,
immutable checkpoint metadata and atomic receipt head are separate from native
addresses and parent coins. The head retains up to 289 selected checkpoints.
The final publication guard must supply selected-header, run and registry
authority, followed by exact parent receipt and generation CAS checks. Staging
alone exposes no checkpoint. No page-pack or private staged-state backend is used.

Initial traversal validates the parent's live coins and saves a pinned cursor
every 512 outputs. A matching restart resumes it. Later work reuses the native
checked first-before journal transitions for catch-up, rollback and replacement
branches. Each child owns immutable fact hard links, with a checked synced-copy
fallback across volumes, so removing the parent's original fact name cannot
invalidate the child. Saved coin writes/GC and projection staging share a work
slot; native sources also hold their writer lock while staging, released before
the final authority fence.

Focused materialization/fact checks passed in 8.293 seconds, including physical
oracle equality, deltas, rollback, replacement branches, stale work, durable
cancellation/resume, distinct current/legacy policies, corrupt or incomplete
facts and parent fact unlink. The combined Current Addresses, native mapping,
dependency identity and new projection suite passed in 89.057 seconds. These
are bounded fixtures, not full-chain replay or consensus validation.

The declared-saved-source check passed in 6.812 seconds with an evaluated empty
custom Coin generation beside a nonempty native generation. The catalogue/API,
native identity and metadata follow-up passed in 11.475 seconds. Materialization
and catalogue checks also ran after removing completed traversal manifests.
The production build and all four recipe/workspace JavaScript checks passed.

## Query and collection work in progress

The read-only projection adapter now pages stored member rows by physical
script/member key. Its token binds the exact saved revision, semantic identity,
parent namespace and immutable child checkpoint. Empty genesis is distinct from
an unbuilt projection. A later append preserves historical pages; changed local
headers, missing parent heads, expired checkpoints or a changed parent receipt
reject them. Reads verify source membership and use the child's owned facts.
Exclusions are retained as exact counts/sats, not as individual output rows; the
response states that explicitly. The public output route remains gated with the
rest of custom checkpoint execution.

Each complete child checkpoint now owns an independent copy of the source's
native fact-pin COW root. Only changed branches are copied, and facts already
owned by a prior pin root are reused. COW page bytes use separate files because
the existing native collector uses per-owner page timestamps. Immutable output
fact payloads still use hard links with the checked copy fallback. Initial or
resumed traversal restores complete fact ownership before publishing, including
previously checked exclusions.

The private ownership profile is now `gateway-private-address-generation-v2`.
An earlier internal v1 marker cannot be silently adopted; it requires a reviewed
rebuild. This ownership change affects only the unreleased private projection
namespace, not the native schema, its equivalence bundle or existing UTXO data.

Maintenance authenticates all retained snapshots and staged traversal ownership
before collecting. Distinct COW pin/summary pages are enumerated once across
related roots, avoiding repeated whole-index walks for 289 checkpoints. The
existing bounded native page collector, fact grace period and checked deletion
paths are reused. Every retained summary, member and pin root is marked; partial
member facts remain pinned; unreachable snapshot metadata can expire. Source
names and archived generations remain independent. Damaged required pins or
facts stop collection before any fact deletion.

The focused projection/graph/mixed-dispatch suite passed in 21.230 seconds.
Maintenance and incremental COW copy checks passed in 11.917 seconds, including
historical facts, obsolete branch reclamation, partial traversal with exclusions,
corruption deferral, copied-page timestamp independence and cancellation.
Definition graph validation also accepts independent ordered writers in a mixed
sparse DAG run, and distinguishes native checkpoint scope from saved writers.
Actual parent storage is still resolved separately; a graph proves no coverage.

The broader dependency, DAG, lineage, selected ordering, public ordered-run and
projection regression passed in 122.557 seconds. A subsequent focused check
passed in 14.578 seconds after adding direct recovery from an owned retained
selected child ancestor, even when source collection has removed disconnected
branch journals/pages. If neither an owned selected ancestor nor the required
source recovery transition survives, a reviewed rebuild remains necessary.
This preserves the explicit snapshot/recovery boundary instead of fabricating
an older view. The production build passed before that final recovery check.

Public saved copies currently validate and resolve with
`source_scheduling_unavailable`; they remain gated until the complete consumer,
query, guided and maintenance lifecycle is integrated and checked.
