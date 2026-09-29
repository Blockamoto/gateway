# Gateway roadmap notes

This document records the direction discussed during the 0.6.1 testing review. It is a roadmap, not a claim that every item is implemented in the current release.

## Current release work

Gateway supports a compact, positional inscription address derived from Bitcoin evidence:

```
<transaction-index>.i<inscription-index>.<block-height>
```

All indexes are zero-based. `0.i0.0` illustrates the grammar only: genesis contains no inscription, so it must return an absence/error, not invented content. Both `0i0.0` and `0.i0.0` are accepted. The optional `.bitcoin` suffix supports browser namespace routing. The middle `i` marker identifies the inscription occurrence index; the existing input grammar keeps its leading `i` marker. The address resolves by fetching and verifying the referenced Bitcoin block from peers; it does not require an Ord node or a local Bitcoin node. The result still includes the transaction ID, block hash, reveal evidence, and other native identifiers.

The existing positional Bitcoin forms remain supported for blocks, transactions, inputs, outputs, and satpoints. Their grammar and numbering conventions should remain versioned and documented as the schema expands.

## Index layers and comparison

- Keep the default Bitmap index sparse: a compact district-to-inscription representation.
- Offer richer audit output separately, including candidates, evidence, rule profile, and contestability findings.
- Make the rule set visible and bind its fingerprint to checkpoints and exports.
- Add a user-facing extraction action that packages the persisted index without rebuilding it.
- Add comparison for two or more imported indexes using canonical district/inscription serialization and a range-scoped fingerprint. A matching fingerprint proves equality of the compared data, not correctness against Bitcoin.
- Support known schemas first, preview ambiguous external JSON before importing, and preserve OCI's native sat-number and delta representation rather than pretending it is an inscription-ID index.

## Peer and browser architecture

- Proposed transport evolution: a Gateway node would be dual-stack: native Bitcoin peer access on one side and a Gateway browser/peer protocol on the other.
- Prefer direct WebRTC for Gateway-aware browsers and peers, with signaling and TURN relay fallback when direct connectivity fails. Show when a connection is relayed.
- Keep Bitcoin-native discovery as an explicit opt-in capability; ordinary Bitcoin block and header access remains independent.
- Define authenticated, capability-scoped requests and explicit publication controls for indexes, evidence, and cached data.
- Treat hosted sessions as isolated and ephemeral by default. A public node chooses what it publishes; a browser or inscription chooses what it shares.
- Add connectors for other WebRTC or peer protocols only when their signaling and application protocols are understood and their operators permit participation.

## Browser and inscription clients

- Allow a Gateway-enabled inscription to connect over the browser transport and request verified results without embedding a Bitcoin node.
- IndexedDB remains origin-local. An inscription must explicitly export selected records, expose a connector, or speak the Gateway protocol; Gateway must not silently read another origin's database.
- A future browser index standard (working names include Gateway DB) can define namespaces, schemas, versions, sync rules, and optional proofs for client-side indexes such as Atlas-style browsing data.

## Deferred work

Full historical inscription numbering, complete contestability profiles, ownership and parcel layers, automatic external index import, broad cross-stack interoperability, and the complete security/privacy audit remain later stages. Experimental sat-line tooling may stay behind development controls while it is being validated.

## Testing review decisions and acceptance

- Keep fresh Bitmap indexing anchored at height 792435 internally; hide the editable start field and expose only the ending height. Resume must reuse validated checkpoints.
- Display blocks checked (committed coverage) and districts found (unique accepted claims), preserving counts across resume and correcting them after reorganization.
- Temporary peer block unavailability should wait and retry with a visible reason, keep pause available, and preserve committed work. Failed jobs need an obvious retry action.
- Default new Bitmap builds to lean records serialized as `"district:inscription-id"` strings. Keep checkpoint and rule metadata outside each record. Preserve old full indexes without silent conversion; future migration should be explicit.
- Default lean records have no contested flag. A proposed optional **Audit contestability** control would add comparison status and linked evidence without replacing the base winner. Distinguish not evaluated, no disagreement found under named profiles, disagreement, and missing historical context. Full retained records today do not imply that full audit exists.
- Keep inscriptions accessible. Hide Satline UI entry points under a Settings development-password visibility gate for testing; store no plaintext password. This is not an authorization boundary for the public network or local APIs.
- Fix stale version labels consistently in future packaging. Installer-specific feedback remains for a separate user review.

## Comparison and OCI details

The proposed comparison UI accepts the local index plus one or multiple external indexes. Auto-detect supported layouts, preview the district/ID mapping, and ask for field selection only when ambiguous. Sort numerically by district, define strict serialization and duplicate handling, and bind the range and format version to the digest. Hash first, then show changed, missing, and extra district mappings when fingerprints differ. Raw source ordering must not change equality.

OCI uses sat numbers/deltas; its exact adapter awaits the inscription reference and format inspection. Compare normalized native OCI data where compatible. OCI versus an inscription-ID index requires a translation with explicit coverage and provenance. Do not assume that equal raw deltas, sat identifiers, and inscription identifiers represent the same thing. A sat may host multiple inscriptions. Historical sat reconstruction may require substantial Bitcoin history; never hide that as a cheap import.

## Browser, inscription and extension ideas (not release commitments)

The user wants a hosted lightweight Gateway client and API/SDK for arbitrary websites, with per-user local execution and selected browser storage. Prefer data flowing directly from Gateway peers to the browser; the website may deliver code and discovery/signaling. A server that relays payloads still pays bandwidth costs, including TURN. Direct Bitcoin TCP access is not available to ordinary browser WebRTC clients; a Gateway peer supplies that bridge. Network transport support does not establish correctness: clients should verify the evidence they need, not treat a fingerprint alone as Bitcoin validation.

Consider peer-stack extensions that join an existing network as a compatible client, without requiring that network to adopt Gateway. Kenobi/Ord Drops, Switch and Block Forge were mentioned as examples to research, not verified integrations. Each needs its own discovery/signaling and application-protocol support. Do not promise complete peer visibility from WebRTC alone.

Consider a Gateway inscription backed by IndexedDB, tentatively Gateway DB or Gateway Dex. Atlas was described by the user as using recursion and OCI, caching resolved content, history, feeds and subscriptions locally. A future shared schema/connector could expose selected index data. Host origin policies, sandboxing, storage eviction and background execution limits need testing against actual inscription hosts. Browsing history remains private by default. Cross-origin database access is not automatic.

Privacy controls should let people choose what to serve, direct versus relay-only transport, and what metadata they expose. TURN fallback alone does not guarantee that addresses were never exchanged. Formal security review is deferred until a working implementation, while explicit publication and permission boundaries belong in the initial design.

## Human-readable addressing direction

Make structural paths first-class in Gateway interfaces and pursue a documented interoperable Gateway grammar, not a change to Bitcoin consensus. Retain native hashes and chain anchors. Block-height paths resolve against the selected chain and can change after reorganization. Output offsets locate historical output positions; a sat number is a stable ordinal identity whose location changes. Revealed inscription content stays in transaction witness data. Deriving a global sat number or current ownership can require ancestry/history beyond one reveal block and must not be claimed from the path alone.

## Next-version note: inscription ID block-location fallback

When an inscription ID cannot be resolved because Gateway cannot locate its reveal transaction, prompt the user for the reveal block height and retry with the original inscription ID preserved. Missing local block bytes alone should not require this prompt when the block location is already known. The ID supplies the transaction ID and inscription index; the user should not also need to supply a transaction position.

Resolve the supplied height against the selected header chain, fetch the block from available Bitcoin peers, verify the block and witness commitment, locate the matching transaction, and extract the requested inscription. This should work without a local full Bitcoin node, an external Ord server, or a Gateway locator peer. A wrong block must produce an explicit transaction-not-found result; unavailable block sources should allow waiting or retry. This resolves inscription content, not its current owner or global sat lineage.

Build on the existing optional block-hash hint by making the fallback contextual and accepting a block height. This is a follow-up feature request recorded after publication of v0.6.2, not functionality claimed by that release.

## Next-version candidates: Bitmap names, parcels and audit

The user proposed a parcel indexer, Bitmap index audit, comparison tool, and Bitmap address resolution for the next development pass. The comparison tool is a larger project; this records proposed scope, not completed functionality or a guaranteed release bundle.

Use `DISTRICT.bitmap` to resolve the district's accepted Bitmap inscription, and `PARCEL.DISTRICT.bitmap` to resolve the parcel's inscription through the corresponding indexes. These are compact positional addresses with no additional labels. Integrate them consistently wherever Gateway accepts resource addresses, including the browser address bar. Bind results to the applicable rule profile and indexed chain coverage; distinguish missing coverage, no accepted claim, and unavailable inscription content. A parcel number must not be treated as an inscription index. Parcel rules and indexing requirements need to be specified and tested before claiming parcel resolution support.

When the required district or parcel index is absent or lacks coverage, offer an actionable way to obtain it: build the required index locally or fetch a compatible published index from a Gateway peer. Recommend an available route according to peer capabilities and the required coverage, while making the source and work involved clear. Preserve the requested address and retry resolution once the dependency is ready. Peer-provided indexes must satisfy the applicable rule-profile, chain-anchor and verification requirements; a downloaded fingerprint alone does not establish correctness. If no suitable peer is available, retain the local-build option rather than returning an unexplained lookup failure. This acquisition flow is planned work, not a claim of automatic peer import in the current release.

## Next-version refinement: reveal location included by default

Include inscription reveal block heights by default in Bitmap indexes and exports intended for address resolution, with an explicit smaller-format option that omits them. Reveal height is distinct from the district number. Reuse authenticated block-batch context where available instead of duplicating the height in every internal record; preserve the location when producing a standalone export. This refines the earlier minimal district/ID default without adding full audit records. Define and version the extended export format before implementation.

Explain the smaller-format tradeoff in the UI: "Without reveal block heights, Bitmap numbers still resolve to inscription IDs. Fetching inscription content may require a block hint or another provider." Do not claim all Bitmap lookup becomes impossible without heights. Conversely, including heights enables locating the required block but does not guarantee peer availability. This is a next-version decision, not a change to the published release.

Latest decision: retain reveal block location as required information in the standard lean Bitmap index and its standard export, with no normal-user toggle to remove it. The standard index should support locating inscription content, not merely return an ID that needs another locator. This supersedes the optional-height UI proposal above. Lean still excludes full audit records; internal block-batch context may supply the location without per-record duplication. A future IDs-only comparison/export utility can remain separate from the standard resolvable index format. Available block sources are still required to fetch content.

## Implemented after v0.6.2: focused Bitmap resolution patch

The source now accepts `DISTRICT.bitmap` through the shared resolver and advertises the Bitmap browser namespace. District resolution uses the local accepted claim, checks the index checkpoint against the selected chain, and passes its saved reveal block hash to native inscription extraction. Missing coverage offers the Indexes workspace; automatic peer-index import and parcel resolution remain deferred. Existing Windows DNS integration needs to be reapplied to add the new namespace.

Lean query rows now use the explicit `bitmap-lean-v2` object projection with `district`, `inscription`, and `reveal_height`. Exact lookup includes `reveal_height` and `reveal_block_hash`. Immutable internal batches retain their existing encoding and supply the reveal location through their authenticated checkpoints, so existing indexes do not need migration. This is query JSON, not a new complete-index download/export feature.

Inscription resolution accepts a reveal block height or hash through the existing block-hint field. Failed bare-ID navigation offers a Supply reveal block action that preserves the ID and opens the hint field. Main Go tests, the reveal-location projection regression, index workspace interaction tests, browser companion tests, and real-browser shell navigation passed. Fresh live Bitcoin-peer resolution of a district and the new fallback flow have not yet been exercised. This patch is not part of the published v0.6.2 assets.
