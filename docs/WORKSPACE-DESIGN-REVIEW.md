# Timeline workspace design review

Make the timeline workspace the application surface for exploring Bitcoin resources and managing indexes. Resource details and contextual advanced settings belong in the upper-left panel; arming, Live, global range marks and indexing transport belong on the timeline. Settings and Peers remain separate destinations. The user authorized implementing that change together with experimental Bitmap Districts, Parcels and Bitmap Chain, alongside the [controls brief](TIMELINE-CONTROLS-BRIEF.md) and [dependency audit](INDEX-DEPENDENCIES.md).

The findings below record the released Sats code paths reviewed on 15 October 2026. Implementation is underway on a separate local development branch. Main, the live updater and existing user profiles remain unchanged; acceptance of the new portable preview must be recorded separately from this design review.

## Findings in the current application

| Finding | Current behavior | Design consequence |
| --- | --- | --- |
| Two resource presentation paths | Blocks, transactions, positional outputs and inscriptions use the timeline workspace iframe. Sats and addresses mount into the outer content root, replacing that iframe. | Register Sat and address views inside the existing workspace instead of rebuilding the explorer engines. |
| The workspace routes some resources out again | Its dispatcher sends numeric Sat addresses and conventional addresses to the shell; named Sats and P2PK keys follow different classification branches. | Use one canonical resource dispatcher for all entry points and resource links. |
| Two competing panel roles | Details shows resources; Inspector contains index controls. Selecting a track forces Inspector, and an earlier explicit tab choice can suppress later resource detail switching. | Make one contextual details area with clear resource and index-settings contexts, preserving each context's state. |
| Execution is spread across panels | Generic cards/forms, the current-job panel and specialized UTXO/Address/Sat panels contain separate On/Live/Build/Pause/Resume controls. | Route execution exclusively through the timeline controller; keep drafts, dependency/recovery review and evidence in the contextual panel. |
| Workspace state is lost on resource changes | Sat/address mounting clears the shell root. Returning creates another workspace instance. | Keep one workspace alive through resource navigation and preserve hierarchy, marks, focus, drafts and geometry. |
| Browser history treats internal navigation differently | Outer address entry pushes history, while workspace focus feedback replaces the current entry. | Distinguish intentional resource navigation from scrubbing, canonicalization and Back/Forward restoration. |
| Navigation controls vary by surface | Ruler/compact arrows step one item; expanded lanes sometimes pan instead. Range selections currently belong to individual index owners. | Use one navigation action layer and one global In/Out pair. Per-track ranges are deferred. |

The workspace already has a resource-panel registry used by inscriptions. Its `addPane`, `registerView` and `activateResource` hooks are the appropriate integration seams. Sat/address components can mount into dedicated panel roots; passing the whole outer shell as their root is what currently removes the workspace.

## Layout and responsibility

```text
Main resource address bar and global status
----------------------------------------------------------
Contextual details                 Resource visualization
Resource overview / records        Block / transaction /
Index advanced settings            isolated inscription view
----------------------------------------------------------
Shared indexing toolbar and global In / Out
Timeline tracks with Arm / Live / progress
```

The upper-left area remains one scrollable contextual Inspector. The upper-right remains a visualization of the selected resource where a useful renderer exists. A Sat or address does not need a fabricated block diagram merely to fill that space. Keep an intentional summary/empty state or show a containing output only when the user has selected one.

The timeline remains present through all explorer/index contexts, including unavailable results. Its collapsed form still exposes the relevant position/range and transport. Preserve independent pane resizing, track scrolling, modifier zoom and pinch. Narrow layouts can collapse or switch the visualization within the workspace instead of falling back to separate resource pages.

The main address bar is the common resource-entry control. Do not add another global Sat/address search form to individual resource details. Local filtering, pagination, choosing an output-relative offset and supplying a missing reveal-block hint remain legitimate contextual inputs.

## Contextual details and index settings

Use a concise heading identifying the selected resource or track, followed by its relevant content. Overview, Records and Advanced can be contextual sections rather than a permanent new set of global management tabs.

| Context | Principal content | Advanced content |
| --- | --- | --- |
| Block / transaction / input / output | Identity, value, exact destination, relationships and available ranges. | Source/verification evidence, original bytes or references and missing dependencies. |
| Sat | Origin/properties and saved location, output, offset and destination when known. | Exact checkpoint, coverage and location status. Satline stays disabled. |
| Address or P2PK destination | Saved balance, expandable UTXOs, historical received outputs and known holdings where available. | Separate current/history coverage, source checkpoints and dependency/upgrade explanations. |
| Selected index or layer | Saved checkpoint, real coverage/gaps, principal counts and immediate waiting/error reason. | Capability-specific mode/history/retention drafts, source alternatives, storage inspection, recipe/profile and recovery review. |
| Run details | Per-consumer progress, reuse, dependencies, failures and accepted request. | Exact checkpoints and journal evidence. Start/Pause/Resume remains on the timeline. |
| Workspace overview | Useful entry guidance, data readiness and links to the existing resource/track contexts. | Relevant diagnostics without a second index catalog or duplicate controls. |

Keep saved-data readiness, execution state and selected-chain relationship distinct. A Running or Paused job must not conceal damaged state, obsolete source evidence or partial coverage. Historical received totals are not current balances. Exact P2PK scripts remain distinct from same-key P2PKH or other encodings. Missing identity evidence does not establish empty inscription holdings.

Selecting a track opens its contextual settings without changing Arm/Live, the global range or the last explored resource. Navigating a resource shows its details reliably, rather than being blocked by a sticky prior tab selection. Returning to index settings restores its staged drafts. Do not silently select Blocks merely to navigate away from a state track.

Resource missing-data actions become Inspect required track or Review recovery settings. They focus the relevant track/panel and may prepare a draft. They do not start, arm or resume work. A reviewed rebuild preserves its checkpoint guard and archived-evidence semantics; the timeline Start action commits the reviewed run. Use one shared run review instead of stacking generic and specialized reviews for the same operation.

Current Addresses has no historical-range editor. The global range is shown in the shared toolbar, not copied into every index's Advanced section. Capability-specific mandatory starts and prerequisites are explained as constraints on that run, not editable per-track overrides.

## Controls to consolidate

Remove or adapt the execution paths in generic cards, generic build/current-job forms, the old Also index these block results checklist and specialized UTXO/Address/Sat panels. They must all use the same staged consumer specifications and timeline transport. In particular, do not carry forward the UTXO helper's hardcoded Live choice or Sats' separate Save Live action.

Keep read-only refresh, pagination, copy and explicit bounded storage inspection in the panel. Keep provider credentials, global privacy, browser integration and updates in Settings. Peer connection management remains in Peers; contextual source availability can still be explained beside an index. The shell status popover should show status and a shortcut to the Headers track rather than introducing another copy of synchronization execution controls.

Correct these content issues while consolidating:

- Recognize the valid pruned retention policy rather than labelling it Retention unknown. Separate discarded raw bodies from retained mandatory facts.
- Remove stale Sats-panel prose implying new Satline requests are available.
- Do not imply a measured range-fragment count when the status response does not supply one. Show measured metrics only where supported.
- Label the generic newest-records list as a bounded preview until actual complete pagination exists.
- Derive readiness from effective feature gates, available evidence and executors, rather than stale provider descriptions.

## Routes and persistent workspace lifecycle

Keep the existing outer shell as the owner of the address bar, browser history, status, Settings and Peers. Retaining the current trusted workspace iframe is the smallest first implementation; removing that boundary is not necessary to meet the user's design.

Route Home, Bitcoin, Ord, Sats, Indexes, saved activity, storage and sync-coverage addresses to appropriate contexts within the same workspace. Preserve old links as aliases. Locked future features should show a contextual unavailable explanation without enabling them or replacing the whole app. Settings and Peers are the only separate application destinations in this phase.

Mount the Sat and address components through workspace adapters and load the missing Sat-view asset there. Provide a resource API adapter: the current index helper prefixes every request with the index API path, while these views expect absolute resource endpoints. Reusing the wrong helper would silently call incorrect routes.

Maintain explicit workspace state for the focused resource, its optional anchored position, selected track/settings context, global range, staged consumer choices and active run. A resource is not always one block position. An address can have many outputs; a Sat's issuance origin can differ from its saved containing output. Keep the playhead where it is until a unique selected location or explicit output navigation establishes another position.

Use a navigation intent and generation for every explicit resource change. Deactivate the old adapter, abort outstanding resource reads/polls and close inscription viewer sessions. Reject late detail, pagination, metadata and error responses. Existing serial checks remain necessary even when requests support abort. Indexing jobs belong to the runtime and must not pause or restart merely because a panel changes.

When visiting Settings or Peers, suspend workspace view polling and preserve the workspace instance, or serialize/restore its explicit state. Do not keep an invisible page issuing redundant view queries. Returning revalidates source/checkpoint anchors before displaying joined data.

## Address history and external handoff

Use one canonical classifier for address-bar entry, resource clicks, timeline selections, startup links and browser-companion activation. Preserve the existing precedence for public keys/hashes, explicit namespaces, selected-tip number classification, Sat names and BPS coordinates. Retain the canonical address even when data is unavailable.

| Navigation intent | Browser history behavior |
| --- | --- |
| Explicit address entry or resource link | Push one navigable entry. |
| Scrub preview, passive focus or canonical spelling correction | Replace or leave the current entry; do not flood history. |
| Back/Forward restoration | Restore without creating another entry. |
| Track settings or panel disclosure | Change workspace context without a resource-history entry, unless following an explicit settings deep link. |

Use one ready/intent/acknowledgment protocol so startup and iframe-load callbacks cannot execute a request twice. Shell feedback must not overwrite newer navigation or double-push an entry. Companion activation is displayed only after its intended resource is established in the panel, not merely after posting a message.

Restore appropriate resource context, hierarchy, playhead, focus and view state through Back/Forward, while leaving the user's current global indexing marks and draft run unchanged. A source reorg invalidates stale positional metadata and caches before they are reused.

## Global range and navigation design

The user has explicitly limited this phase to one global range. The shared [range and keyboard contract](TIMELINE-CONTROLS-BRIEF.md#global-in-and-out) covers address-bar-to-playhead navigation, I/O marks, draggable handles, clearing, step/skip controls and their shortcuts. Selecting another track must not swap in another hidden range.

Preserve the exact resource positions of marked endpoints, including deep output/Sat positions when established. Show the containing whole-block interval that existing index builders can process. Precise browsing selection must not imply that a UTXO or Sats engine can begin from an arbitrary Sat or halfway through a block. Missing placement or prerequisite state must be explained rather than guessed.

Keep navigation actions distinct from indexing transport. Previous/Next/Skip moves focus; Start indexing begins work. Holding Space for panning or pressing an arrow must never start a scan. Shortcuts apply only in the relevant workspace context, respecting text fields, menus, dialogs and isolated content frames.

## Source reads policy and isolation

Moving a view does not make its existing resolver side effects disappear. Navigation and block-map APIs can retain browse-index receipts under separate preferences. Transaction visualization can request historical previous-output evidence through the flow endpoint, and inscription viewers can fetch content dependencies. Distinguish explicit resource retrieval, passive metadata revalidation and indexing execution; apply the effective Off policy to optional derivation through every path.

Context changes never submit builds, arm indexes or enable tracing. A user-requested object can still fetch its required evidence with a clear loading/source/error state. Preserve bounded auxiliary repair rules without turning a refresh into an expensive historical rebuild. Reuse resolved resource data instead of resolving it again independently in the left panel and right visualization.

Keep local-management/API access checks, no-store responses, bounded reads, nonce CSP and updater request draining. Validate bridge messages against both the expected frame and origin with bounded canonical payloads; the current generic navigation message checks origin but lacks the same sender check used by several other message types.

Inscription HTML/media stays on its existing separate content origin with the current sandbox and restricted capabilities. Integrating details must not inline untrusted content into management DOM or nest a full application shell as a preview. Preserve exact snapshot/commitment checks for address pagination, Sat output ranges and known holdings. Sharing a panel does not imply different datasets have one common current checkpoint.

## Dependency presentation and Bitmap Chain

Requirements are visible beside each resource or index overview, before Advanced. Describe the relationship in plain language: obtainable source bytes, required complete saved state, substitutable lookup evidence, optional acceleration or a checkpoint-sensitive join. A visual parent does not imply a build dependency: Sat ranges is grouped under TXIO but Sats does not require a prebuilt TXIO index.

The shared Start review lists each armed consumer, reuse versus new work, the complete block interval, and its exact prerequisite or unresolved reason. Inspect opens the relevant track settings. Include in run explicitly stages an eligible prerequisite; it does not start it. A genesis-prefix requirement offers a visible change to the shared In point and requires another review. Preserve separate errors and progress so one unavailable optional consumer does not conceal healthy work or falsely claim completion. Source availability never means the Raw Blocks track must be armed.

The Bitmap group contains separate Districts, Parcels and Bitmap Chain rows. They share a block decode and inscription extraction but retain their own identities and coverage. Parcel recognition needs checked parent provenance, which an inscription's parent tag alone cannot establish. Explain that dependency as unresolved when the required saved evidence is absent; do not quietly launch Sats or Satline.

The Bitmap Chain panel leads with the evaluated block and a clear outcome: selected district claims, evaluated with no selected claims, or evaluation incomplete. Show the source block, development rule profile, scope start, completed coverage and gaps as distinct evidence. An evaluated empty block advances the chain; missing or unresolved evaluation does not. A scoped complete commitment is unavailable across a gap. The audit describes evaluated outcomes and does not prove ownership or Bitcoin consensus validity. Exact hashes, ordered winner identifiers and encoder evidence belong in Advanced.

New District, Parcel and Chain stores preserve the legacy locked Bitmap recipe. Development profiles pin the current reviewed interpretation and reference revision and are labelled experimental. They must not be presented as the final canonical Bitmap rules. No new publication or main/updater promotion is authorized by this implementation pass.

## Implementation sequence

1. Define canonical resource/context state, navigation history, one global range and shortcut actions. Keep the shared run controller from the controls brief as the sole execution owner.
2. Keep one persistent workspace and register Sat/address adapters through its existing resource registry, with the proper API and cancellation lifecycle.
3. Move all nonexception routes to resource, overview or run/settings contexts. Preserve old deep links and native/browser handoff.
4. Replace competing Details/Inspector roles with contextual resource and index-settings sections. Remove duplicate execution controls and correct misleading status prose/metrics.
5. Integrate shared transport, global marks and semantic navigation without duplicating draft state or source reads.
6. Verify actual shell/workspace composition, Settings/Peers restoration, keyboard/focus, narrow layouts, exact checkpoint behavior and source/indexing policy enforcement.

The route/panel changes can be reviewed independently of the shared index coordinator. Neither requires replacing the Sat/Address engines. The additional Bitmap consumers use separate development identities and are covered by the extension contract below. Keep main and publication on hold during development under the user's current branch instruction.

## Acceptance checks

- Follow address bar to block, transaction, output, P2PK/address, Sat and inscription inside the same actual workspace instance. Assert iframe/root identity, not only successful headings.
- Verify numeric/name Sat links, BPS, native transaction/outpoint forms, public keys, inscription hints and companion activation through the production dispatcher.
- Test explicit Back/Forward entries, scrub replacement, no duplicate startup navigation and late responses after rapid resource changes.
- Check unknown Sat location, missing reveal locator, damaged/stale source state and checkpoint changes during paginated ranges/holdings. Never turn them into known zero/empty results.
- Verify track/context selection preserves global marks, draft settings and saved work. Missing-data actions only inspect/prepare their dependency.
- Prove there are no second Arm/Live/Start/Pause controls in contextual panels. Rebuilds retain their source checkpoint and explicit review safeguards.
- Instrument source and mutation endpoints: switching context, passive status refresh and editing drafts must not start builds or bypass Off through browse indexing. Distinguish permitted explicit source reads.
- Close inscription viewer sessions and cancel stale reads on deactivation. Keep content origin, sandbox, local-access and bridge sender checks intact.
- Visit Settings/Peers and return without losing position, marks, hierarchy or drafts, or starting work. Suspend unnecessary hidden view polling.
- Exercise I/O and step/skip keyboard actions with text inputs, dialogs, narrow layouts, long keys and missing child counts. Navigation cannot start indexing or rewrite the global range accidentally.
- Run real composed-shell browser checks and compiled-app acceptance, including actual Sat-selection callbacks, pane resizing, vertical wheel, modifier zoom and pinch.

## Code references

| Area | Current implementation |
| --- | --- |
| Shell resource routes and history | [app.js](../ui/shell/app.js), [shell index](../ui/shell/index.html) |
| Workspace dispatcher and index forms | [indexes.html](../ui/shell/indexes.html) |
| Resource registry and lifecycle | [timeline-explorer.js](../ui/shell/timeline-explorer.js) |
| Reusable Sat and address views | [sat-view.js](../ui/shell/sat-view.js), [address-view.js](../ui/shell/address-view.js) |
| Specialized index panels | [utxo-panel.js](../ui/shell/utxo-panel.js), [address-panel.js](../ui/shell/address-panel.js), [sat-panel.js](../ui/shell/sat-panel.js) |
| Timeline controls and geometry | [index-timeline.js](../ui/shell/index-timeline.js), [index-workspace.js](../ui/shell/index-workspace.js), [index-timeline.css](../ui/shell/index-timeline.css) |
| Cards and overview/settings pages | [index-cards.js](../ui/shell/index-cards.js), [workspace.js](../ui/shell/workspace.js) |
| Isolated inscription content | [inscription-viewport.js](../ui/shell/inscription-viewport.js), [index_interface.go](../index_interface.go) |
| Resolver and private API guards | [gateway_shell.go](../gateway_shell.go), [activation.go](../activation.go), [index_sat_runtime_077.go](../index_sat_runtime_077.go) |
