# Proposed Details and Inspector pass

This is a proposal for the next review. It is separate from the current map, flow and tracker refinement; the existing controls retain their behavior in this build.

Keep **Details** and **Inspector** as the two choices in the left pane. Details follows the navigated resource; Inspector follows the selected index and preserves its draft build ranges when browsing elsewhere. A short location breadcrumb and resource title give orientation without repeating the global address field.

In Details, keep the resource summary and parent navigation visible. Show the most useful fields first: block verification and transaction count, transaction inputs/outputs and fee evidence, or inscription type and dependency status. Put large lists in their own bounded scrolling regions. Keep exact hashes, source evidence and raw records in an expandable Technical details section; opening it must not resize the viewport or timeline.

In Inspector, separate three groups in a single continuous panel:

- **Index status:** local coverage, active job, progress and visible errors.
- **Build:** selected ranges, exact editable heights, preparation results and explicit start/cancel controls.
- **Settings:** browsing policy, retention and sharing choices, with locked features explained where they appear.

The title and current job state should stay visible while the relevant long section scrolls. Build drafts, errors and prepare results must survive switching to Details and back. Disabled controls should remain discoverable without taking the same space as active controls.

At smaller sizes, summaries and field/value pairs reflow within the left pane. The input/output list, range list or evidence section scrolls when needed; the app itself stays within the window. The existing draggable split controls the space given to the viewport. Avoid changing the split automatically after the user has chosen a width.

Before implementing this pass, compare Advanced Controls against the current interface. Make a function-by-function inventory covering sources, indexes, jobs, serving, diagnostics and recovery. Every supported action must remain reachable or receive an explicit replacement; simplify presentation without silently removing capabilities. Review this inventory and a rendered layout with the user before adopting the redesign.
