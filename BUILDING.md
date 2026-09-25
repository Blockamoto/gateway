# Building Gateway Client v0.5.4

Baseline: the supplied v0.5.3 discovery candidate. Go 1.23+, Python 3 and `GO111MODULE=off` are required for compilation. The Go program uses the standard library with no fetched modules. Node is used for companion/UI checks; Chromium and Python Playwright are rendering-test dependencies, not shipped runtime dependencies.

```sh
export GO111MODULE=off
python3 scripts/build-release.py --out ./build
GATEWAY_TEST_BINARY="$PWD/build/gateway-client" go test -race -count=1 -json -timeout=600s ./... > go-test-race.jsonl
go vet ./...
node browser-companion/test.js
node scripts/test-ui-helpers.js
node --check ui/shell/discovery.js
python3 scripts/build-release.py --out ./build
python3 scripts/audit-release.py --build ./build --out ./binary-audit.json
python3 scripts/test-ui-054.py --binary ./build/gateway-client --out ./ui-results
python3 scripts/probe-public-network.py --binary ./build/gateway-client --out ./public-probe
```

The build emits Linux amd64, Windows amd64 client, GUI launcher, native host and GUI installer. Windows icon COFF resources are regenerated from the checked-in ICO with Python's standard library. The installer embeds the exact same three Windows executables and companion that portable distribution uses. These builds are unsigned. Cross-compilation and icon inspection are not native installation or execution tests.

`Test053ScaleResources` measures 50,000 and 250,000 synthetic endpoints. Run it without `-race` for representative instrumentation-free timings, and with `-race` for concurrency checking. Both remain controlled Linux measurements, not public-network performance estimates.

`Test053UnknownGatewayThroughNoisyGraph` drives the production scheduler across 6,301 retained endpoints per variant, with repeats and simulated failed dials for synthetic noise. Actual local TCP Bitcoin intermediaries introduce the unknown Gateway. Gateway/BOD negotiation, block lookup and retrieval use production handlers and a non-genesis Bitcoin block fixture. It deliberately never opens public sockets to synthetic addresses. The two variants defer an early target and introduce another target late. The test fixture uses accelerated scheduling, not production wall-clock limits.

`Test053AbruptProcessRecovery` starts a child test process, commits a planned attempt, writes a partial append and exits abruptly without normal shutdown. Recovery must preserve committed evidence and expose the interrupted state. Tests also cover canonical endpoints, budget exhaustion, exact counts, negative gossip, fair retries, legacy import, corruption and I/O failure.

`test-ui-054.py` starts an isolated offline binary and imports explicitly synthetic documentation-range endpoint records. Its screenshots therefore show real runtime/API results for fixture data, not public peers. It exercises candidate details, no-dial local lookup, budget persistence, complete NDJSON export, restart, existing navigation and seven setup stages at desktop/narrow widths. Browser fetch is bridged through Python in this environment; browser-native origin/CSP, actual extension approval and Windows integration are separate gates.

`probe-public-network.py` uses actual normal Bitcoin bootstrap without supplied Gateway addresses. A zero-connection result remains unsuccessful even if the diagnostic script itself exits normally.

## Discovery storage and scheduling ownership

`discovery_ledger_053.go` owns canonical record admission, evidence and counter definitions. `discovery_scheduler_053.go` rebuilds the bounded working set and allocates shared dial opportunities. `discovery_persistence_053.go` journals committed records and cursors. `discovery_api_053.go` exposes bounded pages, local lookup, budget updates and deliberate export. Existing `network_051.go` / `network_052.go` remain the managed transport; this is not a second peer manager.

Older low-level network helpers remain for legacy/nil-manager fixtures. Runtime initialization creates `app.network`; overlay, header and data requests use managed sessions. The unused older `superviseHeaderMirror` path is not started by main; `superviseIndependentHeaders` is. Do not enable legacy helper paths to bypass ledger eligibility.

## v0.5.4 repair gates

`discovery_054_test.go` tests stable/fresh public-IP election, immutable/rate-limited announcements, protected negative checks after EOF and restart, evidence-class fairness, exact masks and candidate pagination, indirect discovery plus retention of an empty Gateway, and multi-range BOD sample selection. The source still uses the v0.5.3 exact ledger and the same Bitcoin transport.

`internal/cleanup` is shared by the runtime and the custom Windows installer. Tests cover explicit preview/removal, wallet/provider/export preservation, links, root/overlap rejection, exclusive locks, changed files, ambiguous legacy data, and separately configured archives. `Test054RebuiltRuntimeCleanProfile` requires GATEWAY_TEST_BINARY; it starts the rebuilt host runtime, requests authenticated shutdown, runs the same removal engine, verifies the external sentinel and restarts with only fresh offline test isolation settings. It is not a Windows uninstaller execution test.

`test-ui-054.py` imports 120 synthetic endpoints, including 112 hints. It pages all 112, checks weak/attempted filters and imported uncertainty, verifies server-exported 64-bit masks and the existing setup/privacy/header/restart flows. Synthetic data is labelled in the fixture; no public peers are dialled by this test.
