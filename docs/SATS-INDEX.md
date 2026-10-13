# Sats index

The Sats index assigns ordinal identities from genesis in Bitcoin transaction order. Values describe how many sats an output holds; ordered ranges describe which sats it holds. A saved UTXO set cannot bootstrap these identities by itself.

## Storage and retention

The committed Sat head binds its selected block hash, live output ranges, sat location tree, shared output facts, compact native-outpoint translation, known inscription identities and recovery savepoints. Range records use BPS output positions. Amounts and exact scripts remain shared output facts; range pages do not duplicate complete transactions or raw blocks. Large fragmented range lists use bounded pages with authenticated output offsets.

The default retains live ranges and recovery data. Retaining ranges for spent outputs is an optional independent history layer. Source blocks may be pruned after processing. A Satline query follows and caches its visited path; it does not silently create a global historical range index. Per-transaction fee totals and fee prefixes avoid repeatedly retrieving predecessor values when traversing fees into coinbase.

Lost rewards, genesis outputs, provably unspendable scripts and the historical duplicate-coinbase losses have explicit locations or terminal states. They are not treated as ordinary unspent outputs.

## Savepoints

Retain the newest two processed heights divisible by **12**, and the newest two divisible by **144**. Shared heights reference one state, so there are at most four distinct savepoints plus the live head. These roots pin their reachable range, identity and shared-fact pages.

For example, after processing height 575, the near savepoints are 552 and 564, and the deep savepoints are 288 and 432. After processing 576, they rotate to near 564 and 576, and deep 432 and 576; the shared height is stored once.

Recovery chooses a saved state that is still on the selected chain and precedes the replacement branch, then replays forward. Missing chain evidence leaves saved data intact. Recovery beyond retained heights requires a reviewed genesis rebuild. This policy does not guarantee rollback for every reorg of up to 288 blocks. The separate UTXO index still retains its existing 288 reversible transitions.

## Explorer

Enter a sat number in the Sats view, or use an internal address such as `5000000000.sat`. Bare numeric searches still open blocks. The sat page shows issuance, ordinal properties, containing output, output-relative offset and the address derived from its exact script when one exists. Satline tracing is a separate explicit action.

Satline is available locally without an enable switch. It is a tracing tool rather than another index to build. Input views show known BPS positions; unresolved previous-output positions keep their native outpoint reference until evidence establishes the translation.

Address UTXOs can expand into paginated ordered ranges. Address holdings join only known indexed inscription identities to current Sat ranges at matching address/Sat heights and block hashes. Separate commitments identify the two indexes. Identity coverage is explicit; no known inscriptions does not prove complete absence or wallet ownership.

Queries disclose their exact processed checkpoint. A missing or lagging index is unavailable or catching up, not an empty range set or a zero balance. Viewing pages starts no indexing, repair or historical trace.

## Building and compatibility

Review a genesis-first build, choose whether to retain historical output ranges, and optionally follow new blocks with Live. Local source blocks are preferred; missing bytes use the existing providers. Pause retains readable committed state, and Resume continues the saved request.

Older experimental Sat profiles require a reviewed rebuild. Their prior files and referenced shared facts remain preserved. Addresses, UTXO, TXIO, transaction locators and serialized block bodies remain compatible. This release does not establish full Bitcoin consensus validation, add Gateway peer-state exchange or import assumed snapshots.

Ordinal notations follow the pinned [Ord 0.29.0 sat implementation](https://github.com/ordinals/ord/blob/0.29.0/crates/ordinals/src/sat.rs) and [degree notation](https://github.com/ordinals/ord/blob/0.29.0/crates/ordinals/src/degree.rs). Whole-mainnet storage size and replay speed require separate measurement; early-chain and synthetic fixtures cannot establish them. Explorer reads use bounded range pages, but FIFO processing still materializes input ranges in memory. Peak memory for extremely fragmented inputs has not been measured.
