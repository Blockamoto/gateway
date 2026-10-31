# Authenticated block-context point reads

Ordered transaction/reveal table recipes can declare a `bitcoin.block_context`
lookup with provider `selected`, key `height` and value type `block_context`.
The installed catalogue supplies its exact revision and semantic fingerprint;
these bind the mainnet source/verifier profile. Aliases are distinct from own
tables and checkpoint dependencies. Only read instructions may target them.

A key is canonical decimal text from zero through the current source height.
The result is a typed object containing exact `height`, `block` and
`transaction_count`. The current block reuses the independently owned cohort
source. Historical reads acquire only the requested selected block through
the shared base acquisition path and independently own/verify its bytes.
They do not invoke Raw retention, incidental indexing or locator repair.
Unavailable bytes wait; invalid/future keys fail. These point facts grant no
prefix coverage and never start a parent writer.

Each used result is retained as a checksum-bound owned source fact inside the
child's imported-fact COW root. The fact binds the declared lookup, child block,
logical key and typed value. It is independent of later producer collection.
Selected source headers are checked again within the final child publication
fence, without fetching bytes or reacquiring the header mutex there. Existing
checkpoint fact encodings are unchanged; the new source-fact schema and semantic
profile apply only to definitions declaring these point lookups.

The fact authenticates what the local verified source established. Its compact
metadata is not a detached Bitcoin proof that can establish transaction count
without the verified body. It also makes no consensus-validation claim beyond
the host's admitted source authority.

Source point facts share operation, aggregate-byte and touched-key limits with
the child's own state and checkpoint reads. Recovery, archived rebuilds, query
snapshots and corruption-aware collection retain their ordinary table lifecycle.
Maintenance validates source-fact bindings, shapes and required bytes before
cleanup. Source declarations and parent checkpoints remain separate contracts.

The guided editor adds installed block-context bindings, preserves nested alias
references and keeps referenced declarations protected as instructions change.
Selecting a height-keyed point source supplies a `decimal_format(height)` key
expression, which can be edited. Point aliases are absent from write choices.
Planning describes required point reads without claiming source availability.

Focused checks cover current/historical counts, repeated reads, byte-unavailable
sources, invalid/future keys and bindings, cancellation, source file preservation,
public saved execution/query, owned facts, native writer isolation and rejection
after a historical selected header changes. Unit and headless real-DOM checks
exercise source pins, alias renames, key defaults, live removal protection and
narrow layout. Parcel identity/provenance source composition and full visible
preview acceptance remain open.

The wider table/TXIO source-lookup/catalogue/District suite passed 47 checks in
174.438 seconds. It includes the real TXIO saved/source-evidence lifecycle
alongside both imported checkpoint schemas and the new source facts. Recipe
unit and headless real-DOM checks, plus the wider index workspace checks, passed.
The audit is `gateway-080-point-lookups-regression.jsonl`.
The final production build passed (25,282,560 bytes).
