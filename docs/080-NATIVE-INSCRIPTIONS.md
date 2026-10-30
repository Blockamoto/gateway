# Native inscription recipes

Native Lean and Full derivation now executes actual JSON through the sparse
row interpreter. `inscriptions-lean.json` writes admitted positional references;
`inscriptions-full.json` additionally writes each exact source record;
`inscriptions-conventional-ids.json` selects reveal index zero and projects
its transaction position/ID. Browse retention may include that optional layer.
The retroactive saved-inscription locator derivation also executes its own
`inscriptions-saved-conventional-ids.json` composition. That table explicitly
retains identical encoded rows once and rejects conflicting values at the same
transaction key. This is a generic table operation available in guided controls,
not hidden deduplication in the physical adapter.

The source adapter admits a complete parser-owned extraction receipt tied to
the host's verified block. A synchronous host operation may share that admitted
inventory between recipe layers. The host owns its immutable decoded data.
The receipt does not establish selected-chain authority; existing native
admission/publication/query fences remain responsible for it. Legacy Full
numbering fixtures still use their separate explicit historical machinery.
No new Lean/Full job starts numbering or a hidden historical input scan.

Reveal sources add `tx_ref`, `tx_index`, `reveal_index`, and opaque object
`record`. The record serializes to the original occurrence JSON, including
exact body/payload/envelope, verification and parser fields. It is not a
whole-index opcode. Individual source facts remain available for filters.
The physical sink validates admitted record identity, order and completeness,
then retains source content in the existing native layout. Lean avoids body
encoding, metadata concatenation and properties decoding when unused.

Generic reveal execution now streams event-atomic proposals into a sink.
Late filters, export checks, duplicate detection, cancellation and exact encoded
byte accounting still precede complete publication. The old block-wide row map
remains only when a generic caller explicitly requests materialized rows.
New record recipes have a 64 MiB event/output bound, matching the existing block
output bound. Other recipe event limits are unchanged. Generic durable units
still have their current 16 MiB engineering bound; large custom full-record
retention and durable fact sharing remain unfinished release work.

The three-program semantic bundle is
`200c2ef4cf177d51e631bc6bc362ce49acf6113016d10627140e0a598a4baf9a`.
It explicitly maps to frozen native v1 rule
`8d3eb00515980a735f429c6b7cf6a6656ce8983af4e01d8deed08ec9ee2d6fb1`
and v2 rule
`2bbc7edf72c2baaaefd6a29c3b2efeebb3461309ba9d3fbd64b09e1cd596f05e`.
Changed program meaning cannot enter those generations without an explicit
migration. Additive source fields bind a new profile only when referenced;
existing recipes and the pinned native transaction locator keep their hashes.
Executable display documents and variants are assigned after frozen hashes.
The internal inscription-locator definition now clears inherited executable
display metadata before hashing, restoring the pre-display identity rule.

Settings lists Lean, Full and optional conventional-ID JSON documents. Viewing
or switching documents performs no write/run and polling preserves the choice.
Lean and Full copies round-trip through the guided editor. A copied draft has
its own instance identity and must be validated/saved/selected explicitly.

Settings also lists the saved conventional-ID composition. Its native source
adapter validates the saved inscription checkpoint and exact record commitment;
Full supplies saved reveal IDs without fetching, while Lean needs the matching
verified block when positions are nonempty. Empty evaluated blocks remain
source-free. Saved coordinates do not become a raw extraction receipt. The
same row interpreter projects each admitted position; its declared identical-row
policy performs deduplication. Existing sparse coverage, source commitment and
native reverse-query layouts are retained. Cancellation propagates from the
native operation before publication.

The saved projection plus source profile is bound to semantic
`66d52fd73611644c1decb8df7bf03bfadc83a3f9966190ceee66bb271fae4741`,
native rule `bb1e9cf30432ef9ec5aa5c7e2e68771bef10a736c604021ade52409a810d5521`
and legacy rule `db7b4bfc92b82c311dbed0ff322a33c1fa63daf4704771bb05cc43f51ef7ef91`.
The optional table declaration `"on_conflict":"identical"` binds a separate
interpreter profile; absent policy keeps strict duplicate rejection and existing
semantic hashes. Equality is exact deterministic encoded row bytes, without
numeric normalization or overwrite. Native compact-backend choice and saved
native-source imports for arbitrary custom tracks remain open release work.

Differential fixtures compare exact payload objects and serialized hashes for
empty verified genesis, synthetic multi-envelope reveals, real block 792435,
and optional locally pinned block 908657. Custom Full recipes retain the exact
source JSON, and a changed predicate changes both execution and identity.
These fixtures establish bounded parity, not replay of complete chain history.
The broad recipe/saved/sparse/positional/inscription suite passed in 188.404 s;
subsequent identity, admitted-source reuse, durable record, rejection and
dependency checks passed in 72.358 s. Editor/workspace checks and production
`go build` passed. These checks are also recorded in the engine ledger.

A paired 15-iteration measurement on real block 908657 (56 reveals) includes
source admission and final native payload encoding, with optional conventional
IDs enabled. Lean measured 3.47 ms / 644,597 bytes / 11,516 allocations per
recipe operation versus 2.22 ms / 377,961 bytes / 7,021 allocations for the frozen
projection. Full measured 4.06 ms / 2,252,370 bytes / 12,416 allocations versus
3.00 ms / 1,058,329 bytes / 7,269 allocations. These are short Windows-host
measurements without disk publication or selected-chain work, not chain-speed
estimates. Reproduce with `GATEWAY_RECIPE_REFERENCE_BLOCK` and
`go test -run '^$' -bench '^BenchmarkRecipeNativeInscriptionRetention$' -benchtime=15x`.
