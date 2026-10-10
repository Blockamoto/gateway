# Indexing and positional inscriptions

The published baseline enables **Headers**, the foundational selected-chain index, and **Bitcoin Blocks**. The working inscription stage adds **Inscriptions**. Related transaction locator construction and the complete Transaction Index remain gated alongside the later peer stage. Locked implementations cannot be enabled through a setting, command or API.

## What coverage means

Headers identify blocks and their place in the selected proof-of-work chain. They do not contain transaction bodies or establish that any block is stored locally. A packaged snapshot accelerates first startup; ordinary Bitcoin synchronization validates and extends it.

Blocks records processing and availability for requested ranges. A block can also be fetched on demand without downloading every earlier body. Header height, committed index coverage, the current job's batch and retained block bytes are separate facts. Completing a batch does not imply whole-chain coverage.

The schema and explorer work with a known block's transactions, inputs, outputs and output-relative offsets. A transaction-ID lookup can use evidence already available from Blocks; an unknown ID may need a containing-block hint. An offset is not proof of global sat identity, ownership or movement history.

Standalone transaction-location, related inscription transaction locators, spender/address, UTXO, Sat/Satline, Bitmap, parcel and Terrain Claim Chains features remain gated. Gateway peerhood and index sharing are gated too. Fetching or viewing a block does not start those jobs.

## Inscription payloads

The Lean payload is a flat, numerically ordered list of compact positional coordinates:

```json
{"inscriptions":["12i0.840000","500i0.840000"]}
```

These are syntax examples, not claims about inscriptions at those positions. The number before `i` is the actual zero-based transaction position in the block, the number after `i` is the recognized inscription's transaction-wide index, and the number after the dot is block height. A transaction hash is not required in this Lean list. Verified block anchors, transaction counts, parser/format identity and processing coverage are committed separately.

**Full** additionally retains authenticated reveal records and content bodies. Lean/Full is separate from source retention: retaining a Bitcoin block does not turn a Lean payload into Full. Empty evaluated blocks commit scanned coverage too. Neither mode silently starts global numbering, ownership, sat tracing or recursive dependency resolution.

## Related transaction locators — later stage

Both the optional build-time choice and the retroactive action belong in the **Inscription inspector**. They are implemented but disabled in the first inscription stage. Once promoted, a scan can save one sparse transaction locator per transaction containing inscriptions, even if that transaction contains several inscriptions. This is independent of the complete Transaction Index.

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

Selecting a track or range does not start a scan. A build selects an explicit range, mode and source-retention choice. Requests containing locked outputs or locator enrichment fail before fetching or writing data. One index writer runs at a time; accepted additional requests enter a durable queue. Pause and cancellation preserve committed work.

**On** permits an available index to run. **Live** authorizes catch-up and following the selected chain in batches. It starts work immediately from the saved/default start, which may be genesis. Review a small bounded range first. Header pause and outbound-network disable remain effective controls.

Core is optional. Sources include Gateway's local block data, optional read-only Core sources and ordinary Bitcoin peers. Peers may not have a requested historical block. Source unavailability is not evidence that data does not exist.

Gateway checks header hash, proof of work, transaction Merkle root, witness commitment and selected-chain context. These checks do not perform every Bitcoin Core script or UTXO consensus validation step.

## Source retention

- **Ephemeral:** does not add newly fetched raw source blocks to the cache; existing cached, retained and external data is not deleted.
- **Cache:** keeps new source blocks in the bounded Gateway cache, respecting privacy classifications.
- **Retain:** keeps sources outside cache eviction under the profile's index sources. Existing external Core/archive data is read in place rather than copied merely because it was used.

Changing retention does not refill or delete earlier bytes automatically. Committed processing coverage does not promise that ephemeral source bodies remain available indefinitely. Turning Private cache off does not change older objects from private to public.

## Checkpoints and recovery

Committed batches bind the definition, rules, network, range, block hash, dependencies, previous commitment and canonical records. A batch is written before its head is selected. Corrupt or incompatible checkpoints fail closed.

Restart checks saved commitments; interrupted work resumes from safe state rather than claiming unfinished coverage. An actual selected-chain disagreement rewinds to a common committed checkpoint. A temporarily unavailable source does not cause a destructive rewind. Old-branch batches do not count as selected coverage.

See [the QA guide](TESTING-0.7.0.md) for small-range restart and reorganization checks, and [current status](STATUS.md) for acceptance evidence.
