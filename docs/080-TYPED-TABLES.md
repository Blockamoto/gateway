# General typed table state

`bitcoin.ordered` recipes can declare `history: "ordered"` and
`storage: "tables.cow.v1"`. Their typed program reads, puts and deletes keys in
1–64 named tables. Table names do not select a built-in algorithm. Values retain
their exact text, bytes, unsigned integer, boolean, object or FIFO interval type.
Missing values can be tested but cannot be stored as rows. Duplicate-key policy
is explicit program order: the last put/delete in a successful block wins.

The declaration's origin is the empty-state boundary, not necessarily genesis.
Every block from that origin to the current prefix must be explicitly admitted.
Selecting a later In without the required prefix reports missing lead-in; it
does not fetch earlier bodies. An origin above the selected tip is no work and
creates no generation. Live can reach that origin later. A completed prefix can
be reused without body reads. Block 1 needs neither a genesis body nor UTXO/Sat
state for a program that only counts raw output events.

Use **New state table draft** in the recipe editor, or import
`recipes/output-count-state.json`. The executable starter counts outputs from
block 1 using a read, missing-value branch, exact increment and put. Guided
controls edit the origin, declarations, expressions and nested instructions.
Renaming a table updates its instruction references; a referenced table cannot
be removed. Apply updates the JSON draft. Saving, arming and editing start no
indexing; Play reviews the same saved program before Start.

The source is authenticated raw transaction events. Physical native fact-page
references are not fields in this contract. Independent emits, outgoing exports
and incoming dependency lookups are deliberately rejected until their separate
typed coverage/publication contracts are implemented. This is arbitrary keyed
state, not a claim that broader dependency integration is complete.

## Durable state and recovery

The backend uses the shipping native immutable COW page codec with checked path
access, rather than the staged-state or page-pack experiments. Small typed values
up to 32 KiB encoded stay in COW leaves. Larger values use SHA-256 addressed
immutable blobs. Net mutations produce a second COW tree of first-before/final-
after references; a third tree counts large-value references. It does not write
a whole-index map or cap the number of keys at the old 4,096-row fixture limit.

Each successful block publishes an owned point containing state, journal and
blob-pin roots, exact entry/change counts and its predecessor. The atomic
`table-head.json` receipt is the sole visibility boundary. Staging cannot publish
coverage. Final publication rechecks the prior receipt/point under the selected
header, admitted run, registry and generation CAS fences. Missing ownership or
a lost committed head is corruption, not empty state.

The private root is
`indexes/recipe-tables-v1/<instance SHA-256>/<semantic fingerprint>/`.
It retains the current point and up to 288 previous consecutive points. Reorg
recovery selects an authenticated retained ancestor and restores its roots. If
the first transition is still retained, its known empty origin seed is also a
recovery position; this never claims block coverage. Beyond that retained window
an explicit rebuild is required. Reviewed rebuild archives the owned generation
and starts at the declared origin. It changes no native index or other recipe.

Collection holds the generation work/commit locks, validates required metadata,
large values and reachable COW pages before value deletion, and preserves all
retained state/journal/before-and-after pin roots. Old unreferenced points and
blobs have a one-hour grace period. Archives are outside this collection root.

## Reading committed state

The existing local recipe output endpoint returns exact typed rows keyed by
`[table, logical key]`, in stable physical hash-key order. State-entry and changed-
key counts are decimal strings. A cursor requires the token for that exact
revision, semantic fingerprint and retained checkpoint. Historical pages remain
valid after unrelated appends while their checkpoint is retained. Unbuilt,
evaluated-empty, disconnected, corrupt and expired snapshots remain distinct.

Pages stay below the 1 MiB response budget. A larger row returns a pinned download
link and an optional continuation cursor. Consecutive large rows are delivered
individually. The complete typed JSON attachment is staged and authenticated
before attachment headers, with content length and SHA-256. Damage fails before
payload exposure. Downloads neither build state nor start a source writer.

## Evidence and limits

Focused tests cover inline/blob state, shared blob references, read-own-writes,
net-zero changes, aborts, 4,100 distinct keys, owned CAS, metadata corruption,
retained-root recovery, maintenance deferral, block-1 public admission/reuse/
archived rebuild, future-origin no-work and Live arrival, immutable historical
queries, empty state, cursor scope, forks/lost heads, and consecutive >1 MiB
typed-row downloads with damaged-value rejection. Existing ordered/Coin and
saved/native Sat integration checks remain part of the focused regression.

The small 4,100-key storage fixture takes several seconds on this Windows
workspace; it is not a full-chain throughput or memory benchmark. Per-block
state work remains bounded by the existing operation/touched-key/aggregate-byte
budgets. One typed value has a 16 MiB logical and 32 MiB encoded budget. Those
limits fail evaluation without publishing a partial block. Large-row downloads
currently materialize one bounded typed value and use temporary disk, rather
than claiming constant-memory arbitrary-sized rows.

Source-identical headless DOM checks cover the actual starter, reference-
preserving rename, table declarations, keyed writes, exact integers, Apply and
wide/narrow layouts without network work. These are code QA, not visible Chrome
acceptance or evidence that the complete 0.8.0 preview is ready. Remaining
built-ins, wider typed dependencies and final preview acceptance are tracked in
`080-PARITY-MATRIX.md`.
