# Indexing and positional inscriptions

The 0.7.9 development preview adds bounded parent relationship inspection,
Bitmap address badges backed by selected saved evidence, and a fresh Parcel
track. Child lists reflect locally observed declarations, not complete global
coverage; saved links may need revalidation. See [the 0.7.9 brief](IMPLEMENTATION-BRIEF-0.7.9.md).

The 0.7.8 development source enables **Headers**, **Bitcoin Blocks**, **Inscriptions**, **TX ID Locators**, **Transaction Input/Output (TXIO)**, **UTXO set**, **Addresses**, **Sats** and fixed canonical **Bitmap Districts**. Addresses offers Current UTXOs and independently retained Historical outputs; see [the Addresses guide](ADDRESSES-INDEX.md). The TXIO store supports IO Links and optional Values and exact ScriptPubKeys history with independent coverage. Sat Ranges and Inscriptions appear together under Ordinals while retaining separate consumers and stores. Gateway peerhood and later index stages remain gated. Published releases and updater delivery remain tracked separately in [status](STATUS.md).

## Development: Raw blocks and optional transaction lookup

Raw Blocks retains verified serialized block bodies and compact block-level metadata. New Raw Blocks writes do not build a reverse transaction-hash index. The **TX ID Locators** index is the separate, explicit opt-in choice for new transaction-ID-to-block/position coverage, with the same timeline Arm choice governing optional sparse facts from explicitly viewed blocks.

A known block or blocator transaction/output position still opens directly: Gateway loads and decodes that exact block. A transaction hash with a known containing block can also resolve within that single block. A bare transaction ID needs existing saved location evidence or a capable provider such as an available Core txindex; Gateway does not scan all retained Raw Blocks to discover its location. Existing legacy Raw locator evidence remains readable without creating or repairing more of it. Private bridges required by explicitly running TXIO, UTXO or Sats consumers are separate from the general TX ID Locators index.

The Raw Blocks timeline control governs newly fetched bodies everywhere: **Off** uses them temporarily; **On** retains them for both Explorer and index requests. Turning it on does not start a historical scan, and turning it off does not erase existing files. Per-index source-retention controls and the Settings cache toggle are removed from the current UI. Mandatory UTXO/Sat recovery facts and optional Sat historical ranges remain separate derived data. Saved Chain history is preserved, but Chain execution is disabled.

Timeline **Arm** is also the consent for new optional TX ID Locators and Inscription facts from blocks explicitly opened in Explorer. There is no second Index as you browse preference. Disarming preserves prior facts and prevents new optional singleton writes. Viewed-block facts remain sparse; they do not advance a contiguous build head or launch Sats, UTXO, Bitmap or other prefix processing. Legacy browsing data stays readable, while missing, damaged or unconfigured timeline consent authorizes no optional writes.

Verified local bytes are reused before requesting missing bodies. Core-style block download coordination does not make Core mandatory, change index consent or authorize copying every transaction into another lookup store. Previous published release notes describe their original behavior; this development policy supersedes their Raw-derived transaction-location behavior for new writes.

## Canonical Bitmap portable preview

Gateway 0.7.8 provides **Bitmap Districts** using the pinned [Bitmap rules](https://github.com/Blockamoto/bitmap/blob/335958311eb1c8ce4d14c48bc5c21d23c7a4b134/README.md). Its identity authenticates the implemented rules and source revision; it does not resume experimental or earlier canonical checkpoints. Read-only index views expose the rules and machine metadata. A fresh index starts at Bitmap's origin, block **792435**, and evaluates consecutive blocks or resumes its own authenticated checkpoint.

Bodies come from the claimant's own inscription envelope. Parsed `text/plain` matching is case insensitive and permits MIME parameters. Evaluation uses the actual ASCII body bytes and ignores the declared charset. Exact decimal grammar rejects leading zeros, whitespace and trailing bytes. A District cannot exceed its reveal height; the first qualifying reveal wins in Bitcoin order. Current ownership and inscription numbering are separate evidence.

The new **Bitmap Parcels** track has a distinct identity;
old Parcel generations and their saved checkpoints remain read-only. A
reviewed run publishes only validated coverage and selected winners. Missing
proof stops Parcel processing without classifying unknown evidence as invalid.
Experimental discovery data is preserved read-only and is not presented as
Parcel results. Validation uses one explicitly configured proof route and the
reviewed indexing scope. There is no per-run validation-operation quota.
The plan reports prerequisite gaps without starting them. The retained Raw route
requires complete local Raw inventory from genesis through the current validation
phase and reads local blocks only. The TX ID Locator route requires complete
committed selected-chain locator coverage through that phase. It uses locators
for transaction positions, then reads or fetches verified selected blocks and
may scan forward for spends. Raw retention follows its own setting.

The IO Link route requires committed selected-chain IO Link coverage from genesis
through the current Parcel block and an audit of each batch against its verified
source block's complete input inventory and creation anchors. A complete authenticated audit of an unchanged, selected-chain
ancestor is reused when the prefix extends; the new suffix is checked. The audit
can fetch missing
source blocks; missing reverse links require a forward scan of the selected
history, and an index miss does not prove absence. Route selection and the
reviewed scope authorize the required evidence work, not other index builds.
Coverage alone does not establish readiness. Locators and links do not themselves
prove transactions, values or parent relationships. Verified transaction bytes
provide values; a separate Values history build is not required. District
publication and validated Parcel completion share a fixed catch-up boundary and
then advance together. Bitmap Districts must remain armed for Parcel work.
Parcel Live follows selected-chain extensions after catch-up when Districts and
the chosen proof producer are also Live. It waits for actual compatible evidence
and resumes when the required committed prefix arrives. A marked Out caps all
of this work.

Pause, stop and cancellation remain available. Checkpointed scan progress is
reused when its selected chain remains valid; unavailable or incomplete evidence
stops validation without advancing coverage. Positive parent proofs replay authenticated source blocks. A complete negative
scan produces a profile-authenticated local evaluation; independent replay
requires the source interval; an independent audit action is not yet available.
Deleting Raw can therefore make independent replay unavailable without changing
the recorded local evaluation. Scan results are header-anchored, not full
Bitcoin consensus validation. Both Bitmap chains remain disabled.

Positive detached receipts are bounded to 128 source blocks, 64 MiB and 128
movement hops; larger proofs remain pending. Same-transaction relationships are
resolved when pointerless envelopes share an input and their stable order is
known; other cases remain pending. A pending earlier candidate holds up the
validated checkpoint so later candidates cannot be presented as final winners prematurely. See the [workflow proposal](INDEXING-WORKFLOW-PROPOSAL-0.7.9.md) for current review and retry limits.

Custom creation, rule editing, comparisons and legacy execution are unavailable. Legacy definitions and results remain available for inspection and explicit data management. District coverage and timeline Start/Pause/Resume and Live remain available. Opening a view starts no indexing. See [portable testing](TESTING-0.7.8.md) and [verified-block ingestion](SPARSE-INDEXING-0.7.8.md).

## What coverage means

Headers identify blocks and their place in the selected proof-of-work chain. They do not contain transaction bodies or establish that any block is stored locally. A packaged snapshot accelerates first startup; ordinary Bitcoin synchronization validates and extends it.

Raw saved-body coverage counts durable bodies retained by the Raw control, including bodies fetched by other armed consumers. It is distinct from the Raw backfill cursor. A complete saved-body map provides an exact count and extent; a partial or rebuilding map shows only an observed lower bound and does not establish an end. External Core/archive/cache availability remains a separate source layer. The row never substitutes the worker checkpoint for physical body possession.

Blocks records processing and availability for requested ranges. A block can also be fetched on demand without downloading every earlier body. Header height, committed index coverage, the current job's batch and retained block bytes are separate facts. Completing a batch does not imply whole-chain coverage.

The schema and explorer work with a known block's transactions, inputs, outputs and output-relative offsets. A transaction-ID lookup can use explicit TX ID Locators coverage or existing legacy/location evidence; an unknown ID may need a containing-block hint. New Raw block storage alone does not supply that reverse lookup. An offset is not proof of global sat identity, ownership or movement history.

Sats adds genesis-first ordinal identity. Satline history tracing is deferred in this version; saved Sat locations and output ranges remain available. Bitmap District and Parcel tracks and disabled Chain execution are described above. Terrain Claim Chains, Gateway peerhood and index sharing remain gated. Fetching or viewing a block, address or sat does not start a Sat build or history trace. TX ID Locators and Inscriptions remain separately opt-in through their timeline Arm controls; their sparse viewed-block capture does not start a historical scan.

New mainnet Inscriptions scan records begin no earlier than block 767430, or a later selected start. This applies to incidental sparse capture as well as coordinated builds, even when another armed track reads earlier blocks. The older index definition retains a start height of zero for compatibility with its existing identity; that value is not the production scan origin. Previously saved earlier records remain readable and are not removed or rewritten.

TXIO keeps one committed set of spend relationships with automatic lookup in both directions. A supplied blocator needs no transaction-hash locator. Construction still needs verified creation evidence; sparse missing predecessors remain unresolved. Relationships survive source-body eviction, while navigation may need to fetch the target's bytes. See [the canonical format and measured overhead](TXIO-INDEX.md).

Values and ScriptPubKeys are optional output-history layers under TXIO, with independent coverage. Their shared output facts can also supply mandatory UTXO live/recovery records. Turning history off cannot erase required UTXO dependencies; it does not remove earlier committed history automatically. Complete facts are required for UTXO even when neither historical layer is selected.

The timeline groups Blocks into Headers/raw bodies, Transactions into TX ID Locators/IO Links/Values/ScriptPubKeys/UTXO, and Ordinals into Sat Ranges/Inscriptions, with Addresses and Bitmap as their own houses. Collapsed groups show independent coverage bands. Grouping does not combine the underlying stores: the TX ID Locators index remains separate, and UTXO has its own state track.

## Sats and Satline

Sats processes a continuous prefix from genesis, preferring local verified blocks and fetching missing evidence. Start, Pause, Resume and Live keep a readable committed checkpoint; raw bodies may be pruned independently of mandatory live and recovery facts. Ordered ranges identify which sats an output holds. Full ranges for spent outputs are optional and start off. Satline history tracing remains deferred and cannot start a background scan from the explorer.

Recovery retains the newest two savepoints at heights divisible by 12 and the newest two divisible by 144, deduplicating matching heights and sharing unchanged pages. A reorg restores an eligible selected-chain state and replays forward. This is separate from UTXO's 288 reversible transitions and does not promise 288-block Sat recovery at every height. Incompatible experimental Sat profiles and deeper recovery require an explicitly reviewed rebuild with prior evidence preserved.

Sat locations, output ranges and known address inscription holdings require compatible saved checkpoints and disclose missing coverage. See [the complete feature breakdown](SATS-FEATURES-0.7.7.md) and [Sat guide](SATS-INDEX.md). The development timeline provides Arm, Live, shared Start/Pause and index management; inspecting a resource does not start a prefix build.

## UTXO state

UTXO builds one complete prefix from genesis in block/transaction order. It saves live membership, exact amount/script facts, creation positions/anchors and the native-outpoint bridge together under an atomic head. It can reuse local blocks and fetch missing bodies through existing providers. Pause and restart preserve the last committed head; isolated arbitrary ranges cannot establish complete UTXO state.

The fixed policy retains 288 complete reversible transitions, with shared facts pinned even when a spent Coin originated years earlier. Raw bodies can be evicted after commit because local undo is self-contained. Raw Blocks remains the separate source-body control; replacement-branch processing and old-block serving still need bodies. The UTXO Pruned choice does not delete preexisting retained or external files.

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

Both the optional build-time choice and the retroactive action belong in the **Inscription inspector**. A scan can save one sparse transaction locator per transaction containing inscriptions, even if that transaction contains several inscriptions. This is independent of the complete TX ID Locators index.

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

Selecting a track or range does not start a scan. Historical builds use the global In/Out range and staged track choices; Raw Blocks controls newly fetched source bodies. UTXO uses its genesis-only state workflow and fixed recovery policy. Current Addresses setup selects UTXO Arm and Live when compatible up-to-date state is unavailable; saved compatible coverage is reused. Historical addresses allow sparse creation ranges and reuse complete retained output facts before fetching raw fallback blocks. Requests containing locked outputs fail before fetching or writing data. One coordinated run admits compatible consumers at safe block boundaries. Arming during an active run does not silently backfill earlier history; missing prior state remains blocked until an explicit Stop and a valid new Start. Pause and cancellation preserve committed work.

**Arm** selects an available track; **Live** chooses continued following after catch-up. Start begins the selected work. Choices staged during an active run apply to a later reviewed Start; the active admitted scope does not change. A stopped run can start from the validated saved/default origin, including genesis where required. Header pause and outbound-network disable remain effective controls.

Core is optional. Sources include Gateway's local block data, optional read-only Core sources and ordinary Bitcoin peers. Peers may not have a requested historical block. Source unavailability is not evidence that data does not exist.

Gateway checks header hash, proof of work, transaction Merkle root, witness commitment and selected-chain context. These checks do not perform every Bitcoin Core script or UTXO consensus validation step.

## Source retention

- **Raw Blocks Off:** newly fetched bodies are temporary, whether requested by Explorer or an index. Existing retained/cache/external data stays available.
- **Raw Blocks On:** newly fetched bodies are retained with block-level metadata. This does not start a bulk scan or create transaction-hash lookup coverage.

Older profiles, stored receipts and API parameters remain readable for compatibility. They do not add another everyday storage switch. Changing Raw Blocks does not refill or delete earlier bytes automatically. Committed processing coverage does not promise that discarded bodies remain available. Privacy and serving choices do not change older objects from private to public.

Bitcoin Blocks is durable body storage: new development records keep block-level metadata and a verified Bitcoin serialized raw body rather than permanent decoded input/output arrays or a new per-transaction reverse index. Older raw-backed formats may retain ordered transaction IDs and legacy locators; those remain compatibility evidence. Shared readers decode that body temporarily for queries, JSON inspection, builders and inscriptions. Blocks bodies are independent of ordinary cache eviction and pruneable Core files. Legacy decoded (v0) and per-block raw-backed (v2) receipts remain readable in place. Opening, querying or appending does not automatically convert them or backfill Raw transaction locators; new Raw appends use v3 body segments. Old immutable batches, cache JSON and hex files remain unless explicitly cleaned up.

## Checkpoints and recovery

Committed batches bind the definition, rules, network, range, block hash, dependencies, previous commitment and canonical records. A batch is written before its head is selected. Corrupt or incompatible checkpoints fail closed.

Restart checks saved commitments; interrupted work resumes from safe state rather than claiming unfinished coverage. An actual selected-chain disagreement rewinds to a common committed checkpoint. A temporarily unavailable source does not cause a destructive rewind. Old-branch batches do not count as selected coverage.

See [the Sats QA guide](TESTING-0.7.7.md) for current identity, range and recovery checks, [the UTXO guide](TESTING-0.7.5.md) for its independent state/recovery behavior, and [current status](STATUS.md) for exact acceptance evidence.


## Export saved records

Inscriptions and the two Bitmap indexes offer native blocators or
conventional inscription IDs in Index settings. Export uses committed saved
coverage and does not start indexing or fetch missing history. See
[Saved index exports](INDEX-EXPORTS-0.7.8.md) for the file format, snapshot
checks and explicit lookup requirements.
