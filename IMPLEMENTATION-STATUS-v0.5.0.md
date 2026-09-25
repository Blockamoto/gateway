# v0.5.0 implementation status against the brief

| Area | Delivered | Remaining limitation |
|---|---|---|
| Integrity | Merkle mutation and witness-commitment gates; verifier-v2 receipts; old-derived-data recheck | Not complete Bitcoin consensus validation |
| Activation | Native queue, cold-start host, exact bootstrap, slash normalization | Native Windows shell must be exercised on Windows |
| Browser | Stable-ID native host, popup/status, known-namespace search recovery, per-user registrations | Each profile must approve unpacked alpha; no store publication; omnibox search may precede recovery |
| Core performance | Connection reuse, probe cache, per-object coalescing, value/fee RPC fast paths, batched writes and async ingestion | Initial full-store scan and real mainnet performance not benchmarked here |
| Satline | Durable operational jobs and pause/cancel/resume, checkpoints reused; no fixed whole-job deadline | Live 23+ hop Core lineage and real reorg recovery still need external testing |
| Addresses | `.gateway`, positional names, raw outpoint/satpoint, known inscription ID, generic URI envelope | Browser `.gateway` alias uses home.gateway or loopback; not public DNS ownership |
| Flow | Value-scaled first transaction view, unknown inputs, exact selected output and sat offset | No new mempool backend; full animated Satline flow overlay later |
| Ord | Known-ID native envelopes/content, optional local ord API, conservative initial sat binding, isolated cached content | Global indexing, numbering, provenance and complete historical/recursive parity are not implemented |
| Peers | One shared inventory, protocol participation, Core peers, manual connect/disconnect | No advanced reputation/ban interface or Ord wire protocol |
| Interface | New responsive portal shell and shared settings/jobs/storage surfaces | Existing advanced Satline view is embedded; no separate large first-run wizard |
| Migration | Permanent archive copy, readback, native locators, pause/resume, source-root safety, move preparation | Automatic Core reclamation remains locked; no source disk released |
| Packaging | GUI installer, portable, companion, Linux, source, tests and hashes | Windows installation/UAC/NRPT/profile behaviour is unverified here |

The next development pass should focus on observed Windows results and live
Core/ord differential testing, not infer public readiness from alpha test counts.
