# Gateway roadmap

Gateway 0.7.1 builds on the first public release with a timeline workspace for Headers and Bitcoin Blocks. This roadmap describes scope, not promised delivery dates or support for every related protocol draft. See [status](STATUS.md) for publication and acceptance status.

## Scope in 0.7.1

- Foundational Headers index, selected-chain tracking, validated bootstrap snapshot and ordinary Bitcoin catch-up.
- Bitcoin Blocks on demand, bounded index jobs, persistent coverage and configurable source retention.
- Explorer and schema for known blocks, transactions, inputs, outputs and output-relative positions.
- A docked timeline, resizable Inspector/Block Explorer pane, anchored zoom, playhead navigation and explicit range selection for preparing indexing plans.
- Optional read-only Bitcoin Core and mounted block sources.
- Ordinary Bitcoin peer controls and serving, private-cache controls and optional peer address reveal.
- Windows installer and portable packages, Linux packages, optional Windows browser registration and `.bitcoin` address entry.
- Signed application and incremental header delivery, with manual, automatic-check and explicitly chosen automatic-install modes.

See [status](STATUS.md) for release acceptance and [the timeline tester guide](TESTING-0.7.1.md) for behavior to verify. The [setup and browser checklist](TESTING-0.7.0.md) covers the other enabled features.

## Next: refine the workspace and expand features

The timeline is the first step in the design overhaul. Further interaction and index-specific features will be scoped individually as testing informs the next brief.

The inscription work is a staged rollout, with each stage tested before promotion:

1. **Inscriptions:** a content viewport within the timeline workspace, compact positional indexing, Lean/Full storage choices and visible unresolved content dependencies. Positional lookup works from a known block without a global transaction index.
2. **Transactions:** enable the Transaction Index, plus the optional inscription-related transaction locator choice and its retroactive action in the Inscription inspector. These connect conventional transaction/inscription IDs to block positions. Both inscription enrichment controls remain gated in stage one.
3. **Gateway peers:** enable verified locator requests and explicitly public locator serving. The shared protocol foundation is implemented alongside the earlier stages but remains gated. Peers supply location hints; fetched evidence is checked locally.

These stages describe order, not promised dates. They do not enable current ownership, complete parent-child enumeration, global sat history or unrelated index features.

- Refine the timeline workspace from tester feedback; dependency visualization and deeper index-specific views remain future work.
- Roll out additional features, with the individual capabilities selected as that brief takes shape.
- Act on first public testing feedback, especially onboarding, address entry, data availability and recovery.
- Continue testing hosted delivery and renewal, including expiration, unavailable-service behavior and idle-service startup.
- Improve documentation and diagnostics where testers cannot distinguish downloaded headers, stored blocks and committed index coverage.
- Promote additional indexes individually only after their dependency, replay, restart, reorganization, missing-data, performance and UI checks are adequate.

## Future, currently locked

Beyond the staged inscription/transaction work, spender/address, UTXO, Sat/Satline, Bitmap and parcel capabilities remain unavailable. **Bitmap: Terrain Claim Chains** is the name of the locked Terrain Claim Chains feature; it introduces no active behavior in this release.

Gateway-to-Gateway peerhood and index sharing also remain locked. Their eventual scope and trust model require separate validation. Related schema and reference-project alignment can inform future work without implying conformance or a release commitment today.
