# Bitmap ruleset tracks, attached Chains and comparisons

Design review, 16 October 2026. The user subsequently authorized implementation on the development branch. This brief defines the implementation target; it does not claim unfinished capabilities are available in the running preview. Its District build, saved profiles, main and the updater remain separate.

The reference remains the same pinned source, now **Blockamoto/bitmap-old, `main` (formerly `new-main`), commit `1751e0d37073ab920cbd8b5548c61b74c37716d1`**. Read this alongside the [rule-by-rule review](BITMAP-NEW-MAIN-REVIEW.md), [Chain format review](BITMAP-CHAIN-NEW-MAIN-REVIEW.md), [dependency review](INDEX-DEPENDENCIES.md) and [workspace review](WORKSPACE-DESIGN-REVIEW.md). The pinned [District][D], [Parcel][P] and [Chain][C] specifications supply the source rules; the application design and operational policies below are Gateway proposals.

## Recommended product model

Each custom ruleset gets **one timeline track, typed Districts or Parcels**. It has a stable track ID and its own configuration revision, progress, winner state and coverage. Rules remain editable while the track and affected dependents are unindexed and unreserved; indexed evidence locks the interpretation. Multiple District interpretations can build in the same shared run without overwriting one another.

**Bitmap Chain belongs beneath the District track whose outcomes it commits.** Its parent binding is explicit and immutable. Enabling Chain stages that child consumer for the next reviewed run; each durably completed District block can then feed one Chain step. It is not a global Chain that mixes all interpretations.

```text
Bitmap                                     [click: comparisons]
  Reference Districts                     Arm   Live   Chain
    [thin Chain lane in the time area only, when enabled]
    Reference Parcels                     Arm   Live
    Parcels — alternative rules           Arm   Live
    [+ add Parcel ruleset under this District]
  Districts — alternative rules           Arm   Live   Chain
    [+ add Parcel ruleset under this District]
  [+ add District ruleset for comparison]
```

The illustration uses readable control names; the production controls retain compact symbols, accessible names and hover explanations. Chain has no separate named left-column track row or duplicate Arm control. Its enabled mini lane stays immediately beneath its District canvas. Its independent Live and **Full history** or **Pruned — current + recovery** choices remain in the parent track's settings. Live scheduling and pruning answer different questions.

The current reference Chain format commits District outcomes only. A Parcel track may link to its selected District's Chain, but must not imply that its Parcel results are included. Parcel-to-Parcel result comparisons are useful without inventing a Parcel Chain format. [Source: Chain specification][C].

## Creation and editing

Create tracks from the timeline's blank plus rows. Built-in tracks remain present; do not restore the old catalogue of optional built-ins or duplicate add-track actions in the top-left details panel.

1. Use Bitmap's plus row to create an unarmed Standard-settings District track. Expand a District and use its nested plus row to create a Parcel variant bound to that exact District. Pairing is optional; every Parcel remains a distinct profile/track inside its parent.
2. Creating selects that track's top-left **Index Settings** tab. Choose Standard settings or an existing named profile and give the track a display name.
3. Edit named rule switches, with a short question, the reference value and the selected `0/1` value. Group core and optional rules; offer a concise changed-values view.
4. For Parcels, show the exact District dependency by name and fingerprint. Changing parent requires a new Parcel track; editing does not reparent an existing row.
5. Validate the interpretation. Unsupported configurations may be retained as clearly labelled drafts, but cannot start indexing.
6. Save supported rule changes into the same unindexed track, updating its configuration revision and rules fingerprint. Saving starts no work. The District's attached Chain is initially Off; enabling it is staged until Start.
7. Arm the desired tracks, set Live and the global range, then use the single reviewed Play/Start transport. It preserves per-track Live choices; there is no separate global Record mode changing those choices.

Once indexing evidence exists, the track's rules lock persistently, including after ordinary data deletion. Admitted or resumable work also reserves affected bindings until canceled or replaced through review. An unbuilt District edit atomically updates its attached Chain and linked unbuilt Parcel fingerprints, retaining each Parcel's rule answers; any locked dependent blocks the edit. Prepared reviews and comparison cursors bind configuration revisions and become stale after an edit. Renaming is independent metadata and does not change the rules fingerprint or configuration revision. Existing fixed experimental profiles preserve their original interpretation and provenance.

Standard/Non-standard is derived from the selected reference answers rather than a nickname. Generated names follow that classification while they remain automatically managed; explicitly renaming a track preserves the chosen name. Keep the stable track identifier available in details without restoring the removed ordinal track-number column.

The review panel must explicitly say when a plan is ready and expose a visible **Start indexing** confirmation. Pressing Play a second time is the equivalent shortcut. The former preview's confirmation was too easy to miss. The single Start model supersedes the earlier finite-Play/following-Record design: Arm and Live on each track are the only scheduling choices. Mixed historical catch-up and caught-up Live consumers share fair turns. Idle Live consumers do not re-fetch or reindex unchanged blocks; selected source changes, including same-height reorgs, require new work/reconciliation. Global Out still caps following.

## Track layout and saved-data deletion

Arm and Live use the icons themselves as keyboard-operable pressed buttons, without an adjacent classic switch. Active, inactive and partial group states have visible shape/outline and accessible state. Remove track numbers consistently. Reordering uses a dedicated drag grip and keyboard alternative; persist sibling order by parent. Move parent groups with their descendants, and never move a child outside its owning parent. Reordering must not arm tracks, change dependencies or start work.

Index Settings offers a bin action for saved index data. The first action reviews the scope and dependencies; explicit confirmation is required before removal. The track/profile definition remains available to rebuild, while its saved data and stale run/job consent are cleared or cancelled. Pause active indexing first. Built dependent data blocks deletion of its source until the dependent is cleared; do not silently cascade. Preserve shared output facts, independent archives, source caches, external files, settings and other indexes. Bitcoin headers remain a foundational reset requiring a separate full-profile operation. TXIO layer selections must identify their parent-store deletion scope explicitly.

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

Keep these concepts separate:

| Concept | Purpose |
| --- | --- |
| Track instance | Durable identity for one saved track and its data. Display names may change. |
| Configuration revision | Version of an unindexed track's editable configuration; binds reviews and dependent interpretations independently of its name. |
| Semantic profile `R` | Exact executable interpretation, including named answers and semantic dependency/activation policy. |
| Source/scope | Network, selected Bitcoin branch, evaluated interval and sufficient prior context. |
| Storage and scheduling | Retention, Live, Arm, cache policy and checkpoint cadence. These do not alter `R`. |

A Parcel profile binds its District semantic `R` and exact District instance. An eligible unbuilt District edit recomputes the dependent Parcel profile atomically within the same track ID. Once any affected track is indexed or reserved, use a separate ruleset track instead of reinterpreting its evidence. An attached Chain binds the exact District instance, `R` and configuration revision.

Runs, heads, APIs, URLs/history and pagination must select **instance + kind + expected profile**, with mutable pre-build configuration revisions bound at review/admission/resume/publication. Distinct instances retain distinct directories; a profile dropdown over one shared store would overwrite or confuse evidence. See [profile definitions](../bitmap_development.go), [store acceptance](../bitmap_development_store.go), [read selectors](../bitmap_development_api.go), [run admission](../index_run.go) and [timeline definitions](../ui/shell/index-timeline.js).

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

Clicking the **Bitmap group name** opens its Overview/Compare resource in the existing top-left Resource tab. Its chevron only expands/collapses the tracks. Individual tracks retain their own rules and settings. Select a baseline and variants of the same type, then choose a common checkpoint and segment. Their live heads need not coincide; compare only an authenticated common scope. Pin both snapshots so pagination cannot mix results while Live advances.

**Compare actual saved results even when Chain is Off.** Chain is an optional compact commitment layer, not a prerequisite for winner, mapping or candidate-audit comparison. Availability comes from the District/Parcel evidence actually retained. Source blocks need not be fetched merely to display a comparison of sufficient authenticated local records.

Live comparison incrementally consumes bounded pages through the common completed frontier. Coalesce repeated head notifications, cancel obsolete requests when selections change, and invalidate/retract affected results after a reorg. Append-only producer growth preserves previously authenticated comparisons. Compare one selected baseline against selected variants rather than automatically starting every pairwise combination. Opening Compare starts no index, source scan or prerequisite build.

Show compared complete-block count, blocks with actual winner differences, mapping/audit differences, unknown coverage and each track's lag separately. A differing cumulative Chain hash is not evidence that every subsequent block has new winner differences. Unknown evidence is excluded from the compared denominator. Use delta markers and counts for differences, question marks/patterns for unknowns, and a labelled common-frontier marker; color supplements these cues. Each marker has an accessible list equivalent and opens paired block results. Live updates must not move the playhead, replace the inspected detail or steal keyboard focus.

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

The main Chain view labels these layers separately, with District numbers and authenticated winning IDs:

| Layer | Meaning |
| --- | --- |
| Bitcoin source `B` | Selected Bitcoin block hash at this height. |
| Rules `R` | Exact District semantic profile; the same profile identity applies across its evaluated range. |
| Isolated outcome `L` | Hash of this block's physically ordered new winning inscription ID records. It does not hash every inscription or the whole current winner map. |
| Cumulative outcome `C` | Hash combining the preceding cumulative outcome with this block's isolated outcome, using the specified domain. |
| Combined commitment `F` | Binds segment start/end, Bitcoin source, cumulative outcome and semantic rules. |

With Chain enabled, every completed block calculates its isolated, cumulative and combined commitments. Full history retains each block's records; Pruned retains the current tip plus authenticated recovery points. Both calculate all steps, including completed empty outcomes. Keep the Chain storage-profile identifier in Advanced so it cannot be mistaken for the outcome-producing `R`.

## Parcel dependency boundary

Parcel `range` needs authenticated transaction-count context; parent recognition needs the exact selected District interpretation and checked creation-time child evidence. Current ownership is a separate optional join.

The preview has a real positive pre-block Sat evidence path, but it does not supply arbitrary historical or same-block provenance. A later Sats head cannot prove an old parent edge. Missing proof stays unknown; authenticated evidence that disproves an edge is invalid, not an endless wait. Parent strings alone are insufficient. Complete that evidence contract before declaring broad Parcel support or offering associated alternatives as ready. [Detailed limitations](BITMAP-NEW-MAIN-REVIEW.md#parent-proof-is-presently-a-limited-evidence-adapter).

## Implementation order and acceptance

| Stage | Deliverable and acceptance gate |
| --- | --- |
| 1. Exact executable interpretations | Pin reference and declare decoder, parent, selection and activation/context policies. Keep unresolved drafts unsupported. Preserve every existing profile's identity. |
| 2. Multiple instances and editor | Separate same-kind tracks through storage, run plans, APIs, URLs and snapshot-bound cursors. Optional linked Parcel creation works immediately and later without silently rebinding. Clone, rename and draft save perform no indexing and do not alter the user's active District run. |
| 3. Attached Chain and outcome-only input | Bind each child to its District. With raw blocks unavailable and peers disabled, a Chain-only fixture over retained outcomes completes with zero raw-source reads, block decodes and inscription extractions. Reorg/Off/cancel guards still reject stale publication. Newly evaluated District+Chain reads each needed source once. |
| 4. Comparison | Test Bitmap-group entry, comparisons with Chain Off, source/scope mismatch, different rules with equal winners, different mappings with equal IDs, losing-only differences, genuine first divergence, sparse/unknown coverage, bounded Live updates, no focus/playhead theft, reorg invalidation and pagination pinned to both snapshots. |
| 5. Retention and recovery | Test 12/144 rotation boundaries, corrupt-point fallback, missing dependency pages, forks beyond retained coverage, interrupted compaction, reader pins, post-GC restart and canceled/resumed backfill. No pruned record becomes a false empty outcome. |
| 6. Exact development preview | Package the committed development source, run native create/configure/arm/review/pause/restart/compare checks in disposable profiles, and publish measured feature/coverage limits. Main, released packages, updater and user data remain outside this design work. |

This proposal does not call the current fixed experimental profile canonical Standard, promise all theoretical rule combinations, invent Parcel Chain bytes, or relabel existing state. The next implementation should make supported choices useful and explicit, while keeping unimplemented semantics and unavailable evidence distinguishable.

[D]: https://github.com/Blockamoto/bitmap-old/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/DISTRICT-RULESET.md
[P]: https://github.com/Blockamoto/bitmap-old/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/PARCEL-RULESET.md
[C]: https://github.com/Blockamoto/bitmap-old/blob/1751e0d37073ab920cbd8b5548c61b74c37716d1/BITMAP-CHAIN.md
