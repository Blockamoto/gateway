# Bitmap new-main: District and Parcel review

Reviewed 15 October 2026 against Gateway source `5b2b1c01368a2dd745148bff98146e29e2100e7a`. This is a design and implementation-gap review. It does not change runtime rules, saved profiles, released applications or user data.

The required reference is **Blockamoto/bitmap, branch `new-main`, commit `1751e0d37073ab920cbd8b5548c61b74c37716d1`**. The previously reviewed `main` revision is not substituted for this source.

## Finding

The current development evaluator implements much of the new reference's core behavior under one fixed, explicitly experimental Gateway profile. It does **not** implement a configurable binary ruleset system, a complete Ordinals parent validator, or a final canonical Standard fingerprint.

The next implementation should expose the reference's named rule questions and values, validate their actual dependencies, and distinguish unsupported alternatives from contradictory settings. District and Parcel results must identify their exact profiles. Parcel results must also bind the District profile defining the recognized parent. Existing development-v1 results retain their original interpretation and provenance.

Read this with the [separate new-main Chain review](BITMAP-CHAIN-NEW-MAIN-REVIEW.md). District semantic R, the Chain consumer's storage/profile identity, and the readable L/C/R/F fields are distinct; adding configuration must not conflate them.

The reference itself calls its numerical registry, canonical profile bytes and some parser/provenance details unfinished. That permits explicit Gateway development choices; it does not justify calling those choices the frozen canonical Standard.

## Source selection and attribution

| Pinned source | Role in this review |
| --- | --- |
| [README][R] | Theory, Standard, evaluation and Chain are distinct layers; reference presets and scope. |
| [DISTRICT-RULESET.md][D] | Five core questions, seven optional questions, decoding and precedence requirements. |
| [PARCEL-RULESET.md][P] | Six core questions, seven optional questions, recognized District dependency and parent-child model. |
| [BITMAP-CHAIN.md][C] | Separation of semantic rules from outcomes/source; evidence, scope and profile-identity boundaries. Exact Chain encoding is reviewed separately. |

Exact connector-read copies and `SOURCE.json` are retained outside the checkout at `C:\GatewayReleaseBuild\timeline-bitmap-development\bitmap-reference-new-main`. This document paraphrases the reviewed rules rather than importing the private reference implementation.

The pinned documents attribute the District naming/existing-block/first-valid-transaction foundations to 2023, the parent-child Parcel model to 2024, and the precise undelegated-body, charset, full-match and same-transaction clarifications to 2026. Keep these attributions separate. The linked historical publications were not independently reread in this pass; the pinned new-main documents are the immediate evidence here. In particular, the former address-matching/tapping model is not the reviewed parent-child Parcel Standard. Third-party marketplace behavior is not silently promoted into it. [Sources: README][R], [District provenance][D], [Parcel provenance][P].

## Reference values are named questions, not allocated bit positions

| Profile | Core reference values | Optional reference values | Question count |
| --- | --- | --- | --- |
| District | `content=1`, `text=1`, `regex=1`, `height=1`, `fiftxi=1` | `brotli=0`, `fifin=0`, `cursed_exclusion=0`, `reinscription_exclusion=0`, `unbound_exclusion=0`, `district_child_exclusion=0`, `burns=0` | 12 |
| Parcel | `content=1`, `text=1`, `regex=1`, `range=1`, `parent=1`, `fiftxi=1` | `single=0`, `brotli=0`, `fifin=0`, `cursed_exclusion=0`, `reinscription_exclusion=0`, `unbound_exclusion=0`, `burns=0` | 13 |

These are the reference defaults in [District][D] and [Parcel][P]. Their illustrative all-one core sequences do not allocate stable bit numbers, packed bytes, ordering for a registry, or trailing-zero elision. A user-facing binary control can use symbolic IDs without inventing those unfinished standards.

A zero has the meaning of its specific question. For example, `brotli=0` removes a decompression eligibility route, while `cursed_exclusion=0` removes an extra status-based rejection. A zero never means all missing evidence can be treated as valid.

## District: reference versus current implementation

The upstream column below is governed by [the pinned District ruleset][D]. Current implementation references point to the reviewed Gateway source.

| Question | Reference behavior | Current Gateway behavior | Gap or required verification |
| --- | --- | --- | --- |
| `content=1` | Evaluate the inscription's own body. A delegate field alone does not disqualify an otherwise qualifying own body. | Extraction retains raw own-body bytes; Apply uses those bytes and performs no delegate resolution. | Behavior fits this rule. Add explicit own-valid-body-plus-delegate and delegate-only negative fixtures. Do not implement it as “reject every delegate field.” [Extraction](../inscription_index.go#L62), [Apply](../bitmap_development_store.go#L372). |
| `text=1` | Require parsed `text/plain`; strictly decode the declared charset. Valid casing/parameters alone do not reject. | `mime.ParseMediaType`, case-insensitive media type, strict ASCII/UTF-8/UTF-16/UTF-32 policies. Unknown charset yields unresolved. | The exact label registry, absent-label fallback and BOM policy are Gateway development choices, not a finalized shared decoder Standard. Tests cover several cases, not exhaustive interoperability. [Decoder](../bitmap_development.go#L78), [tests](../bitmap_development_test.go#L81). |
| `regex=1` | Consume the entire decoded claim, canonical ASCII decimal and lowercase suffix; no trailing LF or extra suffix. | Exact reconstructed whole-text equality and canonical unsigned parsing. | The repeated/embedded-suffix regression is present. Add a complete table of whitespace, newline, non-ASCII digits, upper suffix, both boundary zeros and maximal values. A uint64 parser limit is an implementation representation constraint, not an extra published bit. [Apply](../bitmap_development_store.go#L503), [decimal parsing](../bitmap_development.go#L165). |
| `height=1` | Target must exist at reveal, including equality. A premature claim is not activated later. | Compares target height to the reveal height before selecting. No later reevaluation activates it. | Add equality, premature-then-later-exists, and alternative-claim fixtures. Keep source-height authentication separate from this numeric rule. [Apply](../bitmap_development_store.go#L372). |
| `fiftxi=1` | First eligible claimant by reveal height, transaction position, then transaction-wide inscription ID index. | Sequential blocks, stable transaction/index sort, immutable first winner and retained losing rows. | Fits the reference order. Parser/envelope-to-ID-index correspondence still needs pinned adversarial and independently checked witness vectors. It is not input/output ordinal or global inscription number. [Ordering](../bitmap_development_store.go#L458), [ID derivation](../inscription_index.go#L109). |

## Parcel: reference versus current implementation

The upstream column is governed by [the pinned Parcel ruleset][P]. Parcel lexical components are transaction index followed by District height. Coinbase is transaction zero.

| Question | Reference behavior | Current Gateway behavior | Gap or required verification |
| --- | --- | --- | --- |
| `content=1` | Own-body claim; delegate field alone is allowed. | Shares the District own-body path. | Add Parcel-specific delegate/body fixtures, without introducing an extra delegate exclusion. |
| `text=1` | Same strict media-type/declared-charset interpretation as District. | Shares the decoder. | Bind one declared decoder policy consistently; do not let the two presets drift. Unknown decoding remains a coverage gap. |
| `regex=1` | Whole `transaction.height.bitmap` text, each numeric component canonical. | Parses two components and requires whole-text equality. | Add Parcel repeated/embedded suffix, leading-zero-in-either-component and trailing-newline negatives. |
| `range=1` | Target block exists; `0 <= transaction < transaction_count(target block)`. | Checks reveal-height bound, obtains target block through shared source callback, checks integrity/count/selected hash, rejects out-of-range index. | A separately retained Raw Blocks or transaction-locator index is not required. Missing authenticated transaction-count evidence must be unresolved rather than a fabricated empty block. The extra numeric future-height check does not justify adding a redundant Parcel `height` question. [Target context](../bitmap_development_store.go#L531). |
| `parent=1` | At least one valid Ordinals child edge must point to the District recognized under the identified District profile. A string is not proof. | Binds the fixed District profile and exact same-height District evaluation; checks declared parent ID, saved Sat identity/location/value and consumption by the reveal transaction. Retains a detached receipt. | This is a real checked positive path, but not complete general parent-child validation. See the coverage and provenance limitations below. [Parent check](../bitmap_development_store.go#L664), [retained evidence](../bitmap_development_evidence.go#L22). |
| `fiftxi=1` | First eligible child for the same District/transaction coordinate, including same-transaction ID-index ties. | Same ordered first-winner mechanism as District, with Parcel key. | Add accepted-child competition and same-transaction ties through the full Apply path, rather than only testing the parent helper. |

The current profile already includes the District profile digest in its Parcel fingerprint. That binding must remain when configuration is introduced; selecting a different District winner set can change valid Parcel parents with otherwise identical Parcel rule values. A different parent set is not a new “contested District” bit. [Profile dependency](../bitmap_development.go#L44), [reference identity requirement][P].

## Every optional question

All defaults in this table come from [District][D] and/or [Parcel][P]. “Absent” below means not a selectable evaluated alternative; it does not imply the reference default is violated.

| Question / kinds | Reference value and meaning | Current implementation | Requirements for a supported alternative |
| --- | --- | --- | --- |
| `brotli` / both | `0`: decompression alone cannot make a claim eligible. | Never decompresses; raw own body plus declared charset only. | A `1` comparison needs a bounded decompressor, exact content-encoding handling, an explicit decode pipeline and failure policy. Brotli is not a charset. Do not treat `content=1` and `brotli=1` as automatically contradictory: an explicitly defined comparison could decompress the candidate's own bytes. |
| `fifin` / both | `0`: global Ord inscription numbering does not replace physical precedence. | No numbering lookup during selection. | A `1` comparison needs a specified numbering/activation profile, complete numbering evidence, ordering of negative/vindicated numbers and winner-finalization semantics. Simply sorting an integer is insufficient. It conflicts with `fiftxi=1`. |
| `cursed_exclusion` / both | `0`: a curse/negative-number label alone is not an added rejection. | Apply does not reject parser flags or inspect canonical inscription numbers. | A `1` comparison needs an exact historical curse/numbering profile and evidence, including Jubilee handling. Extraction flags alone are not a complete global curse classification. |
| `reinscription_exclusion` / both | `0`: no blanket rejection merely because the sat was previously inscribed. | No prior-inscription eligibility check. | `1` requires historical identity/inscription occupancy at reveal and an activation policy. Optional retained TXIO links or current UTXOs alone do not prove that history. |
| `unbound_exclusion` / both | `0`: unbound status alone is not an extra rejection. | District selection does not reject `Envelope.Unbound`; Parcel selection has no independent unbound exclusion. | `1` needs exact historical boundness semantics, not merely the parser's unrecognized-even-field flag. Missing ability to prove a valid parent is a `parent` evidence issue, not permission to pretend this exclusion was enabled. |
| `district_child_exclusion` / District only | `0`: having an Ord parent alone does not reject a District. | District Apply ignores parent claims for eligibility. | `1` needs a definition of whether declared or verified parenthood is excluded and the corresponding evidence. Do not add this District question to the Parcel core. |
| `single` / Parcel only | `0`: multiple parents are allowed; one verified correct District edge is still required. | Looks for the selected District ID among all declared IDs and proves that edge; unrelated IDs do not disqualify. | `1` requires defining the count being restricted: exact valid Ord parents under the chosen validator, with deduplication/invalid-reference handling. Counting raw strings is not automatically equivalent. Add positive extra-parent and invalid-only-parent fixtures. |
| `burns` / both | `0`: burning a winner does not revoke its claim or reopen the coordinate. | Winner records are not removed in response to later spending/burning. Persisted Parcel creation evidence is independent of later parent ownership. The behavior is not explicitly stated in the current profile Rules list. | Declare `burns=0` explicitly in the next profile. A `1` alternative is not yet executable from the reference: define a burn predicate, timing, activation and replacement policy first. An absent UTXO, spent output, unavailable location or stale checkpoint is not by itself a burn proof. |

Ordinary duplicate claims are already governed by first-eligible precedence. Parent-profile differences are already governed by `parent` plus the District fingerprint. Do not manufacture additional binary questions for these derived consequences. Burn status, claim recognition, spendability, ownership and tradability remain separate.

## Dependencies, exclusions and undefined alternatives

These are three different classes and should be displayed differently in configuration review:

| Class | Case | Admission behavior |
| --- | --- | --- |
| Explicit conflict in the reference | `fiftxi=1` and `fifin=1` | Reject the configuration with both named ordering choices. Do not silently select one. |
| Missing selection semantics | Both ordering questions `0` | The documents supply no replacement winner rule. Do not offer a recognized-winner/Chain build until a named comparison defines one. This is unsupported semantics, not a quoted Standard prohibition on every conceivable comparison. |
| Required semantic dependency | Parcel `parent=1` | Bind the precise District semantic profile and a defined Ord parent validator. Require completed applicable District context and checked edge evidence. |
| Conditional evidence | `range=1` | Obtain authenticated target-block transaction count. Optional persisted locator/Raw Blocks indexes may help, but are not mandatory stores. |
| Conditional historical state | `fifin=1`, `cursed_exclusion=1`, `reinscription_exclusion=1`, or historical boundness checks | Require the selected semantics and evidence they actually need. Never auto-build numbering/Sats or claim that one current UTXO snapshot supplies arbitrary history. |
| Undefined alternate interpretation | `content=0`, `text=0`, `regex=0`, District `height=0`, Parcel `range=0` or `parent=0` | Turning off a required constraint does not define delegate resolution, raw-vs-rendered text, a target grammar, future activation, a virtual transaction or a replacement Parcel model. Mark unsupported until an explicit comparison profile defines the missing behavior. |
| Undefined revocation | `burns=1` | Disabled/unsupported until predicate, temporal behavior and winner replacement are declared and tested. |
| Orthogonal questions | Charset decoding versus Brotli; curse versus reinscription; optional exclusions versus claim ownership | Do not invent mutual exclusion merely because the implementations need related evidence. |
| Product preset constraint | A chosen Gateway parent-child preset fixes core values at `1` | Explain this as the supported preset's scope. For example, do not claim `single=1,parent=0` is a universally contradictory Boolean formula; the reviewed Standard does not define that alternate model. |

Not every one of the theoretical `2^12` District or `2^13` Parcel bit combinations has a specified executable meaning. The source gives the reference answers and several comparison questions, not a complete combinatorial validator.

## Parent proof is presently a limited evidence adapter

The current positive check is materially stronger than trusting a parent-ID field. It requires an exact pre-block saved Sat checkpoint matching the reveal's previous block, the selected District inscription's saved identity, a live output containing its sat at the claimed offset, authenticated value/range/native-output facts, and that output consumed by the child's reveal transaction. Its detached content-addressed receipt retains the required pages/facts after Sat garbage collection. [Checked path](../bitmap_development_store.go#L664), [receipt authentication](../bitmap_development_evidence.go#L75).

It still has concrete limits:

- A valid parent created earlier in the same block cannot be found in the pre-block Sat state. A parent transferred earlier in the same block also needs ordered within-block provenance. Current code leaves these cases unresolved rather than completing an edge validator.
- A later Sats head does not automatically provide the exact historical pre-block view needed for a retrospective Parcel build. Requiring exact evidence is correct; silently starting Satline or a genesis Sat build would not be.
- The helper returns the same nil-proof result for missing context and some provable negatives. For example, with an authenticated pre-block live parent location and a complete reveal transaction, failure to consume that output is evidence that this proposed edge fails, rather than a reason to wait forever. The caller currently labels it unresolved. Introduce distinct checked-valid, checked-invalid and evidence-unavailable results.
- The check does not establish all historical Ord recognition/child-edge semantics merely because the sat and input match. Pin the actual positive validator semantics and source vectors. Verify child-ID/parent encoding, malformed or repeated references, input placement and relevant activation behavior.
- The 4,096-range proof-preparation limit is an operational evidence budget. Exceeding it causes unknown coverage, not an extra Standard rejection. Its availability limit must not be advertised as `unbound_exclusion=1`, a failed parent predicate or a canonical rule bit.
- A retained creation edge remains valid evidence after a later ordinary transfer or parent burn. This is distinct from a current-location/ownership join.

The current tests establish a real positive helper path, wrong-input/wrong-snapshot nonacceptance, retained receipt integrity and missing-evidence waiting. They do not establish a complete mainnet Parcel validator or a full accepted-Parcel Apply/competition matrix. [Existing tests](../bitmap_development_test.go#L231).

## Build, query and retention consequences

| Consumer or question | Necessary data/state | Optional assistance | What cannot be inferred on demand from one known block |
| --- | --- | --- | --- |
| Reference District eligibility | Full authenticated reveal block/witnesses, pinned extraction/decoder, preceding relevant winner context. | Retained occurrence records or locally available raw blocks; shared extraction from the same scan. | Whether an earlier competing winner exists outside evaluated coverage. |
| Parcel `range` | Authenticated target block and transaction count, possibly much older than reveal. | Shared source cache or retained block/transaction metadata with proper source binding. | A count from a hash alone. |
| Parcel `parent` | Applicable District selection plus defined creation-time parent-edge evidence. | Saved Sat identity/location and input facts; future checked within-block adapter; retained proof receipts. | Arbitrary ancestry, a historical sat location or a valid edge from a parent string/current address alone. |
| Global numbering / curse / reinscription alternatives | Complete required historical ordinal state under a pinned interpretation and activation schedule. | Existing complete state, explicit reviewed historical work, or already retained independently checked evidence. | Complete historical status from parser flags, a sparse TXIO index or today's UTXO membership. |
| Burn-sensitive comparison | Defined temporal burn evidence and replacement policy, only after the rule is specified. | Selected-chain output/Sat evidence where it actually proves the chosen predicate. | A burn merely because data is missing or an output is spent. |
| Current ownership | Matching current Sat/inscription location plus exact output scripts/destinations and checkpoint. | Current Addresses when built at a compatible checkpoint. | Ownership from a recognized claim or Bitmap Chain digest. |

The existing shared coordinator is the right source boundary: one prepared block/extraction can feed District and Parcel consumers while they retain distinct state. A missing conditional Parcel dependency should not stop independently complete District outcomes. Turning a consumer Off preserves its committed profile/state and must not start hidden prerequisite work. [Coordinator dependency reporting](../index_run.go#L492), [shared-run adapter](../index_run_worker.go).

The fixed scan start `792435` is a Gateway development choice. The reviewed new-main rules do not themselves specify that constant as a universal eligibility activation height. Do not claim first-winner completeness outside the declared source/prior-context scope merely from that start. Either justify and identify a sufficient prior winner baseline, evaluate the required earlier prefix, or label the limitation. If a semantic activation schedule is chosen, it belongs in the declared interpretation; a scan range alone must not silently become a new eligibility rule. [Current start](../bitmap_development.go#L20), [reference scope boundary][C].

## Configuration and audit representation gaps

Current `bitmapDevelopmentProfile` contains a name, parser pin, prose Rules array, provenance revision and optional District digest. `bitmapProfileHash` hashes an ordered Gateway JSON semantic projection and deliberately omits nickname/research revision. This is internally explicit, but lacks machine-readable named answers, rule dependencies, support states and activation/validator fields. The runtime still cites the old reference revision. [Profile model](../bitmap_development.go#L23).

The run request has no Bitmap profile selector. Repeating the same consumer ID is rejected, directories are keyed by fixed consumer ID, current heads are accepted only under the one expected profile hash, and query kind chooses that fixed store. Consequently users cannot currently build or compare two District or Parcel interpretations side by side. This is an API/storage/planning limitation, not a hidden capability awaiting a checkbox. [Run request/admission](../index_run.go#L32), [store namespace and validation](../bitmap_development_store.go#L105), [read API](../bitmap_development_api.go).

Compact candidate rows retain identity, reveal coordinates, body hash/short preview, parent IDs, selection/reason and optional provenance receipt. They do not retain a complete per-rule result matrix, MIME/charset decisions, all parser flags, full raw body/witness evidence or an independently reusable candidate universe. The current prefilter is tailored to the fixed grammar. Enabling a decompression or different-content comparison cannot safely operate only on those already-filtered rows. Reuse sufficient authenticated original evidence or perform an explicitly reviewed replay; unavailable evidence remains unavailable. [Candidate shape](../bitmap_development_store.go#L20), [candidate classification](../bitmap_development_store.go#L466).

Recommended development contract, without allocating canonical bit positions:

1. A named symbolic rule registry records the question, reference answer, applicable kind, support status, semantic dependencies, true conflicts and evidence requirements separately.
2. The reference preset uses the exact values above. Unsupported comparisons remain visible with a concrete reason; no setting can silently bypass a required validator.
3. A development semantic profile identifies exact implemented answers, decoder/parser/parent-validator policies, required District digest and any semantic activation schedule. Operational retention, resource budgets, nickname, software build and scan bounds remain separate unless they actually change interpretation.
4. Every candidate records per-rule `pass`, `fail`, `unresolved` or `not_applicable` with an evidence reference/reason. Final selection is separate from eligibility. Missing proof does not become false, and a proved-negative edge does not remain a generic wait.
5. Build plans and reads select a profile explicitly. Scope, source checkpoint, profile identity, coverage and current ownership are independently visible. Additional profiles need separate immutable state; they must not overwrite the reference preset.

## Migration and profile identity

Do not change the old development-v1 profile's meaning, alter its version marker to make it pass, or replace its provenance with new-main. Preserve the old locked `bitmap` recipe as well.

Adding typed answers, explicit burn semantics, a new decoder/parent interpretation or a different fingerprint encoding calls for an explicitly identified new development profile and compatible namespace/admission rules. Parcel identity must bind the selected new District digest. Reevaluate outcomes over the required prefix or sufficient prior context; attaching a new R to old winner outcomes is not evidence those outcomes were evaluated under it. The separate Chain review determines the resulting commitment consequences. [Reference identity boundary][C].

A research-reference update alone is provenance, not automatically a new semantic rule. A new cache/evidence acquisition path that proves the same predicate may improve coverage without changing semantic R. Conversely, changing what counts as a valid parent or which claimant wins is a semantic change even if the UI preset name is unchanged. Keep these distinctions explicit in migration tests.

## Required next implementation and acceptance work

| Phase | Concrete deliverable | Completion gate |
| --- | --- | --- |
| A: source and semantics | Pin new-main; preserve old receipts; resolve the declared activation/prior-context basis; record exact decoder and parent-validator choices as development policy. | The supported preset and every unresolved/unsupported question have an attributed explanation. No old state is relabeled. |
| B: named profiles and admission | Implement symbolic registry, reference preset, true conflicts, semantic/evidence requirements, profile-aware plan and read selectors, and separate immutable profile state. | District has all 12 questions, Parcel all 13; mismatched parent-profile dependencies and unsupported combinations cannot start. UI and backend enforce the same contract. |
| C: evidence and outcomes | Add per-rule audit results; distinguish invalid from missing parent proof; handle supported same-block provenance and deliberate historical reads. | Positive and negative witness fixtures cover the actual Apply path, not just metadata/helpers; unknown evaluation cannot publish a complete empty result. |
| D: comparisons and recovery | Enable only alternatives whose semantics/evidence pipelines are explicitly implemented; maintain profile-bound replay, reorg and checkpoint recovery. | One profile cannot overwrite or resume another. Different R with identical selected IDs remains distinguishable from an actual winner disagreement. |
| E: reviewable preview | Build an exact development package and verify saved/unbuilt/invalid/unresolved views, configured runs, pause/restart and preserved old-profile evidence in isolated profiles. | Receipt identifies source, binary, supported semantics and limitations. Existing user data, main and release/updater delivery stay outside this review. |

Phase B can expose unsupported choices with reasons; it must not make every theoretical Boolean combination executable merely to populate controls. The user has delegated development choices, so implementation can select and name a conservative supported interpretation without pretending unfinished upstream alternatives were finalized.

1. **Pin the correct source.** Record new-main commit `1751e0d…` for the new work while preserving existing preview receipts and old reference metadata.
2. **Implement named reference values and configuration validation.** Cover all 12/13 questions in the review/API representation. Reject the actual ordering conflict. Explain unsupported alternatives and missing evidence without inventing extra bits or mandatory stores.
3. **Define the next development fingerprint and state selector.** Add profile-aware planning/storage/queries and District-to-Parcel binding before permitting simultaneous interpretations. No canonical-standard claim while upstream bytes remain unfinished.
4. **Finish parent result semantics.** Separate checked-invalid from unavailable; add ordered same-block evidence and a deliberate historical-evidence strategy. Preserve bounded work and no implicit Satline/Sat builds.
5. **Make audits reproducible.** Retain or reference sufficient original evidence for the promised checks and per-rule explanations. A body hash, inscription ID or outcome digest alone is not witness/interpretation proof.
6. **Add focused fixtures before changing evaluation.** Own-body plus delegate; delegate-only; MIME casing/parameters/missing/malformed headers; complete UTF/BOM/label failure vectors; whole-match negative matrix; height equality/premature claims; transaction-zero/last/out-of-range counts; same-transaction competing claims across inputs/envelopes; multiple valid/unrelated/malformed parent references; valid and invalid creation edges, same-block transfer, missing prior state; persistent winners after later parent/child burn under `burns=0`; source reorg/restart and profile-incompatible resume. Add every supported nonreference option and invalid configuration to backend/API/UI tests.
7. **Keep acceptance claims bounded.** Current local fixtures and unbuilt compiled routes are useful evidence. They do not establish whole-mainnet runtime/storage, canonical interoperability, every Ord edge case, full consensus validation or marketplace acceptance.

No production implementation was changed by this review. The next change can proceed under explicitly declared development choices where the reference leaves details open, while unsupported choices remain clearly unavailable and old saved results keep their identities.

[R]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/README.md
[D]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/DISTRICT-RULESET.md
[P]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/PARCEL-RULESET.md
[C]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/BITMAP-CHAIN.md
