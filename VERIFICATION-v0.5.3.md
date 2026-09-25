# Gateway Client v0.5.3 verification
## Discovery Continuity test candidate

**Report date:** 23 September 2026. **Build/test environment:** Linux amd64, Go 1.23.2. **Status:** actual packaged candidate; not native Windows or unrestricted public-network certification.

## Provenance

Baseline outer ZIP SHA-256: `8b68d171daaadcb07be8bd2d4b059e6ec1796792ac627105d2085c0298df858d`.

Baseline embedded source ZIP SHA-256: `e9b6f7793c5537b0e2f977e8b2eb13974522a758e9b49e4efef1bb946d86e1d0`.

The eight supplied audit diagnostics were executed against untouched v0.5.2 production source before repair and reproduced the documented faults. That baseline diagnostic pass is not evidence of a fixed release. Intermediate compile/test logs remain under `verification/diagnostic-history`; release claims below use the final `release-*` logs.

Production compile-input hashes are recorded in `verification/release-compile-inputs.json`. Packaging rechecks those hashes and requires successful completed build, regression, static, binary and UI checks. The Linux binary tested by the final UI and public probe has SHA-256 `d988a3aae6267f5cc04f52fd9c01773d2730755ace3dd98dd0b39689c4cdd843`.

## Implemented and exercised

The 4,096-entry working set no longer owns encounter history. An exact retained ledger stores canonical identity, pending work, first introduction, gossip/direct evidence, attempts, cooldowns and demonstrated service. Queue replacement, repeats and normal restart do not erase those records.

The shared dial budget uses explicit role fairness rather than greedy fixed-order consumption. Separate fresh/retry buckets keep due recovery eligible without allowing false hints to starve ordinary exploration. The production concurrency and aggregate dial-rate caps are unchanged.

Discovery exposes cumulative unique records/hints, actual handshake evidence, repeat observations, current-state partitions, candidate outcomes and resource limits. A local endpoint lookup does not add or dial a peer. Full ledger export is explicit and privacy-sensitive. Byte/record limits and persistence failures stop intake or new attempts rather than silently discarding retained history.

The journal is checksummed and commit-framed, with synced attempt records before dialing. Partial trailing writes recover to the last committed transaction with a warning. Corrupt committed data fails closed. Abrupt process exit, interrupted attempts, failed writes, bounded checkpoint staging and legacy import have dedicated tests.

## Final test results

| Check | Result | Scope |
|---|---|---|
| Complete Go regression run with `-race`, `-count=1` | **243 passed entries, 0 failed, 1 skipped** | Includes top-level tests, subtests and an example; these are not 243 independent product features. No race report. |
| Static analysis | Passed | `go vet ./...`. |
| Companion/UI JavaScript | Passed | Existing companion tests, Satline helper tests, shell syntax checks. |
| Windows and Linux compilation | Passed | Linux amd64 and three Windows amd64 executables plus installer; cross-compilation, not Windows execution. |
| Native PE icon and payload audit | Passed | Four Windows PE resource trees, seven icon sizes each; installer payload equals the portable core executables and companion bytes. Unsigned. |
| Linux Chromium UI/API test | Passed | Actual final binary, synthetic imported endpoint fixture, details, budget, lookup, export, resume; home/discovery/sync/peers and seven setup stages at desktop and narrow widths. |
| Actual public bootstrap probe | **NO_PUBLIC_CONNECTION_ESTABLISHED** | Fresh isolated runtime, no supplied peers; 0 maximum Bitcoin connections, public Gateway negotiation `false`. Not an acceptance pass. |

Skipped entry: `TestSatlineProcessHelper`. It is a subprocess-helper entry, not silently counted as a pass.

The UI harness imports twelve explicitly synthetic documentation-range endpoints into the real offline runtime. Its screenshots and counts represent that fixture, not discovered public nodes. A Python HTTP bridge is used for browser fetch in this container. Native browser origin/CSP, Windows extension approval and native messaging are not established by this harness. It verifies that a normal Linux runtime restart preserves the same session, records and saved budget without reclassifying historical peers as live.

## Discovery-specific acceptance

**50,000-record retention/replay:** 50,000 distinct synthetic endpoints plus normalized-IP and distinct-port cases survive repeated batches and restart. Replays increase observations/repeats rather than exact unique identity. The active queue remains bounded. Tests preserve pending candidates beyond its capacity, direct negative checks, retry deadlines and imported historical limits.

**Fair scheduling:** thirty single-token opportunities under continuous role eligibility allocate ten each to transport, candidate and exploration roles. Fresh arrivals do not prevent due retries receiving their documented share. This is a finite scheduling-opportunity property when slots/candidates are available, not a fixed public discovery-time guarantee.

**Noisy indirect discovery:** two variants each retain 6,301 endpoints and 350 repeated observations. One Gateway is introduced early but deliberately deferred until further exploration; another is introduced late. Seven ordinary local Bitcoin TCP intermediaries lead to the previously unknown Gateway. The production handshake, BOD exchange and block parser retrieve Bitcoin block 1, 215 bytes, with appropriate integrity checking and pending selected-header anchoring. The receiver is never preloaded with the Gateway endpoint.

> early_deferred: 6301 recorded endpoints, 350 repeats, 4446 simulated stale attempts; seven actual local Bitcoin intermediaries; source=bitcoin_addr:127.0.0.1:45951; Gateway/BOD returned block 1 (215 bytes), pending local header anchoring; no supplied Gateway endpoint
> late_introduction: 6301 recorded endpoints, 350 repeats, 4506 simulated stale attempts; seven actual local Bitcoin intermediaries; source=bitcoin_addr:127.0.0.1:45783; Gateway/BOD returned block 1 (215 bytes), pending local header anchoring; no supplied Gateway endpoint

Synthetic stale-endpoint dial failures in these scale tests are simulated and never sent to public IP addresses. The actual intermediate Bitcoin connections, address messages, Gateway/BOD negotiation and block exchange use real local TCP. Accelerated deterministic scheduling in the fixture is not a mainnet speed benchmark. Existing controlled service-filter and provider-loss tests also pass; their intermediary is not stock `bitcoind`.

**Storage interruption and pressure:** a child process exits abruptly after a committed attempt and partial append. Recovery retains the attempted identity as interrupted and removes only the uncommitted tail. Tests separately cover checksum corruption, an unwritable data path, lower-budget rejection, byte-limit pause across restart, record-limit overflow, metadata-only commits, source-only inbound ports, privacy exclusions and a reused bounded checkpoint staging file.

## Resource measurements

The following run omits race instrumentation. Values are measured synthetic Linux results, not estimates of public discovery speed or Windows peak memory. `live_heap_delta_bytes` is additional live Go heap after GC, not process RSS or peak allocation. Other test/build work may share the machine. Records with larger evidence fields cost more.

| Exact records | Journal bytes | Additional live heap bytes | Admission ms | Checkpoint ms | Queue refresh ms |
|---|---:|---:|---:|---:|---:|
| 50,000 | 34,976,828 | 40,600,768 | 591 | 299 | 9 |
| 250,000 | 175,511,294 | 205,750,104 | 3,291 | 1,774 | 105 |

Both fixtures retain every admitted record. An ordinary-only cohort fills its reserved 1,024-slot fresh queue, not every one of the four possible queue classes. Default budgets are 250,000 records and 512 MiB of journal storage. Controls allow up to 500,000 records / 1,024 MiB. RAM use is real: this is an exact in-memory ledger backed by a journal, not a disk-only database. Compaction temporarily needs one extra checkpoint. At a limit, arriving records that cannot be retained are explicitly counted and completeness is no longer claimed; already-committed records are not silently evicted.

## Important evidence boundaries

Native Windows installation, startup integration, browser approval, URI delivery, uninstall and Windows filesystem durability were **not executed here**. Windows files are unsigned cross-compiled candidates. Preserve data and test the isolated portable route first.

The actual public probe returned no Bitcoin bootstrap addresses and established no connections in this container. It does not prove or disprove successful discovery on the user's network. The intended serving Gateway's real advertisement, public reachability and propagation through unmodified Core remain **open**. Controlled protocol tests do not close those gates.

Attempts are recorded before the socket syscall for crash recovery. An interrupted recorded start does not prove a TCP connection happened. A history-commit failure is explicitly classified as occurring before dialing. Direct handshakes, negotiated capability and useful requests require separate actual events.

Historical imports cannot reconstruct v0.5.2's evicted endpoints. Imported attempt counts may be lower bounds. Missing or corrupt journal data is reported, not fabricated. Full ledger exports include private local history and should be reviewed before sharing. No hosted explorer fallback, supplied Gateway directory, libp2p overlay, full indexing subsystem or new archival service claim was added.

The broader pre-existing Core-off Satline/Ord and full native setup gates remain as previously documented. This release fixes discovery continuity, scheduling and observability rather than claiming unrelated completeness.

## First receiving-machine test

Extract the Windows portable archive and run `TestStandalone.cmd`. It uses `%LOCALAPPDATA%\GatewayClient-053-QA`; repeating that launcher resumes the same session. `TestFreshStandalone.cmd` instead creates a separate GUID-suffixed directory and a matching `ResumeTest.cmd`, without deleting previous tests.

Do not manually add the serving Gateway. Let Bitcoin supply the introduction. Inspect Discovery's recorded/attempted/repeated counters, expand hints for direct outcomes, and use local-only endpoint lookup to establish whether the expected server was ever learned. Export before and after a normal restart. Compare the session identifier and retained identities/checks, not just current socket counts.

## Package contents

Windows installer, portable archive, Linux archive, source, Browser Companion, current readme/migration/implementation notes, source diff, per-file build manifest, exact test logs, synthetic UI screenshots and SHA-256 checksums. Packaging checks nested ZIP CRCs and matches the executable payloads against the audited build. The external artifact audit and ZIP checksum identify the delivered outer file.
