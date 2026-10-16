# Index dependencies and on demand evidence

Use this review to plan the [timeline controls and coordinated indexing work](TIMELINE-CONTROLS-BRIEF.md) together with the authorized Bitmap development implementation. It describes the released Sats execution paths reviewed on 15 October 2026, including behavior that a catalog dependency list alone does not explain. The released behavior below is the audit baseline; implementation changes are described in the brief and must be verified separately. New Bitmap consumers use distinct experimental identities, and existing user profiles remain untouched.

## Dependency types

Distinguish five relationships in the planner and UI:

| Relationship | Example | What the planner should explain |
| --- | --- | --- |
| Source evidence | Sats consumes verified Bitcoin blocks. | Blocks must be obtainable; a retained Blocks index need not already exist. |
| Required state | Current Addresses projects a complete saved UTXO set. | The source state must exist or its build must be explicitly included. |
| Substitutable lookup evidence | TXIO needs an older input's creation position. | A local bridge, locator or suitable Core evidence can establish it; missing evidence remains unresolved. |
| Optional acceleration or history | Retained TXIO values/scripts avoid fetching old creation blocks. | Faster local answers do not imply full historical coverage. |
| Query join | Address inscription holdings join address membership, Sat placement and known identities. | Independently saved results must have compatible checkpoints and coverage. |

Presentation nesting is a separate UI concern. Sat ranges belongs visually under TXIO, while the Sats builder operates without a prebuilt TXIO index. A directory shared by internal translation data and another index also does not prove that the other index's advertised coverage has been built.

## Blocks links and state

| Capability | Required build evidence or state | On demand behavior and limits |
| --- | --- | --- |
| Headers | Connected selected-chain header evidence, using local headers or an available Core authority. | A known height/hash can be checked against that authority. Pausing a local mirror does not remove the downstream need for selected-chain evidence. Header coverage does not mean raw blocks are retained. |
| Raw blocks | A selected-chain target and actual serialized bytes from the configured local/cache/mounted/Core/Bitcoin-peer source. | One known block can supply transactions, positions, output values/scripts, witnesses and supported destinations. A pruned provider may lack it. Off stops this consumer's new acquisition/retention work; another armed consumer may still read available provider bytes without building the Blocks index. Current Blocks storage retains raw serialized objects; decoded compatibility evidence belongs to older formats. |
| Transaction locators | Verified blocks across the requested range. A fresh instance can choose an initial range; normal saved-instance continuation keeps its checkpoint rules. | BPS already identifies the containing block and transaction. A hash alone needs a known location, retained locator/graph evidence or a capable provider such as usable Core transaction indexing. Ordinary block retrieval does not discover a block from an arbitrary transaction ID. |
| TXIO positional links | Verified spending blocks plus translation of native previous transaction/output references into anchored creation positions. | Same-block and already processed origins can resolve locally. A sparse scan may need older locator/Core evidence or creation blocks. Preserve unresolved references if unavailable. Missing spender evidence never proves an output unspent. |
| TXIO Values and ScriptPubKeys | Exact output bytes for each retained block, with independent value/script coverage. | The output's own block supplies both. Enrichment does not require all spend links resolved. Required UTXO/Sat facts do not automatically advertise optional TXIO history coverage. |
| UTXO set | Ordered processing from genesis, or an authenticated saved UTXO checkpoint followed by every subsequent block. | A block supplies new outputs and native spend references, but not the prior live coin set. UTXO maintains its own native lookup and needs no prebuilt public Transaction/TXIO index. Live values/scripts and 288 reversible transitions are mandatory independently of raw retention. |
| Current Addresses | A complete saved selected-chain UTXO checkpoint. | Build by walking saved UTXOs, without another block-history scan. A known script can display its address while this index is absent; address-to-all-UTXOs and balance need the reverse lookup. Missing, damaged or stale source state is not a zero balance. |
| Historical Addresses | Reviewed block ranges, or complete retained TXIO output values and scripts for those exact blocks. | Derive matching destinations from each block or reusable output facts. No UTXO or Current Addresses build is required. Totals cover scanned creation ranges only. Historical output spend status currently remains unknown; automatic UTXO filtering is not implemented by this API. |
| Sats live state | Ordered FIFO processing from genesis, or an authenticated Sat checkpoint/savepoint followed by ordered replay. | Maintains its own native lookup, live ranges, locations, required facts and fee receipts. Prebuilt UTXO, TXIO, Transaction and Inscriptions indexes are not required. A current UTXO snapshot or one fetched block cannot supply the sats' earlier identities. |
| Historical Sat ranges | Explicit retention while the Sat engine processes those outputs. | Historical retention is optional and off by default. Enabling it later does not fill old gaps. A spent output's missing ranges are unavailable; this release starts no reconstruction scan. Live and recovery ranges remain mandatory. |

TXIO's builder translation uses a private subdirectory within the locator store, referenced by active TXIO commitments. Resolved TXIO links remain positional; unresolved translations retain the native references needed to resolve them. Turning the general Transaction locator track off must not forbid another explicitly running consumer's required internal translation, or claim that the general locator index has been built.

For inputs, distinguish the native previous transaction reference present in the spending block from the previous output's amount, script and creation position. Those facts need already retained evidence or a verified creation lookup. Sharing the current block does not manufacture unavailable older facts.

## Inscriptions identities and holdings

| Capability | Required evidence | Boundary |
| --- | --- | --- |
| Occurrences and positional IDs | Verified witness-bearing reveal blocks and the pinned parser. | Can extract from a known block without UTXO, TXIO or Sats. Lean retains positions/counts; Full retains occurrence/content evidence. |
| Content | Retained checked content/Full evidence, or the authenticated reveal block. | A known BPS/reveal location avoids global transaction lookup. A conventional inscription ID may first need its reveal transaction located. Lean may require fetching the reveal again. An optional external content provider does not establish inventory completeness. |
| Conventional inscription IDs | The known reveal transaction plus optional inscription-scoped locators committed with occurrence results. | These internal locators are separate from the general Transaction Index. Their selected build option can operate without arming general transaction history. |
| Canonical inscription numbering | Complete applicable historical placement/rule state and verified input values. | New Lean/Full builders explicitly do not perform numbering. The standalone numbering feature is locked; older evidence remains readable. A count from one block cannot establish canonical numbers. |
| Inscription Sat identity | Known identity evidence retained during Sat processing or from prior checked work. | The reveal identifies an envelope, not the complete provenance of its sat. Sats can parse and retain identities independently of an Inscriptions history build. Missing identities stay unknown while Satline discovery is disabled. |
| Address inscription holdings | Current address membership, Sat placement/ranges and known inscription identities at matching processed height and block hash. | This is a checkpoint-bound join, not another whole-chain build. No known rows does not establish no inscriptions. Content retrieval and universal identity completeness remain separate. |

Provider descriptions must agree with actual release gates and executors. Existing provider prose still describes automatic canonical numbering and Satline-derived placement, while new Lean/Full numbering is excluded and Satline is release-gated. Capability badges must use effective availability, evidence and executor behavior rather than repeating that prose as an active dependency promise.

## Disabled tracing and future Bitmap consumers

Satline forward/reverse discovery, start and resume remain disabled. Existing trace evidence is retained. Building a Sat state or opening saved ranges must not silently enable tracing to fill a dependency.

Future Bitmap preparation must distinguish candidate extraction, historical winner selection and current holder/location enrichment. One reveal block can supply claim content, but first-valid selection needs earlier winner context. A coordinate-only Lean record may still need its original reveal body fetched. Ownership requires separate known identity/location evidence and must not become a mandatory prerequisite for every reveal-only operation.

Districts, Parcels and the Bitmap audit chain are separate planned consumers. The creator's reference repository was reviewed at a fixed revision on 15 October 2026; none is enabled by this preparation. The first development pass covers generic controls and orchestration, preserving separate semantic data and avoiding duplicated source/parse work.

| Future consumer | Required context | Shared work and boundary |
| --- | --- | --- |
| Districts | Earlier candidates/winners, source coverage and the selected semantic interpretation. | Consume a shared authenticated reveal stream. A standalone reveal classifies a candidate but cannot establish the first valid historical winner. Optional interpretations may add explicit historical evidence requirements. |
| Parcels | Prior Parcel selection state, recognized District/parent context, checked provenance, and referenced block transaction-count evidence. | Share reveal parsing and block retrieval. Publish in deterministic dependency order with District state. Declared parents are claims until checked; headers do not contain transaction counts. Current ownership is separate. |
| Bitmap Chain | Completed ordered outcomes for each evaluated block, the previous cumulative outcome-chain digest and authenticated evaluation context, source identity, semantic identity and contiguous scope. | Consume evaluation receipts instead of rescanning blocks. Complete empty outcomes advance; unresolved or missing evaluation does not. A hash is not proof of correct or complete evaluation. |
| Holder/location views | Known claim identity plus compatible location/transfer evidence at a stated checkpoint. | Optional query joins; do not impose a full Sat/location requirement on every base reveal evaluation. |

Keep District, Parcel and audit data under their correct separate consumer/profile ownership while referencing shared source, extraction and output facts. The existing reference outcome stream is District-oriented; Parcel outcomes must not be folded into it without a separately defined commitment scope. Semantic profile identity is distinct from application revision, per-block outcome digest, cumulative history and richer audit-export digests. Adding Parcel interpretation must not rewrite District-only identity.

The reference contains commitment encoding/vectors, but not a complete production witness evaluator or frozen canonical profile serialization. Its Parcel edge semantics and production recovery acceptance need further work. The current locked Gateway District evaluator also has different interpretation rules, so catalog presence is not evidence of conformance. Preserve the gates and make eventual semantic/profile changes explicit. See [the future integration contract](TIMELINE-CONTROLS-BRIEF.md#future-bitmap-integration).

## Every write path needs an effective policy

| Existing path | Consequence for track controls |
| --- | --- |
| Explicit and queued build workers | Admit the consumer only through its effective run/arm policy. Preserve accepted request, pause and cancellation intent. |
| Live enable | Currently makes eligible work runnable immediately. Stage new preferences until shared Start instead of wiring the old action directly to Arm. |
| Index as you browse | Can separately persist singleton Transaction/Inscriptions results during navigation. Off must govern this optional work as well as jobs. Singleton receipts do not prove contiguous coverage. |
| Legacy GraphIndex during caching | Can derive transaction/spend knowledge independently of newer track workers. Include it in policy precedence and migration. |
| Automatic Current Addresses projection | A built address head currently advances after UTXO publication without consulting its own Off policy. Add an explicit gate; its absence must not cause an implicit address build. |
| Auxiliary repair on read | Some TXIO locator shards can be rebuilt, and a missing retained raw object can be restored during retrieval. Distinguish bounded repair of existing coverage from new history indexing; disclose source fetching when needed. |

Off stops new optional derivation, not the mandatory internal state of another armed engine. Saved evidence stays available subject to its branch, checkpoint and retained dependencies. Selecting or displaying a track must not repair, rescan or enable anything implicitly. An explicit read may resolve its requested object or perform permitted bounded auxiliary repair, but cannot start a whole-history job.

Current Addresses needs special retention and resume handling. A stopped projection can fall behind UTXO's retained transitions; after that window, rebuilding from the current saved UTXO set is the appropriate source path. If its frozen head remains queryable, retain its own pages/facts independently before UTXO cleanup can reclaim them. A frozen source checkpoint must not appear as a balance at the new tip.

## Retention and evidence ownership

The shared-fact collector currently enumerates UTXO live/recovery/archives, TXIO retained history/enrichment, historical Addresses and Sats live/savepoints/archives. Turning off a track cannot delete another owner's dependencies. Preserve exact block identity, state roots and recovery references. Corrupt or incomplete ownership manifests stop cleanup.

Required internal facts, optional historical coverage and retained raw bytes are independent policies. A mandatory fact page existing on disk does not establish complete historical value/script/range coverage for its block. Fetching a raw block also does not build a reverse address index, a complete UTXO set or a Sat identity state.

## Planner contract and examples

Expose effective availability, source alternatives, arbitrary-start rules, required processing state, internal data ownership, optional output layers, checkpoint/profile compatibility, Live behavior and query-join requirements. Use distinct readiness states: Ready, Not built, Missing source, Needs prerequisite, Waiting for selected consumer, Catching up, Paused, Stale/reorg, Repair required, Rebuild required and Locked. Give a concrete reason and action without silently starting expensive work.

Examples:

- **Sats alone:** scan/replay verified blocks into Sat state with its own live lookup. Do not arm TXIO, UTXO or Inscriptions merely because related facts are shared.
- **Current Addresses without UTXO:** explain the missing saved state and offer to include a UTXO build explicitly. Do not turn the address request into an unreviewed genesis scan.
- **Historical Addresses with retained values/scripts:** reuse complete exact-block facts where present and fetch the remaining block evidence once for the armed consumers.
- **Transaction browsing while address indexing is off:** show output destinations from scripts. Resolve previous outputs only through available authenticated evidence. Address reverse lookup/balances retain their own availability rules.
- **UTXO and Sats together:** share decoded blocks, created facts and compatible predecessor reads while retaining different state editors, recovery rules and commitments.
- **A sparse TXIO range:** show unresolved creation translations and their missing evidence; do not label the result a complete UTXO state or launch an undisclosed whole-chain scan.

The shared-pass promise is one fetch/decode of a required block for aligned ready consumers in a coordinated pass. Different frontiers, missing older prevouts, later-enabled history, eviction and reorg replay can require other reads. Bounded memory and fair Live processing take precedence over a misleading once-ever claim.

## Source references

| Subject | Implementation |
| --- | --- |
| Actual release availability and request gates | [release_availability.go](../release_availability.go), [index_catalog.go](../index_catalog.go) |
| Source reuse and serialized Blocks | [index_shared.go](../index_shared.go), [raw_blocks_074.go](../raw_blocks_074.go), [block.go](../block.go) |
| Transaction/previous-output lookup | [txresolve.go](../txresolve.go), [flow.go](../flow.go), [index_locator.go](../index_locator.go) |
| TXIO translation and independent enrichment | [index_txio.go](../index_txio.go), [index_txio_translation_075.go](../index_txio_translation_075.go), [index_layers.go](../index_layers.go), [index_txio_enrichment_075.go](../index_txio_enrichment_075.go) |
| UTXO state and automatic projection | [index_utxo_runtime.go](../index_utxo_runtime.go), [utxo_store.go](../utxo_store.go) |
| Current and Historical Addresses | [index_address_runtime_076.go](../index_address_runtime_076.go), [index_address_state_076.go](../index_address_state_076.go), [index_address_history_076.go](../index_address_history_076.go) |
| Independent Sat state and joins | [index_sat_worker_077.go](../index_sat_worker_077.go), [sat_core_077.go](../sat_core_077.go), [index_sat_runtime_077.go](../index_sat_runtime_077.go) |
| Occurrences numbering and known identity | [index_store.go](../index_store.go), [inscription_index.go](../inscription_index.go), [inscription_numbering.go](../inscription_numbering.go), [inscription_sat.go](../inscription_sat.go), [ord.go](../ord.go) |
| Browse and legacy cache writes | [index_browse.go](../index_browse.go), [block_map.go](../block_map.go), [cache.go](../cache.go), [graph.go](../graph.go) |
| Live policy and provider presentation | [index_live.go](../index_live.go), [index_providers.go](../index_providers.go) |
| Shared-fact and Sat recovery owners | [shared_output_facts_gc_075.go](../shared_output_facts_gc_075.go), [sat_recovery_077.go](../sat_recovery_077.go), [sat_pins_077.go](../sat_pins_077.go) |

The future Bitmap review used the creator's access-controlled reference at a fixed revision: [commitment methodology](https://github.com/Blockamoto/bitmap-old/blob/3dbe009313a34abf0fc0cce51a2bde39fc92d1ff/methodology/BITMAP-CHAIN.md), [audit contract](https://github.com/Blockamoto/bitmap-old/blob/3dbe009313a34abf0fc0cce51a2bde39fc92d1ff/gateway/AUDIT-CONTRACT.md) and [Parcel draft](https://github.com/Blockamoto/bitmap-old/blob/3dbe009313a34abf0fc0cce51a2bde39fc92d1ff/methodology/PARCEL-BINARY-RULESET-DRAFT.md). The summaries above describe integration boundaries; implementation and exact protocol acceptance remain separate work.
