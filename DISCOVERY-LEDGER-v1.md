# Discovery ledger v1: definitions and invariants

A record represents a canonical mainnet TCP address/host plus port, or a recognized unsupported addr-v2 family represented by its exact address bytes and port. It does not identify a unique human or physical machine. IP aliases normalize; different ports remain distinct. DNS names stay names, not assertions of a permanent IP identity.

`observations` counts admission observations including duplicates, invalid rows and explicitly unretained arrivals; `repeat_observations` counts repeated retained identities. Imports have a separate counter and do not manufacture historical observations. Inbound source-only endpoints are observations, but their ephemeral source port is excluded from dial eligibility unless independently advertised/explicitly selected later. Malformed messages have their own counter.

`attempts` counts recorded attempt starts. The start is committed before opening the socket so a crash cannot make planned work appear fresh. A crash between that commit and the syscall is necessarily ambiguous; it restores as interrupted, not as a handshake. A history-commit failure is explicitly `history_commit_failed_before_dial`. Total attempts are not successful TCP or protocol connections. Direct-version evidence, completed Bitcoin handshakes, Gateway negotiation and useful requests each require their own actual event.

`unique_candidates_checked` requires direct Bitcoin/feature evidence, not merely a failed TCP dial. Gossiped service claims, direct services and negotiated protocols remain separate. The latest service result has its own timestamp and reason. A past successful service remains historical after disconnection.

Connected/checking/cooldown/queued/deferred/excluded form the mutually exclusive current-record partition. Candidate outcome summaries are an evidence view, not an additional partition of all records. A cumulative count does not decrease with working-set or socket removal.

The ledger retains exact records in bounded RAM and committed disk frames. Four rotating work buckets reserve up to 1,024 active entries each for ordinary fresh/retry and hinted fresh/retry work. The queue can therefore be smaller than 4,096 when only one class has work. Queue caps and network-group/source diversity constrain scheduling, not retention. Role round-robin is bounded by actual free slots and global dial tokens. No guaranteed completion time is claimed under changing network conditions.

The default record budget is 250,000 and journal budget 512 MiB. Admission reserves frame/metadata overhead. Thresholds pause new intake; already in-flight results may add small bounded state overhead. One fixed staging checkpoint bounds temporary crash residue. Corruption fails closed. Uncommitted observations and a partially written tail are not treated as durable history. Ordinary operation assumes one process owns the data directory.

The full ledger export is local, opt-in and privacy-sensitive. Peer address replies use only an independently eligible small sample of public directly-successful endpoints, not the encounter ledger or failed-probe history. Unsupported/private rows are never turned into automatic public TCP probes.


## v0.5.4 additive fields and classifications

The journal schema stays v1. Optional fields preserve first/latest individual claim mask/source/time, specific-hint evidence, all-bits observations and an independent Gateway recheck deadline. Legacy OR masks remain aggregates, not reconstructed wire claims. Work cursors now distinguish ordinary/specific/weak first-attempt and retry classes. Existing shorter cursor arrays load with unused slots zeroed.

`unique_hints_attempted` counts hinted endpoints with Gateway-attempt/direct-check evidence, not generic imported lower-bound attempts. `historical_unknown` remains distinct from demonstrated unreachable and no compatibility. Historical negotiation counts are not current connections. The direct server diagnostic export is lossless; a browser JSON.parse/stringify round trip is not the authoritative representation of 64-bit service masks.

A direct negative check gates Gateway/candidate/scout rechecks for 30 minutes independently of generic transport failures; needed ordinary Bitcoin transport may reconnect under its own policy. Rumours cannot shorten the gate. Queue class fairness and overall socket/dial budgets remain independent of ledger retention.
