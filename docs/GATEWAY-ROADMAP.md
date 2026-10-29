# Gateway roadmap

The 0.7.9 prerelease candidate builds on the published [0.7.8 Districts release](https://github.com/Blockamoto/gateway/releases/tag/v0.7.8). It adds validated Bitmap Parcels, explicit evidence-route and dependency setup, coordinated indexing and Live following. Missing or unsupported Parcel proof remains pending; existing experimental records are preserved. Full-mainnet Parcel throughput and Live behavior have not been established by bounded tests. This roadmap describes scope, not promised delivery dates. See [status](STATUS.md) and the release acceptance record for publication and validation evidence.

## Historical development: timeline workspace and Bitmap

The earlier timeline and Bitmap development plan is preserved below as historical context. Current 0.7.9 behavior and evidence requirements are described in [indexing](INDEXING.md); Bitmap Chains remain disabled. The earlier experimental consumer plan does not imply that every planned feature is enabled.

- Arm available tracks, choose Live where supported, and use shared Start/Pause/Resume controls. Configuration alone must not start work.
- Coordinate one fetch/decode of a required block for ready armed consumers in the same pass, sharing parser/output facts while preserving per-index checkpoints and dependency order.
- Put everyday controls in the tracks and keep deeper settings/evidence in the Inspector. Review all write paths, including Live, browsing, legacy cache enrichment and automatic address projection.
- Correct Sat ranges ordering under TXIO, use one stable timeline disclosure, and show all available built-ins by default. Preserve Plus as disabled for future custom indexes.
- Add one global precise In/Out pair and Previous/Next/Skip navigation at the current block/transaction/output/local-Sat level without starting indexing. Per-track ranges remain deferred.
- Keep resource details, requirements and advanced settings inside the persistent timeline workspace; preserve Settings and Peers as separate destinations.
- Implement Districts, Parcels and Bitmap Chain as separate development consumers, sharing ingestion while preserving explicit provenance/outcome dependencies, coverage gaps and experimental profile labels.

## Available: v0.7.7 Sats

- Derive Sat identities from genesis, retaining live ordered output ranges, Sat-number lookup, terminal states and shared output facts at exact selected-chain checkpoints.
- Add a Sat explorer, compatible address UTXO/range views, P2PK destination navigation and known inscription holdings with explicit identity coverage.
- Defer Satline on-demand history tracing for this version; block new and resumed traces while preserving saved evidence. Saved Sat lookup/ranges and fee receipts remain. Complete historical output ranges are optional and off by default.
- Retain the newest two savepoints at multiples of 12 and the newest two at multiples of 144, sharing matching heights. Restore an eligible selected-chain point and replay; preserve incompatible prior state for reviewed rebuild.
- The published app uses reviewed per-index Build/Pause/Resume/Live controls. Shared transport, arming and hierarchy cleanup are now in the before-Bitmap preparation above; general deletion/replay management remains separate later work.
- Authorized Sats testing correction: ordinary vertical wheel input scrolls across the left track-name column and actual track lanes; Ctrl/Command is required for wheel zoom in the timeline time/data area. Preserve Shift horizontal pan and trackpad/touch pinch zoom. This shipped gesture correction is separate from the shared-control preparation above.
- Route bare integers inside Gateway using the selected chain tip, with explicit `.sat`/`.bitcoin` overrides and `.sat` browser-companion handoff. Browser bare-number search stays unchanged.
- Keep full consensus validation, assumed snapshots and Gateway peer/state sharing outside this scope. Sats is published and selected by the verified updater; publication and validation evidence are in status.

See [the Sats feature breakdown](SATS-FEATURES-0.7.7.md), [brief](IMPLEMENTATION-BRIEF-0.7.7.md), [guide](SATS-INDEX.md) and [test guide](TESTING-0.7.7.md).

## Available: v0.7.6 Addresses

- Build Current UTXOs from saved complete UTXO state, with checkpoint-specific balances and a deeper paginated output view.
- Offer independently retained Historical outputs over explicit sparse creation ranges, with coverage and range totals rather than inferred balances.
- Share existing output values/scripts and retain address-to-output blocator membership; do not copy complete decoded blocks.
- Add typed-address entry, clickable output addresses and independent Current/Historical timeline bands.
- Include exact compressed/uncompressed P2PK public-key destinations, with explicit upgrade/backfill requirements for older conventional-only coverage.
- The Addresses release defers inscription holdings and Sat tracking to Sats; full consensus validation remains future work. Its publication and verified update delivery are recorded in status.

See [the Addresses brief](IMPLEMENTATION-BRIEF-0.7.6.md) and [guide](ADDRESSES-INDEX.md).

## Available: v0.7.5 UTXO set

- Build a complete live-output state from genesis to an explicit processed selected-chain tip, with exact values/scripts, blocator creation identity and a durable native-prevout bridge.
- Pause/resume at an atomic head; retain a fixed 288 complete recovery transitions, independently of raw-body retention. Deeper reorganizations require an explicit reviewed rebuild and preserve prior state archives.
- Add optional TXIO Values and ScriptPubKeys output history with independent coverage and shared fact retention. Transaction Locator and TXIO remain separate stores/tracks.
- Group Blocks into Headers/raw bodies and TXIO into Positional links/Values/ScriptPubKeys; give UTXO its own state/build panel with honest progress, anchors and measured storage.
- Report header-anchored processing with `consensus_validated=false`. Independent full consensus validation is deferred to v0.8; this release adds no snapshot, new peer/state-sharing, Sats, fees or address features.

Exact packaging, public release and hosted delivery acceptance are recorded in [status](STATUS.md). See [the implementation brief](IMPLEMENTATION-BRIEF-0.7.5.md), [UTXO guide](UTXO-INDEX.md) and [test guide](TESTING-0.7.5.md).

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

## Later workspace and index management

The shared transport, arming, track-local controls and hierarchy work formerly scheduled here have moved to the before-Bitmap preparation. Broader management remains a separate scope:

- Clear per-index deletion, replay/rescan and refresh/upgrade controls. Distinguish saved-job Resume from a newly selected range and expose destination-type coverage upgrades. Existing reviewed recovery rebuilds and P2PK upgrades remain available; removing a track does not delete its index, and status refresh does not replay data.
- Custom index registration and the future Plus menu, with its own capability and storage contract.

Further interaction and index-specific features will be scoped individually as testing informs later briefs.

The inscription and transaction stages are available; Gateway peers remain a locked stage with separate scope and validation before promotion:

1. **Inscriptions:** a content viewport within the timeline workspace, compact positional indexing, Lean/Full storage choices and visible unresolved content dependencies. Positional lookup works from a known block without a global transaction index.
2. **Transactions:** the Transaction Index, plus the optional inscription-related transaction locator choice and its retroactive action in the Inscription inspector, connect conventional transaction/inscription IDs to block positions. Both inscription enrichment controls were gated during the earlier inscription-only stage.
3. **Gateway peers:** enable verified locator requests and explicitly public locator serving. The shared protocol foundation is implemented alongside the earlier stages but remains gated. Peers supply location hints; fetched evidence is checked locally.

These stages describe order, not promised dates. They do not establish wallet ownership, complete parent-child enumeration, universal Sat history or unrelated index features.

- Refine the timeline workspace from tester feedback; dependency visualization and deeper index-specific views remain future work.
- Roll out additional features, with the individual capabilities selected as that brief takes shape.
- Act on first public testing feedback, especially onboarding, address entry, data availability and recovery.
- Continue testing hosted delivery and renewal, including expiration, unavailable-service behavior and idle-service startup.
- Improve documentation and diagnostics where testers cannot distinguish downloaded headers, stored blocks and committed index coverage.
- Promote additional indexes individually only after their dependency, replay, restart, reorganization, missing-data, performance and UI checks are adequate.

## Future, currently locked

Beyond the available inscription/transaction/TXIO/UTXO/Addresses/Sats stages and Bitmap Districts and Parcels, **Bitmap: Terrain Claim Chains** remains locked. The separate Bitmap Chains and future audit capabilities require their own scope and validation; District and Parcel availability does not enable them. Fee receipts prepare future Satline traversal, which remains disabled; a separate fee-market explorer is not added. UTXO values/scripts alone do not identify individual sats; the Sats index supplies ordinal ranges and provenance with separate recovery/history coverage.

Gateway-to-Gateway peerhood and index sharing also remain locked. Their eventual scope and trust model require separate validation. Related schema and reference-project alignment can inform future work without implying conformance or a release commitment today.
