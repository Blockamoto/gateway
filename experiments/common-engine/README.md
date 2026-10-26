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
