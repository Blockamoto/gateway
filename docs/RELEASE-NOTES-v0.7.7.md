# v0.7.7: Sats

Development preview for testing. Publication, promotion to main and automatic-update promotion remain held for later approval. The acceptance record distinguishes prior source checks from pending checks of the new preview bytes.

- Genesis-first Sat identity and live placement, with shared output facts and paginated ordered ranges.
- A Sat ranges subtrack under TXIO, plus optional historical output ranges.
- Sat explorer navigation to containing outputs, offsets and addresses, plus address UTXO range views. Strict P2PK public-key destinations use the same exact-script identity as Addresses, separately from same-key P2PKH and alternative key encodings.
- Known inscription holdings at compatible checkpoints, with explicit identity coverage.
- Compact on-demand Satline evidence, visited-path caching and transaction fee-prefix caches.
- Satline is automatically available from Sat and transaction views, without an enable switch or a separate index build. Known input positions use BPS labels.
- Reviewed Build, Pause, Resume and Live controls with local-first evidence, missing-data waiting and independent pruned block retention. Retain the newest two savepoints at block heights divisible by 12 and the newest two divisible by 144, sharing identical heights and unchanged pages. Recovery restores an eligible selected-chain savepoint and replays forwards; this does not guarantee recovery from every 288-block reorg.

Existing Addresses, UTXO, TXIO and locator data remain compatible. Older experimental Sat state requires a reviewed rebuild and is preserved. Full consensus validation and Gateway peer-state sharing remain deferred.

Historical ranges start off; Satline can reconstruct a visited route without keeping ranges for every spent output. Known inscription holdings and retained history have explicit coverage limits. Full-mainnet build time, memory and storage are not established by the bounded authentic-block and synthetic fixtures. See [Sats testing](TESTING-0.7.7.md) and [this preview acceptance record](RELEASE-ACCEPTANCE-0.7.7.json).
