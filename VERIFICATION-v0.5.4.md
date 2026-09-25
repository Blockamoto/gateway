# Gateway Client v0.5.4 verification

**Status: test candidate, not completed public-network/native-Windows acceptance.** Built and checked in this Linux container on 24 September 2026. This report separates implementation, controlled evidence, actual runtime evidence and unperformed gates.

## Baseline and purpose

The actual supplied v0.5.3 candidate has SHA-256 `8b5db865b2c183637352b04f2f0c615ad4c2593d79b55c9e81a8ca9910bdeb47`. Its source was extracted before editing. BASELINE-v0.5.4.json identifies source and diagnostic inputs by hash. The original private node reports are not distributed.

Repairs address unstable automatic endpoint advertisements; weak all-bits hints; ineffective no-Gateway results after EOF; hidden candidate checks and rounded exports; retirement of empty but compatible Gateway links; and explicit safe data removal. The exact v0.5.3 discovery ledger, Bitcoin-only introductions, integrity checks and privacy boundaries are retained.

## Final executed checks

| Check | Actual result |
|---|---|
| Full Go regression, `-race -json -count=1 -timeout=600s ./...` | 260 passing test entries, including subtests/example; 1 intentional helper skip; zero failures. Application package 264.119 seconds. |
| Rebuilt-runtime cleanup test | Passed with `GATEWAY_TEST_BINARY` pointing to the exact bundled Linux executable. Authenticated stop, exclusive lock, reviewed purge, external sentinel preservation, then new empty session without imported peers. |
| Current v0.5.4 repair entries | 17 passing entries across networking, API, cleanup and real-runtime tests. See TEST-SUMMARY-v0.5.4.json for names. |
| Go static analysis | `go vet ./...` and Windows amd64 cross-vet exited 0. Cross-vet is not execution on Windows. |
| JavaScript | Companion tests, existing UI helper tests and discovery syntax check exited 0. |
| UI/runtime | Linux Chromium with a bridge to the rebuilt application's real local API passed desktop 1440px and narrow 390px surfaces, seven setup stages, pagination, exact export, privacy, header pause and restart checks. |
| Binary audit | Four Windows PE resource trees and Linux ELF inspected. Seven icon images per Windows executable. Installer embeds the exact same three Windows executables and companion bytes shipped separately. |
| Public probe | **NO_PUBLIC_CONNECTION_ESTABLISHED**. Fresh actual runtime, Core/mounts disabled, no supplied peer list, normal Bitcoin DNS bootstrap, 12 seconds sampled, maximum 0 connections. No Gateway discovery demonstrated. |
| Native Windows installer/runtime/uninstaller | **Not run.** Unsigned executables are cross-compiled. |
| Unmodified Bitcoin Core propagation | **Not run.** Ordinary-peer fixtures are not an unmodified `bitcoind`. |
| User's public listener reachability | **Not tested from here.** No supplied user endpoint was dialled. |

The skipped entry is `TestSatlineProcessHelper`. It is a subprocess helper whose standalone invocation is intentionally skipped; the tests that invoke subprocess workflows are separate. Prior diagnostic reproductions asserted old bugs and are not substituted for these repair tests.

Final Linux executable SHA-256: `eaaa93cf33ff368dab4087fd2418c9d2770701a9aa4a00abfb9b8167525aade5`. BUILD-MANIFEST.json ties the checked source/resource inputs and binaries together. UI/public results reference the same binary hash.

## Discovery evidence

Stable-selection tests perform 2,048 reads with unchanged qualifying votes, exercise retained choices, stale/undated evidence rejection and delayed replacement. Announcement tests compare actual framed address messages across recipients, endpoint changes and recreated sessions against the rate limits.

Negative-result tests preserve independent Gateway recheck deadlines after EOF, repeated rumours, queue rebuilding and journal restart. A reconnect role cannot turn that result into a fresh candidate. The classification test gives specific and weak eligible candidates bounded opportunities without enlarging global budgets. Imported lower-bound attempts remain unknown rather than being asserted unreachable.

The new empty-member test starts actual local TCP ordinary-peer/Gateway fixtures. The receiver initially knows an ordinary introduction and learns the Gateway endpoint through address exchange. Gateway/BOD negotiation succeeds despite no usable non-genesis sample, and the connection survives beyond scout lifetime while provider search continues. The overflow-member test verifies a bounded initial service probe completes before an excess link becomes eligible for retirement. These prove exercised protocol/policy paths locally, not public discovery.

The existing noisy-graph test passes both **early-deferred** and **late-introduced** targets. Each variant retains 6,301 endpoints and 350 repeats while seven local TCP ordinary Bitcoin intermediaries lead to the previously unknown Gateway. Stale/synthetic failed dials are simulated; the address exchange, feature/BOD negotiation and block-1 request use actual local TCP and production handlers. Returned non-genesis bytes receive the existing integrity checks and stay pending chain anchoring where local headers are insufficient. Scheduling is accelerated for the fixture, not presented as production discovery time.

The 50,000 and 250,000 endpoint tests, duplicate replay, interrupted journal/crash recovery, capacity failures, malformed input, peer-private-data boundaries and existing block-integrity regressions remain in the final suite.

## Storage measurements

These non-race figures came from Test053ScaleResources on this build line with the same additive ledger fields. They are measurements of a synthetic ordinary-address workload on Linux, run alongside other build/test work, not clean isolated benchmarks or peak resident-set estimates. Actual masks, suppliers, repeated observations and provenance increase record cost.

| Records | Checkpoint/journal bytes | Live heap delta bytes after GC | Admission ms | Checkpoint ms | Queue rebuild ms |
|---:|---:|---:|---:|---:|---:|
| 50,000 | 46,261,703 | 54,855,856 | 3,872 | 2,193 | 173 |
| 250,000 | 232,127,090 | 275,488,072 | 10,892 | 2,304 | 166 |

Both samples reported complete retention and zero pending records. The active eligible queue was 682 in this concentrated synthetic address pattern, reflecting diversity policy, not lost ledger records. The race-enabled final suite repeats both sizes with instrumentation and its own measurements in release-race.jsonl. Defaults remain 250,000 records and a 512 MiB journal budget; the ledger is still memory-resident, not a disk-only index.

## UI and export scope

The UI fixture imports 120 synthetic documentation-range endpoints, including 112 hints. All 112 appear across pages; 106 weak/aggregate entries are identified by the API filter. Historical imported attempts without direct evidence do not inflate the attempted-hint counter. The fixture verifies server-produced UINT64_MAX JSON survives exactly, 121 full-ledger NDJSON lines including the metadata row, no-dial lookup, budget persistence and same-session restart.

Screenshots show runtime-derived fixture data, **not real public connections**. The renderer uses a Python fetch bridge because direct browser networking is blocked in this environment. Native browser origin/CSP behavior, actual companion approval/profile selection, cold/warm Windows address activation and OS integrations require separate native acceptance. Desktop and narrow discovery screenshots were also visually inspected; no horizontal overflow was reported by the harness.

## Cleanup safety and limitations

The shared standard-library engine is used by the runtime and custom Windows installer. Linux tests check preview without deletion, explicit execution, legacy/QA recognition, receipt-backed archive selection, refusal of roots and external overlaps, link/metadata-link rejection, byte-for-byte wallet/provider/export preservation, exclusive locks, changed-file rejection, invalid configuration and ambiguous ownership.

The real rebuilt Linux runtime test stops through the authenticated loopback API, verifies the writer releases its lock, removes selected owned data through the shared engine and starts another runtime with only new offline isolation settings. It checks a new session, no imported peers and the untouched external sentinel. It is not a graphical Windows uninstaller test.

Graphical Windows removal defaults to keep. Data deletion receives path/file-count/size/exclusion preview and a second confirmation. Separate registered custom/QA/archive stores are individually optional. Old binaries must be closed since they predate the lock. Unknown portable copies are not searched across disks. Administrative namespace removal may require UAC; quiet mode does not silently request elevation. Browser-owned approval and browser databases are not deleted.

Partial removal, locked/changed files and ambiguous normal stores are not a clean-reset success. Excluded user files can retain their directories. Native Windows CreateFile/reparse/junction, GUI confirmation, UAC, locked-file, interrupted removal and reinstallation require real-machine tests before release acceptance. The current fixtures are not a claim of an adversarial OS filesystem race-proof implementation.

## Development failures retained

The first cleanup fixture exposed deletion of the ownership marker before a later changed-file failure. The code was repaired to preflight the complete preview and remove ownership/configuration metadata last; repeated repair tests and the final suite pass. The original failed log is preserved.

The first UI run expected a filtered-total label the UI did not promise to display and timed out. The harness now checks the actual API's 106 weak records and rendered page content; the original failed attempt and subsequent successful exact-binary run are retained separately. Earlier intermediate passing runs are not labelled as the final-source run.

## Next real-machine acceptance

Back up normal Gateway data and close old copies. Upgrade both machines using matching v0.5.4 packages. Watch the server's stable chosen advertisement, conflicting observations and actual incoming-session evidence separately. Observing an outgoing IP does not establish an inbound mapping or bypass a firewall/CGNAT.

Use TestFreshStandalone.cmd on the receiver for a clean, nondestructive Bitcoin-only discovery run; it creates a new QA directory without deleting earlier state. Do not supply the Gateway endpoint to the receiver. It must be learned through ordinary Bitcoin addresses. The first real success must show introduction, attempted connection, negotiated Gateway, the required module and a useful checked request. Retaining an empty Gateway is membership evidence, not useful BOD proof.

For clean uninstall, first upgrade the installed application to v0.5.4: an old uninstaller has no new purge option. Choose the reviewed destructive alternative only after backup. Verify external Core/Ord/wallet state is unchanged and the subsequent fresh setup has no imported peer ledger. Keep this test separate from discovery retention testing, which should preserve and resume its session.

## Artifact evidence

The candidate includes matching Windows installer/portable binaries, Linux build, complete source, companion, release/migration/status notes, source diff, source/build manifests, SHA-256 checksums, final and development test logs and labelled UI screenshots. No original private diagnostic export, real peer list or user credentials are bundled. All included files are checked for ZIP integrity and audited payload hashes during packaging. See ARTIFACT-AUDIT.json for the executed artifact checks.
