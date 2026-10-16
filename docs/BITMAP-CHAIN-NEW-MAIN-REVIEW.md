# Bitmap Chain review against new-main

Reviewed 15 October 2026 against [`Blockamoto/bitmap-old` main (formerly new-main)](https://github.com/Blockamoto/bitmap-old/tree/1751e0d37073ab920cbd8b5548c61b74c37716d1), specifically its [separated commitment specification](https://github.com/Blockamoto/bitmap-old/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/BITMAP-CHAIN.md). This supersedes the earlier main-branch reference for future Bitmap design. It does not change or relabel saved experimental data.

The existing Gateway development encoder already uses the new separated outcome format. The remaining work is chiefly semantic profile definition, evaluation coverage and presentation; matching hash framing does not establish full Standard conformance.

## Byte format and state transitions

| Requirement in new-main | Gateway development implementation | Assessment |
| --- | --- | --- |
| Isolated L contains the domain and fixed 36-byte winning-inscription records only. | `bitmapIsolatedOutcome` reverses displayed TXID bytes, appends uint32 little-endian inscription index, rejects duplicates and overflow. | Framing matches. Caller still has to authenticate the IDs and their order. |
| Winning records remain in physical Bitcoin reveal order, even under an alternative selection policy. | District evaluation processes authenticated extraction in reveal order and supplies `OrderedWinnerIDs`; Chain reuses that completed District result. | Current fixed physical-order profile fits. A future global-inscription-number selector must preserve physical serialization after choosing winners. |
| C uses the previous C plus current L, with zero predecessor at a declared segment start. | `bitmapCumulativeOutcome` and the Chain consumer use the prior saved `ChainHash`, or zero for the initial step. | Matches; previous L/F are not substituted. |
| Fully evaluated empty blocks advance; unresolved blocks do not. | A completed zero-winner District outcome yields L-empty and advances C. Missing District evaluation creates pending evidence without a committed step. | Matches bounded tests. Unknown provenance must continue to block the affected evaluator rather than become an empty result. |
| F binds little-endian start/end, serialized Bitcoin block hash, raw C and raw R. | `bitmapScopedCommitment` uses those fields and byte orders. | Framing matches. |
| R identifies the exact outcome-producing semantic profile, including required activation policy. | F uses the District development profile hash, not the Chain software/profile identifier. | Correct dependency direction. Current fingerprint text does not fully declare the hard-coded activation/start policy; a successor profile must make that explicit. |
| District-only outcomes stay independent of Parcel methodology. | Chain consumes District winners; no Parcel winners enter its L/C/F. | Matches. Parcel Chain remains separately unspecified by the reference. |
| Earlier mixed-format state cannot be a rolling predecessor. | The legacy locked Bitmap recipe and new development stores are separate. | Preserve this boundary when adding configurable profiles. |

The independent literal digest vectors in `TestBitmapDevelopmentSeparatedGoldenDigests` cover empty L, one nonzero inscription index, first C, F byte order, duplicate rejection and consecutive empty advances. Witness/order, restart, reorg and missing-evidence tests provide additional bounded coverage. They are not independent full-history interoperability or performance evidence.

## Rules, coverage and saved identities

District selection, not the Chain encoder, must establish eligibility and first-winner context. New-main explicitly requires sufficient earlier context when evaluating a segment in the middle of history. A declared start cannot manufacture earlier completeness. Gateway currently starts the experimental District evaluator at block 792435; that is an implementation boundary, not a newly inferred Standard activation rule.

A successor semantic profile must identify the chosen activation/context policy, parser and charset behavior, explicit core/edge choices, and any historical schedule. Its R must change when any of those meanings change. Merely changing the reference revision, application version or display nickname must not rewrite existing winners or attach a new R to old outcomes. Re-evaluation is required unless an equivalence check establishes reusable outcomes under the new profile.

Keep at least four identities distinct:

- Source Bitcoin checkpoint and evaluated interval.
- District semantic profile R, which F commits.
- Chain storage/encoder profile, which describes how these records were produced and persisted.
- Optional richer audit digest, covering rejected candidates and explanations independently of winners-only L/C.

Parcel profiles bind the exact selected District profile. District-only R must not change because Parcel rules were registered. Shared Bitcoin blocks, inscription extraction and unchanged facts can be reused across profiles; selected winners, progress, dependency heads and Chain predecessors remain profile-owned.

## Workspace presentation

The resource panel should expose Bitcoin source, segment coverage, isolated outcome L, rolling history C, outcome-ruleset R, and combined F as separately labelled fields. The Chain storage profile must not be mistaken for R. Show resolved District numbers beside winning IDs, with links to their authenticated claim records. A zero-winner outcome is a positive evaluated result; a missing block remains a gap.

Comparisons must align source, segment and encoding before interpreting hashes. Different F can mean changed source, scope or rules even when winners match. Different R with equal L/C is not itself a winner dispute. Rejected/losing candidates and per-rule reasons belong in audit details rather than being added to the agreed outcome bytes.

The current preview retains raw evidence and the separate digest fields in Advanced, but its main Chain view does not yet present this full comparison model. Add those labelled fields with the rule-profile UI rather than silently changing the commitment encoder.

## Implementation boundary

The new-main branch explicitly leaves canonical profile bytes, a frozen bit registry, parser/charset edge vectors and complete ancestry evidence unfinished. Use named Boolean rule IDs and versioned Gateway development profile serialization for proposals; do not invent official packed bit positions or a canonical Standard hash. Alternative behavior that lacks executable semantics must be marked unsupported, with an explanation, rather than exposed as a functioning toggle.

See [the full new-main rule review](BITMAP-NEW-MAIN-REVIEW.md) for the District/Parcel rule matrix and configuration dependencies.
