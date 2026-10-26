# Common operation interpreter experiment

This module has one typed, versioned JSON recipe format, a single interpreter
and a shared fixture block/transaction model. `transaction-locations.json`
executes a field-selecting `emit` operation for every transaction.
`utxo-fixture.json` runs `consume-inputs`, `create-outputs`, and that **same**
`emit` operation in the same transaction loop. Tests run both recipes against
the same synthetic block and compare their projected rows.

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
only two exact instruction sequences; it is an initial capability experiment,
not a usable general index constructor. It lacks real block parsing, Bitcoin
consensus, BIP30 and other historical rules, raw evidence, arbitrary lookups,
relationships, winner selection, Sat FIFO, a durable embedded backend,
bounded resources, crash recovery and reorg/undo. A changed chain fences new
publication; if saved receipts no longer match selected anchors, views and new
work fail explicitly. Old receipts are retained and require a future explicit
reconciliation mechanism. The reference backend checks all receipts linearly,
which is not a production performance design. No production or index-parity
claim is made.
