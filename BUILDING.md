# Building Gateway

Gateway uses Go's standard library, with `GO111MODULE=off`. Go 1.23 or newer and Python 3 are required for builds; Node is used for helper tests. Browser tests use Playwright and Chromium. These test dependencies are not shipped with Gateway.

## Development build

From the repository root on Linux:

```sh
export GO111MODULE=off
export CGO_ENABLED=0
python3 scripts/build-release.py --out build/development --without-header-baseline
```

In PowerShell, set `$env:GO111MODULE='off'` and `$env:CGO_ENABLED='0'`, then run the Python command using your Python executable. A development build explicitly omits the full snapshot and synchronizes headers normally. Test with a disposable `-data` directory rather than a working installation's profile.

## Public release build

Build the exact approved source commit from a neutral workspace outside personal home directories and outside `GOPATH`, for example `C:\GatewayReleaseBuild\0.7.0\source` on Windows or `/opt/gateway-release/0.7.0/source` on Linux. Restrict that workspace to the release operator. Gateway's relative imports retain an absolute package identity when `GO111MODULE=off`; `-trimpath` alone does not remove a username from that identity. Moving the source into `GOPATH/src` is not supported by these relative imports.

Use `--headers-baseline PATH` pointing to a completed genesis-first mainnet header file. `--update-channels PATH` supplies a reviewed public channel descriptor containing the publisher URL and public verification key. It must contain no private key or service credential. From the neutral source workspace, for example:

```powershell
python scripts/build-release.py --out build/release-0.7.0 --headers-baseline C:/GatewayReleaseBuild/0.7.0/headers.bin --update-channels assets/bootstrap/update-channels.json
```

Installer staging defaults to `build/release-work` inside that source workspace, independently of the user's temporary directory. `--work-dir` may select another directory inside the neutral source workspace. The public builder rejects personal source paths and `GOPATH` locations, and checks the Go package identity in every built executable. Explicit development builds remain usable from normal checkouts.

Before packaging, run `go version -m` against the five Windows executables and both Linux executables. Their `path` entries must refer only to the neutral workspace, with no personal home prefix such as `_/C_/Users/<name>` or `_/home/<name>`. Also inspect the final bytes for your actual workstation path in UTF-8 and UTF-16; normal Windows API strings are not evidence of a personal-path leak. See [header delivery](docs/HEADER-BASELINE.md) and [updates](docs/UPDATES.md).

## Validation

Example Linux checks against a fresh runtime:

```sh
export GATEWAY_TEST_BINARY="$PWD/build/development/gateway-client"
go test -count=1 -json -timeout=900s . ./internal/... ./packaging/...
go vet . ./internal/... ./packaging/...
CGO_ENABLED=1 go test -race -count=1 -timeout=900s . ./internal/... ./packaging/...
node browser-companion/test.js
node scripts/test-ui-helpers.js
node scripts/test-index-workspace.js
node scripts/test-index-cards.js
node scripts/test-shell-063.js
node scripts/test-home-state-063.js
python3 scripts/test-update-packaging.py
```

Race checks require a supported compiler environment. On Windows, use the built Windows application as `GATEWAY_TEST_BINARY` and run native suites with `-p 1` after packaging. Parallel process-startup fixtures can contend on Windows. Report failed checks and justified reruns rather than hiding them.

The [CI workflow](.github/workflows/development-checks.yml) specifies browser and race coverage. Native installer previews, browser registration, ordinary Bitcoin-network retrieval and live hosted updates need separate acceptance checks. Fixtures do not prove those external paths work. The [QA guide](docs/TESTING-0.7.0.md) covers manual checks; skipped optional fixtures must remain identified as skips.

## Packaging and evidence

Run the build and packaging scripts with `--help` for supported inputs. Fresh packages contain a validated external snapshot; application-update packages omit it. Include the MIT license, third-party notices, Go license and tester guide in redistributed packages.

Record the source revision, tool versions, commands, results, artifact sizes and SHA-256 values. A release tag must resolve to the same commit recorded in `SOURCE-REVISION.txt` and `artifact-manifest.json`; the publisher checks this correspondence. A documentation-only change after packaging does not change the packaged source identity.

Before release, check fresh startup, restart, Headers/Blocks, optional local sources, locked API/CLI/job paths, update signatures and recovery, and the actual hosted delivery path. Do not infer acceptance of a new binary from tests or antivirus results for another binary. Keep test profiles and credentials out of source and artifacts.
