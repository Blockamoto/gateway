# v0.7.7: Sats

Implement on `v0.7.7`, starting from the tested Addresses source. Main, release publication and the live updater remain unchanged until the user requests release. Incorporate any subsequent Addresses fixes before that review.

## Sat identity and output ranges

Adapt the existing FIFO engine to the shared output-facts model. Calculate ordered sat ranges from genesis, processing transactions and outputs in Bitcoin order, including issuance, fees entering coinbase, unclaimed rewards, burned outputs and historical duplicate coinbases. A current UTXO snapshot supplies amounts and scripts, but does not establish sat identities. Use local blocks first and the existing on-demand providers for missing evidence; no new peer-state or snapshot protocol is added.

Retain current output ranges and a sat-number-to-output lookup at one exact selected-chain checkpoint. Use BPS positions and shared amount/script references rather than duplicating full transactions. Keep range fragmentation bounded with pages. Mandatory state also represents lost, destroyed and unspendable sats, which are absent from ordinary live UTXO membership. Missing or corrupt dependencies cannot produce a known location or an empty result.

Add a Sat ranges subtrack beneath TXIO, with independent coverage. Display retained live ranges and explicitly retained historical ranges without inferring complete block coverage from selected outputs. Full historical range retention is optional and off by default. A request for one Satline follows and caches the visited path; it does not automatically build or retain every historical output's range set.

Cache authenticated per-transaction fee totals and block fee prefixes for on-demand fee traversal. Ordered input/output amounts and spend links remain necessary. A cached path for one sat does not prove the full contents of an old output. Missing historical evidence remains unresolved; full historical ranges and raw block retention remain separate choices.

## Savepoints and recovery

Keep the newest two Sat savepoints at heights divisible by 12 and the newest two at heights divisible by 144. Matching heights share one state. The live head is independent of these at most four savepoints. Publish replacement references atomically and keep all reachable range pages, inscription identity pages and shared output facts pinned before releasing old dependencies.

After a reorg, select a retained savepoint on the replacement chain at or before the common ancestor and replay forward. A savepoint is a restore point, not a proof of full history or a guarantee that replay data is local. Missing chain evidence cannot trigger a destructive reset. Once established, the oldest deep savepoint is 144–287 blocks behind the processed tip; recovery deeper than the available saved heights requires a reviewed rebuild, preserving old data. The UTXO index's existing 288 reversible transitions remain unchanged.

## Explorer and address integration

Provide a Sat explorer with issuance, ordinal properties, saved location, output BPS position, output-relative offset and a clickable address derived from the shared locking script. Non-address scripts and lost sats show their actual script/location state.

The address page's UTXOs expand into paginated ordered Sat ranges, with navigation to individual sats. Show ranges only at compatible exact checkpoints; missing or lagging state is unavailable or catching up. Include known indexed inscription holdings by joining inscription identity to its Sat location and output script. Expose identity coverage and distinguish unknown holdings from zero. This stage does not establish wallet ownership, spendability, mempool balances or universal inscription completeness.

Enable Satline's existing local progress, cancellation, resume and anchored-cache behavior with compact TXIO/fact adapters. Forward and backward tracking include fee-to-coinbase transfers; avoid full-block decoding when sufficient authenticated local facts exist. Querying or opening an explorer view never silently starts a genesis build.

Local Satline tracing is available whenever released, without a separate enable setting or an Add Index entry. Preserve old configuration fields for compatibility while removing their local tracing gate. Network exchange remains separately locked. Transaction inputs show their known BPS positions and verified original-output positions; native outpoints remain evidence or an unresolved-position fallback.

## Build controls and compatibility

Use reviewed Build, Pause, Resume and Live controls for genesis-first Sat state. Raw block retention is pruned and independent of mandatory Sat state/savepoints. Show exact processed/target anchors, saved checkpoint heights, recovery limits and honest progress/storage measurements. Preserve incompatible legacy experimental Sat evidence for a reviewed rebuild. Existing Addresses, UTXO, TXIO and locator data remain compatible.

Full Bitcoin consensus validation remains deferred to v0.8. Bitmap and Gateway peer/state-sharing remain locked. Test bounded authentic blocks and synthetic fragmentation/reorg cases; measure and qualify fixture costs rather than claiming a full-mainnet storage or speed benchmark.

## Acceptance checks

Check FIFO ordering, fees/prefixes, same-block spends, genesis/lost/burned/BIP30 states, live range/value agreement, shared-fact pins after eviction, savepoint divisibility/rotation/deduplication, interrupted publication, common-ancestor eligibility, replay/resume and deeper recovery failure. Check sparse historical coverage, cursors, stale responses and unknown results. Exercise real explorer/address navigation, known inscription holdings and coverage, range pagination, reviewed genesis controls, paused queries, restart/reorg cache reuse, narrow light/dark layouts and no implicit indexing. Run focused regressions, full Go/static/race checks, packaging consistency and browser checks on the final branch source.
