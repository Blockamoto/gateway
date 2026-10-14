# Sats index

The Sats index assigns ordinal identities from genesis in Bitcoin transaction order. Values describe how many sats an output holds; ordered ranges describe which sats it holds. A saved UTXO set cannot bootstrap these identities by itself.

This is the v0.7.7 development preview, following the published Addresses release. Read [the complete feature breakdown](SATS-FEATURES-0.7.7.md) for the distinction between saved Sat state, on-demand traces, optional history and unavailable evidence. Preview readiness does not authorize publication or updater promotion.

## Storage and retention

The committed Sat head binds its selected block hash, live output ranges, sat location tree, shared output facts, compact native-outpoint translation, known inscription identities and recovery savepoints. Range records use BPS output positions. Amounts and exact scripts remain shared output facts; range pages do not duplicate complete transactions or raw blocks. Large fragmented range lists use bounded pages with authenticated output offsets.

The default retains live ranges and recovery data. Retaining ranges for spent outputs is an optional independent history layer, off by default. Enabling it later applies to newly processed blocks and leaves earlier gaps; disabling it preserves history already retained. Source blocks may be pruned after processing. A Satline query follows and caches its visited path; it does not silently create a global historical range index. Per-transaction fee totals and fee prefixes avoid repeatedly retrieving predecessor values when traversing fees into coinbase.

Lost rewards, genesis outputs, provably unspendable scripts and the historical duplicate-coinbase losses have explicit locations or terminal states. They are not treated as ordinary unspent outputs.

## Savepoints

Retain the newest two processed heights divisible by **12**, and the newest two divisible by **144**. Shared heights reference one state, so there are at most four distinct savepoints plus the live head. These roots pin their reachable range, identity and shared-fact pages.

For example, after processing height 575, the near savepoints are 552 and 564, and the deep savepoints are 288 and 432. After processing 576, they rotate to near 564 and 576, and deep 432 and 576; the shared height is stored once.

Recovery chooses a saved state that is still on the selected chain and precedes the replacement branch, then replays forward. Missing chain evidence leaves saved data intact. Recovery beyond retained heights requires a reviewed genesis rebuild. This policy does not guarantee rollback for every reorg of up to 288 blocks. The separate UTXO index still retains its existing 288 reversible transitions.

## Explorer

Enter a sat number in the Sats view, or use an internal address such as `5000000000.sat`. Bare numeric searches still open blocks. The sat page shows issuance, ordinal properties, containing output, output-relative offset and the address derived from its exact script when one exists. Satline tracing is a separate explicit action.

BPS positions and offsets are zero-based. An illustrative output `3.42.900000` identifies output 3 of transaction 42 in block 900000; `17.3.42.900000.bitcoin` selects offset 17 within that output. A location is interpreted with its saved block hash and checkpoint. Ordered Sat ranges have exclusive ends and retain FIFO order, which need not be ascending Sat-number order.

Satline is available locally without an enable switch. It is a tracing tool rather than another index to build. Input views show known BPS positions; unresolved previous-output positions keep their native outpoint reference until evidence establishes the translation.

The journey follows a chosen sat number or satpoint forward, including fees entering coinbase. The explicit local reverse API and inscription sat discovery can trace a point backward to establish its sat number. Both use retained compact evidence when sufficient, otherwise verified provider data. Missing spend coverage remains unresolved. A trace can be cancelled and continued from authenticated saved progress; reading a saved record alone does not revalidate it.

Address UTXOs can expand into paginated ordered ranges. Address holdings join only known indexed inscription identities to current Sat ranges at matching address/Sat heights and block hashes. Separate commitments identify the two indexes. Identity coverage is explicit; no known inscriptions does not prove complete absence or wallet ownership.

Sat locations and output views link to supported exact destinations. P2PK uses a clearly labelled full public key, separate from a conventional address. Compressed/uncompressed public keys and same-key P2PKH scripts remain different destinations. Current/historical address results retain the Addresses index's own coverage and upgrade requirements.

Queries disclose their exact processed checkpoint. A missing or lagging index is unavailable or catching up, not an empty range set or a zero balance. Viewing pages starts no indexing, repair or historical trace.

## Building and compatibility

Review a genesis-first build, choose whether to retain historical output ranges, and optionally follow new blocks with Live. Local source blocks are preferred; missing bytes use the existing providers. Pause retains readable committed state, and Resume continues the saved request.

Older experimental Sat profiles require a reviewed rebuild. Their prior files and referenced shared facts remain preserved. Addresses, UTXO, TXIO, transaction locators and serialized block bodies remain compatible. This release does not establish full Bitcoin consensus validation, add Gateway peer-state exchange or import assumed snapshots.

The preview uses existing per-index review/build controls in the Inspector. Shared Play/Pause, track arming, a redesigned track-local build workflow and general index deletion/replay/refresh controls are explicitly deferred until after Bitmap. Removing a timeline track keeps its stored data and jobs; status refresh is not a rescan.

Ordinal notations follow the pinned [Ord 0.29.0 sat implementation](https://github.com/ordinals/ord/blob/0.29.0/crates/ordinals/src/sat.rs) and [degree notation](https://github.com/ordinals/ord/blob/0.29.0/crates/ordinals/src/degree.rs). Whole-mainnet storage size and replay speed require separate measurement; early-chain and synthetic fixtures cannot establish them. Explorer reads use bounded range pages, but FIFO processing still materializes input ranges in memory. Peak memory for extremely fragmented inputs has not been measured.

See [the testing guide](TESTING-0.7.7.md) for recorded evidence and [release notes](RELEASE-NOTES-v0.7.7.md) for the release scope.
