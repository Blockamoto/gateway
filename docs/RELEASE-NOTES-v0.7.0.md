# Gateway 0.7.0 — first public testing release

Gateway retrieves the Bitcoin blocks you request and lets you inspect their transactions, inputs, outputs and positions. Bitcoin Core is optional. The published release artifacts passed local validation and final runtime-source Linux CI. The public hosted updater and manual commissioning of its weekly renewal workflow passed; see [status](STATUS.md) and the [acceptance record](RELEASE-ACCEPTANCE-0.7.0.json).

## Available

- Headers and Bitcoin Blocks, with a validated external header snapshot for fresh installs and ordinary Bitcoin catch-up.
- Block explorer, schema, known-block transaction navigation, small-range index jobs and source-retention choices.
- Optional read-only Core and mounted block sources; ordinary Bitcoin serving independent of locked Gateway sharing.
- Clear private-cache and serving toggles, peer aliases with deliberate address reveal, light/dark appearance and accessible navigation.
- Windows installation/portable launch and Linux packages. Optional Windows browser routing and companion approval support real `.bitcoin` address entry.
- A separate installer Updates page with manual, automatic-check and explicitly chosen automatic-install modes. Advanced source controls support reviewed bundled trust or an independently verified custom publisher.
- Signed application/header delivery, one-time installer preferences, staged-update recovery and local-data preservation. App update packages omit the full header snapshot.

## Scope

Only Headers and Blocks are enabled. Additional transaction-location, spender/address, Sat/Satline, inscription, Bitmap and **Bitmap: Terrain Claim Chains** features remain locked, including their API/CLI/job paths. Gateway-to-Gateway peerhood and sharing remain locked too.

Gateway verifies blocks and chain context but does not replace Core's full script/UTXO consensus validation. An output position is not proof of ownership; unavailable data is not proof that it does not exist.

## Packages and testing

Choose the Windows installer or a **fresh** Windows/Linux ZIP for a first installation. Application-only ZIPs are update payloads. Fresh packages include the header snapshot, tester guide and license notices. Compare downloads with the release's checksum file.

Follow [Getting started](GETTING-STARTED.md), [the QA guide](TESTING-0.7.0.md) and [known issues](KNOWN-ISSUES.md). Windows executables are unsigned; a local scan is not a promise that all security products will accept them.

## Validation and delivery status

Initial source `38e96fcf38bb76739787a8c731df697e5118f92f` passed the Windows Go suite with 817 passes and 54 expected skips, Go vet, six JavaScript helper suites and [all Linux CI stages](https://github.com/Blockamoto/gateway/actions/runs/37805624668). Focused packaging and native wrapper fixtures passed. The build audit verified 969,480 bundled headers, external snapshot layout and included notices.

A native automatic update from 0.7.0 to a synthetic local 0.7.1 fixture completed download, installation and restart with settings and headers preserved. This demonstrates the local updater path, not live production delivery. Local Defender scans reported no threats in the exact installer and four application executables with unchanged definitions 1.459.601.0; that does not establish cloud or browser-download clearance.

The public hosted updater passed a native automatic install/restart into the exact 0.7.0 release using an isolated synthetic older-version fixture, plus a fresh-install automatic check. Settings and headers were preserved. Independent HTTPS verification passed for both application archives and all 97 header chunks, and repeated successfully against the renewed feeds. Acceptance used a warmed service; idle cold-start latency was not measured.

The dedicated service is live, and the configured weekly approved-release renewal workflow passed its [manual commissioning run](https://github.com/Blockamoto/gateway/actions/runs/37816973175). Both feeds now expire on 7 November 2026 at 17:30:26 UTC, at application sequence 3 and header sequence 2. Released application and header content remain unchanged. Future scheduled execution still depends on working permissions and hosting.

The package and `v0.7.0` tag identify source `e7c81ea0aa059e700881eff3fd7f62c3506645f0`; the later feed-operation fix is separate. The acceptance record identifies final runtime-source CI, rebuilt binary/payload checks and exact scanned files. See [updates](UPDATES.md) and the [renewal operator guide](../packaging/update-publisher/RENEWAL.md).

Gateway's original source is now distributed under [MIT](../LICENSE), with [third-party notices](../THIRD-PARTY-NOTICES.md) retained.
