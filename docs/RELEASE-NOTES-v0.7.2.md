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

Use [the inscription tester guide](TESTING-0.7.2.md), [known issues](KNOWN-ISSUES.md), [privacy notes](PRIVACY.md) and [roadmap](GATEWAY-ROADMAP.md). The exact Windows build passed 921 Go checks with 46 expected skips and no failures, native viewport/isolation checks, real Bitcoin positional lookup without a transaction index, and a local signed automatic upgrade from 0.7.1 with settings and all bundled headers preserved. The final installer and four app components had no threats reported by local Defender diagnostics with unchanged definitions. These checks do not establish hosted delivery, system installation, global identity evidence, or cloud/other-machine antivirus clearance. [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/38054908829) passed build, tests, static checks, race, helper, packaging and browser checks. Windows executables remain unsigned. See [this build's acceptance record](RELEASE-ACCEPTANCE-0.7.2.json).

## Additional review checks

- Scrubbing the block playhead debounces Block Explorer loading. Inscription marker/address navigation loads the viewport separately; rapid changes must not let obsolete content replace the current selection.
- Explicit local Ord adapter answers remain provider-reported. They must not acquire native witness, selected-chain, numbering or ownership claims. Renderer fixtures and real Bitcoin reveal verification are separate checks.
- Use **Inspect saved storage** to compare Lean and Full records, retained source data and the separate content cache. The report counts encoded file bytes within its stated traversal/read limits, shows partial or unavailable results honestly, and must not fetch data or start indexing.
- Delegated preview content may follow its target; raw/download actions should preserve the requested inscription's own body.
- If the default Bitcoin serving port is busy, automatic fallback chooses a different available port when Gateway LAN discovery is inactive. An explicitly configured address remains strict.
