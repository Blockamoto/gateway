# Typed parent table checkpoints

Saved ordered table recipes can declare read-only inputs from another saved
table recipe, or from the enabled native Standard District `winners` and
`candidates` tables. A dependency pins its alias, instance, exact revision,
semantic fingerprint and table name, with `coverage: "snapshot"` and
`type: "typed"`. Table declarations supply this checkpoint surface; they do not
need a separate scalar export declaration.
Native winner keys are canonical unsigned decimal district numbers; candidate
keys are canonical inscription coordinates. Saved parent tables use their
declared logical text keys.

Each child transition reads the parent's complete retained checkpoint at that
same Bitcoin height and block hash. A missing key at a complete checkpoint is
typed `missing`. Missing, expired, forked or incompatible checkpoints cannot
become an empty table. Only `read` can target a dependency alias; `put` and
`delete` remain confined to the child's own tables. There are at most sixteen
incoming aliases, with the existing operation, touched-key and aggregate byte
budgets shared between local state and imported reads.

Explicitly selected saved parents dispatch before their children regardless of
the request's display order. A saved-only parent remains read-only. The child
does not initialize or start its producer. Native parents retain their existing
earlier dispatch order. Planning names the required checkpoint without claiming
it exists; execution waits for unavailable coverage.

Every imported point read, including a known absence, becomes an owned immutable
fact containing the binding, checkpoint receipt, logical key and typed value.
Facts have checksums, small inline records and content-addressed large blobs.
The child checkpoint binds a cumulative COW fact root and the current parent
receipts. Parent collection therefore cannot erase the evidence used by an
already committed child. These are authenticated local checkpoint facts, not
detached Bitcoin consensus proofs.

Publication checks saved revisions inside the existing selected-header/run/
registry fence and rechecks parent checkpoints inside the generation fence.
A short native head-publication lease closes the District receipt-check/CAS
race. The source reader starts no writer or repair operation. Child recovery
selects its retained state and imported-fact roots together; reviewed rebuilds
archive the whole previous generation. Maintenance authenticates required fact
roots and blobs before deleting anything, marks their shared pages and retains
reachable imported blobs. Unreachable blobs use the existing one-hour grace.

Existing recipes without dependencies keep their original semantic and point
encodings: the new fields are omitted. Incoming table recipes additionally bind
the owned-fact interpreter profile in their semantic fingerprint. Native
District rule identities, physical rows, pages and checkpoints remain unchanged.

The guided editor supports aliases, exact saved revision pinning, installed
built-in bindings, nested read-reference renaming and stale-response fencing.
Dependency aliases appear in read choices and cannot appear as write targets.
Referenced declarations cannot be removed. These edits start no work.

Focused tests cover public topological execution, saved-only parent reuse,
owned values and absences, child queries after producer deletion, native and
historical saved reads, withdrawn-parent publication rejection, archived
generations, large facts and corruption-aware collection. Guided unit and
headless real-DOM checks cover pinning, aliases, read/write choices and narrow
layout. Full preview acceptance and the Parcel proof-source composition remain
separate open work.

The wider table/DAG/catalogue/District suite passed all 62 checks in 81.246
seconds. After the final snapshot lease, the focused compatibility and lifecycle
set passed in 25.368 seconds; a dedicated busy-snapshot publication check passed
in 1.251 seconds. The original dependency-free District equivalence binding is
unchanged. The audit is `gateway-080-table-imports-regression.jsonl`.
The final production build and wider index workspace unit/headless checks passed.
