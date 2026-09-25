# v0.5.3 implementation status

Implemented: exact retained ledger; canonical address/port identities; unsupported/private accounting; bounded recoverable pending queues; role and retry fairness; persistent direct evidence; checksummed transaction journal; partial-tail recovery; corruption/I/O fail-closed behavior; budget controls; cumulative versus live counters; endpoint detail and no-dial lookup; explicit ledger export; additive legacy import; resume/fresh QA launchers.

Controlled verification: canonical/replay tests, scale at 50k/250k, queue churn, deferred target, preserved negatives/cooldowns, fair roles/retries, abrupt child exit, checksums, write failures, metric partitions, API export/budget and indirect noisy-graph Gateway/BOD block retrieval. Exact results are in the generated verification report, not implied by this status list.

Preserved: Core/provider settings, block/header caches, existing Satline/Ord data, protocol negotiation and block-integrity gates, privacy defaults, Windows icons, guided setup and Browser Companion. No new indexing module, libp2p, supplied Gateway directory, archive migration or automatic Core reclamation.

Open: native Windows install/runtime durability; unrestricted public Gateway discovery; stock-bitcoind address propagation; broader pre-existing Core-off Satline/Ord gates. Controlled wire fixtures do not close these gates.
