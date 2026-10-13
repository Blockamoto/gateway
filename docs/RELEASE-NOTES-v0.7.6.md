# v0.7.6: Addresses

Development release notes. This version has not been published or promoted to the automatic updater.

Enter a supported Bitcoin mainnet address or click an address on an output to explore its saved balance and output positions.

- **Current UTXOs** builds a reverse lookup from the saved UTXO set. Balances and counts describe that exact checkpoint, with an expandable, paginated output list. Missing or stale state shows an unavailable balance. Building addresses never starts a UTXO scan automatically.
- **Historical outputs** retains outputs from explicitly reviewed block ranges, including outputs that are later spent. It reuses complete TXIO output facts or verified raw blocks, keeps the required facts independently of raw-body retention, and reports coverage gaps. Received totals within those ranges are distinct from current balances.
- The Addresses timeline expands into independently colored current and historical coverage bands. Build, Pause, Resume and reviewed recovery use the existing indexing workflow.

Existing profiles, raw blocks, transaction locators and TXIO records remain compatible. The new address projections are optional; upgrading does not start them or delete older data. Supported destination scripts are P2PKH, P2SH, P2WPKH, P2WSH and P2TR. Outputs without a conventional supported address retain their existing BPS and script views.

Inscription holdings wait for Sats. Results exclude mempool activity, do not imply wallet ownership or spendability, and retain the underlying UTXO stage's `consensus_validated=false` status. Full consensus validation remains a later update.

See [the address guide](ADDRESSES-INDEX.md) and [development checks](TESTING-0.7.6.md) for storage, recovery and verification scope.
