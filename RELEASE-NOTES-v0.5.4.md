# Gateway Client v0.5.4 test candidate
## Stable advertising, effective candidate checks, and explicit clean removal

Built from the supplied v0.5.3 candidate, following the receiver and Core-backed serving-node diagnostic reviews. This release does not add a Gateway bootstrap list, another discovery network, or a public HTTP data fallback.

### Discovery repairs

- Public IP votes expire after 30 minutes. Selection is deterministic and stays with a still-supported choice. A replacement of a previous unsupported choice has a two-minute stabilization period. Disagreements remain visible. These are outbound observations, not inbound port tests.
- An announcement cycle uses one endpoint/service snapshot for all recipients. Unchanged announcements have a 30-minute recipient interval; changes have a five-minute minimum. In-process recipient history survives reconnects. Short-lived scouts do not emit the repetitive announcements seen in the diagnostic report.
- Specific bit-25 claims are distinct from indiscriminate all-bits and legacy aggregate claims. The exact first/latest claim and its supplier are retained for new observations. Existing accumulated masks are not retroactively invented as individual wire messages.
- Candidate opportunities give specific evidence a 3:1 share over weak evidence when both classes remain eligible. Weak evidence is not discarded. The aggregate dial rate, concurrency, ledger limits and role fairness remain bounded.
- A direct no-Gateway check has a separate 30-minute recheck deadline. EOF, a generic connection failure, repeated gossip, a restart or relabelling as a scout does not shorten it. Ordinary Bitcoin transport can still be maintained for its separate purpose.
- A bounded set of negotiated Gateway connections stays retained even when it cannot provide a non-genesis BOD sample. Missing data services keep discovery seeking providers. Excess negotiated peers get a bounded initial probe, not an unlimited socket reservation.
- The sample selector checks later ranges rather than concluding failure from a leading genesis-only range. No sample is labelled as no demonstrated sample, not proof that the entire remote machine has no data.

### Visible checks

The leading view shows hints seen, hints attempted, directly checked hints and historical negotiations beside current connection and useful-service state. The candidate-check panel comes before storage statistics. Filters and 25-row Next/Previous pages traverse retained candidates beyond the compact export's 80-row sample. Imported records without direct-check evidence are labelled unknown.

Diagnostics are downloaded directly from the server rather than parsed and re-encoded by browser JavaScript. Full-width uint64 values and explicit hexadecimal masks survive. Full-ledger NDJSON export and no-dial local endpoint inspection remain available.

### Uninstall and data ownership

Windows uninstall preserves data by default. Explicit removal asks for the installed/recognized legacy stores, separately offers known QA/custom profiles and archives, then previews paths, file counts, bytes and exclusions before final confirmation. Quiet deletion requires both `--purge-data` and `--confirm-purge` with `--uninstall --quiet`.

The shared removal engine requires an exclusive profile lock, safely stops the matching runtime, rejects roots/ambiguous ownership/external overlaps, does not follow symlinks or junctions, and deletes only unchanged approved files. Core/Ord databases, wallets, mounted source files and recognized user exports are excluded. Separately configured archives require matching Gateway block/receipt pairs. Failed or partial removal is not reported as a clean start.

Runtime ownership markers and profile locks prevent concurrent v0.5.4 writers. Installed profiles register their actual path for later review. Portable launches do not register Windows integration merely by running. Old binaries do not honor this lock, so close every old copy before deletion.

### Scope and limitations

No global transaction/sat/Ord indexing expansion, new logo, browser fork, libp2p, automatic NAT mapping or trusted hosted bootstrap is included. The dial ceiling remains one token per five seconds with a burst of three: improved prioritization is not a claimed measured public speed increase.

Actual Windows execution, unmodified Bitcoin Core propagation, public Gateway discovery and external listening-port reachability remain separate acceptance gates. All executables are unsigned. Read VERIFICATION-v0.5.4.md for the exact executed tests and outcomes; historical notes describe their own versions, not this release's evidence.
