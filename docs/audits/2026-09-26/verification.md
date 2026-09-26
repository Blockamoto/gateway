# Audit verification record

Date: 2026-09-26. Source v0.5.4 at `5acabb0ac27c6bcd9786b65a43236dd2962982b8`; checked head `7f2ce1b9d39f2105c7ba0f116d5cd9925f0d2ff2` only adds the brief. Production source was not edited. This record is audit evidence, not a repaired-release acceptance certificate.

## Acquisition and environment

- Authenticated GitHub connector access succeeded. Local Git/gh credentials were expired; no credential changes were made.
- All 195 repository blobs were retrieved at the pinned commit and checked against their Git SHA-1 object IDs, including binary assets. Reconstructed tree `dbf5bb3f4f5bcb6225d9015f3bc48d516399ed27` and commit IDs matched exactly. The local checkout is a shallow snapshot, not a complete history clone.
- Native Windows amd64; portable Go `go1.27.1 windows/amd64`, standard-library-only build with `GO111MODULE=off`.
- Official Go archive `go1.27.1.windows-amd64.zip`: 78,931,360 bytes; SHA-256 `a3911b5e0e1b1053f25ed0675f4c1c6aad1e2bfcf253df2b9be4caabd2edd95d`. Downloaded from the official Go distribution, verified before extraction, installed only in an audit temporary tools directory. Interrupted downloads were resumed and the complete hash checked; no system installation or PATH modification.
- `CGO_ENABLED=0`, configured compiler name `gcc`, no installed gcc found. Race testing was not executed.
- Rebuilt Windows client SHA-256: `7a77c1927dca52ed74c824ff8d1cb0be78585b081855b395c6780dffdb7f104d`. Build command: `go build -o <audit-output>/gateway-audit.exe .`.

## Baseline checks

| Check | Result |
|---|---|
| Native build | Passed |
| `go vet . ./internal/cleanup ./packaging/native-host ./packaging/gateway-launcher` | Failed: existing `satline_store.go:182:21`, non-constant format string in `fmt.Errorf(a.satlineInitErr)` |
| Runtime suite with vet separated | **251 passing test entries, 4 failed, 6 skipped**, including subtest entries; application package 61.568 seconds |
| Rebuilt-runtime cleanup | `Test054RebuiltRuntimeCleanProfile` passed, 4.52 seconds, using the exact rebuilt binary and temporary profile |
| Companion JS tests | Search parsing and namespace privacy filter passed |
| UI helper JS tests | Escaping, exact coordinates and terminal labels passed |
| `node --check ui/shell/discovery.js` | Passed |
| Race, browser rendering, two-machine soak, GUI installer/uninstaller, stock Core topology, new public discovery | Not executed |

Runtime command (all four source packages explicitly listed):

```powershell
$env:GO111MODULE = 'off'
$env:GATEWAY_TEST_BINARY = '<absolute-path-to-rebuilt-gateway-audit.exe>'
go test -vet=off -count=1 -json -timeout=600s . ./internal/cleanup ./packaging/native-host ./packaging/gateway-launcher
```

`-vet=off` separates runtime evidence from the reported static-analysis failure; it is not a claim that ordinary `go test` is green. The first exploratory `./...` invocation also picked up a transient audit-only `.go` fixture as an unrelated package. That fixture was renamed `.go.txt` and all baseline runtime results above come from the subsequent explicit-package command. Packaged reproductions use virtual overlays and do not change test discovery or production files.

### Failures

1. `Test051HeaderBadBatchRollsBack`: a rejected header batch changed the selected header file. The same test failed again in isolation. Additional Windows reproduction established the cause: `O_APPEND` opens a handle on which `Truncate` returns access denied; the error is ignored. File size changes from 80 to 160 while count 1 and a validation error are returned. The first retained header passed validation; this does **not** demonstrate acceptance of the invalid second header.
2. `Test054PurgeKeepsExternalAndUserFiles`: cannot create required fixture symlink; Windows reported a missing privilege.
3. `Test054PurgeRejectsOverlapAndRoot`: same symlink fixture privilege failure.
4. `Test054MetadataLinksCannotGrantOwnership`: same symlink fixture privilege failure.

The last three do not establish deletion-engine defects or passing native link protection. They leave the respective checks unexecuted at their required boundary. No attempt was made to elevate privileges or change Windows configuration.

### Skips

- `Test050ArchivePathCannotEnterCoreThroughSymlink`: missing symlink privilege.
- `TestCoreLookupAndOverlay`, `TestLightPeerServesVerifiedCacheAndHeaderLocation`, `TestBIP434NegotiationBetweenBODPeers`, `TestUnknownIsNotNegative`: fixed port 48333 unavailable. The existing port owner was not stopped or reconfigured. Supplementary session fixtures use loopback ephemeral ports but are not substitutes for every skipped assertion.
- `TestSatlineProcessHelper`: intentional subprocess helper.

The [machine-readable summary](test-summary.json) includes per-package results and exact test messages, with local user/temp paths sanitized.

### Controlled ledger scale measurements

These are one native Windows, non-race run of existing synthetic fixtures, not public-network estimates or worst-case bounds. The active queue is only 682 because this fixture populates one evidence class.

| Retained endpoints | Admission | Checkpoint | Queue rebuild | Journal bytes | Live heap delta bytes | Active queue |
|---:|---:|---:|---:|---:|---:|---:|
| 50,000 | 644 ms | 1,113 ms | 67 ms | 45,960,573 | 54,023,488 | 682 |
| 250,000 | 5,188 ms | 2,292 ms | 295 ms | 230,625,702 | 274,932,104 | 682 |

This supports the source observation that a small working queue does not imply a small retained-memory footprint. Measure session latency during compaction/export and varied evidence distributions before selecting a replacement storage/scheduling design.

## Supplementary audit reproductions

Run from the repository root:

```text
python docs/audits/2026-09-26/run-reproductions.py --go <path-to-go-executable>
```

The runner sets `GO111MODULE=off`, creates a temporary Go overlay, runs only `^TestAudit` with vet explicitly disabled, and removes that overlay. No production file is created or modified. The fixtures are [network](network_floor_repro_test.go.txt) and [index/apps](index_apps_repro_test.go.txt). The Windows-specific rollback reproduction skips on other platforms. All use temporary stores, local sockets or direct production handlers; no public peers are contacted. The manifest fixture creates a unique module directory beside the temporary Go test executable and removes only that fixture directory.

**These tests pass when they observe baseline bugs. They must be inverted into desired-behavior regressions when implementing repairs.** Graph fixtures contain synthetic receipts to isolate storage/publication behavior; they do not establish Bitcoin consensus validity of synthetic transactions.

| Test / group | Observation |
|---|---|
| Request timeout | Successful ping/pong followed by transport removal when withheld headers time out |
| Inbound routing | Valid inbound Bitcoin/Gateway/BOD negotiation and status work, but managed routes/BOD counts remain empty |
| Lifetime request quota | 64 valid BOD status replies, EOF on request 65 |
| Serving disabled | Managed BOD handler emits no unsupported reply |
| Header selection | Receive-only Gateway member is eligible for Bitcoin header requests |
| Automatic replenishment | Positive counter-evidence: losing one, then two ordinary sessions is repaired without Retry; token interval shortened only for the fixture |
| Windows rollback | Valid prefix persists after rejected batch; append-only handle rejects truncation, normal write handle can truncate |
| Graph damage: delete/truncate/CRC | All three damage variants yield false `unspent_at_snapshot` while markers survive |
| Private graph publication | Private-aware cache refuses serving; graph-first BOD handler returns the same hint and status coverage |
| Manifest consent | Incompatible, non-advertised passive manifest appears in BOD extension inventory |

The initial six networking tests completed in 2.528 seconds; the isolated header diagnostic in 0.208 seconds; three index/app tests (including three damage subtests) in 0.437 seconds. After packaging and formatting, the combined runner was executed successfully as well. Retained transcripts: [network](network-floor-test-output.txt), [header regression failure](header-rollback-isolated-output.txt), [header cause](header-rollback-cause-output.txt), [index/apps](index-apps-test-output.txt), [combined packaged run](packaged-reproductions.txt). Transcripts sanitize local paths; original full runtime logs remain in local audit working files.

## Unproven acceptance boundaries

No new stock-Core propagation/topology experiment, public unknown-Gateway first contact, NAT reachability proof, adversarial load test, full historical Ord/Bitmap differential scan, browser-rendering QA or GUI installer/uninstaller acceptance was performed. Source inspection of pinned Core establishes its filtering policy; it does not measure deployment density or public convergence. The prior repository verification reports remain historical evidence with their stated environments. This audit does not convert their fixture successes into public-network success.
