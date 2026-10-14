# v0.7.7: Sats

Development release candidate. Publication and automatic-update promotion remain held for later review.

- Genesis-first Sat identity and live placement, with shared output facts and paginated ordered ranges.
- A Sat ranges subtrack under TXIO, plus optional historical output ranges.
- Sat explorer navigation to containing outputs and addresses, and address UTXO range views.
- Known inscription holdings at compatible checkpoints, with explicit identity coverage.
- Compact on-demand Satline evidence, visited-path caching and transaction fee-prefix caches.
- Satline is automatically available from Sat and transaction views, without an enable switch or a separate index build. Known input positions use BPS labels.
- Reviewed Build, Pause, Resume and Live controls, independent pruned block retention, and two savepoints each at twelve-block and daily intervals.

Existing Addresses, UTXO, TXIO and locator data remain compatible. Older experimental Sat state requires a reviewed rebuild and is preserved. Full consensus validation and Gateway peer-state sharing remain deferred.
