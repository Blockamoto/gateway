# Sats development checks

This source is for **v0.7.7: Sats** on its development branch. Main, release publication and updater promotion remain held. Use isolated profiles for recovery, corruption and raw-eviction checks; never reuse a running user profile.

## Reproducible automated checks

Run from the repository root with Go, Node.js and the existing Playwright/browser dependencies available. For Go, set `GO111MODULE=off` and `CGO_ENABLED=0`. In PowerShell:

```powershell
$env:GO111MODULE = 'off'
$env:CGO_ENABLED = '0'
go test . -run 'Test077Sat(HTTP|Address|Reverse|Issuance)' -count=1
go test . -run '^Test077' -count=1 -timeout 40m
go test ./... -count=1 -timeout 40m
go vet ./...
```

The focused API tests cover genuine genesis/block 1, local request guards, explicit tracing, known inscription identities, compatible address checkpoints and rooted cursors. The wider `Test077` selection adds FIFO/fees, terminal states, fragmentation, shared-fact retention, savepoint rotation, interruption and reorg/rebuild fixtures. Recovery fixtures process hundreds of synthetic heights and need a longer budget than the small API tests. Race checks require a supported host and toolchain with `CGO_ENABLED=1`; use the development workflow's race selection.

```text
node scripts/test-sats-077.js
node scripts/test-sats-077-browser.js
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
node scripts/test-sats-native-077.js --binary work/qa-077/validation-5040ac9/GatewayClient.exe --out work/qa-077/browser-sats-new-candidate
```

This script starts its own disposable profile, uses genuine serialized genesis and block 1, and records the binary SHA-256 in `result.json`. It checks reviewed genesis/pruned/history-off build, missing-source Waiting, Pause and saved-job Resume, exact Sat/output snapshots, addressless genesis state, always-available local tracing controls, own-input BPS, narrow themes, and queries after raw-source eviction and process restart. It must not connect to, stop or modify an existing user instance. Keep earlier receipts when retesting a rebuilt candidate.

## Explorer and controls

1. Open `0.sat` and `5000000000.sat`, including browser back navigation. Check issuance/properties, saved height/hash, output BPS and offset. Genesis is unspendable and addressless; a non-address script must not receive an invented address. Unissued, missing, stale and terminal states must remain distinct.
2. Open a Sat before building global Sat state. Confirm **Trace Satline** is available and runs only after an explicit click. Shell Settings has no enable switch; `/legacy?settings=1` has no `satlineEnabled` control and says local tracing is available automatically. Add Index has one Sat index choice and no separate Satline entry, including old saved preferences. Opening a view must not start indexing or tracing.
3. Open transaction inputs. Show their own input BPS immediately and the verified original-output BPS when supplied; unresolved creation positions retain the native outpoint fallback. **Follow a sat** on an output requires an explicit valid offset and opens the existing Satline journey.
4. Expand current address UTXOs into ordered Sat ranges. Range ends are exclusive, output offsets are contiguous, and all pages agree with the output value. FIFO range order need not be ascending by Sat number. Require exact address/Sat height and block hash; their different index commitments must not be compared as equal.
5. Page known indexed inscription holdings. Check both snapshot identities, explicit identity coverage and partial completeness. No known rows is not proof that the address has no inscriptions. Changed checkpoints/cursors, unavailable responses and navigation away must clear or discard stale pages.
   P2PK outputs expose a separately labelled public-key destination in Sat locations, output range views and holdings queries. Its current balance and historical outputs use the same exact script identity as the Addresses explorer; same-key P2PKH outputs and other key encodings remain separate.
6. Review the genesis-first Sat build before Start. History starts off and raw retention is pruned. One-off target changes invalidate review; Live resolves the selected tip when queued work starts. Missing evidence waits without replacing readable committed state. Pause/Resume uses the saved job, including an explicitly reviewed rebuild.
7. Check the TXIO **Sat ranges** child has independent retained coverage. A few live outputs do not establish whole-block or full-history coverage. Enabling historical ranges later leaves earlier gaps; disabling the option preserves already retained history.
8. Check actual savepoint heights: newest two at multiples of 12 and newest two at multiples of 144, with shared heights deduplicated. Display the saved recovery boundary. Reorg recovery needs an eligible selected-chain savepoint and replay evidence; deeper failure requires reviewed rebuild with previous evidence preserved. There is no unconditional 288-block Sat recovery guarantee.

Repeat relevant explorer/panel checks in light and dark themes at narrow widths. Missing or corrupt facts, range pages, fee pages or selected-chain evidence must remain unavailable/unresolved rather than become a known empty result.

## Evidence and limits

The frozen development candidate built from source `5040ac9b1f2a9cb69698505fec8ee1485837cea9` passed native acceptance in `work/qa-077/browser-sats-5040ac9/result.json`, with binary SHA-256 `aec63f9520d1499df13b25486d70d89225009b3893e90e30041a917547f518ad`. This includes always-on Satline, BPS controls and raw-eviction/restart checks. The separate `work/qa-077/browser-legacy-5040ac9/result.json` opened its compiled legacy settings page, checked the absent enable control and automatic tracing text, and confirmed no mutation requests or implicit Sat build. Both receipts have no browser errors or external requests; new narrow light/dark Sat captures were inspected.

The full Windows run passed in **781.282 seconds**, with **1,127 passing and 42 skipped test/subtest events, zero failures**, against engine source `8fc462000e254e7c351b33deaf85fc0661babc3f` and its frozen executable. All eight tested packages passed; two helper packages have no tests. `5040ac9` changes only the remaining legacy settings presentation/save payload, with separate source-browser and compiled-page acceptance above.

All **25 workflow browser scripts passed locally**, including native Sats, inscriptions, index cards and viewport stages, against the frozen final app. The source at `2356aaacde37859c838b3ce9f4f183f9fd784638` updates only three browser fixtures to use the released Sat ranges panel and future Bitmap gate. The earlier hosted run passed full Go tests, vet, race checks, companion/helpers, packaging and new Sats browsers, then stopped at the outdated workspace expectation.

[The complete corrected Linux CI run](https://github.com/Blockamoto/gateway/actions/runs/38486470079) passed against `2356aaacde37859c838b3ce9f4f183f9fd784638`: **1,105 passing and 47 skipped full-Go test events**, **583 passing and 17 skipped regression-race events**, **159 passing and one skipped updater-race events**, vet, companion/helpers, packaging and all browser/native checks. Its archived source digest was independently checked. Earlier receipts are preserved and certify only their recorded source/binary and fixture scope.

These are bounded authentic-block and synthetic-fixture checks, not a whole-mainnet build, storage or performance benchmark. Persisted range pages and API pagination bound storage units and responses, but fragmented input ranges are still materialized during FIFO transaction processing. On-demand visited-path/fee caching does not establish complete historical output ranges. Known inscription joins do not establish universal inscription completeness. Full Bitcoin consensus validation, public update delivery and Gateway peer-state sharing are outside this acceptance scope.
