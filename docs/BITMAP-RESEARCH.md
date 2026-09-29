# Bitmap research and evidence

Research date: 2026-09-29. This document separates Bitcoin evidence, published theory, an implemented indexer's rules, and Gateway's own profile decisions. It is not a claim that all Bitmap indexers agree or that Gateway reproduces a complete Ord index.

## Origin verified from Bitcoin data

The supplied origin is inscription `86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0`. Its reveal transaction occurs at **zero-based transaction index 953 in block 792435**, hash `000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c`. The block timestamp is 2023-06-01 22:08:52 UTC. The inscription has one envelope in input 0, content type `text/plain;charset=utf-8`, and exactly eight body bytes: `302e6269746d6170` (`0.bitmap`, without a newline).

The included verifier parses the raw block and independently checks the header hash and proof of work, BIP34 coinbase height, transaction Merkle root, BIP141 witness commitment, and exact reveal envelope. Checking only the ordinary transaction Merkle branch would not authenticate inscription witness bytes. The raw block came from [mempool.space's block endpoint](https://mempool.space/api/block/000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c/raw); the [transaction endpoint](https://mempool.space/api/tx/86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732) and [Ord explorer](https://ordinals.com/inscription/86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732i0) corroborate it. Explorer inscription number 10423123 is a provider assertion; this research did not rebuild inscription numbering.

Run from the repository root, with Python 3:

```text
python docs/bitmap-evidence/verify_bitcoin_evidence.py
python docs/bitmap-evidence/verify_profile_rules.py
```

Both checks passed during this research. The first writes `genesis-verification.json`; the second passed 16 synthetic cases using the actual pinned OPI grammar function, without loading its database or running its indexer.

**Limits:** This verifies inclusion and committed witness contents in the specified block, not full Bitcoin consensus/UTXO/script validation or the selected chain's cumulative work. It does not prove that no earlier transaction contained a Bitmap claim. Starting a Gateway build at 792435 is an explicit project origin boundary; the supplied inscription's inclusion alone does not establish exhaustive prehistory.

## What the author actually specifies

The author repository is pinned to [`Blockamoto/gitbook` at `caf9e3a6f1e8be195f9f74f264f36983a55fec91`](https://github.com/Blockamoto/gitbook/blob/caf9e3a6f1e8be195f9f74f264f36983a55fec91/bitmap-theory-whitepaper/theory.md). This is the inspected revision, not a claim that its wording was already fixed in June 2023.

It maps blocks to districts and transactions to parcels. A district claim has the form `{block-height}.bitmap`; priority belongs to the earliest qualifying inscription of a block that exists. Unseparated parcels accompany their district; individually inscribed parcels remain independent. Parcel text has the form `{tx-index}.{block-height}.bitmap`, and a valid parcel must preserve child provenance from its district. The author's example `0.404.bitmap` establishes that parcel indexing starts at zero.

The inspected theory does **not** completely specify byte grammar, MIME matching, ties within a transaction/block, historical negative numbers, Jubilee, compression, delegates, malformed envelopes, or the exact machinery for validating ancestry. Consequently, a fully deterministic Gateway profile must state its choices rather than attribute all of them to an already complete historical specification.

For a bounded district implementation, record these choices in the machine-readable rule profile: exact body grammar; accepted envelope syntax and field handling; content-type policy; compression/delegation policy; earliest eligible block; target-height comparison; ordering tuple; duplicate resolution; and treatment of unavailable historical context. The requested Bitcoin chronological order can be defined as `(reveal height, transaction index, input index, envelope index)`. This is a Gateway definition, not interchangeable with OPI inscription-number order.

## Pinned OPI behavior

Named comparator: [`bestinslot-xyz/OPI`, commit `0a09b987c87692ec3cabd404c8bcc7367707ee9a`](https://github.com/bestinslot-xyz/OPI/tree/0a09b987c87692ec3cabd404c8bcc7367707ee9a). Its Bitmap module identifies itself as `opi-bitmap-full-node v1.0.0`, database version 4. These labels alone do not pin its Ord dependency; the repository commit does.

| Concern | Source-supported behavior at this commit | Consequence for Gateway |
| --- | --- | --- |
| Body grammar | Strict UTF-8 decoding, exactly ASCII digits followed by lowercase `.bitmap`, nonempty decimal, no leading zero except `0`. No trim, normalization, signs, Unicode decimal digits or trailing newline. | An implementable canonical grammar is `(?:0|[1-9][0-9]*)\.bitmap` over the entire byte body. Compare to the reveal height without integer overflow. |
| Existing block | Rejects a number greater than the reveal block height. Equality passes this check. | Do not accidentally use `<` if intending this OPI height rule. |
| First claim | Processes blocks forward; within each block the DB reader sorts ascending inscription number. SQL uniqueness on district number preserves the first inserted eligible candidate. | Sorting raw transaction/envelope order is a different profile. |
| Candidate eligibility | Reader excludes negative inscription numbers, JSON content, and bodies outside its plain-text filter. The upstream updater excludes unbound inscriptions from its emitted records. | Body syntax alone cannot reproduce the eligibility set. |
| MIME | Ingestion accepts exact `text/plain` or a `text/plain;` prefix for non-JSON plain text. The downstream reader has a broader raw `text/plain` prefix predicate. | Interpret both stages together: `text/plainXYZ` does not become eligible merely because the downstream predicate accepts that prefix. MIME case is significant. |
| Jubilee | Updater treats curses as negative only before mainnet height 824544; later otherwise-cursed inscriptions may be positive and marked vindicated. Bitmap filters number, not the separate `cursed_for_brc20` flag. | A permanent blanket exclusion of every inscription with a curse-like characteristic is not this OPI profile. |
| Reinscriptions | Curse determination reads prior inscription state at the input offset, including whether an earlier inscription was cursed or vindicated; count matters. | A local transaction parser cannot generally determine this from the reveal alone. |
| Encoded or delegated content | The inspected path persists the envelope's raw body, and the Bitmap module decodes those bytes directly. No body decompression or delegate-content lookup occurs in that path. | Do not use rendered explorer content as a substitute for the actual reveal body. A delegate-only envelope is not proved equivalent to a body claim. |
| Unknown even fields | Updater marks these unbound, including after Jubilee; unbound records do not enter the Bitmap path. | Jubilee does not erase every eligibility restriction. |

Primary code references:

- [Bitmap grammar and insertion, `bitmap_index.py` lines 133–218](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/modules/bitmap_index/bitmap_index.py#L133-L218).
- [Reader filter, `server.rs` lines 309–324](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/db_reader/src/server.rs#L309-L324), and [candidate extraction/order, lines 662–729](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/db_reader/src/server.rs#L662-L729).
- [Historical curse and reinscription classification, updater lines 154–218](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/src/index/updater/inscription_updater.rs#L154-L218); [ingestion MIME gate, lines 381–387](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/src/index/updater/inscription_updater.rs#L381-L387); [number assignment and raw-body persistence, lines 544–587](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/src/index/updater/inscription_updater.rs#L544-L587).
- [Mainnet Jubilee height, `chain.rs` lines 48–55](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/src/chain.rs#L48-L55).
- [Envelope parsing and body concatenation](https://github.com/bestinslot-xyz/OPI/blob/0a09b987c87692ec3cabd404c8bcc7367707ee9a/ord/src/inscriptions/envelope.rs#L29-L136). It parses tapscript instructions, not arbitrary `.bitmap` byte substrings in transaction data. Supported numeric pushes are recorded as a curse characteristic. Duplicate and incomplete fields have explicit flags.

Further ordering dependency: the updater sorts floating inscriptions by sat offset before placement, assigns numbers during location updates, and handles inscriptions sent as fees through the coinbase path. Thus the number-order dependency is substantive, even when all candidates have nonnegative numbers. A complete OPI comparison needs its Ord placement/history, not just an explorer number attached to an otherwise different parser. This is not a claim that every comparison requires indexing every sat from Bitcoin genesis; the minimum sufficient dependency closure was not established here.

### Gateway base implementation reviewed

The new `index_store.go` implementation's `bitmapCandidates` function was reviewed during this pass. It uses canonical raw direct body bytes, the existing-block test including equality, and occurrence order from the bounded extractor. It accepts a canonical claim regardless of MIME, parser flags, unknown-even-field/unbound classification, or historical inscription numbering. Content encoding does not trigger decompression; a delegate does not supply a replacement body. Those are explicit **Gateway base-profile choices**, not demonstrated historical community consensus or OPI equivalence. Compatibility remains `not_evaluated` and ownership remains `unknown`.

These choices must be included in the catalog's machine-readable rule language and fingerprint. Useful regression cases include absent/wrong MIME, flagged envelopes, delegate-only inscriptions, encoding metadata with a canonical raw body, target equal to reveal height, integer overflow, and duplicate canonical claims in a single block. This research review did not execute production Go tests or certify the complete index runtime.

## Other named profiles and parcel provenance

The pinned [TAP specification, commit `0ba25b37d51e41e8b8958b0eb4a2c420a84a6361`](https://github.com/Trac-Systems/tap-protocol-specs/blob/0ba25b37d51e41e8b8958b0eb4a2c420a84a6361/README.md#bitmap), says its Bitmap path does not support cursed inscriptions. This is insufficient to declare it identical to OPI or to infer its exact Jubilee and tie-breaking behavior. No complete TAP Bitmap implementation was validated in this research.

The [TRAC public endpoint documentation, commit `b0130f1d66178da7f3e89c6db1a0244be6ea4d3c`](https://github.com/BennyTheDev/trac-bitmap-public-endpoint/blob/b0130f1d66178da7f3e89c6db1a0244be6ea4d3c/README.md), exposes district/parcel resolution and accumulated-versus-tapped parcel queries. API output is an indexer's assertion, not a Bitcoin inclusion proof or a complete normative algorithm. No live TRAC winner comparison was performed.

For current author-style parent/child parcels, text matching alone is insufficient. Require the selected valid district, a transaction index inside that district block's actual transaction count, and verified inscription ancestry. In the pinned Ord updater, claimed parents are retained only when present among inscriptions participating in the transaction; merely placing a parent ID in a tag is insufficient. Ownership/sat placement therefore remains relevant. This research did not settle historical tapping compatibility, parcel activation boundaries, multiple-parent behavior or complete first-valid-child ordering. Parcel support should stay explicitly incomplete until those semantics and fixtures are pinned.

## Historic disagreement: evidence gap remains

**No real historical district with a verified conflicting winner was established in this bounded research pass.** The included origin is real Bitcoin evidence; the grammar fixtures are synthetic. Neither is a historic contested example. Targeted primary-repository searches and source inspection established several mechanisms for divergence but did not produce sufficient candidate IDs, authenticated reveal facts and complete prior-claim context to certify an actual changed winner.

Do not label every structurally unusual inscription contested. Use separate states such as `compatibility-not-evaluated`, `compatibility-context-missing`, and `profiles-disagree`. Only the last requires observed differing outcomes under named, pinned profiles. Missing historical evidence is not rejection and is not agreement. A verified negative-number Bitmap candidate would establish a candidate-level eligibility difference if the base accepts it; it would still not establish a different district winner without earlier-claim coverage.

A sufficient historical disagreement fixture should contain both candidate IDs where applicable, exact reveal bytes, block hash/height and transaction/input/envelope positions, witness-authenticated inclusion, named profile versions, the required Ord/ancestry evidence, and prior-claim coverage up to the decision point. Include the result from each profile and its reason. Until such a fixture exists, historical compatibility and the release brief's real contested-example requirement remain unverified.

## Evidence inventory and reuse

`bitmap-evidence/source-manifest.json` records public URLs, revision pins and SHA-256 hashes. `genesis-block.raw` is the complete 1,834,669-byte Bitcoin block used for reproducibility; `genesis-tx.raw` is derived from it. No wallet secrets, credentials or local live-peer endpoints are included. The verification scripts make no network requests. Pinned OPI source copies are unmodified and accompanied by upstream root and Ord license files. Exploratory search downloads are omitted from the deliverable.
