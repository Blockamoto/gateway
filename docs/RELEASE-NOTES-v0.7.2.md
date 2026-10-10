# Gateway 0.7.2 — inscription prerelease preparation

This is a draft for the local review build. It has not replaced the published 0.7.1 release or the approved hosted update feed. The release will be marked as a prerelease when its review and publication checks are complete.

## Scope

- An inscription content viewport within the timeline workspace, alongside Inspector and Explorer.
- Positional inscription resolution from a known Bitcoin block, using the same extraction handler as indexing.
- Lean inscription payloads containing compact positional coordinates; explicit Full mode for authenticated reveal records and bodies. Verification and coverage metadata remain separately committed.
- Shared groundwork for sparse transaction locators and scoped content dependency resolution, with transaction and Gateway peer stages gated.
- Related inscription locator controls in the Inscription inspector: optional enrichment during a build and a retroactive action. Both remain disabled in this first stage.
- Recovery when another process occupies the default Bitcoin serving port, with the actual port reported and explicitly configured addresses kept strict.

## Staged capabilities

Headers, Blocks and Inscriptions are the enabled indexes in this source stage. Related inscription transaction locator construction and the complete Transaction Index will be enabled in the transaction stage. Gateway peer locator requests and public serving will be enabled in a later, separately validated peer stage.

Inscription reveal evidence does not establish global numbering, current ownership, sat history or parent-child provenance. Recursive and delegated content needs a known location for each referenced inscription; missing locations and unavailable block bytes must remain visible. Indexing positions alone does not recursively fetch referenced content.

## Testing and limits

Use [the inscription tester guide](TESTING-0.7.2.md), [known issues](KNOWN-ISSUES.md), [privacy notes](PRIVACY.md) and [roadmap](GATEWAY-ROADMAP.md). Results for the new build will be recorded after validation. Existing release acceptance does not clear new executable bytes. Windows executables remain unsigned.
