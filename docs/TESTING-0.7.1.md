# Gateway timeline development testing

This is a local development build for the proposed 0.7.1 update. It has not been published to GitHub or the hosted update service. Use a separate test profile. The current release's index locks still apply.

## Workspace and tracks

- Open Indexes. The timeline occupies the lower workspace immediately, with the inspector above it. Expanding settings or a long record must scroll inside the inspector without moving the timeline off screen.
- Drag the horizontal divider to resize those panes. Focus the divider and use Up/Down, Shift+Up/Down, Home or End; double-click resets it. Both panes must remain usable, and the chosen proportion must survive reload.
- Bitcoin Headers is always the first track. Other tracks appear when added or when local data, saved state or a job already establishes that index. Empty, unused definitions do not create tracks.
- Use **Add index** to add Bitcoin Blocks on an empty profile. Its track and inspector should appear, including its settings; adding it must not start indexing or enable following. The empty track must remain after refresh and reload.
- Locked indexes appear as disabled choices in Add index. They cannot be added or used to start work. The existing release locks remain in force.
- Select a track's name, a covered range or a close-up block. The upper inspector should show the matching selection. Headers describe local headers; Blocks describe raw block files. A processing checkpoint alone must not appear as stored block coverage.

## Navigation

These are Gateway's mappings for this development build.

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
- **Exact coverage** in the inspector provides keyboard-accessible range selection. Opening a missing block in Explorer is an explicit fetch action; selecting it alone never fetches data.

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

The [public tester guide](TESTING-0.7.0.md) still covers header synchronization, block fetching, the Explorer, schema, peers, browser registration and .bitcoin address entry, storage/privacy and update settings. Exercise those paths for regressions using a disposable profile. The local development build is ahead of the public update feed; a public update check does not publish this build.

Dependency visualization, dragging build ranges directly on timeline tracks, the inscriptions viewer and a unified Explorer/timeline are later work. Additional derived indexes remain locked in this iteration.

## Repeatable browser checks

The source browser suites use disposable browser profiles and local API fixtures. They do not contact peers or real external services, and their plan/build responses are simulated. They complement testing against the compiled app.

- `node scripts/test-index-timeline-browser.js`: sparse physical coverage, exact gaps, inspection, locks, status recovery and Explorer navigation.
- `node scripts/test-index-timeline-workspace-browser.js`: docking, divider persistence and bounds, adding empty tracks, scrollbar handles, wheel, touch pinch, Space-grab, ruler landmarks, range drafts, themes and fixed-height embedding.
- `node scripts/test-index-workspace-browser.js`: existing plans, queues, records, per-track drafts and polling behavior.

Each script accepts `--out DIRECTORY` to save local screenshots or acceptance evidence. Keep test profiles and generated evidence out of the public source tree.
