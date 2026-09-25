# v0.5.1 candidate implementation status

This matrix describes the current code and evidence, not aspirational release copy.

| Area | Implemented here | Evidence / remaining gate |
|---|---|---|
| Independent ordinary Bitcoin pool | Persistent sessions, bounded addresses/retry, cached/manual/DNS/addr sources | Real loopback sockets and block 1 pass; public attempt establishes zero connections |
| Gateway over Bitcoin discovery | Unknown endpoint learned through Bitcoin addr, bit hint, feature/protocol/capability exchange | Controlled indirect topology and actual BOD request pass; public propagation untested |
| Header and hash retrieval | Checked chain, controls, known hash before anchoring, saved restart | Block 1 fixture passes; mainnet duration and fork recovery open |
| Core controls | Separate RPC/mount/outbound toggles, independent provider fallback | Explicit Core-disabled tests pass; live partial-Core daemon fixture open |
| Checkpoints | Match/conflict/unavailable, retain pending hops | Zero-hop and saved-hop Core-unavailable regressions, conflict and extension checks pass |
| Mounted files | Read-only reuse, opt-in locator scan, file-boundary pause | Existing mounted-block tests pass; combined offline multi-hop plus inscription gate OPEN |
| Evidence | Bytes, locator, authority and original cache provenance | Pending-to-anchored-to-offline-cache fixture passes; higher-level evidence completeness partial |
| Peer UI | Managed outbound/attempt/recent views; Core RPC peers separate | Controlled counts and rendering pass; incoming telemetry depth incomplete |
| Guided setup | Seven shared stages; two-page native installer bootstrap | Linux rendering/API-state tests pass; native Windows workflow OPEN |
| Browser connection | Selected browser/profile launch, explicit host registration, authenticated native readiness, displayed acknowledgement | Local state-machine tests pass; real profile/extension/native host/cold-warm delivery OPEN |
| Data-directory choice | Existing Gateway path displayed; archive path editable; CLI -data supported | Automatic relocation/data-directory picker not implemented |
| Upgrade/uninstall | Staged owned payload copy; existing install path detection; data-preserving owned-file removal | Cross-compiled only; crash/locked-file/native upgrade and uninstall OPEN |
| Privacy/integrity | Publication opt-in retained, HTTP destination audit, old Merkle/witness rejection gates | Regression suite passes; wider adversarial/network firewall campaign OPEN |

Full Ord compatibility/native global indexing, peer-served Ord services, new
transport protocols, browser sessions, wallets and destructive reclamation remain
outside this v0.5.1 candidate. The latest supplied handoff brief places provider
compatibility in 0.5.2, native Ord profiles in 0.5.3, and peer-served Ord capabilities
in 0.5.4; older historical roadmap ordering is not the current handoff.
