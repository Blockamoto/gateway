# Implementation status

RELEASE COMPLETE: Gateway v0.6.0-alpha.1 is published at https://github.com/Blockamoto/gateway/releases/tag/v0.6.0-alpha.1 from d89193e8e1d925731d21cbb3455de28b21b3b403. Windows portable ZIP, standalone installer, Linux ZIP, checksums and manifest are uploaded; all five GitHub server digests and sizes match local files. See RELEASE-PUBLISHED-0.6.0-alpha.1.json. Visual QA remains explicitly deferred; this is not complete master-spec acceptance.

Current published main-client prerelease: v0.6.0-alpha.1 (previous repair release: v0.5.4-repair.1).
Tagged release source: d89193e8e1d925731d21cbb3455de28b21b3b403.
Starting HEAD (local and GitHub verified 29 September 2026): deb67c6b81467184a349e37b2ca43cc16ad7f639.

Checkpoint B/C/D foundation: generic index definitions/dependency planner, explicit lower provider catalogue, and bounded inscription extraction are implemented with focused passing tests. Canonical inscription numbering remains unknown unless a separate historical provider supplies it. No libp2p migration is underway.

Implemented: durable index instances/jobs, contiguous checkpoint/resume/reorg handling, explicit source-retention policies, Bitmap district derivation and exact-key lookup, CLI/API/Indexes page, frozen opt-in peer snapshots, configurable multiple Gateway entrances and simple/forensic discovery views. No unverified peer result is imported as local verified state.

Live acceptance: a genuinely fresh profile with Core, mounted Core and Ord disabled synchronized ordinary Bitcoin headers and derived the known first Bitmap at 792435. Source retention was ephemeral and raw cache file count stayed zero. See ZERO-STATE-INDEX-ACCEPTANCE.json. A bounded header-validation window removed repeated file-open overhead without dropping checks (about 118 to 13,000 headers/sec in this one run).

Existing: repaired Bitcoin/Gateway sessions, BOD retrieval, mounted/Core providers, sparse locator/spender state, Satline, Ord-on-demand, discovery ledger, browser routes, Windows/Linux packaging. Preserve their privacy and ownership gates.

Baseline build succeeds. Native baseline test run completed; see BASELINE-2026-09-29.json for exact results. Three cleanup symlink fixtures require Windows privileges. TestCoreLookupAndOverlay now correctly expects remote address-wide scans to be denied and verifies that they do not trigger another Core scan. Production privacy policy is unchanged.

Release publication completed. Restart/extension now passes through 792436; empty optional arrays are canonicalized so zero-candidate blocks reopen correctly. Foundation checkpoint 94451bc is already pushed. The integrated main application suite passed; the same three symlink-privilege cleanup fixtures failed. Final versioned artifact checks are recorded in RELEASE-PUBLISHED-0.6.0-alpha.1.json; final suite recorded 459 passing test events and the same three privilege failures.

Unmet/limited acceptance: no authenticated historical conflicting-winner Bitmap fixture. The pinned OPI necessary-condition comparator can show MIME-based exclusion, but otherwise reports missing historical context; no complete OPI winner is claimed. Canonical inscription numbering and parcel ownership are not implemented. Peer pages cannot export individual block batches over 32 KiB and instead require Bitcoin replay. One contiguous instance per index/profile; disjoint inscription ranges are not implemented. Browser connector blocked the local page, so automated CLI/API/CSP/JavaScript tests do not constitute visual acceptance. Native installer execution and external two-house bootstrap acceptance are not claimed. These are alpha limitations, not waived master-brief requirements.

Deferred: Runes, metaprotocol indexes, galleries, wallets, marketplaces, Bitmap worlds, WebRTC. Ord database salvage is stretch only. Public-network Bitcoin-only first contact remains unproven.
