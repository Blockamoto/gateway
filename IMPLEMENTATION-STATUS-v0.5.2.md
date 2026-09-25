# v0.5.2 implementation status

**Discovery test candidate.** Detailed evidence and remaining gates: `VERIFICATION-v0.5.2.md`.

Implemented: independent bounded Bitcoin-address crawler; persistent bounded frontier; honest full-Core-backed service readiness; ordinary Bitcoin header/inventory/block responses; modern address exchange; Gateway/BOD useful-request probes; service-loss replacement; real discovery UI and export; initial Data & Sync; result provenance; setup/browser action repairs; reviewed integration defaults; original native/browser icon assets.

Controlled proof: an initially unknown Gateway learned through two ordinary TCP Bitcoin peers supplies a checked non-genesis block before local header anchoring. Replacement through another learned Gateway also passes. No bundled Gateway endpoints.

Not accepted as proven: actual stock-Core address propagation, unrestricted public discovery, native Windows install/browser/tray behavior, and the continuing mounted-data/reorg/provider-transition gates. See current verification for exact results and environment limitations.

Deferred: full provider compatibility, native indexing profiles, full Ord/sat indexing, Runes, Chromium fork, extension database bridge and broader transports. Existing data, integrity, privacy and schema boundaries remain.
