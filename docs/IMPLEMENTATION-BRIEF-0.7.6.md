# v0.7.6: Addresses

Implement on the `v0.7.6` development branch. Main, release publication and the live updater require a later explicit instruction from the user.

## Address views

- Current UTXOs derives a complete reverse address lookup from the saved UTXO state. It reports a balance, output count and paginated output list at that exact height, block hash and state commitment. Building it never implicitly starts a UTXO genesis scan.
- Historical outputs scans reviewed ranges, reuses retained values and scripts where verified, and fetches missing raw blocks through the existing on-demand providers. It retains facts after outputs are spent. Explicit coverage and gaps distinguish a sparse range total from a complete history or current balance.
- Accept supported mainnet addresses with strict checksum and network checks. Index the exact scriptPubKey; do not invent addresses for bare public-key or nonstandard scripts. Their existing BPS and script access remains available.
- Entering an address or clicking an output address opens the explorer. Balance leads the current view; UTXOs and historical outputs expand into paginated lists. Unavailable balances are never displayed as zero.

## Storage and recovery

Address lookup rows reference shared immutable output facts and BPS positions. Current lookup follows one exact UTXO checkpoint and reconciles the same 288 retained reversible transitions. An interrupted or stale projection fails closed until repaired or explicitly rebuilt. Historical commits authenticate block anchors, retain empty evaluated blocks and pin output facts even after raw eviction or a spend. Reorganizations remove disconnected rows from active coverage while retaining prior committed evidence. Queries bind cursors to their address and snapshot.

The Addresses timeline group expands into Current UTXOs and Historical outputs, with independent coverage and colors. Use existing reviewed Build, Pause and Resume controls. Current mode has no arbitrary historical range; historical mode supports explicit sparse ranges. Existing profiles remain compatible.

## Boundaries

Inscription holdings and fee/sat ranges wait for Sats. This release adds no mempool balance, ownership claim, peer sharing or complete consensus validation. Saved UTXO state remains header-anchored processing with `consensus_validated=false`. No automatic publication or feed promotion is authorized for this development branch.

## Verification

Check address encodings, cross-address and revision cursors, exact balance totals, current dependency failures, paused/restarted builds, updates and reorganizations, historical gaps and empty blocks, output-fact retention after eviction, old-profile compatibility, explorer navigation and automatic query-free rendering. Run focused backend and browser checks followed by the applicable regression suite.
