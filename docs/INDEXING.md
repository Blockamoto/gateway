# Indexing and positional inscriptions

The 0.7.6 development source enables **Headers**, **Bitcoin Blocks**, **Inscriptions**, **Transaction Locator**, **Transaction Input/Output (TXIO)**, **UTXO set** and **Addresses**. Addresses offers Current UTXOs and independently retained Historical outputs; see [the Addresses guide](ADDRESSES-INDEX.md). TXIO retains optional Values and exact ScriptPubKeys history. Gateway peerhood and later index stages remain gated. The public release and updater remain tracked separately in [status](STATUS.md).

## What coverage means

Headers identify blocks and their place in the selected proof-of-work chain. They do not contain transaction bodies or establish that any block is stored locally. A packaged snapshot accelerates first startup; ordinary Bitcoin synchronization validates and extends it.

Blocks records processing and availability for requested ranges. A block can also be fetched on demand without downloading every earlier body. Header height, committed index coverage, the current job's batch and retained block bytes are separate facts. Completing a batch does not imply whole-chain coverage.

The schema and explorer work with a known block's transactions, inputs, outputs and output-relative offsets. A transaction-ID lookup can use evidence already available from Blocks; an unknown ID may need a containing-block hint. An offset is not proof of global sat identity, ownership or movement history.

Sat/Satline, Bitmap, parcel and Terrain Claim Chains features remain gated. Gateway peerhood and index sharing are gated too. Fetching or viewing a block or address does not start historical index jobs. Transaction and Inscription indexing as you browse remain independent opt-in choices.

TXIO keeps one committed set of spend relationships with automatic lookup in both directions. A supplied BPS needs no transaction-hash locator. Construction still needs verified creation evidence; sparse missing predecessors remain unresolved. Relationships survive source-body eviction, while navigation may need to fetch the target's bytes. See [the canonical format and measured overhead](TXIO-INDEX.md).

Values and ScriptPubKeys are optional output-history layers under TXIO, with independent coverage. Their shared output facts can also supply mandatory UTXO live/recovery records. Turning history off cannot erase required UTXO dependencies; it does not remove earlier committed history automatically. Complete facts are required for UTXO even when neither historical layer is selected.

The timeline groups Blocks into Headers/raw bodies and TXIO into Positional links/Values/ScriptPubKeys. Collapsed groups show independent coverage bands. Grouping does not combine the underlying stores: Transaction Locator remains separate, and UTXO has its own state track.

## UTXO state

UTXO builds one complete prefix from genesis in block/transaction order. It saves live membership, exact amount/script facts, creation positions/anchors and the native-outpoint bridge together under an atomic head. It can reuse local blocks and fetch missing bodies through existing providers. Pause and restart preserve the last committed head; isolated arbitrary ranges cannot establish complete UTXO state.

The fixed policy retains 288 complete reversible transitions, with shared facts pinned even when a spent Coin originated years earlier. Raw bodies can be evicted after commit because local undo is self-contained. Existing raw retention remains a separate setting; replacement-branch processing and old-block serving still need bodies. The UTXO Pruned choice does not delete preexisting retained or external files.

A selected-chain disagreement rewinds to a common ancestor within the retained boundary. Deeper recovery stops with Rebuild required. A reviewed explicit rebuild archives the prior engine data and starts from genesis; normal Resume never resets it. Archives and their fact dependencies are preserved, so rebuild has an additional storage cost.

UTXO status is explicitly `header_anchored_processing`, with `consensus_validated=false`. A complete prefix is complete at its own processed height and becomes Current only at the exact selected target identity. Full consensus/script validation is deferred to v0.8. See [the UTXO guide](UTXO-INDEX.md) for storage, queries and retention.

## Inscription payloads

The Lean payload is a flat, numerically ordered list of compact positional coordinates:

```json
{"inscriptions":["12i0.840000","500i0.840000"]}
```

These are syntax examples, not claims about inscriptions at those positions. The number before `i` is the actual zero-based transaction position in the block, the number after `i` is the recognized inscription's transaction-wide index, and the number after the dot is block height. A transaction hash is not required in this Lean list. Verified block anchors, transaction counts, parser/format identity and processing coverage are committed separately.

**Full** additionally retains authenticated reveal records and content bodies. Lean/Full is separate from source retention: retaining a Bitcoin block does not turn a Lean payload into Full. Empty evaluated blocks commit scanned coverage too. Neither mode silently starts global numbering, ownership, sat tracing or recursive dependency resolution.

## Related transaction locators

Both the optional build-time choice and the retroactive action belong in the **Inscription inspector**. A scan can save one sparse transaction locator per transaction containing inscriptions, even if that transaction contains several inscriptions. This is independent of the complete Transaction Index.

The transaction payload keeps actual positions, rather than renumbering sparse rows:

```json
{"block":{"height":840000,"tx count":1000,"tx's":[{"0":"<transaction hash>"},{"500":"<transaction hash>"}]}}
```

The hashes and transaction count above are illustrative placeholders. The total count is the full block denominator; it is not the number of retained sparse entries. Commitments also identify the selection/completeness and source inscription coverage. Retroactive enrichment reuses committed inscription results and fetches only source blocks needed to recover the selected hashes. Independent checkpoints let it resume without rewriting completed inscription coverage.

A conventional inscription ID identifies a transaction hash and an inscription index. A matching locator supplies the missing block and transaction position; the resolver then fetches and verifies the block. A full global transaction index is not a prerequisite for an individual matching locator. With no locator or block hint, the location remains unknown. It is not proof the inscription does not exist.

## Content resolution

Indexing positions does not fetch recursive or delegated content. The viewer resolves referenced content when requested, using cached content and known local positions first. The later Gateway peer stage can supply missing location hints, subject to explicit permissions and local verification. Private index knowledge is not automatically published.

The on-demand content cache has separate limits of 256 MiB and 4,096 records, with oldest-write eviction. It does not evict committed Full records or retained Bitcoin sources. The Inscription inspector offers an explicit bounded storage inspection, separating coordinates, Full records, checkpoint overhead, related locator records, source blocks and resolved content. A partial inspection reports its limits instead of presenting an incomplete count as the total.

An unknown location, temporarily unavailable block bytes and unverified relationship evidence are different states. Parent references alone do not prove provenance; global numbering, ownership and complete child lists require additional historical/state evidence. An output-relative sat offset is not a global sat number.

## Plans, jobs and Live

Selecting a track or range does not start a scan. Historical builds select an explicit range, mode and source-retention choice; UTXO uses its genesis-only state workflow and fixed Pruned policy. Current addresses build from saved UTXO state without starting that dependency. Historical addresses allow sparse creation ranges and reuse complete retained output facts before fetching raw fallback blocks. Requests containing locked outputs fail before fetching or writing data. One index writer runs at a time; accepted additional requests enter a durable queue. Pause and cancellation preserve committed work.

**On** permits an available index to run. **Live** authorizes catch-up and following the selected chain in batches. It starts work immediately from the saved/default start, which may be genesis. Review a small bounded range first. Header pause and outbound-network disable remain effective controls.

Core is optional. Sources include Gateway's local block data, optional read-only Core sources and ordinary Bitcoin peers. Peers may not have a requested historical block. Source unavailability is not evidence that data does not exist.

Gateway checks header hash, proof of work, transaction Merkle root, witness commitment and selected-chain context. These checks do not perform every Bitcoin Core script or UTXO consensus validation step.

## Source retention

- **Ephemeral:** does not add newly fetched raw source blocks to the cache; existing cached, retained and external data is not deleted.
- **Cache:** keeps new source blocks in the bounded Gateway cache, respecting privacy classifications.
- **Retain:** keeps sources outside cache eviction under the profile's index sources. Existing external Core/archive data is read in place rather than copied merely because it was used.

Changing retention does not refill or delete earlier bytes automatically. Committed processing coverage does not promise that ephemeral source bodies remain available indefinitely. Turning Private cache off does not change older objects from private to public.

Bitcoin Blocks is durable body storage: new records keep compact ordered transaction IDs and a verified Bitcoin serialized raw body rather than permanent decoded input/output arrays. Shared readers decode that body temporarily for queries, JSON inspection, builders and inscriptions. Blocks bodies are independent of ordinary cache eviction and pruneable Core files. Automatic legacy migration stages a versioned root before switching readers; old immutable batches, decoded cache JSON and hex files remain available and are not deleted automatically. Existing disk savings therefore require a later explicit cleanup decision.

## Checkpoints and recovery

Committed batches bind the definition, rules, network, range, block hash, dependencies, previous commitment and canonical records. A batch is written before its head is selected. Corrupt or incompatible checkpoints fail closed.

Restart checks saved commitments; interrupted work resumes from safe state rather than claiming unfinished coverage. An actual selected-chain disagreement rewinds to a common committed checkpoint. A temporarily unavailable source does not cause a destructive rewind. Old-branch batches do not count as selected coverage.

See [the current QA guide](TESTING-0.7.5.md) for state, history, restart and reorganization checks, [the 0.7.4 guide](TESTING-0.7.4.md) for existing Blocks/navigation checks, and [current status](STATUS.md) for acceptance evidence.
