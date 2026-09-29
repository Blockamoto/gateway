# Gateway roadmap notes

This document records the direction discussed during the 0.6.1 testing review. It is a roadmap, not a claim that every item is implemented in the current release.

## Current easy win

Gateway supports a compact, positional inscription address derived from Bitcoin evidence:

```
<transaction-index>.i<inscription-index>.<block-height>
```

For example, `0.i0.0` means inscription occurrence zero in transaction zero of block zero. The `i` marker is reserved for the inscription occurrence index. The address resolves by fetching and verifying the referenced Bitcoin block from peers; it does not require an Ord node or a local Bitcoin node. The result still includes the transaction ID, block hash, reveal evidence, and other native identifiers.

The existing positional Bitcoin forms remain supported for blocks, transactions, inputs, outputs, and satpoints. Their grammar and numbering conventions should remain versioned and documented as the schema expands.

## Index layers and comparison

- Keep the default Bitmap index sparse: a compact district-to-inscription representation.
- Offer richer audit output separately, including candidates, evidence, rule profile, and contestability findings.
- Make the rule set visible and bind its fingerprint to checkpoints and exports.
- Add a user-facing extraction action that packages the persisted index without rebuilding it.
- Add comparison for two or more imported indexes using canonical district/inscription serialization and a range-scoped fingerprint. A matching fingerprint proves equality of the compared data, not correctness against Bitcoin.
- Support known schemas first, preview ambiguous external JSON before importing, and preserve OCI's native sat-number and delta representation rather than pretending it is an inscription-ID index.

## Peer and browser architecture

- A Gateway node is dual-stack: native Bitcoin peer access on one side and a Gateway browser/peer protocol on the other.
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
