# Timeline controls and coordinated indexing brief

Implement the timeline controls and shared indexing workflow together with District, Parcel and Bitmap Chain consumers in a development build. Users select indexes as they would arm tracks in a DAW, choose whether each follows new blocks, and start or pause the selection from one toolbar. A coordinated scan fetches and decodes a needed block once for all ready consumers, instead of starting a separate blockchain scan for each track.

This brief is based on the published Sats application and a code review on 15 October 2026. The user subsequently authorized implementation after review, including Bitmap consumers and a locally served portable preview. Development stays on a separate branch; main, released applications, the updater and existing user profiles remain untouched. This direction supersedes the earlier after-Bitmap scheduling and the initial preparation-only scope.

The companion [index dependency review](INDEX-DEPENDENCIES.md) distinguishes block evidence, mandatory state, optional lookup indexes and explorer joins. The [workspace design review](WORKSPACE-DESIGN-REVIEW.md) places all resource details and contextual advanced settings inside the timeline workspace, with Settings and Peers as the retained exceptions.

## Track controls

Keep these states separate:

| Control or state | Meaning |
| --- | --- |
| Visible | The built-in track is present in the timeline. Visibility does not authorize indexing. |
| Armed or On | Select this consumer for the next indexing run. |
| Live | After initial catch-up, continue following new selected-chain blocks while the run is active. |
| Start indexing | Admit the prepared selection and begin coordinated work. Use a record-style icon with an explicit accessible name. |
| Pause indexing | Save pause intent and stop at safe publication boundaries, preserving completed records. |
| Resume indexing | Recheck dependencies and continue from authenticated saved checkpoints. |
| Off during a run | Stop further dispatch to that consumer at a safe boundary. Preserve its saved data and other consumers' work. |

Arming and Live changes before Start prepare the run; they do not fetch blocks or start workers. New work selections, Live on and retention edits during a run are marked Pending and require the next Start/review. Explicit Pause, Off and Live off constrain the active run: Live off ends that consumer's following after its admitted target, while Off stops further dispatch at a safe boundary. Show applied and pending settings distinctly. These actions do not erase coverage. Saved state remains inspectable when its source and branch dependencies remain valid.

Place Arm, Live, progress and an immediate waiting/error reason on the relevant track. Keep Start/Pause/Resume and one global In/Out pair in a stable shared toolbar. Group actions operate on eligible children and display mixed state; a presentation group does not create its own executor. Selecting a row opens details without arming, starting or changing the global range. The Inspector holds contextual mode/history drafts, source, recovery and evidence settings; it has no duplicate execution controls or per-track range overrides.

Only expose controls supported by the capability. Headers use provider synchronization controls, not a fictitious block-derivation job. Values and ScriptPubKeys configure one TXIO consumer rather than submitting duplicate TXIO jobs. Their history selections are independent, but their Live indication follows the shared TXIO policy; do not display three independently divergent Live policies for Links/Values/Scripts. Sat ranges is visually within TXIO but belongs to the independent Sats consumer. Required UTXO/Sat facts remain retained even when optional historical Values/Scripts tracks are off. Satline remains disabled; new Bitmap development consumers retain separate identities from the legacy locked recipe.

## Default tracks and hierarchy

Show every available, unlocked built-in track on a fresh profile, even with no saved data. An empty derivation track reads Not built and is not automatically armed or Live. Headers retain their provider-managed Waiting/Syncing/Available status. Preserve each layer's own coverage, gaps and color. Use this presentation tree:

```text
Blocks
  Headers
  Raw blocks
Transaction locators
TXIO
  Positional links
  Values
  ScriptPubKeys
  Sat ranges
UTXO set
Addresses
  Current UTXOs
  Historical outputs
Inscriptions
Bitmap
  Districts
  Parcels
  Bitmap Chain
Disabled Plus reserved for future custom indexes
```

Transaction locators and TXIO remain separate stores. There is no duplicate standalone Sats row. The disabled Plus button has a clear accessible explanation and no active catalog menu. Retire built-in row removal from the everyday controls; otherwise disabling Plus could strand a hidden track. Do not expose internal-only indexes as extra build tracks.

Migrate the old added/hidden built-in preferences so they cannot suppress available tracks. Preserve saved data, accepted jobs, group expansion, track height and pane preferences. Map existing work to the new controls without converting appearance or a restored preference into a new build authorization. Previously paused or canceled jobs must not restart through migration.

## Expansion corrections

The Sat ranges placement issue is confirmed. `ordered()` expands the TXIO owner and the later Sats owner in backend catalog order. `renderGroups()` gives Sat ranges the TXIO collapse identity but appends its row after intervening UTXO/Addresses rows. A matching group identifier therefore hides it correctly without putting it in the correct visual position.

Build one explicit presentation tree and render it depth first, keeping every group's children contiguous. Associate disclosure controls with their actual children. The current group button points at the collapsed coverage bands, not a container owning the expanded rows; correct its accessibility relationship at the same time. Preserve the independent executor identity behind each child.

Replace the two global expand/collapse buttons with one persistent Tracks disclosure control in the same toolbar position, with a stable hit area and focus. Its icon and expanded state change; its location and purpose do not. Preserve the hierarchy depth, zoom, playhead, selections, Inspector context and scroll positions through toggling. Keep group disclosure separate from row selection and arming.

The affected implementation is in [the timeline renderer](../ui/shell/index-timeline.js) and [timeline styles](../ui/shell/index-timeline.css). Existing browser tests check Sat group membership but not actual sibling order.

## Shared run and block processing

Extend the existing shared-build coordinator rather than launching competing independent builds or removing the single-writer guard. Persist one run with a consumer specification for each selected owner: range/resume rules, target, mode, optional layers, Live preference, retention and dependencies. Preserve each consumer's own commitment, coverage, progress and error. A run does not invent a common atomic checkpoint for different indexes.

For each required height:

1. Determine the ready consumers and selected-chain block identity they need.
2. Reuse suitable local evidence or fetch the block once; verify and decode it once.
3. Prepare immutable shared data: ordered transactions, provenance, created-output facts, lazily parsed inscriptions and any authenticated predecessor facts already available.
4. Dispatch the prepared data to eligible consumers. Resolve a shared intermediate once where its rules and branch context match.
5. Publish each consumer in dependency order, rechecking its branch anchor. Successful independent commits remain valid if another consumer fails.
6. Persist progress and release temporary data after its consumers finish. Apply explicit raw-retention ownership separately from mandatory live and recovery facts.

UTXO and Sats still have separate mutable engines: UTXO tracks unspent coins, while Sats also records ordinal issuance, fees and terminal/lost/destroyed states. Sharing output facts does not make their membership rules interchangeable. Previous-output caches must include authenticated creation/branch identity and same-block ordering; a transaction ID alone is insufficient for historical duplicate transactions.

The sharing guarantee applies to ready consumers needing the same block in the same pass. A newly enabled index, a resumed consumer whose evidence was evicted, a reorg or unavailable predecessor data can require another read. Do not obtain an apparent once-ever guarantee by retaining unbounded decoded history.

Different checkpoints need explicit scheduling. Use bounded catch-up and Live turns, coalescing requests for the same block/hash and using a bounded prepared-data cache. A new genesis Sats build must not starve an already-following UTXO workflow. Show independent frontiers rather than a misleading single progress height. Report reuse/fetch/decode counts during acceptance so the shared-pass behavior can be measured.

## Dependencies and stopped consumers

Planning must identify whether each prerequisite is ready, obtainable from a known block, available from retained facts, missing a locator, awaiting an armed upstream consumer, or requiring a complete state/rebuild. Explain the missing prerequisite on the track and offer a specific action. Never silently arm or start a prerequisite index. Parent-child display nesting is not a dependency declaration.

Audit every derived-write path against the same effective work policy. Explicit build jobs are not the only writers: index-as-you-browse can retain singleton locator/inscription results, legacy GraphIndex can enrich knowledge while blocks are cached, and UTXO publication can advance Current Addresses. Off must dominate optional index writes through all these paths while allowing the mandatory internal facts of another armed consumer. Existing browse-indexing preferences belong in the dependency/policy migration, not behind an unmentioned bypass. Ordinary navigation may fetch its requested source evidence; it must not start an index build or retain optional index outputs against the user's Off choice.

Current Addresses needs particular work. It currently synchronizes inside UTXO publication even when its own work policy is Off. Add an explicit consumer policy gate before promising that Off freezes it. A frozen projection cannot supply a current balance once UTXO advances; show its saved source checkpoint and stale status. Catch up through authenticated retained transitions where possible. If those transitions are no longer retained, prepare a projection rebuild from the current UTXO set rather than replaying Bitcoin history unnecessarily.

Before allowing a frozen address head to remain readable, retain its referenced facts as an independent owner. The present shared-fact collector enumerates UTXO, TXIO, historical addresses and Sats, but does not enumerate an independently frozen Current Addresses head. Query eligibility and cleanup must agree, so Off cannot leave a readable head pointing at reclaimed pages.

## Pause failure recovery and retention

Keep durable stop/pause intent ahead of cancellation. Stop pending fetches when nobody still needs them; turning one consumer off must not cancel evidence required by another. Resume from authenticated heads, including a crash between consumer publication and run-status publication. Replanning must not silently change profile, mode, range or rebuild behavior.

A shared missing block puts affected consumers into Waiting. A damaged consumer stops that consumer and its dependents, with unrelated healthy consumers able to continue. Do not advance failed coverage or turn an unavailable dependency into an empty result. Preserve existing corruption and reviewed-rebuild distinctions.

Keep the existing recovery mechanisms: UTXO's 288 reversible transitions, Sats' two savepoints at multiples of 12 and two at multiples of 144, normal index anchor reconciliation, and historical-address withdrawal of disconnected anchors. Discard prepared data tied to a disconnected branch. Explorer joins continue to require their exact compatible checkpoints. Missing recovery evidence does not authorize resetting an index.

Raw Blocks participation determines new raw-body retention for the shared run. If armed, retain/index the fetched serialized blocks. If off, consumers use newly fetched bodies temporarily and release them after all pending consumers finish. There is no competing normal raw-retention dropdown per consumer. Preserve preexisting retained/cached bytes and external/mounted data; Off never deletes them. Mandatory live state, undo, savepoints, frozen heads and archives own their facts independently. Run garbage collection at safe boundaries only after enumerating all owners; damaged ownership evidence must stop cleanup.

Raw Blocks Off stops that consumer's new acquisition/retention work. It does not forbid another armed consumer from reading existing local or mounted/provider bytes. Such a read must not falsely advance optional Blocks coverage or turn its retention policy on.

## Global In and Out

Use one global mark pair for all armed consumers. Per-track ranges and disjoint selection editing are deferred. The user can enter a resource in the main address bar, move the playhead there, press I for In, navigate to another resource and press O for Out. A toolbar control exposes draggable mark handles. Right-click can clear a mark, with equivalent accessible Clear In, Clear Out and Clear range actions. Editing or clearing marks never starts work or rewrites an accepted job.

Marks preserve typed, anchored resource positions, including transaction/output positions and output-local Sat offsets when known. A numbered Sat needs a saved containing-output location before it can become a timeline point; properties or issuance origin alone cannot stand in for that location. Preserve the original mark's snapshot and exact position rather than following a moving Sat silently. Unknown/lost placement cannot create a fabricated endpoint.

Existing consumers process whole blocks. Show both precise marks and their inclusive containing-block interval before Start. Complete boundary blocks are processed; a fine mark does not authorize partial-transaction or partial-Sat state construction. Reject reversed or incomparable marks with an explanation rather than silently exchanging coordinate spaces. Dragging a handle clamps it at the other valid boundary. Clearing In means normal resume/required start; clearing Out means the selected tip for initial catch-up and continued following only for Live consumers. Display these defaults explicitly.

Fixed Out is a hard cap, even when a consumer's Live preference is saved. Following beyond it requires clearing Out and explicitly starting/resuming the prepared selection. A saved UTXO/Sat head beyond Out is already beyond the requested window, not a historical state at Out, and is never rewound implicitly. In above zero needs a compatible complete prefix for stateful consumers; if missing, show the required lead-in and offer an explicit reviewed initialization or a global In change. Do not silently scan genesis. Current Addresses projects its eligible saved UTXO checkpoint; In/Out does not filter address membership.

Replace active per-owner range maps and per-row range editors with the global marks. Preserve old accepted jobs and their ranges as existing work; changing the global draft does not mutate those requests. Track selection, resource navigation, Back/Forward and passive status refresh leave the marks unchanged.

## Contextual navigation and shortcuts

Use Previous/Next for one item and Skip for ten items at the current semantic level: block, transaction, output, or Sat offset within the selected output. Clamp to verified bounds and label the exact unit/distance. Pan remains a separate action; arrow buttons must not change meaning with zoom.

| Shortcut in timeline navigation context | Action |
| --- | --- |
| Left / Right | Previous / next item. |
| Shift + Left / Right | Skip ten items at the same level. |
| Up / Down | Parent / descend into the focused item when child metadata is established. Track labels and native controls retain their own directional navigation. |
| Home / End | First / last verified item in the current parent. |
| Enter / Escape | Open or descend / return to parent; an open menu/dialog handles Escape first. |
| I / O | Set global In / Out from the established typed playhead. |
| + / minus | Zoom. |
| Space + drag | Existing hand-pan; Space never starts indexing. |

After explicit address submission, focus the resolved contextual heading or timeline navigation surface so I/O is available. Do not intercept ordinary typing, text selection, contenteditable/composing input, selects, menus, dialogs, isolated content frames or Ctrl/Alt/Meta shortcuts. Keep existing local tab/separator/scrollbar/marker interactions. Route shell and iframe shortcuts through one action owner to prevent double execution.

Reuse `positionKey()`, `focusPosition()` and `childEntity()` rather than introducing another navigation interpretation. At output level, adjacent offsets can hold nonconsecutive sat numbers because FIFO ranges can be fragmented. Resolve numbered Sat identity only from authenticated saved ranges, preserving current unavailable behavior. Unknown child counts disable stepping; navigation never starts indexing, Satline or a chain-wide search. Keep range selection separate from browsing focus.

## Bitmap development integration

Implement three distinct development consumers: Districts, Parcels and Bitmap Chain. The creator's reference repository was reviewed at a fixed revision; its commitment reference is not a complete witness-based Gateway evaluator. Use explicit experimental development profiles for implementable semantics, preserving unresolved evidence and distinguishing their self-described fingerprints from an unfinished canonical-standard profile format.

District and Parcel consumers should share verified block decoding and a single authenticated reveal-extraction result. Preserve original body/witness evidence, physical positions, parser identity and parent metadata separately from interpreted claims. Avoid making a full retained Inscriptions index mandatory merely to use the same extractor. Required source evidence can be shared without claiming that every related user-facing index has coverage.

Districts need earlier claim/winner context under the selected interpretation. Parcels additionally need the recognized District/parent context, checked provenance and the referenced block's transaction-count evidence. A declaration of a parent is not proof of that relationship. Headers alone do not supply transaction counts, and a general transaction locator need not be the source when the referenced block is already known. Consumers can participate in one scan while these dependencies determine publication order. Current ownership/location remains a separate optional join.

Bitmap Chain consumes completed evaluated outcomes; it must not independently repeat the reveal scan. Keep per-block outcomes, cumulative outcome history, Bitcoin source identity, semantic rule identity and the outer scoped checkpoint distinct. Evaluated zero-winner blocks advance the chain, while missing/incomplete evaluation leaves a gap. Chain commitments do not prove the correctness of candidate evaluation. Do not silently insert Parcel outcomes into the District outcome stream; a Parcel commitment scheme requires its own specified scope.

| Data | Canonical ownership |
| --- | --- |
| Serialized blocks and authenticated reveal evidence | Shared source/evidence facilities; consumers reference the same anchored evidence. |
| Shared amounts/scripts | Existing shared output-fact storage, with each surviving owner retaining its references. |
| District candidates, decisions and winners | District consumer, scoped to its source and semantic profile. |
| Parcel candidates, provenance and winners | Parcel consumer, with an explicit dependency on the District interpretation used for parents. |
| Completed per-block outcomes and audit-chain checkpoints | Separate commitment consumer referencing evaluated decision receipts, rather than copying full candidate/owner maps. |
| Current location/ownership and richer comparison exports | Separate joins/exports with their own coverage and assurance. |

Use canonical consumer identities and storage interfaces so visual aliases, parent tracks and optional outputs cannot create duplicate writers or parallel copies in unrelated directories. Preserve existing source/fact paths; choose any new Bitmap store names and schemas during its implementation brief. Shared ingestion is an optimization, not a merger of District, Parcel and audit semantics. Adding Parcel rules must not silently change a District-only semantic identity.

The current locked Gateway District recipe is not established as equivalent to the new reference. Preserve it and use new District/Parcel/Chain consumer identities and separate development stores. Do not reuse its recipe hash after changing semantics. Declare experimental Parcel tie/provenance choices and supported text-decoding behavior; unknown dependencies remain unresolved. Exact canonical profile serialization and broader production acceptance are not established merely by producing a working development preview.

## Implementation sequence

1. Define capability/dependency descriptions, staged controls and a read-only multi-consumer planner. Audit saved-policy and legacy API migration before execution changes.
2. Introduce consumer adapters and the bounded prepared-block context around the existing shared coordinator. Bring UTXO, Sats and historical addresses into the same dispatch architecture; Current Addresses remains a dependent projection.
3. Add durable shared Start/Pause/Resume and independent Off, fair Live/catch-up scheduling, failure isolation and recovery. Resolve frozen-address retention before releasing that control.
4. Render the canonical track tree, default built-ins, stable disclosure and row controls. Connect normal controls to the planner; keep deeper recovery/settings in the Inspector.
5. Add contextual stepping and exact global marks; integrate the persistent resource workspace and Bitmap views, then verify the composed application and migration paths.

Keep legacy single-index entry points as adapters to the same scheduling contract, preserving their documented intent. General deletion/reset tooling, a Satline re-enable and release/updater promotion are outside this development scope. Deliver a tested portable local preview with its own profile and a browser-openable local URL.

## Acceptance checks

- Count one fetch, decode and inscription extraction for aligned consumers sharing a block; compare resulting index data with independent derivation. Distinct optional layers must retain distinct coverage.
- Exercise different starting checkpoints, historical ranges and Live consumers; show bounded cache use and fair forward progress. Do not claim whole-mainnet performance from small fixtures.
- Crash after one consumer commit, during fetch and before status persistence; resume without duplicate advancement or revival of canceled work. Verify shared Pause and independent Off.
- Switch Current Addresses off while UTXO advances, including beyond the retained transition window. Preserve frozen dependencies or explicitly mark the projection unavailable; verify the reviewed rebuild path.
- Verify Off across explicit builds, queued/Live work, index-as-you-browse, legacy cache enrichment and automatic projections. Another armed consumer's mandatory internal translation/fact data must not falsely mark an optional track built.
- Reorg during preparation and between publications. Invalidate old caches and keep checkpoint-bound explorer joins, undo/savepoint recovery and archive pins intact.
- Preserve exact destination separation across current/historical address results and Sat holdings: a P2PK public key, another key encoding and a same-key P2PKH script must not merge balances or coverage.
- Assert actual DOM order: all four TXIO children are contiguous before UTXO/Addresses, with no duplicate Sats row. Collapse/reopen must retain the same ownership and order.
- Check fresh profiles and old hidden-track preferences: all available built-ins appear, Plus stays disabled, and displaying/selecting/arming/configuring alone causes no build or source fetch.
- Verify stable disclosure identity, focus, accessible relationships, distinct colors and mixed group state. Test narrow layouts, mouse/keyboard, vertical wheel, modifier zoom and pinch.
- Test Previous/Next at verified boundaries and with missing counts, fragmented ranges, reorgs and canceled navigation. Navigation must not alter indexing selections or start work.
- Exercise the development District/Parcel/outcome adapters with bounded authenticated fixtures to verify shared extraction, dependency waiting, separate store/commit identities and evaluated-empty versus missing outcomes. Preserve the legacy locked recipe; do not represent experimental profiles as canonical standard rules.
- Extend the existing queue, live, shared-build, UTXO/address/Sats and actual workspace browser tests. After implementation, run relevant regression/race checks and compiled-app acceptance against the final candidate.

## Implementation references

| Concern | Existing code |
| --- | --- |
| Catalog and build capabilities | [index_catalog.go](../index_catalog.go) |
| Planning, source reuse and shared dispatch | [index_shared.go](../index_shared.go) |
| Single writer and specialized workers | [index_runtime.go](../index_runtime.go) |
| Queue durability and Live scheduling | [index_queue.go](../index_queue.go), [index_live.go](../index_live.go) |
| UTXO publication and automatic address projection | [utxo_store.go](../utxo_store.go), [index_address_state_076.go](../index_address_state_076.go) |
| Sats publication and recovery | [index_sat_worker_077.go](../index_sat_worker_077.go), [sat_recovery_077.go](../sat_recovery_077.go) |
| Shared-fact ownership | [shared_output_facts_gc_075.go](../shared_output_facts_gc_075.go), [sat_pins_077.go](../sat_pins_077.go) |
| Timeline hierarchy and preferences | [index-timeline.js](../ui/shell/index-timeline.js), [index-workspace.js](../ui/shell/index-workspace.js) |
| Actual workspace integration | [indexes.html](../ui/shell/indexes.html), [workspace browser checks](../scripts/test-index-workspace-browser.js), [hierarchy browser checks](../scripts/test-timeline-hierarchy-browser.js) |

## Delivered ruleset preview review notes

Captured during the user's review of runtime `74e7529` on 16 October 2026, then authorized for implementation on the development branch. A replacement build will receive separate acceptance; the delivered preview remains unchanged.

- **Panel maximization:** the physical backtick key maximizes the hovered panel, with the last focused panel as a keyboard fallback. The same key, Escape or a visible Restore layout control returns the exact previous arrangement. Suppress the shortcut in editable fields, during composition and for modifier chords or repeated keydown events. Use one workspace controller for the Inspector, viewport and timeline, preserving live DOM, split ratios, focus and scroll state. Open menus, help and dialogs consume Escape before layout restoration; layout restoration precedes timeline ascent. Coordinate with the existing viewport fullscreen control. Keep Restore outside isolated inscription content, which must not gain authority over app layout.
- **Inactive track controls:** Arm and follow-new-blocks icons need a faint outline or raised button treatment when off, preserving the clearly pressed treatment when on.
- **Compact Bitmap rows:** avoid oversized group rows caused by multiple nested control levels; retain a consistent compact height and legible hierarchy.
- **Add District ruleset:** inside expanded Bitmap, provide a blank track row with a plus button for creating another District ruleset.
- **District-owned Parcel variants:** each District ruleset remains expandable. Its child rows are the linked Parcel rulesets, followed by a blank plus row inside that District for adding another Parcel ruleset variant. Each Parcel keeps its exact District dependency. The outer District-add row and inner Parcel-add rows have distinct scope.
- **Creation versus configuration:** add comparison tracks from the timeline's plus rows, without a duplicate add-track action in the top-left panel. Selecting a newly added track opens its rule configuration in the top-left details/settings panel. Enter that track's specific rules or choose an existing profile, including Bitmap District Standard or Bitmap Parcel Standard; Standard denotes the standard ruleset. This split applies to every custom District and its linked Parcel variants.
- **Per-track rules and locking:** clicking a District or Parcel track opens that track's binary rule editor, rather than using the Bitmap group as the editor. An unindexed track remains editable; once indexed data exists, its rules are locked. Changing any Standard option automatically marks the track Non-standard. Keep a stable track identifier, a separate rules fingerprint and a user-renamable display name. This revises the delivered creation-time immutability rule and needs a lifecycle review of active/resumable work, linked Parcel/Chain dependencies and stale plans or comparison cursors before implementation. Do not silently rebind indexed dependents when a parent draft changes.
- **Group versus track selection:** clicking the overarching Bitmap track opens comparison results. Clicking an individual District or Parcel track opens its own rules for inspection, and editing where eligible. Keep both views in the existing top-left workspace panel.
- **Attached Chain mini lane:** remove the separate named Bitmap Chain row from the left track list. When a supported parent track has Chain enabled, render a slim Chain lane immediately beneath the parent's timeline lane. The parent's Chain toggle remains the control; this visual refinement does not change store identity, dependency, retention or supported parent kinds.
- **Contextual Skip follow-up:** use the existing Skip controls and Shift+Arrow shortcuts for index boundaries. Forward Skip before a selected track's positive start jumps directly to that block; backward Skip after its known saved end mirrors to the end. Within its range, keep zoom-relative ruler notches, treating encountered authenticated coverage edges as additional stops so skipping lands on an indexed piece's boundary rather than crossing it. Prefer saved checkpoint/segment scope, then authenticated coverage, then a declared start. Do not use advertised provider coverage as saved bounds. At deep levels compare the actual containing block, preserving local notch behavior within scope; a boundary snap returns to block level. Navigation changes no indexing choices or global marks. This supersedes the initially suggested extra per-track start button and is authorized for a UI-only replacement of the delivered preview.

The read-only lifecycle review found that unbuilt run plans currently rely on creation-time immutable profiles and do not explicitly bind a mutable configuration revision. Before allowing edits, bind track ID, rules fingerprint and configuration revision at preparation, admission, resume and publication. An edit invalidates a staged review; admitted or resumable work temporarily reserves every affected binding. Persist the rules lock once indexing evidence exists, including recovery evidence; ordinary data deletion does not silently unlock it. Update an unbuilt District and all unbuilt linked Parcel/Chain bindings atomically, retaining Parcel answers, and block the edit if any affected dependent is locked. Derive Standard/Non-standard from the pinned reference answers, independently of the display name. Preserve existing fixed legacy profiles and their identities. These are implementation constraints for the next revision, not changes to the delivered app.
