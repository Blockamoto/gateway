# Declarative state transition experiment

`utxo-program.json` provides the executable transaction operations
`consume_inputs` and `create_outputs`. The small Go interpreter applies them in
transaction order, with a typed outpoint and value/script record, against a
single block. It requires genesis or the exact next height and parent hash;
each block either publishes completely or changes nothing. A bounded undo
history allows a named current head to be rolled back before a replacement is
applied. Stale rollback, unresolved inputs, duplicates, and insufficient undo
fail explicitly. Inputs may spend outputs created earlier in the same block.

Run `go test ./...` from this directory. Tests use synthetic fixture anchors.

This is a small, isolated experiment. Its caller must authenticate Bitcoin
blocks and selected-chain evidence. Hash strings and fixture transactions are
not validated here. It is not a Bitcoin consensus validator, does not support
historical duplicate-transaction exceptions such as BIP30, and does not model
Sat FIFO state or prove index parity. The machine copies all state per block;
it has no persistent store, crash recovery, authenticated state commitments,
resource limits, parallel scheduler, recipe migration or production API. A
retained undo depth is a fixture policy, not a Bitcoin reorg guarantee.
