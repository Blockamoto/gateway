# Gateway timeline development testing

This is a local development build for the proposed 0.7.1 update. It has not been published to GitHub or the hosted update service. Use a separate test profile. The current release's index locks still apply.

## Timeline

- Open Indexes. Bitcoin Headers should be the first track, followed by Bitcoin Blocks and the locked index tracks.
- Select a track's name. Its inspector appears above the timeline. Headers describe available local headers; Blocks describe available raw block files. A processing checkpoint alone must not appear as a stored block range.
- Use Fit chain, zoom, pan and Go to block. All tracks must share the same height ruler. At a close zoom, individual blocks become selectable.
- Select a filled range, then Focus selection. Its exact endpoints and any holes must survive zooming. At a distant zoom, a striped segment means a mixture of stored and absent heights, rather than full coverage.
- Select an empty range or an individual block. The inspector should make missing or unknown coverage explicit. Opening an unavailable block in Explorer is an explicit fetch action; selecting it alone never fetches it.
- Open Exact coverage to select precise visible ranges using the keyboard. Track buttons, zoom buttons, block-height entry and the inspector should work without a pointer. A focused track also supports arrow keys, plus/minus and Home.
- While Fit chain is active, new headers extend the view. After zooming or panning, refresh must preserve the chosen view. A chain rollback may clamp a view to the remaining height.
- Toggle light/dark in Settings, then check a narrow window. State must remain readable, and the page must not overflow horizontally.

## Existing controls

- Select Bitcoin Blocks. Indexing and Follow new blocks use the existing red Off / green On switches. Indexing alone saves permission; following may catch up many historical blocks.
- Expand Settings, jobs & records to inspect the existing build, records and data-source controls.
- Select a range and choose Build this range. It only prepares the form. Review the plan, then start it explicitly. Check pause/resume, saved drafts and bounded job progress.
- Inspect a locked track. Its reason should be visible, with no build, follow, lookup or Explorer controls that bypass the release lock.
- Disconnect or stop the local service. Previously displayed coverage must be identified as last known and controls disabled. Restore the service and refresh.

## Other available functionality

The [public tester guide](TESTING-0.7.0.md) still covers header synchronization, block fetching, the Explorer, schema, peers, browser registration and .bitcoin address entry, storage/privacy and update settings. Exercise those paths for regressions using a disposable profile. The local development build is ahead of the public update feed; a public update check does not publish this build.

Dependency visualization and a unified Explorer/timeline are later work. Additional derived indexes remain locked in this iteration.
