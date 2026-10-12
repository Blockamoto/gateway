# Transaction Input/Output (TXIO) index

The Input index finds the original output consumed by a particular input. The Output index finds the input that consumed a particular output. Both lookup directions are built automatically from one committed set of spend facts. The compatible storage and API identifier remains `txo-spender`; the workspace calls the feature **TXIO index**.

Positions are zero based. Input `i1.2.840000` means input one (the second input) in transaction two of block 840000. Output `0.7.839999` means output zero in transaction seven of block 839999. A supplied position needs no transaction hash lookup. Transaction zero is coinbase, whose input has no previous output.

## Canonical version 2

The existing immutable JSON block batch and checkpoint envelope remains the visibility boundary. Shared checkpoint fields carry the spending block height, hash, chain identity, previous commitment and record digest. The `spenders` payload is versioned independently:

```json
{
  "version": 2,
  "rows": [
    {"vout": 0, "tx_index": 2, "vin": 1, "output": "0.7.839999", "previous_transaction": 0}
  ],
  "origins": [
    {"txid": "<coinbase transaction ID>", "tx_index": 0, "output_count": 1, "coinbase": true},
    {"txid": "<transaction one ID>", "tx_index": 1, "output_count": 2},
    {"txid": "<spending transaction ID>", "tx_index": 2, "output_count": 2}
  ],
  "creation_anchors": {"839999": "<verified creation block hash>"},
  "previous_transactions": ["<previous transaction ID>"]
}
```

The input position derives from each row's `vin`, `tx_index`, and shared spending height. `previous_transaction` indexes the deduplicated previous-transaction hash table; the spending transaction hash comes from `origins[tx_index]`. Output position is included only when the original transaction and output were located and verified. An unresolved row retains its original previous transaction ID and output ordinal through that table, omits `output`, and produces an **unresolved** Input index result. It does not fabricate a creation position or prove unspentness.

Origination metadata records transaction order and output cardinality for each processed block. It is independent of raw body retention. A later sequential build can therefore position known predecessors after body eviction. Same-block predecessors must occur before their spending transaction. Cross-block lookup chooses the newest eligible verified incarnation within the consuming-block bound, including the existing historical duplicate-coinbase rules. A sparse range may contain unresolved predecessors, and no full-chain prerequisite is added.

Version-zero legacy rows, their committed JSON bytes, rule hash and checkpoint remain readable without modification. Legacy graph spender records are consulted only through their existing verified resolver; their binary layout is never reinterpreted as a positional spend record. Legacy rows lack an output position until an explicit navigation fallback obtains creation evidence.

## Private acceleration and recovery

`indexes/txo-spender/txio-v2` stores rebuildable, sharded pointers to immutable batches. Its namespace is separate from the old `spenders-v1` directory. Each pointer retains the established 81-byte encoding: format byte, 32-byte hashed key, 32-byte commitment, 8-byte height, 4-byte slot, and 4-byte CRC. It contains neither transaction bodies nor complete spend facts.

Each spend reserves three slots: compatible `txid:vout` lookup, Input index BPS lookup, and Output index BPS lookup. Unresolved output slots use an unqueryable placeholder key. Each origin transaction adds one origination pointer. Thus pointer payload size is `81 × (3 × spend_count + transaction_count)` bytes, plus a 36-byte active commitment table entry per indexed block. JSON receipts, directory entries and filesystem allocation are additional costs. Canonical JSON size depends on positions, cardinalities, distinct hashes and shared anchors; it is not automatically half or twice the legacy size. The focused storage test reports actual serialized fixture sizes.

The measured three-spend fixture with three origin transactions and two distinct previous transaction IDs uses **565 bytes** for legacy canonical JSON and **818 bytes** for TXIO canonical JSON. Its twelve private pointers occupy **972 bytes**, plus the 36-byte active block entry and receipts. This deliberately includes richer directional/origination knowledge and is larger for that tiny fixture; hash deduplication benefits blocks with shared multi-input transaction identities. These are serialized payload measurements, not a claim about filesystem allocation or typical-chain compression.

Pointers are durable before the atomic head advances. Both directions use the same active commitment table and verify their referenced canonical batch. Reconciliation withdraws disconnected spending batches and therefore both directional pointers; origination lookup filters the same active range. Output links also recheck their saved creation block against the selected chain. Missing chain evidence produces an error, rather than a negative spend answer. Damaged pointers fail closed; reconstruction uses immutable canonical facts and is covered by a restart/rebuild test.

The logical relationship remains available after raw eviction. Navigating to its target may need to fetch and decode a verified block. A known BPS skips transaction ordinal discovery; it does not promise instant transaction bytes or a direct byte seek.

## Lookup API

`GET /api/v1/txio/lookup?direction=input&position=i1.2.840000` and the equivalent `direction=output` request return `direction`, `position`, `state`, `rows`, `checkpoint`, `chain_state`, and an explanatory `absence` or `note`. States are `known`, `unresolved`, and `unknown`; unknown never means unspent. Rows include normalized legacy transaction/outpoint fields, `input`, `output` when known, `block_hash`, and `creation_block_hash` when known.

An explicit `fallback=true` may obtain verified current transaction and predecessor or spender evidence using existing bounded providers. It does not create a range build or require a Transaction Index merely to open a supplied BPS. Missing source bytes or a historical incarnation mismatch remains unknown. Gateway peer access retains its separate release gate.
