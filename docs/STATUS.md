# Gateway status

## Current source preparation: v0.7.5 UTXO set

The development source adds genesis-first UTXO state, optional TXIO Values/ScriptPubKeys history, shared output-fact retention and grouped Blocks/TXIO coverage. Transaction Locator remains a separate track/store. UTXO has a dedicated state/build panel, an atomic head, mandatory live native-prevout translation and fixed 288 self-contained reversible transitions. Raw-body retention is independent. Deeper recovery requires an explicit reviewed rebuild which preserves prior engine data and its dependencies.

UTXO processing is header anchored and reports `consensus_validated=false`; full Bitcoin consensus validation remains deferred to v0.8. This stage adds no Sats, fee/address indexing, snapshot download or Gateway peer/state-sharing protocol. See [the brief](IMPLEMENTATION-BRIEF-0.7.5.md), [UTXO guide](UTXO-INDEX.md), [test guide](TESTING-0.7.5.md) and [release notes](RELEASE-NOTES-v0.7.5.md).

Focused development-worktree engine/rebuild tests passed on **13 October 2026**, covering ordered/same-block state changes, raw-free recovery, COW page splitting, interrupted head publication, missing/double-spent prerequisites, exact 288-transition recovery/deeper failure, ancient Coin fact pins, metadata/page cleanup, historical BIP30 restoration, corrupt head/root/facts/undo, and idempotent reviewed rebuild/archive preservation. These are bounded fixtures, not a full-chain UTXO build, disk/performance benchmark or exact package acceptance. Broader integration/static/race/browser results and final Windows/Linux packages must be recorded separately.

The public release, exact packaged executables, genuine previous-client automatic upgrade, hosted signed feeds and renewal checks for **0.7.5 remain pending**. The published/approved delivery baseline below is still 0.7.4 until the release receipt records promotion. This source preparation does not itself change the hosted updater.

## Published prerelease: 0.7.4 TXIO and serialized block bodies

The source enables TXIO bidirectional input/output relationships while retaining the `txo-spender` storage/API identity. Blocks use compact ordered transaction metadata and verified durable serialized bodies, with compatible versioned migration and shared temporary decoding. Legacy batches and decoded/hex duplicates are retained. The inscription potential range uses the catalog's suggested start without restricting explicit earlier scans; map controls occupy their own accessible toolbar. Gateway peerhood remains locked.

The [public 0.7.4 prerelease](https://github.com/Blockamoto/gateway/releases/tag/v0.7.4) was published on **12 October 2026 at 21:08:04 UTC**. Its tag and application source remain `87770d70c8162139f19fdf37e55a474e6370be40`. Packaging-policy correction `65c00fc` removed supplemental documents from the two automatic-update ZIPs and pinned their links to the source; all compiled bytes, fresh ZIPs, installer and header ZIP stayed unchanged. The implementation brief and test guide are recorded in [the 0.7.4 brief](IMPLEMENTATION-BRIEF-0.7.4.md) and [tester guide](TESTING-0.7.4.md).

Initial hosted delivery passed after approval commit `c8fd6c750bc777a6d20a56eeb3dba0d9ba2d4d47` became live on Render at **21:33:34 UTC**. Independent verification downloaded both application ZIPs and all **97 header chunks**, checking signatures, validity, sequences, sizes and hashes across **99 objects / 87,741,245 bytes**. These feeds used application sequence **7** and header sequence **6**, with the existing public verification key and app source unchanged.

A genuine published **0.7.3** Windows client automatically downloaded, installed and restarted into 0.7.4; all four executable hashes matched the published package. Settings, theme, live-index policy, user data marker, all 969,480 saved headers and bootstrap sidecars were preserved. A separate fresh 0.7.4 client automatically checked both feeds without Settings setup and reported up to date. Its snapshot prefix remained identical while ordinary Bitcoin peers appended 1,662 headers. Disposable profiles were removed. These checks used a warmed service and did not perform a system installation or measure idle cold-start latency.

The [renewal workflow](https://github.com/Blockamoto/gateway/actions/runs/38311565343) passed and the workflow-maintained [feed status](UPDATE-FEED-STATUS.json) is verified. The renewed feeds are application sequence **8** and header sequence **7**, both expiring **11 November 2026 at 21:36:03 UTC**. Independent signature and byte comparison confirmed that only sequences, issue times and expiry times changed; all 99 application/header payloads stayed identical. A fresh download of all **99 objects / 87,741,245 bytes** passed at **21:43:31 UTC**. The exact 0.7.4 client automatically accepted both refreshed feeds through normal startup checks, reported up to date and started no download, staging or installation. Settings, live-index policy and the full snapshot prefix stayed intact.

[Linux CI at app source 877](https://github.com/Blockamoto/gateway/actions/runs/38305738277) and [approval commit c8](https://github.com/Blockamoto/gateway/actions/runs/38311238112) passed full Go, static, race, helper, packaging and browser/runtime checks. The original full Windows result remains **991 passing and 44 skipped test events with three obsolete locked-TXIO expectations**; reviewed corrections to those two test files passed a separate **16-test** run. No corrected full Windows rerun is claimed. Exact native migration, saved TXIO links, restart, renderer and local Defender checks passed within their recorded scope; native WebView sharpness and clearance on other machines are not established. The [acceptance record](RELEASE-ACCEPTANCE-0.7.4.json) records the evidence and limits.

Render automatic deployment on `main` is enabled for `gateway-updates`; application approval and signing remain separate from publisher deployment. Weekly renewal refreshes only the exact approved release, not whichever GitHub release is newest. Existing update modes, custom-source choices and explicit opt-outs are preserved.

## Previous source: 0.7.3 The transactions update

The source enables the Transaction Index and inscription-related locator builds. Independent opt-in browsing indexes store exact processed blocks without advancing sequential checkpoints. The workspace has context-aware Details and Inspector, a square shared viewport, complete BitFeed-style block maps, automatic transaction flow and a collapsible timeline. Gateway peers remain gated. The published `v0.7.3` prerelease uses source `e3b2b53d0553dc7103eedfaa0a88c465cc1d1e5b`. A later local/main correction at `53739195` preserves newer same-block hierarchy navigation when older responses arrive; actual navigation and block-anchor changes still cancel obsolete work. The deterministic regression and affected workspace checks passed. That correction is included in 0.7.4. The earlier 0.7.3 review draft remains separate from the new release. See [the transaction test guide](TESTING-0.7.3.md).

## Previous source: 0.7.2 inscription prerelease preparation

The 0.7.2 preparation stage introduced positional inscription indexing and an isolated content viewport. Related inscription transaction locators, the standalone Transaction Index and Gateway peerhood were gated for later stages. See [the inscription test guide](TESTING-0.7.2.md) and [draft release notes](RELEASE-NOTES-v0.7.2.md) for that earlier stage.

## Previous 0.7.2 inscription draft validation

The exact local 0.7.2 Windows build passed **921 Go checks, 46 expected skips and no failures**, including the final cancellation and coordinate regressions. Native viewport tests separately checked controlled local Ord fixtures and real Bitcoin block 840000: 3,050 transactions, 878 Lean inscription coordinates, and verified positional content without a transaction locator index. The retained checkpoint and header snapshot survived the final-build restart.

A genuine 0.7.1 client automatically upgraded and restarted into the exact final 0.7.2 components through a local signed publisher fixture, preserving settings and all 969,480 headers. Same-size tampered bytes were rejected. This was a local update test, not a production deployment or system installation. The installer and four Windows components had no threats reported by local Defender scans with protection active and unchanged definitions; they remain unsigned. [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/38054908829) passed build, full tests, static checks, race checks, helpers, packaging and actual browser regressions. See the [0.7.2 acceptance record](RELEASE-ACCEPTANCE-0.7.2.json) for exact scope and limits.

At that review stage, the GitHub release remained a draft and the production updater and approved weekly renewal served 0.7.1. That historical state does not describe the current 0.7.4 delivery above.

## Previous published baseline: 0.7.1

Gateway **0.7.1 was published with hosted updater and renewal acceptance passed**. That build replaced index cards with a timeline workspace and brought Inspector and Block Explorer tabs into its upper pane. Headers and Bitcoin Blocks were its enabled indexes; additional indexes and Gateway-to-Gateway sharing were locked.

The [release downloads](https://github.com/Blockamoto/gateway/releases/tag/v0.7.1) are public. The [acceptance record](RELEASE-ACCEPTANCE-0.7.1.json) identifies the tested runtime source, exact packages, live delivery checks and their limits.

## Previous 0.7.1 validation

Validation on **9 October 2026** used runtime source `e508085f34c406f41ded7eadbc33d5ee0ab4324b`.

- Windows Go suite: **858 passed, 54 expected skips, no failures**. Windows and Linux development payloads built successfully.
- [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37944091190): all 14 stages passed, including Go tests, vet, race, helper, packaging and browser checks.
- Real-data testing fetched and retained blocks **840000, 840001 and 850000** from ordinary Bitcoin peers with Core RPC and mounted block sources disabled. Headers synchronized beyond the bundled snapshot. The stored blocks appeared as two exact coverage ranges and survived restart.
- Native browser checks used **no API mocks**. They exercised block 840000's 3,050 transactions, pagination, a known transaction, correct previous-output index zero, range selection, independent playhead movement, delayed scrubbing and narrow layout. Browsing and modified selection did not start indexing jobs.
- Final package audit passed for Windows and Linux, including archive member hashes, license notices and the unchanged 969,480-header snapshot external to the client. The exact packaged Windows client passed native offline UI checks in both themes and desktop/narrow layouts.
- All eight release downloads and the initial signed delivery bundle matched their local hashes and sizes. The public `v0.7.1` tag resolves to `da673faf517a8ae85f130da6ae3ea89efc64fd6a`.
- The exact final installer and four Windows components had no threats reported by local Defender diagnostic scans with active protection and unchanged definitions **1.459.636.0**. They remain unsigned; these scans do not establish cloud, browser-download or other-machine clearance.

## Previous 0.7.1 publication and hosted delivery

The release tag, application packages and header snapshot remain fixed at `da673faf517a8ae85f130da6ae3ea89efc64fd6a`. Later documentation and feed-operation changes do not alter those released application bytes.

The public update service at `https://gateway-updates.onrender.com` passed independent HTTPS verification for both application archives and all 97 header chunks at application sequence **4** and header sequence **3**.

A genuine released 0.7.0 Windows client automatically downloaded 0.7.1, installed and restarted with all four executable hashes matching the published package. Settings, appearance, indexing preferences and the full saved headers were preserved. A separate fresh 0.7.1 client automatically checked the public feeds; its validated 969,480-header snapshot prefix remained intact while ordinary peers appended 1,169 headers. These checks used disposable profiles and a warmed service; idle cold-start latency was not measured.

The weekly approved-release renewal passed its [commissioning run](https://github.com/Blockamoto/gateway/actions/runs/37951160873). At that commissioning run, the feeds were application sequence **5** and header sequence **4**, both expiring on **8 November 2026 at 15:22:35 UTC**. Final independent checks confirmed that both live feeds matched that renewed signed bundle and that both application archives and all 97 header chunks remained byte-identical to the accepted 0.7.1 release. Only signed sequences and timestamps changed. The current workflow-maintained [feed status](UPDATE-FEED-STATUS.json) now records 0.7.4.

The exact final 0.7.1 Windows client also automatically accepted both renewed feeds and reported up to date, with its validated header snapshot preserved and no unnecessary download or installation offered.

Weekly renewal is configured for Mondays at 06:17 UTC and only refreshes the explicitly approved release; it does not automatically approve a newer version. Its tooling also passed [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37950638326). Future scheduled runs depend on functioning permissions and hosting; failures need operator attention. The [renewal operator guide](../packaging/update-publisher/RENEWAL.md) explains the policy and recovery. The earlier completed public release checks remain in the [0.7.0 acceptance record](RELEASE-ACCEPTANCE-0.7.0.json).

Read [release notes](RELEASE-NOTES-v0.7.1.md), [Getting started](GETTING-STARTED.md), [the timeline tester guide](TESTING-0.7.1.md), [the setup/browser checklist](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
