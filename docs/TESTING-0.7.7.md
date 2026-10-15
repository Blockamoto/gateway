# Sats checks and release acceptance

This source describes **v0.7.7: Sats**, following Addresses. The prerelease is published and approved for the signed updater. Use isolated profiles for recovery, corruption and raw-eviction checks; never reuse a running user profile. [The feature breakdown](SATS-FEATURES-0.7.7.md) describes implemented scope; [release notes](RELEASE-NOTES-v0.7.7.md) summarize it. The acceptance record preserves source-specific tests separately from actual public-package execution, real Addresses-to-Sats automatic upgrading, and initial/renewed live-feed verification.

Shared timeline Play/Pause, per-track arming, the redesigned track-local review/build workflow and general per-index deletion/replay/refresh controls are deferred until after Bitmap. Exercise the existing reviewed per-index controls. Do not count those deferred controls as missing Sats-preview acceptance, or mistake removing a track/status refresh for deleting/replaying an index.

## Updated preview candidate

The user has now authorized timeline wheel correction, selected-tip number entry, `.sat` browser-companion handoff and ordinal-name entry. Satline history tracing is explicitly disabled for this version, superseding earlier acceptance below describing automatic tracing. Keep old receipts tied to their original source and binaries.

New checks must verify plain vertical scrolling across labels/lanes, Ctrl/Command anchored wheel zoom, Shift/horizontal pan and trackpad/touch pinch; exact-tip/tip-plus-one classification, changing tips, unknown authority and stale responses; known ordinal-name vectors, range/overflow rejection and existing hash/destination precedence; native numeric/named `.sat` validate/open/display acknowledgement without OS registrations; exact retained output-offset to Sat lookup, missing history, corrupt segments, stale replies and status recovery without raw-block fallback; blocked trace starts/resumes/identity fallback with preserved saved Sat lookup/ranges and no trace controls in either interface.

The corrected package is frozen at source `0f60051a2560c43b5e78f1b3dc80e14fda64f6a4`, client SHA-256 `4bcd779b3abae25babdd7a0416cca0d5600444ca362d06b6c5a6609e4fe13228`. Exact native Sats, cards, viewport and header import/restart checks pass, as do local package/component/installer-payload audits and `go vet ./...`. The actual composed workspace callback regression passes from that clean frozen source. A copied released v0.7.6 profile through block 170 preserves current/historical P2PK results, commitments and settings over two offline starts without raw bodies or new jobs. Source-specific receipt paths and hashes are recorded under `current_candidate.validation` in [the acceptance record](RELEASE-ACCEPTANCE-0.7.7.json).

[Final hosted development workflow 38634815067](https://github.com/Blockamoto/gateway/actions/runs/38634815067) **passes against exact source `0f60051`**. Authenticated archived revision and source blobs match. Full Go records **1,141 passing, 50 skipped, zero failed test/subtest events**; regression race **622 passing, 17 skipped, zero failed**; updater race **159 passing, one skipped, zero failed**. Vet, companion/helpers, packaging and all browser/native stages pass. The inspection receipt and hashes are recorded in `current_candidate.validation.final_hosted_development_workflow` and remain distinct from public-package execution. **Final complete Windows Go tests pass on the same frozen source and client:** 1,163 passing, zero failed and 45 skipped test/subtest events in 1,118.906 seconds. Eight packages pass; two helper packages have no tests. The inspected source is clean before/after and the executable SHA-256 remains unchanged. Windows vet passes in 8.453 seconds. Inspection, raw test receipt and log hashes are retained in `current_candidate.validation.final_windows_full_go_suite`; no older run is relabeled. Published package execution and updater delivery remain separate, held release checks.

The prior `4f6fa935` client `2bc1b992…` is superseded. Its original helper/layout receipts are retained, but a later compiled negative control showed that `indexes.html` bypassed the Sat helper and opened a positional transaction. The corrected regression now follows the real timeline callback through `indexes.html`, saved output-offset lookup and numbered Sat navigation. It checks missing ranges without raw fallback or history scans. The final compiled fixture also exercises that actual callback; a helper-only pass is insufficient.

The earlier `bc981dd` complete Windows run remains failed with **1,160 passing, two obsolete Satline-provider expectation failures and 46 skipped test/subtest events**. Test-only `0b0d55e` corrects those two expectations, asserting the release gate survives saved settings/publications; its focused tests pass. Its hosted run was canceled after Go/vet and before all remaining checks, so it is not final CI acceptance. Preserve these receipts alongside the separately recorded final passing run.

## Reproducible automated checks

Run from the repository root with Go, Node.js and the existing Playwright/browser dependencies available. For Go, set `GO111MODULE=off` and `CGO_ENABLED=0`. In PowerShell:

```powershell
$env:GO111MODULE = 'off'
$env:CGO_ENABLED = '0'
go test . -run 'Test077(Sat(HTTP|Address|Point|Name|Issuance)|NumberNavigation|SatlineRelease)' -count=1
go test . -run '^Test077' -count=1 -timeout 40m
go test ./... -count=1 -timeout 40m
go vet ./...
```

The focused API tests cover genuine genesis/block 1, local request guards, trace release gates, known inscription identities, compatible address checkpoints and rooted cursors. The wider `Test077` selection adds FIFO/fees, terminal states, fragmentation, shared-fact retention, savepoint rotation, interruption and reorg/rebuild fixtures. Recovery fixtures process hundreds of synthetic heights and need a longer budget than the small API tests. Race checks require a supported host and toolchain with `CGO_ENABLED=1`; use the development workflow's race selection.

```text
node scripts/test-sats-077.js
node scripts/test-sats-077-browser.js
node scripts/test-sat-point-browser-077.js
node scripts/test-shell-navigation.js
node scripts/test-txio-workspace-browser.js
node scripts/test-timeline-explorer-browser.js
node scripts/test-transaction-workspace-browser.js
node scripts/test-addresses-browser.js
node scripts/test-p2pk-destinations-browser.js
node scripts/test-index-workspace.js
node scripts/test-index-timeline.js
```

Browser fixtures exercise real DOM controls with controlled responses; they are separate from compiled-backend acceptance. To test a newly built native candidate, supply its exact path and a new receipt directory:

```text
node scripts/test-sats-native-077.js --binary path/to/GatewayClient.exe --out work/qa-077/browser-sats-new-candidate
```

This script starts its own disposable profile, uses genuine serialized genesis and block 1, and records the binary SHA-256 in `result.json`. It checks reviewed genesis/pruned/history-off build, missing-source Waiting, Pause and saved-job Resume, exact Sat/output snapshots, addressless genesis state, blocked Satline entry points and absent trace controls, selected-tip number navigation, numeric/named `.sat` activation/display, retained output-offset identity, plain-wheel scrolling and intentional zoom, own-input BPS, narrow themes, and queries after raw-source eviction and process restart. It must not connect to, stop or modify an existing user instance. Keep earlier receipts when retesting a rebuilt candidate.

## Explorer and controls

1. Open `0.sat` and `5000000000.sat`, including browser back navigation. Check issuance/properties, saved height/hash, output BPS and offset. Genesis is unspendable and addressless; a non-address script must not receive an invented address. Unissued, missing, stale and terminal states must remain distinct.
2. Open a Sat before building global Sat state. Confirm **Trace Satline** is absent. Shell and legacy Settings have no enable switch; both explain that tracing is deferred. Direct Satline and reverse API/CLI requests cannot start or resume work, even with an old saved enable flag. Add Index has one Sat index choice and no separate Satline entry. Opening a view starts no indexing or tracing. Individual Sat pages use the main address bar without a duplicate form; the Sats landing accepts a number or ordinal name.
3. Open transaction inputs. Show their own input BPS immediately and the verified original-output BPS when supplied; unresolved creation positions retain the native outpoint fallback. Deferred **Follow a sat** actions are absent. Selecting a timeline Sat offset reads retained authenticated ranges and opens the numbered Sat. Missing ranges show unavailable identity and start no raw-block fetch or historical scan; stale replies and health recovery cannot navigate over a newer selection.
4. Expand current address UTXOs into ordered Sat ranges. Range ends are exclusive, output offsets are contiguous, and all pages agree with the output value. FIFO range order need not be ascending by Sat number. Require exact address/Sat height and block hash; their different index commitments must not be compared as equal.
5. Page known indexed inscription holdings. Check both snapshot identities, explicit identity coverage and partial completeness. No known rows is not proof that the address has no inscriptions. Changed checkpoints/cursors, unavailable responses and navigation away must clear or discard stale pages.
   P2PK outputs expose a separately labelled public-key destination in Sat locations, output range views and holdings queries. Its current balance and historical outputs use the same exact script identity as the Addresses explorer; same-key P2PKH outputs and other key encodings remain separate.
6. Review the genesis-first Sat build before Start. History starts off and raw retention is pruned. One-off target changes invalidate review; Live resolves the selected tip when queued work starts. Missing evidence waits without replacing readable committed state. Pause/Resume uses the saved job, including an explicitly reviewed rebuild.
7. Check the TXIO **Sat ranges** child has independent retained coverage. A few live outputs do not establish whole-block or full-history coverage. Enabling historical ranges later leaves earlier gaps; disabling the option preserves already retained history.
8. Check actual savepoint heights: newest two at multiples of 12 and newest two at multiples of 144, with shared heights deduplicated. Display the saved recovery boundary. Reorg recovery needs an eligible selected-chain savepoint and replay evidence; deeper failure requires reviewed rebuild with previous evidence preserved. There is no unconditional 288-block Sat recovery guarantee.
9. Verify reverse history discovery is blocked before historical provider work, while previously known inscription identities and identities established during Sat indexing remain queryable. A saved publication or pending job cannot override the release gate or resume a trace. Private cancellation of an already-running exact trace remains possible without deleting its completed evidence or stopping another index job. Low-level FIFO/fee arithmetic remains unit-tested without exposing tracing.

Check output/lost/destroyed counts where supplied. The current Sat status does not populate a storage-byte measurement or range-fragment count, so those fields may show **Unavailable**. Do not turn an absent measurement into zero or a whole-mainnet size/performance claim.

Repeat relevant explorer/panel checks in light and dark themes at narrow widths. Missing or corrupt facts, range pages, fee pages or selected-chain evidence must remain unavailable/unresolved rather than become a known empty result.

## Earlier source-specific evidence and limits

The results below certify their recorded historical sources and binaries. Statements about previously available Satline behavior do not describe the corrected preview above.

The P2PK destination refinement at runtime source `2abda5d617e6d8bcc708da96a837e83b3bebbca6` passed focused public-key and Sat adapter tests, static analysis and eight targeted model/browser scripts. It preserves exact-script separation, checkpoint-bound holdings and explicit older-index upgrade review. The new Windows preview has SHA-256 `3e9ae3113e5588339ae94a32bf3044a80e488f74ba97d26f891afed44a437f76`; `work/qa-077/p2pk-native-sats/result.json` and `work/qa-077/p2pk-native-viewport/summary.json` pass against those exact bytes, including genuine genesis/block-one processing, raw eviction/restart and public-key destination navigation without implicit indexing. The real block-170 transaction is also covered by a hermetic parser regression.

[The full P2PK integration workflow](https://github.com/Blockamoto/gateway/actions/runs/38596598253) passed against that source, including full Go tests, vet, regression/updater race checks, companion/helpers, packaging and all browser/native checks. This refinement used focused Windows regressions plus native acceptance; the older complete Windows run below is not a new full Windows rerun.

When reconciling later Addresses changes, retain Sats' actual shared-fact ownership traversal. The conservative older-Addresses cleanup guard is specific to the build that cannot enumerate Sat owners; applying it indiscriminately to the Sat-aware collector would disable ordinary reclamation.

The frozen development candidate built from source `5040ac9b1f2a9cb69698505fec8ee1485837cea9` passed native acceptance in `work/qa-077/browser-sats-5040ac9/result.json`, with binary SHA-256 `aec63f9520d1499df13b25486d70d89225009b3893e90e30041a917547f518ad`. This includes always-on Satline, BPS controls and raw-eviction/restart checks. The separate `work/qa-077/browser-legacy-5040ac9/result.json` opened its compiled legacy settings page, checked the absent enable control and automatic tracing text, and confirmed no mutation requests or implicit Sat build. Both receipts have no browser errors or external requests; new narrow light/dark Sat captures were inspected.

The full Windows run passed in **781.282 seconds**, with **1,127 passing and 42 skipped test/subtest events, zero failures**, against engine source `8fc462000e254e7c351b33deaf85fc0661babc3f` and its frozen executable. All eight tested packages passed; two helper packages have no tests. `5040ac9` changes only the remaining legacy settings presentation/save payload, with separate source-browser and compiled-page acceptance above.

All **25 workflow browser scripts passed locally**, including native Sats, inscriptions, index cards and viewport stages, against the frozen final app. The source at `2356aaacde37859c838b3ce9f4f183f9fd784638` updates only three browser fixtures to use the released Sat ranges panel and future Bitmap gate. The earlier hosted run passed full Go tests, vet, race checks, companion/helpers, packaging and new Sats browsers, then stopped at the outdated workspace expectation.

[The complete corrected Linux CI run](https://github.com/Blockamoto/gateway/actions/runs/38486470079) passed against `2356aaacde37859c838b3ce9f4f183f9fd784638`: **1,105 passing and 47 skipped full-Go test events**, **583 passing and 17 skipped regression-race events**, **159 passing and one skipped updater-race events**, vet, companion/helpers, packaging and all browser/native checks. Its archived source digest was independently checked. Earlier receipts are preserved and certify only their recorded source/binary and fixture scope.

These are bounded authentic-block and synthetic-fixture checks, not a whole-mainnet build, storage or performance benchmark. Persisted range pages and API pagination bound storage units and responses, but fragmented input ranges are still materialized during FIFO transaction processing. On-demand visited-path/fee caching does not establish complete historical output ranges. Known inscription joins do not establish universal inscription completeness. Full Bitcoin consensus validation, public update delivery and Gateway peer-state sharing are outside this acceptance scope.
