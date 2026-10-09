# Gateway 0.7.1 — timeline workspace testing release

Gateway's Indexes page now provides a timeline workspace for exploring Bitcoin and preparing indexing ranges. Bitcoin Headers and Bitcoin Blocks remain the enabled indexes. Bitcoin Core is optional. Publication and hosted update activation are pending; see [status](STATUS.md) and the [acceptance record](RELEASE-ACCEPTANCE-0.7.1.json).

## What's changed

- A bottom-docked timeline with a resizable upper pane, default Headers and Blocks tracks, shared block-height ruler, and exact local coverage and gaps. Expanding details scrolls within its pane instead of pushing the timeline off screen.
- Custom scrollbar handles for zoom and track height, pointer-anchored wheel/pinch zoom, Space-drag panning, keyboard navigation, and halving/difficulty landmarks.
- Shift-click selects an inclusive range; Ctrl-click or Command-click toggles individual blocks. Separate ranges have their own editable markers. Selections prepare indexing intent; work still requires reviewing and explicitly starting a plan.
- An independent playhead follows the explored block across track changes. Scrubbing waits briefly before loading the settled block; direct block clicks load immediately.
- Inspector and Block Explorer tabs share the upper pane. Browse block evidence, paginated transactions, transaction inputs/outputs and value flow without leaving the timeline.
- A circled plus beneath the tracks adds available indexes. Removing a track preserves stored data and indexing jobs; the foundational Headers track cannot be removed.
- Previous output index zero now appears correctly in transaction details and copied outpoints.

## Scope

Filled Blocks coverage describes present local block files. Headers and completed processing alone do not establish possession of those block bodies. Additional transaction-location, spender/address, Sat/Satline, inscription, Bitmap and **Bitmap: Terrain Claim Chains** features remain locked, including API/CLI/job paths. Gateway-to-Gateway peerhood and sharing remain locked too.

Gateway checks block identity, commitments and selected-chain context; it does not replace Bitcoin Core's full script/UTXO consensus validation. This remains testing software.

## Packages and testing

Choose the Windows installer or a **fresh** Windows/Linux ZIP for a first installation. Application-only ZIPs are update payloads. Fresh packages include the header snapshot, tester guide and license notices. The installer is a separate download, not contained in the portable ZIP. Compare downloads with the release's checksum file.

Follow [the timeline tester guide](TESTING-0.7.1.md), [Getting started](GETTING-STARTED.md) and [the setup/browser QA checklist](TESTING-0.7.0.md). Browser registration and `.bitcoin` address entry remain available. See [known issues](KNOWN-ISSUES.md), [privacy](PRIVACY.md) and [the roadmap](GATEWAY-ROADMAP.md).

## Validation and delivery status

Runtime source `e508085f34c406f41ded7eadbc33d5ee0ab4324b` passed [all 14 Linux CI stages](https://github.com/Blockamoto/gateway/actions/runs/37944091190). The full Windows Go suite passed **858 checks with 54 expected skips and no failures** on 9 October 2026. Windows and Linux development payloads built successfully; final release package checks remain pending.

Real-data testing used ordinary Bitcoin peers with Core RPC and mounted files disabled. Headers synchronized beyond the bundled snapshot, and blocks 840000, 840001 and 850000 were fetched and retained as two separate coverage ranges. Native browser checks used no API mocks and exercised block 840000's 3,050 transactions, pagination, a known transaction, selection, delayed scrubbing and narrow layout. Browsing did not start indexing jobs, and headers and blocks survived restart.

Windows executables remain unsigned. Final installer scans are pending, and no universal antivirus clearance is claimed. If a file is flagged, stop that check and report the exact filename and alert; do not disable protection to complete testing.

The existing signed update channel and installer update choices remain supported. Activation and live delivery verification for 0.7.1 are pending; publication alone does not establish that the hosted service offers it. The approved-release renewal workflow refreshes signed metadata without automatically choosing a new application release. See [updates](UPDATES.md) and the [renewal operator guide](../packaging/update-publisher/RENEWAL.md).

Gateway's original source uses [MIT](../LICENSE), with [third-party notices](../THIRD-PARTY-NOTICES.md) retained.
