# Gateway 0.7.0 — first public testing release

Gateway retrieves the Bitcoin blocks you request and lets you inspect their transactions, inputs, outputs and positions. Bitcoin Core is optional. This release is under preparation; see [status](STATUS.md) for acceptance rather than treating these notes as a publication announcement.

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

Follow [Getting started](GETTING-STARTED.md), [the QA guide](TESTING-0.7.0.md) and [known issues](KNOWN-ISSUES.md). Windows executables are unsigned; a local scan is not a promise that all security products will accept them. Live hosted update and renewal acceptance remain pending in [status](STATUS.md).

Gateway's original source is now distributed under [MIT](../LICENSE), with [third-party notices](../THIRD-PARTY-NOTICES.md) retained.
