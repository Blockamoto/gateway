# v0.7.7 Sats: feature breakdown

This document describes the v0.7.7 Sats prerelease, following v0.7.6 Addresses. It explains saved index answers, optional history and deferred tracing. Source tests, package execution and signed updater delivery have separate evidence in the release acceptance record.

## Saved Sat tracking

The Sats index supplies individual Sat locations and ordered output ranges at one saved selected-chain checkpoint. It starts through an explicitly reviewed genesis-first build. A saved UTXO set supplies values and scripts but cannot supply Sat identities by itself.

**Satline history tracing is disabled for this version.** Long on-demand scans need a clearer cancellation and work-control design before release. The explorer does not offer Trace/Follow actions, and backend, CLI and browser entry points cannot start or resume a trace. Existing derived evidence is retained. This change does not disable the Sats index, saved location lookup, output ranges or compatible address holdings.

## Find a sat and its saved location

Enter a sat number in the Sats view, or navigate to a canonical address such as `5000000000.sat`. In Gateway's address bar, a bare integer up to the selected chain tip opens that block; a larger integer opens a Sat. Explicit `N.sat` and `N.bitcoin` keep their meaning. Without a known selected tip, use an explicit suffix. Successful number entry is saved with its canonical suffix, so later tip changes do not change its history entry. Browser-companion `.sat` handoff is supported; ordinary browser number searches remain unchanged. Ordinal names such as `nvtdijuwxlp` or `nvtdijuwxlp.sat` open the same Sat as `0.sat`. Names decode locally without a chain-tip check or historical scan; existing hashes, destinations and explicit resource namespaces keep precedence. Explicit named `.sat` links also work through the companion.

The explorer shows theoretical issuance information and ordinal properties: rarity, name, decimal notation, degree notation and percentile. These properties describe issuance; they do not themselves prove a current location. With a compatible Sat checkpoint, the page also shows the containing output's BPS position, offset within that output, location state, and exact snapshot height/hash. A paused index remains useful for answers at its saved checkpoint, which may precede the selected tip.

Sats not yet issued at that checkpoint, missing state, stale/reconciling state and terminal locations remain distinct. A missing or corrupt dependency cannot become a known location or an empty result.

The index explicitly accounts for genesis coinbase sats, provably unspendable outputs, unclaimed subsidy/fees and historical BIP30 duplicate-coinbase losses. These are not ordinary spendable UTXOs. Recognized unspendable scripts include `OP_RETURN` and scripts exceeding 10,000 bytes. The index does not infer private-key loss or wallet spendability.

## BPS positions, offsets and output ranges

BPS identifies an output as `output.transaction.block`, with zero-based ordinals. The illustrative `3.42.900000` means output 3 in transaction 42 at block 900000. A satpoint adds its zero-based output-relative offset: `17.3.42.900000.bitcoin` selects offset 17 in that output. Known inputs also show their own `i<input>.transaction.block` positions. A previous output's BPS position appears only once its native prevout has been resolved.

Positions are bound to selected-chain block hashes and saved checkpoints. A coordinate alone cannot reinterpret evidence from an orphaned block as evidence for its replacement.

Ranges describe an output's sats in FIFO order, with exclusive ends. `[100,103)` contains sats 100, 101 and 102. A synthetic five-sat output could contain `[100,103)` followed by `[7,9)`; those fragments begin at output offsets 0 and 3. Sorting them numerically would destroy their output order.

Range views are paginated, with checkpoint-bound cursors and authenticated output offsets. The UI checks output identity, matching snapshots, contiguous offsets and total value. Users can open range endpoints or enter another Sat number from a displayed range. Stored fragments and read responses use bounded pages; FIFO processing can still materialize all input fragments in memory.

An individual Sat page uses the main address bar rather than a duplicate search form. At the timeline's output-local Sat level, selecting an offset opens its numbered Sat when authenticated ranges are retained. Missing ranges show unavailable identity; the action does not start Satline, fetch history or fall back to a transaction page.

## Addresses and public-key destinations

The Sat page links to its containing output and supported script destination. Current address UTXOs expand into ordered ranges and link back to individual sats. These joins require Address Current and Sats to share the same processed height and block hash; each retains its own commitment. Missing or lagging indexes remain unavailable/catching up, and opening the page does not build them.

Conventional addresses keep their usual meaning. P2PK outputs expose a clearly labelled full compressed or uncompressed public key as the destination. Clicking or searching it opens the existing Current/Historical destination view. Matching uses the exact locking script: compressed and uncompressed keys and a same-key P2PKH address are separate destinations. Other scripts do not receive invented addresses.

Addresses retains its own coverage and compatibility rules. Older conventional-only Current data needs a reviewed P2PK upgrade; Historical P2PK results require P2PK-aware scanned ranges. Sats integration does not turn missing destination coverage into a known zero balance.

## Known inscription holdings

The address page can list known indexed inscription identities whose sats occupy its saved UTXOs. The join uses inscription-to-Sat identity, current Sat placement and the exact output script, disclosing both Address and Sat snapshots. Results are paginated and checked against those snapshots; checkpoint changes invalidate old cursors/results.

Identity coverage is explicit. No known rows does not prove an address has no inscriptions. The feature does not establish universal inscription completeness, wallet ownership or mempool holdings. Existing inscription content/locator indexes remain separate. Inscription identities established during Sat indexing or retained from earlier work can be joined to saved locations; an address lookup does not start identity discovery.

## Deferred Satline history

Satline's forward journeys, reverse identity discovery and continuation controls are unavailable in this version. Direct routes explain the deferral instead of starting work. A saved location answers where a Sat is at its checkpoint; it does not claim to show the Sat's complete transfer history.

Existing visited-path and fee-cache data is preserved. No full historical range index or on-demand scan starts when opening a Sat, output or address. Optional saved historical ranges still work where the user explicitly retained them during index processing.

## Fee caches and optional history

Per-transaction fee totals and ordered block fee prefixes are stored in authenticated pages under a block-hash anchor. Sat processing writes receipts after resolving ordered input values. These retained receipts prepare future tracing without enabling it in this version. With a compatible cache, tracing need not retrieve every preceding transaction's input values again merely to place a fee sat in coinbase. Ordinary hops still require ordered input/output values and spend links. This is a tracing cache, not a new fee-market explorer or an end-to-end speed guarantee.

| Retained information | Policy |
| --- | --- |
| Live Sat placement, terminal states and recovery dependencies | Required for the explicitly built Sat state |
| Complete historical output ranges | Optional, off by default |
| Visited Satline paths and fee receipts | Fee receipts are derived during index processing; prior trace evidence is preserved but tracing is disabled |
| Serialized raw block bodies | Independent source-retention policy; may be pruned |

Turning historical ranges on later affects newly processed blocks and leaves older gaps. Turning it off preserves history already retained. A few saved outputs or cached paths cannot establish whole-block historical coverage. An old spent output whose full ranges were not retained is unavailable as a complete range list; on-demand reconstruction is deferred with Satline.

## Build, pause, resume and recovery

The Sat index starts at mainnet genesis and processes Bitcoin transaction/input/output order. Existing local sources are preferred; configured providers supply missing evidence. The reviewed build exposes optional historical ranges and Live catch-up/following, with pruned raw-body retention independent of mandatory Sat state. Opening a view starts no build, historical scan or repair.

Progress advances at durable checkpoints. Pause keeps committed queries readable; Resume continues the saved request. Missing evidence can put a job into Waiting, while terminal incompatibility/corruption requires reviewed recovery. The panel shows processed/target anchors, saved heights, recovery boundary and available output/lost/destroyed counts. Sat storage bytes and range-fragment counts are not currently populated by status and may display **Unavailable**.

Savepoints retain the newest two processed heights divisible by **12**, plus the newest two divisible by **144**. A height meeting both rules references one saved state. There are at most four distinct savepoints plus the independent live head, retaining their reachable range, inscription-identity and shared-fact pages.

At height 575 the saved heights are 552, 564, 288 and 432. At 576 they become 564, 576 and 432, sharing 576 between schedules. Recovery restores an eligible savepoint on the replacement chain at or before the common ancestor and replays forward. Replay evidence may need refetching after raw pruning. This is not a guarantee of recovery from every reorg up to 288 blocks. The separate UTXO index still retains 288 self-contained reversible transitions.

Beyond available recovery points, or for an incompatible experimental Sat profile, an explicit reviewed genesis rebuild preserves prior state and referenced dependencies in an archive. Ordinary Resume does not reset it. Missing chain evidence does not authorize discarding old state. Existing Addresses, UTXO, TXIO and locator stores remain compatible; shared-fact collection continues to enumerate Sat current, savepoint and archive owners.

## Validation and deferred work

Results are anchored to the selected mainnet chain and checked local evidence. Full independent Bitcoin consensus validation is not implemented; snapshots report `consensus_validated=false`. A UTXO snapshot cannot bootstrap assumed Sat identities. Gateway peer/state sharing remains locked.

Recorded tests cover bounded genuine blocks and synthetic FIFO, fee, fragmentation, BIP30, missing/corrupt evidence, savepoint, interruption, reorg and UI cases. They do not establish whole-mainnet replay, storage size, replay speed or peak memory for extremely fragmented inputs. Receipts apply only to their recorded source/binary. Final packaging, public downloads and automatic-update acceptance remain separate checks.

Shared timeline Play/Pause, per-track arming, the broader track-local review/build workflow and general index deletion/replay/refresh controls are explicitly deferred until **after Bitmap**. Sats uses existing per-index controls in the Inspector. Compatible Blocks-output scans already share retrieval/decoding, but are not that future all-track transport design. Removing a track keeps its data/jobs; refreshing status does not rescan an index. Bitmap and parcel features are outside this preview.

See [the Sat guide](SATS-INDEX.md), [implementation brief](IMPLEMENTATION-BRIEF-0.7.7.md), [testing guide](TESTING-0.7.7.md) and [release notes](RELEASE-NOTES-v0.7.7.md).
