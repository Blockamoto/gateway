# Common operation interpreter experiment

This module has one typed, versioned JSON recipe format, a single interpreter
and a shared fixture block/transaction model. `transaction-locations.json`
executes a field-selecting `emit` operation for every transaction.
`utxo-fixture.json` runs `consume-inputs`, `create-outputs`, and that **same**
`emit` operation in the same transaction loop. Tests run both recipes against
the same synthetic block and compare their projected rows.

`bitmap-districts.json` uses a fixed first-winner operation over complete,
transaction-ordered reveal envelopes. Its syntax, MIME, reveal-height and
first-winner rules refer to the pinned [Bitmap standard](https://github.com/Blockamoto/bitmap/blob/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/README.md).
Districts require no previous-input, parent or Sat provenance. The fixture
requires every transaction's envelope extraction to be explicitly complete;
missing extraction fails the block rather than fabricating an empty result.
The required start height 792435 is the current Gateway complete-prefix policy,
not a rule stated by the upstream standard. The caller supplies all anchors;
this code does not authenticate that the origin parent or any block is on the
Bitcoin chain. This recipe has not yet been differentially checked against
0.7.8 or a real-block corpus.

Each engine instance evaluates against tentative state and submits its state
and one block receipt together only after all transactions succeed. A
transactional backend interface requires publication to compare the instance
revision, selected-chain epoch and anchors, and consent epoch as part of the
same transaction. The supplied memory backend demonstrates these semantics,
including injected commit failures and retries; it is **not durable**. The state
recipe requires genesis or the exact next anchored predecessor; missing inputs,
duplicates and conflicts fail without partial state or coverage. Sparse
receipts are individual blocks and do not fill gaps. The tests demonstrate
ordered intra-block spends and independent instances.

Run `go test ./...` from this directory. `NewWithBackend` requires independently
provided fixture chain anchors and explicit per-instance consent (off by
default); `New` is a convenience mode that enables its isolated fixture
instance and derives fixture anchors from caller input. Neither verifies Bitcoin evidence. The recipe format supports
only three exact instruction sequences; it is an initial capability experiment,
not a usable general index constructor. It lacks real block parsing, Bitcoin
consensus, BIP30 and other historical rules, raw evidence, arbitrary lookups,
relationships, general winner selection, Sat FIFO, a durable embedded backend,
bounded resources, crash recovery and reorg/undo. A changed chain fences new
publication; if saved receipts no longer match selected anchors, views and new
work fail explicitly. Old receipts are retained and require a future explicit
reconciliation mechanism. The reference backend checks all receipts linearly,
and the District recipe reconstructs winners by scanning every prior receipt
on each block. Neither is a production performance design. No production or
index-parity claim is made.

District views and new work reject a missing or structurally inconsistent
winner prefix. A saved winner row is a stored assertion, not a standalone
proof: the experiment does not retain source envelopes or show that a candidate
qualified or that no earlier candidate existed. Production evidence and prefix
verification remain necessary.

`inscription-fields.json` exercises another operation in this same interpreter.
It takes complete ordered payload-push hex strings in the shape of the 0.7.8
`inscriptionOccurrence.PayloadHex` produced by `extractInscriptionOccurrences`.
For each reveal it retains those pushes, projects raw tag/value pairs, joins tag
5 metadata bytes, retains each tag 7 metaprotocol value and tag 3 parent
declaration, and reports incomplete fields. Empty and absent metadata are
distinct. A dangling later tag is recorded separately and does not erase an
earlier completed metadata, metaprotocol or parent value. A completed parent
declaration is `declared-unresolved`; it is not a proven parent-child
relationship. Missing full-block extraction or payload evidence fails the
entire block instead of yielding empty coverage. Projected occurrences, payload
items and bytes are bounded cumulatively per block; these limits do not replace
the extractor's full witness budget or authenticate caller-provided payloads.

This projection's parser-profile and completeness flags are caller assertions.
The recipe does not invoke the 0.7.8 parser, authenticate witnesses or prove
chain membership, decode CBOR, interpret duplicate-field Ord semantics, resolve
parent IDs, establish parent provenance, track sats, or claim Ord compatibility.
The base parser's [pinned Ord reference](https://github.com/ordinals/ord/tree/03c9b87b14fe1e2c38191b860a08988345ec4e5f) and the later
[Ord field implementation](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/src/inscriptions/inscription.rs)
must be compared before any version-specific conformance claim. Existing raw
fields are recorded as declarations; only additional authenticated lineage
evidence can prove a parent-child relationship.
