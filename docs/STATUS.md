# Gateway 0.7.0 status

The **0.7.0 release artifacts have passed local validation and final runtime-source Linux CI**. It enables Headers and Bitcoin Blocks, with the explorer/schema, ordinary Bitcoin serving, optional local providers, Windows browser access and signed updates. Additional indexes and Gateway sharing remain locked.

The repository remains private during preparation; its owner controls the public visibility change. Public hosted delivery and a live renewal run are still pending that change and release publication. The [acceptance record](RELEASE-ACCEPTANCE-0.7.0.json) separates completed artifact evidence from public hosted checks still required.

## Completed validation

- Initial source `38e96fcf38bb76739787a8c731df697e5118f92f`: Windows Go suite **817 passed, 54 expected skips**, Go vet and six JavaScript helper suites passed.
- [Linux CI on the final runtime source](https://github.com/Blockamoto/gateway/actions/runs/37808706709): all 14 stages passed, including Go tests, vet, race, packaging, companion/helpers and actual browser UI regressions.
- Focused packaging: nine regression tests and four native installer-wrapper fixture suites passed. These use disposable inert payloads; they do not represent a real system installation.
- Release build and binary audit passed with **969,480 headers**, the full snapshot external to the client, matching installer payloads and bundled license notices.
- A native Windows automatic update from 0.7.0 to a **synthetic local 0.7.1 fixture** passed download, verification, install, restart and settings/header preservation. Tampered bytes were rejected. This used a local test publisher, not the production service.
- Five exact Windows files, including the installer, had no threats reported by local Defender diagnostic scans using unchanged definitions **1.459.601.0**. The binaries remain unsigned; this establishes neither cloud/download clearance nor acceptance on other machines.
- The public publisher download adjustment passed focused tests and independent review. It uses the approved release's direct bundle URL instead of depending on anonymous GitHub REST requests, while retaining signature and content verification.

## Before publication and live acceptance

The final runtime source passed Linux CI, and the rebuilt artifacts passed binary/payload audits, native automatic update acceptance and exact-file local Defender scans. The acceptance record identifies the tested source and scanned files; the release checksums identify the packaged downloads.

The dedicated endpoint is allocated at `https://gateway-updates.onrender.com`. The renewal environment is restricted to `main`, its signing/deployment secrets are provisioned, and the weekly workflow is committed. This is prepared infrastructure, not a completed live renewal. After the owner makes the repository public and release assets are available, verify hosted app/header delivery, the application update path and a manual renewal run through that workflow.

The [renewal operator guide](../packaging/update-publisher/RENEWAL.md) describes the approved-release policy, failure reporting and recovery. [Feed status](UPDATE-FEED-STATUS.json) records the current publication/renewal state.

Read [release notes](RELEASE-NOTES-v0.7.0.md), [Getting started](GETTING-STARTED.md), [the QA guide](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
