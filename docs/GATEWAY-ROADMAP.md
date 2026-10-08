# Gateway roadmap

Gateway 0.7.0 starts with Headers and Bitcoin Blocks. This roadmap describes scope, not promised delivery dates or support for every related protocol draft.

## Available in 0.7.0

- Foundational Headers index, selected-chain tracking, validated bootstrap snapshot and ordinary Bitcoin catch-up.
- Bitcoin Blocks on demand, bounded index jobs, persistent coverage and configurable source retention.
- Explorer and schema for known blocks, transactions, inputs, outputs and output-relative positions.
- Optional read-only Bitcoin Core and mounted block sources.
- Ordinary Bitcoin peer controls and serving, private-cache controls and optional peer address reveal.
- Windows installer and portable packages, Linux packages, optional Windows browser registration and `.bitcoin` address entry.
- Signed application and incremental header delivery, with manual, automatic-check and explicitly chosen automatic-install modes.

See [status](STATUS.md) for release acceptance and [the tester guide](TESTING-0.7.0.md) for behavior to verify.

## Next release: design overhaul and feature expansion

The next release will focus on a substantial app design overhaul alongside additional features. The redesign was deliberately deferred until after the initial 0.7.0 public release. Its detailed design and selected feature set will be defined in the next release brief.

- Develop the design overhaul and capture its scope in the next release brief.
- Roll out additional features, with the individual capabilities selected as that brief takes shape.
- Act on first public testing feedback, especially onboarding, address entry, data availability and recovery.
- Continue testing hosted delivery and renewal, including expiration, unavailable-service behavior and idle-service startup.
- Improve documentation and diagnostics where testers cannot distinguish downloaded headers, stored blocks and committed index coverage.
- Promote additional indexes individually only after their dependency, replay, restart, reorganization, missing-data, performance and UI checks are adequate.

## Future, currently locked

Additional transaction-location, spender/address, UTXO, Sat/Satline, inscription, Bitmap and parcel capabilities remain unavailable. **Bitmap: Terrain Claim Chains** is the name of the locked Terrain Claim Chains feature; it introduces no active behavior in this release.

Gateway-to-Gateway peerhood and index sharing also remain locked. Their eventual scope and trust model require separate validation. Related schema and reference-project alignment can inform future work without implying conformance or a release commitment today.
