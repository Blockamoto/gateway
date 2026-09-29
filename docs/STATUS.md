# Implementation status

Current published main-client release: v0.5.4-repair.1.
Starting HEAD (local and GitHub verified 29 September 2026): deb67c6b81467184a349e37b2ca43cc16ad7f639.

Checkpoint B/C/D foundation: generic index definitions/dependency planner, explicit lower provider catalogue, and bounded inscription extraction are implemented with focused passing tests. Canonical inscription numbering remains unknown unless a separate historical provider supplies it. No libp2p migration is underway.

In progress: durable index instances/jobs, Bitmap scanner and CLI/GUI integration. The definition catalog describes their planned executors; this foundation checkpoint alone does not expose a runnable Bitmap build command.

Existing: repaired Bitcoin/Gateway sessions, BOD retrieval, mounted/Core providers, sparse locator/spender state, Satline, Ord-on-demand, discovery ledger, browser routes, Windows/Linux packaging. Preserve their privacy and ownership gates.

Baseline build succeeds. Native baseline test run completed; see BASELINE-2026-09-29.json for exact results. Three cleanup symlink fixtures require Windows privileges. TestCoreLookupAndOverlay now correctly expects remote address-wide scans to be denied and verifies that they do not trigger another Core scan. Production privacy policy is unchanged.

Next: integrate/test generic substrate and lower index views, then inscription/Bitmap resumable derivation, then peer index serving, practical bootstrap/mesh behavior, advanced Probe and release acceptance.

Deferred: Runes, metaprotocol indexes, galleries, wallets, marketplaces, Bitmap worlds, WebRTC. Ord database salvage is stretch only. Public-network Bitcoin-only first contact remains unproven.
