# Protocol evidence and indexing contract candidates

Status: research and test obligations for the 0.8 reconstruction, not a claim of
implemented operations, protocol conformance, or a final storage/runtime choice.
The earlier 0.7.8 source remains the evidence for existing behavior. Definitions
below describe what 0.8 must prove, not features already present in the prototype.

## Pinned external evidence

- [Bitmap District standard](https://github.com/Blockamoto/bitmap/blob/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/README.md), commit `335958311eb1c8ce4d14c48bc5c21d23c7a4b134` (the `main` head checked 2026-10-26). District claims use their own inscription-envelope body: an ASCII, whole-string decimal district with lowercase `.bitmap` and no leading zero except zero itself; a parsed `text/plain` MIME type is required, ignoring charset and allowing MIME parameters. The numeric district must not exceed the reveal block height. The first qualifying reveal for a district wins. District validity does **not** require transaction-input provenance, parent provenance, Sat tracking or current ownership. Reading the reveal's inscription envelope is distinct from proving its input lineage.
- [blocators core 0.1](https://github.com/Blockamoto/blocators/blob/aa116b0280316f7882f31530b251e3988b97507f/SPEC.md), [Ord extension](https://github.com/Blockamoto/blocators/blob/aa116b0280316f7882f31530b251e3988b97507f/extensions/ordinals.md), and [vectors](https://github.com/Blockamoto/blocators/blob/aa116b0280316f7882f31530b251e3988b97507f/TEST-VECTORS.md), commit `aa116b0280316f7882f31530b251e3988b97507f`: both core and Ord extension are experimental drafts. Coordinates name confirmed Bitcoin structure, leaf first; the extension's `<tx-ref>i<inscription-index>.<block-ref>` is a reveal coordinate, not the current location.
- [ord inscription implementation](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/src/inscriptions/inscription.rs), [inscription handbook](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/docs/src/inscriptions.md), [provenance](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/docs/src/inscriptions/provenance.md), [properties](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/docs/src/inscriptions/properties.md), and [recursion](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/docs/src/inscriptions/recursion.md), commit `b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414`. The properties document states that `ord` code is normative; this pin is a comparison target, not a claim of compatibility with every historical or future ord version.

Height-based positions are selected-chain relative and may rebind after a reorg;
block hashes anchor exact blocks. A specified txid must be verified within its
specified block, with no fallback to a different transaction. A coordinate needs
network context; an isolated txid does not reveal its block location. Translation
to a blocator requires trustworthy location evidence. A satpoint identifies an
output-relative position, not by itself a global ordinal or its lineage.

## Candidate common operations and proof matrix

The operation names below are design vocabulary, not an implemented language.
Constructor examples must ultimately use real executable syntax and execute
through the same engine as built-ins. An example passes only when its result,
coverage, recovery and evidence behavior are checked, not when its definition
merely parses.

| Workload / required output | Needed evidence and candidate engine operations | Minimum proof still needed |
| --- | --- | --- |
| Headers, selected chain and block retrieval | Acquire from ordinary Bitcoin peers or optional Core; verify commitments and chain context; select and anchor block; record available vs missing body. | Peer on-demand retrieval without Core, bad data rejection, selected-chain/reorg behavior and restart. Do not equate current header/block verification with full consensus validation. |
| Block, transaction and inscription locations | Decode a verified block once; enumerate ordered transactions and envelopes; emit positional references; lookup by keyed identity; record sparse coverage and explicit unknowns. | Positional-to-txid and inscription-ID translation from verified location; reverse lookup with and without locator; txid/block membership rejection; hash anchoring and reorg vectors. |
| TXIO and output facts | Enumerate input/output facts; lookup outpoints; write creation/spend relations; join exact referenced facts; preserve independent sparse coverage. | Known spend vs known-unspent-at-checkpoint vs unresolved; duplicate/replay behavior and sparse holes. |
| UTXO and Addresses | Ordered state transition with previous-state checkpoint, input deletions, output insertions, deterministic aggregates, atomic commit, rollback and replay; projected address membership and values/scripts. | Genesis-to-checkpoint parity, intra-block dependencies, crash and reorg recovery, unknown vs zero; independent address coverage and upgrades. |
| Sats and inscription positions | Ordered interval split/concatenate and FIFO transfer, values for all relevant prevouts and outputs, fees and coinbase accounting, saved state and recovery; optionally join known inscription evidence. | Proven lineage and current checkpoint, pointer/default assignment and fee handling; holes and unavailable predecessor data never become an invented location. |
| Bitmap Districts | Decode ordered reveal envelopes from authenticated selected-chain blocks; test the pinned standard against the claimant's own body and MIME type; select the first qualifying reveal per district and publish anchored outcomes with explicit coverage. No input, parent or Sat provenance is a District prerequisite. | Reproduce active 0.7.8 District results with fixed real evidence, including exact body/MIME and ordering cases, complete-prefix coverage and reorg handling. A partial scan cannot establish a global first winner. |
| Bitmap Parcels (future scope) | Retain existing data and preserve extensibility for later child-relation and other rule evidence; do not enable or implement Parcels as part of the present 0.8.0 requirement. | Parcel validity and parent-provenance semantics require their own later investigation and proof; unavailable evidence remains unresolved. |
| Inscription fields, metadata and metaprotocol | Decode ordered envelopes and retain raw fields and flags; concatenate split tag values; bounded CBOR parsing; expose content, encoding, pointer, parents, metadata, metaprotocol, delegate, rune, properties, unknown/duplicate/incomplete status. | Differential vectors against pinned `ord`, malformed/duplicate/unknown-even cases, CBOR edge cases and evidence of fields actually retained. These are reveal facts, not global inscription number or ownership. |
| Properties, galleries and attributes | Decode definite CBOR properties; support inline and packed gallery IDs, structured attributes and bounded Brotli; resolve targets only with evidence. | Pinned `ord` vectors, both gallery encodings, trait rules, 4,000,000-byte and 30:1 limits; gallery membership is permissionless and does not prove provenance. |
| Parent declarations vs proven child relationships | Parse each parent ID; resolve its reveal and relevant later location; establish that the parent was spent in a child reveal input under the selected Ord rules. | Declaration-only, valid, invalid and missing-evidence outcomes; bytes/endianness, multiple parents, reorg and incomplete lineage. A tag by itself is not proof. |
| Delegates and recursion | Resolve inscription IDs through evidence-aware lookup; retrieve content, type and encoding; expose supported recursive endpoints in a sandbox with bounded resources. | A missing delegate can be created later: report unresolved until evidence exists; cycle/depth limits, fetch failures, cache invalidation, content security and explicitly enumerated endpoint compatibility. Resolving one content reference is not full recursion support. |
| User-defined indexes and query outputs | Typed source, filters, projections, relationships, deterministic key/order selection, aggregation, dependency declarations, state mutations, evidence/coverage propagation, checkpoint and rollback plus query schemas. | Run the required 0.8.0 examples above as executable definitions on the same engine; prove they survive restart/reorg and explain dependencies. Parcels are deferred. A declarative filter alone is insufficient for stateful UTXO/Sats. |

For inscriptions, a pointer is a position in the transaction output stream;
its valid interpretation requires the applicable rule and output values. Proving
a global sat and following it later additionally requires enough input value and
lineage evidence, including fees and coinbase. An inscription number depends on
historical ordering and Ord-specific rules. Missing evidence is unresolved, not
proof of absence or ownership.

The 0.7.8 active **Bitmap Districts Standard V1** profile at Gateway commit
`efc5b13` pins the same Bitmap revision and six District rules. It starts a
fresh index at height 792435, evaluates a complete consecutive prefix, orders
reveals by block height, transaction position and transaction-wide envelope
index, and records blocator and selected-chain anchors. These are Gateway
implementation and coverage policies; the upstream README does not prescribe
that activation height or checkpoint format. Older profile generations and
their saved data remain separate; compatibility or migration must be explicit.
The profile also distinguishes current ownership from claim validity and does
not add parent or Sat requirements for Districts. This comparison describes
the implementation's declared rules, not an independent proof of parity.

## Runtime candidates, not a decision

- [JSON Schema](https://json-schema.org/draft/2020-12/json-schema-core) can validate the shape of definitions, but does not execute them.
- [CEL](https://github.com/cel-expr/cel-go) provides checked, side-effect-free expressions and partial/unknown evaluation. It is a candidate for predicates and projections; stateful transitions, output commits, recovery, dependency semantics and host operations need a separately specified engine. Bound the input, expression cost and extension functions.
- [SQLite SQL](https://www.sqlite.org/lang.html) provides queries, joins, aggregates, recursive queries and transactions. SQL alone does not define Bitcoin verification, Ord lineage, coverage or reorg semantics; raw SQL may couple definitions to storage. Untrusted queries need restricted capabilities, resource limits and a suitable [authorizer](https://www.sqlite.org/c3ref/set_authorizer.html).
- [WebAssembly](https://www.w3.org/TR/wasm-core/) and [wazero](https://pkg.go.dev/github.com/tetratelabs/wazero) are candidates for advanced executable modules in a Go host. The host must define deterministic inputs/outputs, restrict imports and bound memory, execution and cancellation; sandboxing does not independently ensure determinism or a user-friendly constructor.

An approachable constructor can build checked definitions over a smaller set of
versioned host operations, with an advanced extension only if demonstrated
necessary. This is a hypothesis to test against all rows, not a chosen design.

## Cross-cutting acceptance obligations

1. A consumer's committed checkpoint, derived rows and coverage must agree after interruption, duplicate input, replay or reorg. Incomplete indexes must not block unrelated ready indexes; stateful operations preserve their own order and dependency anchors.
2. Results must carry the applicable network, chain anchor, rule/profile and evidence/coverage; distinguish absent, unknown, invalid and stale. Version definitions, decoders and results so changed semantics cannot silently reuse old results.
3. Verify stable public contracts between engine and UI independently of implementation and physical database layout. Preserve old profiles/data and require explicit compatibility or migration decisions.
4. Compare against pinned vectors and independent reference results with real blocks, especially historical rule changes, malformed fields, missing evidence, fee/coinbase cases and reorganizations. No conformance, parity or operation-set completeness is established by this document.
