# Bitmap rules and Raw coverage: development acceptance

This development candidate from `76d81e1e1c8e02e163fc30c8445a1a71268818a4` includes accurate retained Raw coverage, a durable Raw producer gate, revised Standard/Custom naming and two optional Bitmap rules. Gateway main, releases, updater and existing preview profiles were preserved.

## Behavior

Raw coverage is maintained as compact ranges, reconstructed from local membership metadata when needed and explicitly marked as mapping until complete. Saved body coverage is distinct from a sequential backfill checkpoint. Partial inventory no longer masquerades as the full endpoint. Clearing Raw data suppresses summary recreation through background mapping and restart when only unproven files remain; verified retained-body publication can enable durable coverage again. Raw On requires durable retention before derived consumers receive a block, including cached bodies; unrelated earlier backfill does not block processing of an available later block.

Built-in tracks are Bitmap Districts Standard and Bitmap Parcels Standard. Added tracks receive numbered Custom names independent of their rules' classification. The new optional delegate exclusion and raw UTF-8 rules default to zero. Existing v1/v2 identities and results remain readable with unchanged hashes; choosing new semantics requires an explicit v3 interpretation. Indexed profiles stay locked.

The selected track's Details shows its complete, copyable binary answer string beside the name, with its schema-specific rule order. Legacy built-ins display a labelled projection of their existing policy without changing their stored identity.

The [optional rule contract](BITMAP-OPTIONAL-COMPARISONS.md) links the published Bitmap reference and official Ordinals delegate documentation. No third-party index compatibility or canonical standard profile serialization is asserted.

## Verification

- Full Windows backend: 1380 passing test events, 46 skips, zero failures; static analysis passes. All 379 Go files match the pinned validation archive. Race checks were not run with CGO disabled. The complete backend validation source is separately pinned to `7132622c06d33a0abc7ab92f724d0119b7c84a30`; its sole delta is `raw_blocks_074_test.go`, which waits for completed asynchronous mapping while preserving fork and missing-body assertions. Every runtime source and packaged input is byte-identical to application source `76d81e1e1c8e02e163fc30c8445a1a71268818a4`. The earlier synchronous-fixture failure remains rejected evidence; the corrected complete suite is a new invocation.
- Ten source browser checks and five compiled browser suites pass against the primary application, including version-aware editing, naming, exact versus partial coverage, contextual navigation and existing resource workflows. The viewport fixture explicitly retains a genuine block before asserting private Raw coverage.
- Compiled profile API checks verify both rule defaults and conflicts, old fingerprints, Custom names, lifecycle bindings and restart persistence.
- Compiled acquisition checks verify exact retained bytes, real process restart, accurate coverage, no Raw store created by empty inspection, and independent optional Locator/Inscriptions writes.
- The separate compiled Raw deletion check passes whole deletion, status, actual process restart and repeat Noop, preserving unproven bytes; a genuine subsequent body publication restores durable coverage.
- Cleanup/restart and independent audit of all 33 portable files pass. Bundled headers validate. Initial preview readiness records outbound peers enabled, sharing/Core sources disabled, and no index jobs started.

The same eight real blocks (15,145,474 bytes) took 1.422 seconds in the isolated append observation. It created 22 files and no transaction reverse shards. The timing excludes background coverage mapping and full coordinator execution. This is a local fixture observation, not network throughput, full-chain performance or measured Bitcoin Core speed parity. Stored bytes match after reopening; process restart is checked separately.

## Evidence

The authenticated [acceptance record](C:/GatewayReleaseBuild/bitmap-ruleset-development/verification/core-blocks-alignment/feedback-acceptance-76d81e1.json) binds frozen source, executable, portable archive, native checks, full test events, reference publication and local benchmark. The earlier application candidate failed repeat Raw deletion because its coverage summary was recreated. Its interrupted run is retained as rejected evidence; the replacement includes the deletion, restart and republish correction. Other earlier failed/interrupted attempts remain separate evidence.

- Source archive SHA-256: `3575c0280db2937575bd2ce6f05f2b39a3b3b2a7a8322c973d5553891d3ac6f9`
- Backend validation archive SHA-256: `0ab1eb1e183dcf9e03f9c49c3833ee8f4af8ff79ddc1efb51a5993975a19c73c`
- Client SHA-256: `71e601e0571b7958d8def27b234079c885d6d8530d45da52110f119a378b4ab7`
- Portable ZIP SHA-256: `ad9972546f4e256143c99b2c65c7d6c23c3bd33e9a09178ec744e9b4cce8366e`
- [Development preview](http://127.0.0.1:50985)
- [Portable ZIP](C:/GatewayReleaseBuild/bitmap-ruleset-development/Gateway-Bitmap-Rulesets-Development-76d81e1.zip)
