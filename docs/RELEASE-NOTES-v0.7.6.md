# v0.7.6: Addresses

Public prerelease. Exact Windows/Linux packages, signed hosted delivery, genuine previous-client automatic upgrade and approved-feed renewal passed. See [release acceptance](RELEASE-ACCEPTANCE-0.7.6.json) for scope.

Enter a supported Bitcoin mainnet address or P2PK public key, or click an output destination, to explore its saved balance and output positions.

- **Current UTXOs** builds a reverse lookup from the saved UTXO set. Balances and counts describe that exact checkpoint, with an expandable, paginated output list. Missing or stale state shows an unavailable balance. Building addresses never starts a UTXO scan automatically.
- **Historical outputs** retains outputs from explicitly reviewed block ranges, including outputs that are later spent. It reuses complete TXIO output facts or verified raw blocks, keeps the required facts independently of raw-body retention, and reports coverage gaps. Received totals within those ranges are distinct from current balances.
- **P2PK destinations** accept valid compressed and uncompressed public keys and show the balance and outputs using that exact bare public-key locking script. Public keys are labelled as P2PK destinations; same-key P2PKH outputs, alternative public-key encodings and multisignature scripts are counted separately.
- The Addresses timeline expands into independently colored current and historical coverage bands. Build, Pause, Resume and reviewed recovery use the existing indexing workflow.

Existing profiles, raw blocks, transaction locators and TXIO records remain compatible. The new address projections are optional; upgrading does not start them or delete older data. Supported destination scripts are P2PKH, P2SH, P2WPKH, P2WSH, P2TR and P2PK. OP_RETURN and nonstandard scripts retain their existing BPS and script views.

Profiles indexed before P2PK support keep their conventional-address results. Public-key lookups request an explicit Current UTXOs rebuild or historical range rescan until P2PK-aware coverage is available, rather than reporting a false zero. Historical Live backfills those destination-coverage gaps. Prior evidence is retained.

When a profile contains newer experimental Sat state or rebuild archives, Addresses conservatively defers shared output-fact reclamation. Healthy UTXO and address processing continues, while otherwise reclaimable facts remain on disk until a Sat-aware build can safely enumerate their owners. This preserves newer evidence; it does not convert an incompatible experimental Sat profile or enable Sats in this release.

Inscription holdings wait for Sats. Results exclude mempool activity, do not imply wallet ownership or spendability, and retain the underlying UTXO stage's `consensus_validated=false` status. Full consensus validation remains a later update.

Full Linux source, race, packaging and browser checks pass, alongside focused Windows and authentic block-170 navigation checks. Exact published package and live-update checks are recorded separately; these bounded checks do not establish a whole-mainnet address build or system installation. See [the address guide](ADDRESSES-INDEX.md), [development checks](TESTING-0.7.6.md) and [release acceptance](RELEASE-ACCEPTANCE-0.7.6.json) for the current evidence and remaining checks.
