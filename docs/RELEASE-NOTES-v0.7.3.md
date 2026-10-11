# The transactions update

Gateway 0.7.3 is prepared as a draft for review. It is not a published release and does not replace the approved hosted updater release.

## Transactions and indexing

- The Transaction Index and inscription-related transaction locator builds are available. Optional related-locator creation and the retroactive action remain in the Inscription inspector.
- **Index as you browse** is an independent, persisted choice for Transaction Index and Inscriptions. Both are off by default. Opted-in browsing records exact validated blocks without starting a historical range job or advancing its sequential checkpoint.
- Block maps include every transaction in the validated block, beyond the first table page. Square sizes use output sats; a bounded passive preview shows only the first inscription in each transaction.
- Transaction selection opens its input/output flow in the viewport. Unknown input values and fees stay unestablished. Only verified reveal-output assignments receive output inscription badges; multiple inscriptions remain individually accessible. These assignments do not establish current ownership.

## Workspace and navigation

- Details and Inspector share the left pane. Blocks and inscriptions use a square viewport; transactions can use a wider viewport. A draggable divider controls the split.
- The compact timeline is the default. Known block, transaction and output counts allow progressively closer navigation into transactions, outputs and output-local sat offsets in the same lane. Unknown counts remain unknown; these offsets are not global sat numbers.
- Wheel zoom anchors to the pointer; horizontal trackpad movement and Shift-wheel pan. Plain lane dragging pans, while a click moves the playhead without replacing indexing selections. Shift-click and Ctrl/Command-click edit indexing selections. Ruler scrubbing waits for the final position to settle.
- Map and flow navigation supports zoom, diagonal dragging, remembered views and **Fit view**. Returning to the containing block preserves the block's own view. Fullscreen and narrow layouts preserve timeline state.
- The global address field stays shared with the workspace. Missing inscription locations offer a reveal-block hint; known locations with unavailable bytes remain distinguishable from unknown locations. Status lives in the top-bar popover.
- A delayed same-block transaction or inscription response preserves a newer hierarchy zoom. Actual navigation, different-block responses and changed block anchors still cancel obsolete descent requests.

## Validation and limits

The clean Windows Go suite passed **941 checks, with 48 expected skips and no failures**, before the final narrow gesture, raster-policy and flow-note corrections. Targeted follow-up checks and seven affected browser suites passed. The final local Windows executable separately passed native content authorization and isolation, uncached recursive content without third-party cookies, image/audio/video wrappers, hostile-content responsiveness, saved choices, setup and shell checks. Real Bitcoin block maps, transaction navigation, the resizable split and the output-local tracker were checked separately from controlled content and picker fixtures. Linux build and static checks passed.

Numeric geometry checks compare the flow against pinned Mempool reference cases and ordered square placement against BitFeed reference cases. They do not establish full rendered pixel parity. The permanent CSS `will-change` hint was removed to let Chromium repaint at the current zoom; native sharpness and the reported feathering behavior still need user review because screenshot capture can force a repaint.

Fresh-install packages include a validated header snapshot; application-update packages omit it. Final release packaging, exact-file antivirus diagnostics, system installation, Linux CI for the final committed source, and hosted delivery are separate acceptance steps. Results for an earlier binary do not establish these checks for this draft. Windows executables are unsigned.

Gateway peerhood and later indexes remain gated. Inscription reveal evidence does not establish global numbering, current ownership, sat history or parent-child provenance. Content dependencies require known locations and available source bytes. The broader Details/Inspector reorganization remains a separate design proposal.

See [the transaction tester guide](TESTING-0.7.3.md), [the acceptance record](RELEASE-ACCEPTANCE-0.7.3.json), [status](STATUS.md), [known issues](KNOWN-ISSUES.md), [privacy notes](PRIVACY.md) and [the roadmap](GATEWAY-ROADMAP.md).
