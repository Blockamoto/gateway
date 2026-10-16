# Bitmap ruleset tracks, attached Chains and comparisons

Design review, 16 October 2026. This document proposes the next development work; it does not change the running preview, its District build, saved profiles, main or the updater.

The reference remains **Blockamoto/bitmap, `new-main`, commit `1751e0d37073ab920cbd8b5548c61b74c37716d1`**. Read this alongside the [rule-by-rule review](BITMAP-NEW-MAIN-REVIEW.md), [Chain format review](BITMAP-CHAIN-NEW-MAIN-REVIEW.md), [dependency review](INDEX-DEPENDENCIES.md) and [workspace review](WORKSPACE-DESIGN-REVIEW.md). The pinned [District][D], [Parcel][P] and [Chain][C] specifications supply the source rules; the application design and operational policies below are Gateway proposals.

## Recommended product model

Each saved custom ruleset gets **one timeline track, typed Districts or Parcels**. It has its own immutable interpretation, progress, winner state and coverage. Multiple District interpretations can build in the same shared run without overwriting one another.

**Bitmap Chain belongs beneath the District track whose outcomes it commits.** Its parent binding is explicit and immutable. Enabling Chain stages that child consumer for the next reviewed run; each durably completed District block can then feed one Chain step. It is not a global Chain that mixes all interpretations.

```text
Bitmap
  Reference Districts                     Arm   Live
    Chain                                On    Live
  Districts — alternative rules          Arm   Live
    Chain                                On    Live
  Parcels — alternative rules            Arm   Live
    Requires: Reference Districts
```

The illustration uses readable control names; the production controls retain the compact symbols, accessible names and hover explanations already established. The Chain child also has an independent storage choice in Index Settings: **Full history** or **Pruned — current + recovery**. Live scheduling and pruning answer different questions.

The current reference Chain format commits District outcomes only. A Parcel track may link to its selected District's Chain, but must not imply that its Parcel results are included. Parcel-to-Parcel result comparisons are useful without inventing a Parcel Chain format. [Source: Chain specification][C].

## Creation and editing

Use the existing Plus location for its reserved custom-index purpose: **Add Bitmap ruleset…**. Built-in tracks remain present; do not restore the old catalogue of optional built-ins.

1. Choose **Districts** or **Parcels**, and clone reference values or an existing profile.
2. Open the draft in the top-left **Index Settings** tab. Give it a display name.
3. Edit named rule switches, with a short question, the reference value and the selected `0/1` value. Group core and optional rules; offer a concise changed-values view.
4. For Parcels, select the exact District track defining its recognized parent. Show that dependency by name and fingerprint.
5. Validate the interpretation. Unsupported configurations may be retained as clearly labelled drafts, but cannot start indexing.
6. Save a supported profile to create a distinct track, initially unarmed and non-Live. Saving starts no work. Its District Chain child is initially Off; enabling it is also staged until Start.
7. Arm the desired tracks, set Live and the global range, then use the existing reviewed transport.

Once saved as an executable profile, its semantics are immutable. **Change rules** clones a new draft/track; it never reinterprets an existing track's results. Renaming is metadata and does not change the rules fingerprint. Existing experimental profiles preserve their old interpretation and provenance.

The review panel must explicitly say when a plan is ready and expose a visible **Start indexing** confirmation. Pressing Play/Record a second time may remain the equivalent shortcut. The current preview's confirmation is too easy to miss; this is recorded feedback, not a completed fix.

## Rule registry and support states

Use named questions rather than an invented official packed binary string. The reference has twelve District questions and thirteen Parcel questions, but does not freeze bit positions or canonical profile serialization. A Gateway development encoding must identify itself as such. [Sources: District][D], [Parcel][P].

| Type | Reference core values | Reference optional values |
| --- | --- | --- |
| Districts | `content`, `text`, `regex`, `height`, `fiftxi` = 1 | `brotli`, `fifin`, `cursed_exclusion`, `reinscription_exclusion`, `unbound_exclusion`, `district_child_exclusion`, `burns` = 0 |
| Parcels | `content`, `text`, `regex`, `range`, `parent`, `fiftxi` = 1 | `single`, `brotli`, `fifin`, `cursed_exclusion`, `reinscription_exclusion`, `unbound_exclusion`, `burns` = 0 |

A zero has its own rule-specific meaning. Disabling an exclusion is different from disabling a decoding route; neither licenses treating unknown evidence as valid.

| Editor state | Meaning and action |
| --- | --- |
| Invalid configuration | A defined contradiction. For example, both `fiftxi=1` and `fifin=1` select conflicting precedence rules. Name both choices and require correction. |
| Unsupported interpretation | Required behavior is unspecified or not implemented. Keep the draft, explain the gap and block indexing. |
| Missing evidence | The interpretation is implemented, but the selected run lacks required authenticated context. Show the exact prerequisite and an explicit review action. |
| Ready | The supported interpretation and sufficient source/context are available for the proposed run. |

Do not present every theoretical bit combination as executable. Both precedence questions at zero lack an alternative selection policy. Core-off variants need explicit replacement semantics. `burns=1` needs a burn predicate, time/activation and winner replacement policy. Brotli and own-body evaluation are not inherently contradictory; they require a specified pipeline. These are distinct from implementation budgets or unavailable historical evidence. [Detailed analysis](BITMAP-NEW-MAIN-REVIEW.md#dependencies-exclusions-and-undefined-alternatives).

The profile must declare exact parser, charset/decoder, parent validator, selection and activation policies. Global numbering, curse, reinscription and historical boundness options may need state that cannot be fetched from one reveal block. Current UTXOs do not provide arbitrary historical identity. Missing dependencies must never silently launch Sats, Satline, numbering or a genesis build.

## Identity, isolation and shared work

Keep four concepts separate:

| Concept | Purpose |
| --- | --- |
| Track instance | Durable identity for one saved track and its data. Display names may change. |
| Semantic profile `R` | Exact executable interpretation, including named answers and semantic dependency/activation policy. |
| Source/scope | Network, selected Bitcoin branch, evaluated interval and sufficient prior context. |
| Storage and scheduling | Retention, Live, Arm, cache policy and checkpoint cadence. These do not alter `R`. |

A Parcel profile binds its District semantic `R`; its track also selects the particular District instance providing authenticated data. Changing the District interpretation requires a new dependent Parcel profile. An attached Chain binds both the exact District instance and `R`.

Runs, heads, APIs, URLs/history and pagination must select **instance + kind + expected profile**. Current fixed consumer IDs reject two same-kind selections and map reads to one fixed directory. Merely adding a profile dropdown over that directory would overwrite or confuse evidence. See [profile definitions](../bitmap_development.go), [store acceptance](../bitmap_development_store.go), [read selectors](../bitmap_development_api.go), [run admission](../index_run.go) and [timeline definitions](../ui/shell/index-timeline.js).

Share verified Bitcoin bytes and compatible physical inscription extraction once. Key extraction by its actual parser policy; profiles using a different parser cannot reuse a result merely because it came from the same block. Reuse sufficient authenticated candidate evidence, not only the current grammar-filtered candidate rows or short body previews. Every profile owns its eligibility decisions, winner map, ordered progression and publication guard.

One unresolved Parcel dependency must not block an independent completed District or Chain. Off means no new work/publication for that consumer; its retained authenticated data can still satisfy another consumer. Raw Blocks remains solely the raw-body retention choice and is not a prerequisite for reading source blocks temporarily.

## Chain processing over an existing District index

**Hashing the retained District outcomes should be much lighter than reevaluating inscriptions. No speed multiplier or whole-chain time is measured yet.** Chain still visits every height in order, including fully evaluated empty blocks; it is not one hash of the current winner map. A final winner map alone does not establish each block's ordered additions, evidence completeness or earlier losing context.

Current code already derives Chain hashes from the exact same-height District outcome and does not rerun eligibility inside Chain Apply. However, the coordinator still loads a complete Bitcoin block and requests extraction for every `bitmap-*` consumer before calling Apply. That prevents a genuine outcome-only catch-up path. [Chain Apply](../bitmap_development_store.go), [shared worker](../index_run_worker.go).

Add a distinct **prepared District outcome** input; do not fabricate a verified `blockView` from a hash:

```text
Retained completed District outcomes ──────┐
                                         ├─> Chain L -> sequential C -> scoped F
Shared block -> District commit -> outcome┘
```

The input binds height, Bitcoin hash, exact District instance/profile, immutable outcome receipt, prior context/scope and physically ordered new winning IDs. Check the selected branch through header authority and recheck it at publication. Preserve cancellation, Off, checkpoint authentication and stale-run protections.

For a **Chain-only run over retained outcomes**, the coordinator must bypass raw-source loading and inscription extraction. It still performs metadata authentication and disk reads. For a shared District+Chain build, publish the District outcome durably, then feed Chain at the same height without another fetch or extraction. Caught-up Live Chain consumes each newly completed District outcome.

If required outcomes have been pruned or never evaluated, show the gap and offer an explicitly reviewed replay from sufficient context. Do not silently rescan raw history or claim that a producer head above the target proves complete outcomes at every earlier height. A lagging Chain may keep its own compatible baseline; it must not silently rewind the District producer or pin every future producer generation indefinitely.

## Chain controls and retention

| Control | Behavior |
| --- | --- |
| Chain On/Off | Include or exclude this District's Chain in the staged run. This is the child's Arm choice, not a second enable flag. Off preserves its saved state. |
| Live | Follow newly completed District outcomes after catch-up. It cannot advance beyond its producer's complete coverage. |
| Full history | Retain each completed canonical per-height outcome and commitment for historical comparison. |
| Pruned — current + recovery | Keep current cumulative state and complete validated restore points. Old detailed history may be unavailable. |

Live On + Pruned is valid, as is finite catch-up + Full history. A Live Chain with an Off producer can consume already retained outcomes, then waits for further District evaluation. It never auto-arms the producer. Requirements identifies that exact track; Start review distinguishes ready saved data from missing prerequisite work.

For new comparison-oriented Chains, recommend **Full history initially**, since historical divergence is a primary use and the present store already retains it. Offer Pruned as an explicit choice once its recovery/collection contract is implemented and tested. Measure actual bytes and throughput before promising a small footprint. Retaining Chain history does not require retaining every raw block, original body or rejected-candidate audit.

### Recovery and compact storage

Use the established Sats cadence as a proposed Bitmap policy: newest two checkpoints at heights divisible by **12**, and newest two at heights divisible by **144**, sharing/deduplicating matching heights. Keep a valid bootstrap baseline while the segment is young. Display actual usable restore heights rather than claiming a guaranteed 288-block rollback window. In steady state the older daily point is normally 144–287 blocks behind tip; a fork can still precede it. [Existing cadence](../sat_recovery_077.go).

Each recovery point must bind current height/source, fixed segment start, semantic `R`, cumulative `C`, reconstructible `F`, the exact compatible District outcome/context receipts and their required evidence. Chain-only recovery restores its own baseline and consumes authenticated completed outcomes; it need not own a duplicate District winner map or rewind a healthy producer. If District reevaluation is explicitly included in reviewed recovery, its compatible winner/dependency roots and sufficient earlier context must also survive. Four digest strings alone do not provide this dependency closure. Replay from a surviving selected-chain baseline; preserve evidence and request reviewed recovery when none is available.

Keep checksummed current manifests and a previous valid publication generation to recover interrupted writes. Rotate a checkpoint only after its complete dependency closure is durable. Logical savepoints on shared pages are not independent backups against disk loss.

The current store's roots and previous-commit links retain cumulative history and serve historical dependencies. It has no Bitmap-specific collector. **Deleting old head files is not an implementation of Pruned mode.** Split optional query history from mandatory live winner/provenance state and restore roots. District state can still grow with recognized claims, so pruning Chain history does not make the whole Bitmap index constant-size.

Full-to-Pruned conversion requires reviewed generational compaction: construct new roots, authenticate closure, atomically swap the manifest, then sweep only unowned data. Preserve active readers/comparisons/exports, pending work and other tracks' independently owned facts. Retention changes leave semantic `R` and retained outcome/source commitments unchanged; storage generation and cursors may change.

Pruned-to-Full retains future outcomes immediately. Earlier history stays labelled **not retained** until explicit backfill replays the same interpretation from sufficient prior context and verifies the resulting checkpoint. The cumulative tip hash cannot recover previous isolated outcomes.

## Comparison workspace

Comparison is a resource inside the existing timeline workspace, with its details in the top-left Resource tab. Select baseline and variant tracks of the same type, then choose a common checkpoint and segment. Their live heads need not coincide; compare only an authenticated common scope. Pin both snapshots so pagination cannot mix results while Live advances.

Lead with track names, changed rules, source alignment, compared checkpoint and common evaluated coverage. Show **changed winners**, **changed resolved mappings**, **candidate/audit differences** and **unknown gaps** separately. A different rule fingerprint is expected for a variant and is not itself a winner disagreement.

| Evidence | Interpretation |
| --- | --- |
| Different Bitcoin source `B` | Align branch/source before interpreting a rule disagreement. |
| Different isolated outcome `L` | That block's winning inscription IDs/order differ. |
| Different cumulative `C` over aligned scope | Outcomes differ somewhere in the evaluated history. Current `L` may still match. |
| Different rules `R`, equal `L/C` | Interpretations differ; the committed winning ID stream agrees over this scope. |
| Different combined `F` | Source, scope, rules or outcomes may differ; decompose it before explaining the result. |
| Equal ID hashes, different resolved mappings | Inspect authenticated claim resolution; ID-only hashing does not independently commit a displayed District-number mapping. |

Full retained history supports a bounded search for the earliest retained divergence, then per-block drilldown. Pruned mode supports current/retained-point comparisons; saved anchors may bracket a difference but cannot name its exact block without the missing history or replay. Missing or unresolved records are neither agreement nor empty evaluated outcomes.

Candidate detail shows configured values and per-rule `pass/fail/unknown/not_applied`, reasons and evidence references. Eligibility and winner selection are separate. Losing-candidate disagreements can exist even when selected winners and Chain hashes match. A local comparison is evidence of these declared evaluations, not independent proof of correct rules, consensus or marketplace adoption.

The main Chain view labels source block, scope, outcome, cumulative history, outcome rules and combined commitment separately, with District numbers and authenticated winning IDs. Keep the Chain storage-profile identifier in Advanced so it cannot be mistaken for the outcome-producing `R`.

## Parcel dependency boundary

Parcel `range` needs authenticated transaction-count context; parent recognition needs the exact selected District interpretation and checked creation-time child evidence. Current ownership is a separate optional join.

The preview has a real positive pre-block Sat evidence path, but it does not supply arbitrary historical or same-block provenance. A later Sats head cannot prove an old parent edge. Missing proof stays unknown; authenticated evidence that disproves an edge is invalid, not an endless wait. Parent strings alone are insufficient. Complete that evidence contract before declaring broad Parcel support or offering associated alternatives as ready. [Detailed limitations](BITMAP-NEW-MAIN-REVIEW.md#parent-proof-is-presently-a-limited-evidence-adapter).

## Implementation order and acceptance

| Stage | Deliverable and acceptance gate |
| --- | --- |
| 1. Exact executable interpretations | Pin reference and declare decoder, parent, selection and activation/context policies. Keep unresolved drafts unsupported. Preserve every existing profile's identity. |
| 2. Multiple instances and editor | Separate same-kind tracks through storage, run plans, APIs, URLs and snapshot-bound cursors. Clone, rename and draft save perform no indexing and do not alter the user's active District run. |
| 3. Attached Chain and outcome-only input | Bind each child to its District. With raw blocks unavailable and peers disabled, a Chain-only fixture over retained outcomes completes with zero raw-source reads, block decodes and inscription extractions. Reorg/Off/cancel guards still reject stale publication. Newly evaluated District+Chain reads each needed source once. |
| 4. Comparison | Test source/scope mismatch, different rules with equal winners, different mappings with equal IDs, losing-only differences, genuine first divergence, sparse/unknown coverage and Live pagination pinned to both snapshots. |
| 5. Retention and recovery | Test 12/144 rotation boundaries, corrupt-point fallback, missing dependency pages, forks beyond retained coverage, interrupted compaction, reader pins, post-GC restart and canceled/resumed backfill. No pruned record becomes a false empty outcome. |
| 6. Exact development preview | Package the committed development source, run native create/configure/arm/review/pause/restart/compare checks in disposable profiles, and publish measured feature/coverage limits. Main, released packages, updater and user data remain outside this design work. |

This proposal does not call the current fixed experimental profile canonical Standard, promise all theoretical rule combinations, invent Parcel Chain bytes, or relabel existing state. The next implementation should make supported choices useful and explicit, while keeping unimplemented semantics and unavailable evidence distinguishable.

[D]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/DISTRICT-RULESET.md
[P]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/PARCEL-RULESET.md
[C]: https://github.com/Blockamoto/bitmap/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/BITMAP-CHAIN.md
