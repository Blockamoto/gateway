# Timeline controls and coordinated indexing brief

Prepare the timeline controls and shared indexing workflow before implementing Bitmap features. Users should select indexes as they would arm tracks in a DAW, choose whether each follows new blocks, and start or pause the selection from one toolbar. A coordinated scan should fetch and decode a needed block once for all ready consumers, instead of starting a separate blockchain scan for each track.

This is an implementation brief based on the published Sats application and a code review on 15 October 2026. The controls and corrections below are planned work. Preparation is on a separate development branch; it does not change main, released applications, the updater, user profiles or active jobs. This scheduling decision brings the named timeline work forward from the earlier after-Bitmap roadmap.

The companion [index dependency review](INDEX-DEPENDENCIES.md) distinguishes block evidence, mandatory state, optional lookup indexes and explorer joins.

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

Place Arm, Live, progress and an immediate waiting/error reason on the relevant track. Keep Start/Pause/Resume in a stable shared toolbar. Group actions operate on eligible children and display mixed state; a presentation group does not create its own executor. Selecting a row opens details without arming, starting or changing a range. Routine range and mode preparation should be possible within the timeline, with a compact shared review of the selection before execution. The Inspector holds deeper source, retention, recovery and evidence settings rather than being a mandatory detour through Settings.

Only expose controls supported by the capability. Headers use provider synchronization controls, not a fictitious block-derivation job. Values and ScriptPubKeys configure one TXIO consumer rather than submitting duplicate TXIO jobs. Their retention selections are independent, but their Live indication follows the shared TXIO policy; do not display three independently divergent Live policies for Links/Values/Scripts. Sat ranges is visually within TXIO but belongs to the independent Sats consumer. Required UTXO/Sat facts remain retained even when optional historical Values/Scripts tracks are off. Keep existing release gates, including disabled Satline tracing and unimplemented Bitmap controls.

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

Combine explicit raw-block retention demands without overriding another consumer's existing retained bytes. Mandatory live state, undo, savepoints, frozen heads and archives own their facts independently of historical coverage switches. Run garbage collection at safe boundaries only after enumerating all owners; damaged ownership evidence must stop cleanup.

Raw Blocks Off stops that consumer's new acquisition/retention work. It does not forbid another armed consumer from reading existing local or mounted/provider bytes. Such a read must not falsely advance optional Blocks coverage or turn its retention policy on.

## Contextual navigation

Consider Previous and Next controls alongside the transport after the shared-run contract is stable. They move the playhead by one item at the current semantic level: block, transaction, output, or sat offset within the selected output. Labels should state the actual level. Pan remains a separate action.

Reuse `positionKey()`, `focusPosition()` and `childEntity()` rather than introducing another navigation interpretation. At output level, adjacent offsets can hold nonconsecutive sat numbers because FIFO ranges can be fragmented. Resolve numbered Sat identity only from authenticated saved ranges, preserving current unavailable behavior. Unknown child counts disable stepping; navigation never starts indexing, Satline or a chain-wide search. Keep range selection separate from browsing focus.

## Future Bitmap integration

Prepare extension points for three distinct consumers: Districts, Parcels and Bitmap Chain. The first development pass implements generic controls/orchestration, not Bitmap rules or release availability. The creator's current reference repository was reviewed at a fixed revision; its commitment reference is not a complete witness-based Gateway evaluator, and unfinished profile/provenance semantics must not be treated as frozen implementation rules.

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

The current locked Gateway District recipe is not established as equivalent to the new reference. Its eventual rule/parser changes require deliberate profile identity and migration work. Do not reuse a legacy recipe hash after changing its semantics. Exact profile serialization, Parcel edge behavior and production evaluation/reorg acceptance remain prerequisites for that later implementation, rather than workarounds to unblock the controls phase.

## Implementation sequence

1. Define capability/dependency descriptions, staged controls and a read-only multi-consumer planner. Audit saved-policy and legacy API migration before execution changes.
2. Introduce consumer adapters and the bounded prepared-block context around the existing shared coordinator. Bring UTXO, Sats and historical addresses into the same dispatch architecture; Current Addresses remains a dependent projection.
3. Add durable shared Start/Pause/Resume and independent Off, fair Live/catch-up scheduling, failure isolation and recovery. Resolve frozen-address retention before releasing that control.
4. Render the canonical track tree, default built-ins, stable disclosure and row controls. Connect normal controls to the planner; keep deeper recovery/settings in the Inspector.
5. Add contextual stepping if it remains within the agreed preparation scope, then verify the composed application and migration paths.

Keep legacy single-index entry points as adapters to the same scheduling contract, preserving their documented intent. General deletion/reset tooling, a Satline re-enable, new Bitmap semantics and release/updater promotion are outside this preparation brief.

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
- Use mock District/Parcel/outcome consumers to verify shared extraction, dependency waiting, separate storage/commit identities and evaluated-empty versus missing outcomes. These tests must leave all real Bitmap features gated.
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
