# Ordered state and checkpoint-backed exports

Status: design and isolated transaction primitive. UTXO, Sats and Current
Addresses remain on their existing implementations; no checkpoint migration,
domain parity, scheduler integration or durable state adapter is claimed.

An ordered recipe declares typed keyed state tables, their initialization and
origin, the event order and the operations that read or update each table.
Steps compose bounded `read`, `put`, `delete`, branches, finite iteration and
reusable Bitcoin/Ord algorithms. A step may read changes made earlier in the
same source unit. Whole-index handlers and dispatch by recipe ID are not part
of this contract. The host must require genesis or a compatible authenticated
checkpoint and every next block; a later requested start is not a license to
invent empty preceding state.

At an atomic block boundary, the state adapter compares the expected prior
revision and selected-chain/dependency authority. It commits touched state,
first-touch before images, coverage, anchor, checkpoint and undo together.
Before images preserve the state before the *first* write even if the same key
changes repeatedly. Export net before/after deltas in deterministic key order:
a key created and spent in one block has no net change; a BIP30 replacement
retains both the old and new values. Undo exchanges those exact before/after
values against the expected current revision and authenticated logical checkpoint.
Physical revisions increase even during undo; the logical checkpoint moves back
to the prior anchor so several successive units can be undone without accepting
an older physical revision. The host must authenticate each receipt and logical
checkpoint identity and prevent reuse for a different state. Missing, corrupt or unavailable
state is not known absence. Resource or cancellation failures publish neither
completion nor an empty result.

An authenticated snapshot export is distinct from a sparse lookup. It binds
source instance and semantic profile, network/genesis, exact selected height
and hash, state commitment, completeness, counts/totals and access to the
required facts. A delta export binds the previous and resulting commitments,
the same network and chain transition, and its ordered net records. Current
Addresses consumes a complete UTXO snapshot and compatible deltas; its own
checkpoint binds the exact UTXO commitment. A missing or stale snapshot cannot
mean a zero balance. No dependency implicitly starts a UTXO scan. Retain exact
value/script facts and distinguish unsupported destinations. Existing UTXO's
288-transition undo and deeper-reorg rebuild boundary remain parity targets.

The constructor should offer typed table/key/value cards; origin and required
checkpoint controls; ordered read/write/branch steps; and separate snapshot and
delta export cards. A trace should show source events, values read, first before
images, final rows and net deltas. Display grouping remains independent of
execution dependencies. Runtime availability must be visible and the exported
JSON must encode each semantic choice; a diagram or schema is not an executor.

`recipe_state.go` is a backend-neutral bounded transaction builder. Its backend
must supply version-consistent reads and an atomic compare-and-swap of physical
revision, logical checkpoint and the
complete batch, including all durable metadata and evidence in production.
It does not implement a persistent adapter, authenticated snapshots, source
verification, block ordering, Bitcoin rules, Sat FIFO or crash recovery.

The separate experimental `recipe_state_durable.go` adapter provides a single
durable head, immutable content-addressed state pages and predecessor receipts,
contiguous genesis-origin blocks, and guarded forward/undo operations. The
caller supplies chain and admission authority; hashes and checksums detect
content damage but do not independently establish chain authority or protect
against a malicious replacement of the local head. Interrupted writes before
the head swap leave unreachable objects; an error during the head replacement
can have an uncertain outcome and requires reopening and checking the head.
The adapter is single-process and is not wired into execution.

Its fixed-length hashed tree keys retain and verify the original key to detect
collisions. They support keyed access but do not preserve original key ordering.
This prototype requires valid UTF-8 table and key strings because its receipts
encode those fields as JSON strings; values remain arbitrary bytes.
A future bounded, paginated traversal can enumerate authenticated pages in
hash order; an ordered snapshot requires a separate bounded order-preserving
index or external sort with its own authenticated continuation and limits. The
adapter exports neither traversal nor snapshots, and does not establish UTXO,
Sats or Current Addresses parity.
