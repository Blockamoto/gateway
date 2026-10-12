# Gateway roadmap

The [public 0.7.4 prerelease](https://github.com/Blockamoto/gateway/releases/tag/v0.7.4) builds on the timeline, inscription and transaction stages with TXIO relationships and durable serialized block bodies. Hosted downloads, genuine 0.7.3 automatic upgrade and renewal acceptance passed. This roadmap describes scope, not promised delivery dates or support for every related protocol draft. See [status](STATUS.md) for validation and its limits.

## Available in the 0.7.4 prerelease

- TXIO has one canonical spend set and automatic Input/Output lookup, retaining legacy identifiers and unknown-state distinctions.
- Bitcoin Blocks store verified serialized bodies with compact ordered transaction metadata, shared temporary decoding and resumable compatible legacy migration.
- Legacy duplicate bodies remain until explicit cleanup; migration does not automatically reclaim their space, and broad destructive index management is deferred.
- The inscription potential-range boundary is corrected and map controls remain accessible outside the visualization.
- The public prerelease's exact approved artifacts are delivered through the existing signed channel, respecting each installation's update preferences. Renewal, independent downloads of all renewed artifacts and normal refreshed-client acceptance passed without changing the approved application or header payloads.

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

The inscription and transaction stages are available; Gateway peers remain the next locked stage. Each stage needs its own validation before promotion:

1. **Inscriptions:** a content viewport within the timeline workspace, compact positional indexing, Lean/Full storage choices and visible unresolved content dependencies. Positional lookup works from a known block without a global transaction index.
2. **Transactions:** the Transaction Index, plus the optional inscription-related transaction locator choice and its retroactive action in the Inscription inspector, connect conventional transaction/inscription IDs to block positions. Both inscription enrichment controls were gated during the earlier inscription-only stage.
3. **Gateway peers:** enable verified locator requests and explicitly public locator serving. The shared protocol foundation is implemented alongside the earlier stages but remains gated. Peers supply location hints; fetched evidence is checked locally.

These stages describe order, not promised dates. They do not enable current ownership, complete parent-child enumeration, global sat history or unrelated index features.

- Refine the timeline workspace from tester feedback; dependency visualization and deeper index-specific views remain future work.
- Roll out additional features, with the individual capabilities selected as that brief takes shape.
- Act on first public testing feedback, especially onboarding, address entry, data availability and recovery.
- Continue testing hosted delivery and renewal, including expiration, unavailable-service behavior and idle-service startup.
- Improve documentation and diagnostics where testers cannot distinguish downloaded headers, stored blocks and committed index coverage.
- Promote additional indexes individually only after their dependency, replay, restart, reorganization, missing-data, performance and UI checks are adequate.

## Future, currently locked

Beyond the inscription/transaction/TXIO work, address, UTXO, Sat/Satline, Bitmap and parcel capabilities remain unavailable. **Bitmap: Terrain Claim Chains** is the name of the locked Terrain Claim Chains feature; it introduces no active behavior in this release.

Gateway-to-Gateway peerhood and index sharing also remain locked. Their eventual scope and trust model require separate validation. Related schema and reference-project alignment can inform future work without implying conformance or a release commitment today.
