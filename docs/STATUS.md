# Gateway 0.7.1 status

Gateway **0.7.1 is published, and its hosted updater and renewal workflow have passed live acceptance**. The tested build replaces index cards with a timeline workspace and brings Inspector and Block Explorer tabs into its upper pane. Headers and Bitcoin Blocks remain the enabled indexes. Additional indexes and Gateway-to-Gateway sharing remain locked.

The [release downloads](https://github.com/Blockamoto/gateway/releases/tag/v0.7.1) are public. The [acceptance record](RELEASE-ACCEPTANCE-0.7.1.json) identifies the tested runtime source, exact packages, live delivery checks and their limits.

## Completed validation

Validation on **9 October 2026** used runtime source `e508085f34c406f41ded7eadbc33d5ee0ab4324b`.

- Windows Go suite: **858 passed, 54 expected skips, no failures**. Windows and Linux development payloads built successfully.
- [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37944091190): all 14 stages passed, including Go tests, vet, race, helper, packaging and browser checks.
- Real-data testing fetched and retained blocks **840000, 840001 and 850000** from ordinary Bitcoin peers with Core RPC and mounted block sources disabled. Headers synchronized beyond the bundled snapshot. The stored blocks appeared as two exact coverage ranges and survived restart.
- Native browser checks used **no API mocks**. They exercised block 840000's 3,050 transactions, pagination, a known transaction, correct previous-output index zero, range selection, independent playhead movement, delayed scrubbing and narrow layout. Browsing and modified selection did not start indexing jobs.
- Final package audit passed for Windows and Linux, including archive member hashes, license notices and the unchanged 969,480-header snapshot external to the client. The exact packaged Windows client passed native offline UI checks in both themes and desktop/narrow layouts.
- All eight release downloads and the initial signed delivery bundle matched their local hashes and sizes. The public `v0.7.1` tag resolves to `da673faf517a8ae85f130da6ae3ea89efc64fd6a`.
- The exact final installer and four Windows components had no threats reported by local Defender diagnostic scans with active protection and unchanged definitions **1.459.636.0**. They remain unsigned; these scans do not establish cloud, browser-download or other-machine clearance.

## Publication and hosted delivery

The release tag, application packages and header snapshot remain fixed at `da673faf517a8ae85f130da6ae3ea89efc64fd6a`. Later documentation and feed-operation changes do not alter those released application bytes.

The public update service at `https://gateway-updates.onrender.com` passed independent HTTPS verification for both application archives and all 97 header chunks at application sequence **4** and header sequence **3**.

A genuine released 0.7.0 Windows client automatically downloaded 0.7.1, installed and restarted with all four executable hashes matching the published package. Settings, appearance, indexing preferences and the full saved headers were preserved. A separate fresh 0.7.1 client automatically checked the public feeds; its validated 969,480-header snapshot prefix remained intact while ordinary peers appended 1,169 headers. These checks used disposable profiles and a warmed service; idle cold-start latency was not measured.

The weekly approved-release renewal passed its [commissioning run](https://github.com/Blockamoto/gateway/actions/runs/37951160873). The current feeds are application sequence **5** and header sequence **4**, both expiring on **8 November 2026 at 15:22:35 UTC**. The [public renewal receipt](UPDATE-FEED-STATUS.json) records the live verification. Final independent checks confirmed that both live feeds match the renewed signed bundle and that both application archives and all 97 header chunks remain byte-identical to the accepted release. Only signed sequences and timestamps changed.

The exact final 0.7.1 Windows client also automatically accepted both renewed feeds and reported up to date, with its validated header snapshot preserved and no unnecessary download or installation offered.

Weekly renewal is configured for Mondays at 06:17 UTC and only refreshes the explicitly approved release; it does not automatically approve a newer version. Its tooling also passed [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37950638326). Future scheduled runs depend on functioning permissions and hosting; failures need operator attention. The [renewal operator guide](../packaging/update-publisher/RENEWAL.md) explains the policy and recovery. The earlier completed public release checks remain in the [0.7.0 acceptance record](RELEASE-ACCEPTANCE-0.7.0.json).

Read [release notes](RELEASE-NOTES-v0.7.1.md), [Getting started](GETTING-STARTED.md), [the timeline tester guide](TESTING-0.7.1.md), [the setup/browser checklist](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
