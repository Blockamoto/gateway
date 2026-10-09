# Gateway 0.7.1 tester guide

This guide covers the public 0.7.1 timeline testing release. Check [current status](STATUS.md) for validation and hosted delivery acceptance. Use a separate test profile. Headers and Bitcoin Blocks are enabled; additional indexes and Gateway sharing remain locked.

## Workspace and tracks

- Open Indexes. The timeline occupies the lower workspace immediately, with the inspector above it. Expanding settings or a long record must scroll inside the inspector without moving the timeline off screen.
- Drag the horizontal divider to resize those panes. Focus the divider and use Up/Down, Shift+Up/Down, Home or End; double-click resets it. Both panes must remain usable, and the chosen proportion must survive reload.
- Bitcoin Headers and Bitcoin Blocks appear by default, even without stored blocks. Headers is always first. Other tracks appear when added or when local data, saved state or a job already establishes that index.
- Use the **circled plus beneath the tracks** to choose an index. Adding a track opens its inspector and must not start indexing or enable following. The track must remain after refresh and reload.
- Locked indexes appear as disabled choices in Add index. They cannot be added or used to start work. The existing release locks remain in force.
- Select a track's name, a covered range or a close-up block. The upper inspector should show the matching selection. Headers describe local headers; Blocks describe raw block files. A processing checkpoint alone must not appear as stored block coverage.
- Track settings and Block Explorer share tabs in the upper pane. Changing tabs must not resize the timeline. Status belongs in the shell's status strip; the compact refresh icon refreshes local status.
- Remove Bitcoin Blocks using its track removal control. Cancel first, then confirm: this removes the workspace track only, preserving stored data and jobs. Reload and add it again to verify that preference and data survive. Headers cannot be removed.

## Navigation

These are Gateway's timeline navigation mappings.

| Control | Expected result |
| --- | --- |
| Bottom scrollbar: drag middle | Pan along block heights without changing the visible span. |
| Bottom scrollbar: drag either end | Change that endpoint of the visible range; the opposite endpoint stays anchored. |
| Right scrollbar: drag middle | Scroll the index tracks vertically. |
| Right scrollbar: drag either end | Change track height. |
| Mouse wheel / vertical trackpad scroll | Scroll tracks vertically. |
| Horizontal trackpad scroll or Shift+wheel | Pan along block heights. |
| Ctrl+wheel, Command+wheel or trackpad pinch | Zoom around the pointer. |
| Two-finger touch pinch | Zoom around the midpoint of the fingers. |
| Alt+wheel | Change track height. |
| Hold Space and drag the tracks or ruler | Grab and pan. Releasing Space, cancelling or switching away must release the hand state. |
| Focused track: `+` / `−`, Left/Right, Home | Zoom, pan, or fit the chain; at close zoom Left/Right steps through selected blocks. |
| Focused scrollbar/handle: arrows, Home, End | Move its position or endpoint without a pointer. |

- Use **Fit chain**, **Go to block** and **Focus selection**. Zoom must keep the block under the pointer or pinch midpoint still, except where the chain's ends require clamping. Panning must not also select a block accidentally. Normal typing and Space activation on controls must remain usable.
- Fit chain follows new headers. After zooming or panning, refresh must preserve the chosen view. A chain rollback may clamp it to the remaining height.
- The ruler uses block heights. Halving landmarks appear at multiples of 210,000; difficulty landmarks appear at multiples of 2,016 when there is room. These are independent scales: a halving boundary must not create a false difficulty boundary. Exact coverage and gaps remain unchanged at every scale.
- At a distant zoom, striped coverage means stored and absent heights share a pixel. Zoom into a known gap, including a single missing block, and verify it remains empty. A partly mapped source must say coverage is partial; a blank area must not be presented as proven absence.
- **Exact coverage** in the inspector provides keyboard-accessible range selection. Empty block areas remain selectable without a large placeholder obscuring them. Muted background is not evidence of stored block bytes.

## Playhead, selection and embedded Explorer

- Click a specific block to move the playhead and load it in the upper Block Explorer tab. Missing block data can be requested on demand; existing local data should be reused. Stay inside the Indexes workspace.
- Click and drag the ruler to scrub. The playhead moves immediately, but loading waits about one second after settling. Rapid movement must request only the final block, and a late response must never replace the current selection.
- Switch tracks: the playhead stays at the same height, and the chosen upper tab remains selected. Clicking a track name opens its index settings while retaining its selected indexing ranges.
- Click a block, then Shift-click another: select the inclusive range, in either direction. Ctrl-click (Command-click on macOS) adds or removes that individual block without filling the gap. Test removing a selected block by clicking directly on its marker.
- Separated ranges have their own in/out markers; a single block has one marker. Drag or use the keyboard on a marker to change that boundary. Repeated key presses must keep focus on the moving marker.
- Range edits and modified selection clicks do not fetch blocks or start indexing. The playhead controls exploration; selected ranges control indexing intent. Preparing a range only fills the draft for review. Multiple separated ranges must be prepared individually, without silently indexing their intervening gaps.
- Inspect a block's evidence, paginated transactions, transaction inputs/outputs and value flow inside Block Explorer. Transaction lookup within that known block remains available; derived-index actions remain locked.
- Make local status unavailable while a scrub load is pending. Loading pauses, stale responses are ignored, and restoring status resumes only the latest playhead. A failed request should give a useful retry action.

## Existing controls

- Select Bitcoin Blocks. Indexing and Follow new blocks use the existing red Off / green On switches. Indexing alone saves permission; following may catch up many historical blocks.
- Expand Settings, jobs & records to inspect the existing build, records and data-source controls.
- In **Build range**, drag either endpoint or edit First block / Last block. Both representations must agree. The rail and handles must remain readable in both themes; use Tab or exact numbers when endpoints overlap. Invalid typed numbers or reversed endpoints must remain visible for correction and block plan review.
- A retained instance or an index with a fixed start keeps that first height read-only. Its first handle is disabled. A blank Last block means the verified tip when work starts; moving its handle chooses a fixed end. Following new blocks hides the bounded selector.
- Review a plan, then adjust either handle. The old approval must be invalidated. Selecting a timeline range and choosing **Build this range** only prepares the draft, preserving any required starting height. Review the plan, then start explicitly.
- Switch tracks and return: per-track drafts should survive. Check pause/resume, saved jobs and bounded progress.
- Disconnect or stop the local service. Previously displayed coverage must be identified as last known and controls disabled. Restore the service and refresh.
- Toggle light/dark in Settings and try a narrow window. Both panes and all navigation controls must remain accessible without document overflow. Repeat in the desktop shell as well as the standalone Indexes page.

## Other available functionality

The [setup and browser QA checklist](TESTING-0.7.0.md) also covers header synchronization, block fetching, the Explorer, schema, peers, browser registration and .bitcoin address entry, storage/privacy and update settings. Exercise those paths for regressions using a disposable profile. Check [current status](STATUS.md) for this release's hosted delivery acceptance; a published download alone does not establish that the updater offers it.

Dependency visualization, additional index-specific zoom depths, availability colours and the inscriptions viewer are later work. Additional derived indexes remain locked in this iteration.

## Repeatable browser checks

The source browser suites use disposable browser profiles and local API fixtures. They do not contact peers or real external services, and their plan/build responses are simulated. They complement testing against the compiled app.

- `node scripts/test-index-timeline-browser.js`: sparse physical coverage, exact gaps, inspection, locks, status recovery and embedded Explorer navigation.
- `node scripts/test-index-timeline-selection-browser.js`: real-page range gestures, marker editing, playhead separation and Explorer loading.
- `node scripts/test-timeline-explorer-browser.js`: Explorer debounce, stale responses, transactions, value flow, status loss/recovery and request timeout.
- `node scripts/test-index-timeline-workspace-browser.js`: docking, divider persistence and bounds, adding empty tracks, scrollbar handles, wheel, touch pinch, Space-grab, ruler landmarks, range drafts, themes and fixed-height embedding.
- `node scripts/test-index-workspace-browser.js`: existing plans, queues, records, per-track drafts and polling behavior.

Each script accepts `--out DIRECTORY` to save local screenshots or acceptance evidence. Keep test profiles and generated evidence out of the public source tree.
