# Implementation status

Current published main-client release: v0.5.4-repair.1.
Starting HEAD (local and GitHub verified 29 September 2026): deb67c6b81467184a349e37b2ca43cc16ad7f639.

In progress: master-brief baseline, generic index definitions/dependency planner, lower provider catalogue, bounded inscription extraction and evidence-backed Bitmap rules. No libp2p migration is underway.

Existing: repaired Bitcoin/Gateway sessions, BOD retrieval, mounted/Core providers, sparse locator/spender state, Satline, Ord-on-demand, discovery ledger, browser routes, Windows/Linux packaging. Preserve their privacy and ownership gates.

Baseline build succeeds. Native baseline test run completed; see BASELINE-2026-09-29.json for exact results. Three cleanup symlink fixtures require Windows privileges. TestCoreLookupAndOverlay has a stale expectation that remote address-wide scans are permitted; the intentional privacy repair denies them. This test needs correction rather than relaxing remote serving policy.

Next: integrate/test generic substrate and lower index views, then inscription/Bitmap resumable derivation, then peer index serving, practical bootstrap/mesh behavior, advanced Probe and release acceptance.

Deferred: Runes, metaprotocol indexes, galleries, wallets, marketplaces, Bitmap worlds, WebRTC. Ord database salvage is stretch only. Public-network Bitcoin-only first contact remains unproven.
