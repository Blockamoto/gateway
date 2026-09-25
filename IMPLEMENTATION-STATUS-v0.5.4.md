# v0.5.4 implementation and acceptance status

This matrix describes the current candidate. Executed test counts and environment evidence are recorded in VERIFICATION-v0.5.4.md.

| Area | Implemented behavior | Acceptance boundary |
|---|---|---|
| Exact ledger | Existing v0.5.3 journal, pending work and retention budgets preserved; additional provenance/deadline fields | Synthetic 50k/250k scale, restart/corruption/budget fixtures, not an unlimited-storage guarantee |
| Hint quality | Specific vs weak/aggregate evidence, exact new claim provenance, bounded weak share | Synthetic adversarial scheduler tests; no claim that a specific mask authenticates Gateway |
| Rechecks | Independent negative compatibility deadline, preserved across EOF/gossip/restart | Targeted race tests; normal Bitcoin transport has its own purpose |
| Advertising | Stable fresh-vote election, consistent cycle, per-recipient bounds, conflicts visible | Local policy/wire tests; no router configuration or external reachability proof |
| Gateway retention | Bounded negotiated links independent of demonstrated BOD sample | Indirect local TCP empty-member test; real public membership not established here |
| BOD sample | Later advertised ranges considered; missing sample distinguished from no data | Sample-selector and existing real block-1 local fixtures |
| Candidate UI | Primary checking counters, paginated/filterable details, imported unknown state | Linux Chromium/live API bridge; browser-native origin/CSP and native Windows separate |
| Export | Direct server JSON and NDJSON, exact masks | API/browser download checks; exported diagnostics remain private/sensitive local data |
| Uninstall | Default keep, opt-in path/count/size confirmation, separate optional stores | Windows code compiled; graphical Windows uninstall not executed here |
| Shared cleanup | Locks, ownership/path guards, unchanged-file preflight, no link traversal, protected providers/wallets/exports | Linux unit fixtures and actual rebuilt Linux runtime stop/purge/restart |
| Distribution | Linux/Windows binaries, companion, installer sharing portable payloads and icon resources | Structural/binary equality audit; unsigned, no native Windows execution |
| Public bootstrap | Ordinary Bitcoin DNS bootstrap remains the only fresh default introduction route | Current actual container probe produced no addresses/connections; unsuccessful |
| Stock Core propagation | Existing honest service declarations retained | Unmodified bitcoind intermediary not exercised in this build |

## Explicitly not delivered

No third-party bootstrap/overlay, new port-forwarding/relay/NAT traversal system, new indexing modules, universal Ord compatibility, full Bitcoin consensus validation, proof of a fixed public discovery time, or remote access to the user's computer. Existing .bitcoin/.gateway routing and setup remain; actual browser approval is not manufactured by file copying.

## Important distinctions

Recorded address != attempted connection != completed Bitcoin handshake != negotiated Gateway feature != available BOD capability != successful data request != full consensus validation. Imported historical facts != current-run checks. Observed outgoing public IP != externally reachable listening port. A candidate's disappearance from a live list does not erase its retained ledger record.
