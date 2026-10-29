# Unified block acquisition: development acceptance

The portable development candidate from `d1b68449b9bd660930ce2860ce89aac23f4baf64` passes its complete backend suite, static analysis, exact-client acceptance and independent package audit. No changes were pushed to main or the updater. Existing running previews and saved profiles were preserved.

## Result

Explorer navigation, Graph and index builders use the shared block acquisition and verification path. Available local bodies are reused; concurrent requests share acquisition. The timeline owns optional writes: Raw Blocks On retains verified Bitcoin bytes and minimal block metadata; Off makes new bytes temporary. Transaction Locator remains a separate optional index. Legacy retention and browse switches cannot override those choices.

Acquisition follows the pinned Bitcoin Core v31.1 design: hash-correlated requests, a bounded multi-peer download window, capacity refill, service eligibility, download/stall handling and cancellation. A byte-pressure regression verifies that a newly retained body is not downloaded again after a speculative response is retired.

## Verification

- Complete Windows backend: 1335 passing test events, 46 skips, zero failures; static analysis passes. 375 Go sources match the frozen archive. Race checks were not run with CGO disabled.
- Five compiled browser suites and the profile API checks pass against the exact packaged executable. Source UI checks are bound to identical frozen assets.
- Compiled block-policy checks pass with genuine Bitcoin data: Raw On/Off, optional sparse Locator/Inscriptions, exact retained bytes, real process restart with all remote/archive providers disabled, and preserved external source data.
- Exact-client cleanup/restart passes in owned test profiles and preserves the external sentinel.
- Independent audit verifies all 32 portable members, executable hashes and the bundled 969480 headers. Initial preview readiness records peers enabled, sharing/Core sources disabled, and no started work.

## Bounded local observation

The identical eight-block fixture contains 15,145,474 bytes and 21,719 transactions. Raw append time changed from 698.190 seconds in the old path to 1.494 seconds in this observation. File count changed from 37,064 to 22; the new store creates zero transaction reverse shards. Every stored body matched exactly after reopening the store. Process restart is separately covered by the compiled policy check.

Earlier candidate append observations ranged from 0.700 to 8.195 seconds under different machine load. These are local fixture observations, not a stable application speed multiplier, network throughput measurement, full-chain benchmark or measured Bitcoin Core comparison. Logical file lengths are not allocated disk usage. The baseline toolchain hash was checked at audit time; candidate invocation provenance was captured before execution.

## Scope and evidence

The [Core alignment contract](BLOCK-FETCHING-CORE-ALIGNMENT.md) records Gateway adaptations: general/header and block-focused connections rather than Core transaction relay, a serialized memory cap, one selected header authority, and compatibility checkpoint metadata. No full Core consensus-validation equivalence is claimed. Partial segment deletion removes logical membership while reporting physical bytes that await safe reclamation.

Earlier interrupted or failing test runs remain separate from this passing candidate. The authenticated final [evidence record](C:/GatewayReleaseBuild/bitmap-ruleset-development/verification/core-blocks-alignment/acceptance-d1b6844.json) has SHA-256 `311e0e0c1b99dbb5e95cf088a47d72667f61ca9fbfe6b2f24924de5a4b9b49da`. It binds the source archive, client, ZIP, test inventories, native checks, benchmark and initial preview receipt. The cause of the earliest Sats acceptance failure remains unproven; its later passing replacement is separately identified by the workspace receipt.

- Source archive SHA-256: `9fdd6090b9604973ad276c6020a5f66e000070c6f0c8a2363a73337b18bae589`
- Client SHA-256: `709716fb8435b907ef58ac4adc141582951384fb98b57c646f64071123a71a29`
- Portable ZIP SHA-256: `8afe66c1c5c7a145f304452e3d53fe69ac96aee9a71857960e874f61e3f9d637`
- [Open the development preview](http://127.0.0.1:63033)
- [Portable ZIP](C:/GatewayReleaseBuild/bitmap-ruleset-development/Gateway-Bitmap-Rulesets-Development-d1b6844.zip)
