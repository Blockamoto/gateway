# Headers and Blocks

Gateway 0.7.0 enables **Headers**, the foundational selected-chain index, and **Bitcoin Blocks**. Other index definitions remain visible with a lock explanation. Their implementations cannot be enabled through a setting, command or API in this release.

## What coverage means

Headers identify blocks and their place in the selected proof-of-work chain. They do not contain transaction bodies or establish that any block is stored locally. A packaged snapshot accelerates first startup; ordinary Bitcoin synchronization validates and extends it.

Blocks records processing and availability for requested ranges. A block can also be fetched on demand without downloading every earlier body. Header height, committed index coverage, the current job's batch and retained block bytes are separate facts. Completing a batch does not imply whole-chain coverage.

The schema and explorer work with a known block's transactions, inputs, outputs and output-relative offsets. A transaction-ID lookup can use evidence already available from Blocks; an unknown ID may need a containing-block hint. An offset is not proof of global sat identity, ownership or movement history.

Standalone transaction-location, spender/address, UTXO, Sat/Satline, inscription, Bitmap, parcel and Terrain Claim Chains features are locked. Gateway peerhood and index sharing are locked too. Fetching or viewing a block does not start those jobs.

## Plans, jobs and Live

Opening a card does not start a scan. A Blocks build selects an explicit range and source-retention choice. Requests containing locked outputs fail before fetching or writing data. One index writer runs at a time; accepted additional requests enter a durable queue. Pause and cancellation preserve committed work.

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
