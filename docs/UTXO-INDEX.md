# UTXO set in Gateway 0.7.5

The UTXO index records one complete live-output state at an explicit processed Bitcoin mainnet block. It starts at genesis and processes every intervening block and transaction in order. It cannot start at an arbitrary sparse range. A prefix is complete at its saved height; it is current only when that height and block hash match the selected target tip.

Gateway verifies block commitments and selected-chain context before processing. This release reports `validation: "header_anchored_processing"` and `consensus_validated: false`. It does not perform full Bitcoin script, coinbase maturity, subsidy or other consensus validation. Full node validation remains work for v0.8. “Current” describes the processed state and its selected-chain anchor, not an independent proof that the entire chain satisfies consensus.

## Build and resume

Select **UTXO set** in the timeline. **Build UTXO set** prepares a review; **Start reviewed build** begins processing from genesis and enables following the selected chain. Local Gateway bodies, existing cache/retained bodies and configured read-only Core sources are reused before missing blocks are fetched through existing providers. Core is optional. This feature adds no peer protocol or snapshot download mode.

The panel shows processed and target heights/hashes, live output count and value, the retained recovery boundary, and a storage measurement when available. Progress advances after each durable block commit. A processed block count is not a reliable download-time estimate. **Pause** preserves the last committed head; **Resume** continues from it. Missing headers or bodies leave the state intact and show a waiting state. The existing queue permits one index writer at a time.

UTXO retention is **Pruned**. Required live output facts and recovery dependencies remain local automatically; new raw bodies fetched for this build are not permanently retained by the UTXO job. Existing retained, cached or external Core bodies are not deleted by choosing Pruned. Raw-body retention remains a separate Blocks/cache choice. Offline lookup and recovery work without source bodies, but serving an old block or processing a replacement branch still requires its bytes.

## Live coins and shared facts

Each live output has its exact satoshi amount, exact scriptPubKey hex, creation blocator and creation-block identity. Output blocator is `vout.transactionOrdinal.height`, with zero-based ordinals. The first transaction is structurally checked as coinbase, allowing creation height and coinbase status to be derived from the saved position.

The engine stores membership separately from immutable output facts:

| Record | Retained fields and purpose |
| --- | --- |
| Live member | Output blocator and a shared fact-page hash/row reference. |
| Live transaction | Transaction ID, creation height/hash/ordinal and live-output count, stored once per live transaction. |
| Native bridge | Transaction ID to the current live transaction incarnation. Together with `vout`, this resolves Bitcoin input prevouts even after raw-body eviction. |
| Output-fact page | Shared creation height/hash, ordered output coordinates, exact values and scripts. Pages contain bounded rows; they are not one file per output. |
| Atomic head | Processed block identity, state roots, counts/value, previous commitment and authenticated recovery catalogue. |
| Transition journal | The original values of changed membership, transaction, bridge and pin records, sufficient to restore the preceding state. Coin facts are referenced rather than copied onto each input. |

These are separate JSON files under `indexes/utxo-state`, with bounded copy-on-write radix pages and content-addressed commits/journals. Shared output pages live under `indexes/output-facts-v1`. The transaction hash catalogue is not duplicated in each TXIO spend row. The optional historical **Transaction Locator** remains its own track/store; the engine's mandatory live bridge is published with live membership and does not depend on complete historical locator coverage.

blocator remains the logical live-output identity. Internal live/transaction/native-bridge radix keys disperse their identities with SHA-256 to avoid deep common numeric prefixes; direct blocator or native-outpoint lookup computes its key locally and checks the original identity in the referenced records. The current checkpoint schema is version 2. Earlier unreleased development UTXO heads are rejected rather than interpreted with a different key encoding; published 0.7.4 profiles contain no enabled UTXO state.

The following is an illustrative resolved lookup, not the physical membership record or a claim about an actual output:

```json
{
  "output": "0.2.900001",
  "txid": "<creation transaction ID>",
  "vout": 0,
  "height": 900001,
  "block_hash": "<creation block hash>",
  "tx_index": 2,
  "value_sats": 12345,
  "script_pub_key": "51",
  "coinbase": false,
  "facts": {"page": "<shared page hash>", "row": 7}
}
```

The management API accepts either an output blocator or `txid:vout` for `index=utxo-state` lookup. A bounded query lists live coins, with its checkpoint and validation status. Reads refuse a state whose checkpoint cannot currently be anchored to the selected chain. A negative live lookup means absent from that explicitly identified processed state; it does not prove historical nonexistence, global sat identity, ownership or spendability under full consensus rules.

## Recovery and rebuild

The fixed recovery policy keeps **288 complete reversible block transitions**, or all transitions since genesis while the prefix is shorter. It is not 288 full UTXO snapshots, a consensus reorganization limit or a promise to retain 288 raw blocks. State versions share unchanged radix pages. At tip height 300, for example, a boundary at height 12 permits undoing blocks 300 down through 13 to recover the state at 12.

Recovery identifies the common selected-chain ancestor before rewinding. It applies recorded changes backwards and publishes the authenticated parent roots. Outputs created and spent inside one block stay absent when that block is undone; historical BIP30 displaced incarnations are restored. Genesis outputs and provably unspendable outputs are excluded from live membership. A spent coin may have been created years earlier: its full shared facts stay pinned while any retained transition can restore it.

Beyond the retained boundary, the engine stops with **Rebuild required**. It does not silently reset on Resume. **Review rebuild**, followed by **Start reviewed rebuild**, archives the prior engine directory and starts again from genesis. The persistent rebuild job marker prevents a pause, restart or retry of that same job from resetting its new progress twice.

Prior engine data is preserved under `indexes/utxo-state-archives`, with job records under `indexes/utxo-rebuild-jobs`. Valid archives keep their shared fact dependencies pinned. A damaged archive stops shared-fact collection rather than allowing its dependencies to be deleted. Archive cleanup is not an automatic part of this release. Rebuilding therefore needs space for the preserved state and its replacement.

## Optional history, retention and privacy

TXIO's **Values** and **ScriptPubKeys** history are optional, independently covered layers. UTXO requires both facts for every live/recoverable coin regardless of those settings. Complete TXIO enrichment and UTXO can reference identical shared pages. Values-only or scripts-only projections can have distinct immutable pages; old compatible records remain preserved. This is reuse of shared facts, not a claimed measured disk reduction.

Retention is the union of live membership, retained recovery, preserved rebuild archives and optional historical owners. Cleanup cannot delete a page still referenced by any owner. Collection works at page granularity, so retained pages can contain neighboring historical outputs whose individual rows no longer need retention. Unpublished shared pages receive a one-hour staging grace period. Incremental engine cleanup and source retention are independent.

Engine marking records completed subtrees durably and can continue across bounded passes. A sweep runs with cancellation checks against a stable writer snapshot; it is allowed to finish instead of repeatedly restarting under a short timeout. Whole-state fact enumeration runs outside the status mutex. Each block/query/undo operation uses a fresh bounded cache of authenticated fact pages, avoiding repeated parsing of a page for each output without reusing stale authentication in later operations.

Private engine-page cleanup uses versioned, even-second timestamps and verifies each stored mark before sweeping. A filesystem which cannot preserve those marks stops cleanup with an error; it does not permit deletion based on a successful timestamp-setting call alone. The source checks include an actual exFAT drive fixture as well as simulated precision loss.

These indexes remain local/private. The feature does not enable Gateway-to-Gateway sharing or publish a UTXO snapshot. Local records and preserved archives can reveal which chain data was processed; turning a history option off does not erase earlier retained data. Storage inspection reports file bytes, counts shared dependencies once, excludes raw blocks and unrelated TXIO-only history, and labels budget-limited inspections as partial. Automatic status uses only a completed measurement for the same state revision; otherwise bytes remain unknown.

See [indexing](INDEXING.md), [the 0.7.5 test guide](TESTING-0.7.5.md) and [status](STATUS.md) for scope and current evidence.
