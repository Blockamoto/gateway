# Gateway 0.7.0 status

Gateway **0.7.0 is published, and its hosted updater and renewal workflow have passed live acceptance**, alongside local validation and final runtime-source Linux CI. It enables Headers and Bitcoin Blocks, with the explorer/schema, ordinary Bitcoin serving, optional local providers, Windows browser access and signed updates. Additional indexes and Gateway sharing remain locked.

The [repository and testing release](https://github.com/Blockamoto/gateway/releases/tag/v0.7.0) are public. The dedicated update service is live at `https://gateway-updates.onrender.com`. The [acceptance record](RELEASE-ACCEPTANCE-0.7.0.json) records the checks and their limits.

## Completed validation

- Initial source `38e96fcf38bb76739787a8c731df697e5118f92f`: Windows Go suite **817 passed, 54 expected skips**, Go vet and six JavaScript helper suites passed.
- [Linux CI on the final runtime source](https://github.com/Blockamoto/gateway/actions/runs/37808706709): all 14 stages passed, including Go tests, vet, race, packaging, companion/helpers and actual browser UI regressions.
- Focused packaging: nine regression tests and four native installer-wrapper fixture suites passed. These use disposable inert payloads; they do not represent a real system installation.
- Release build and binary audit passed with **969,480 headers**, the full snapshot external to the client, matching installer payloads and bundled license notices.
- A native Windows automatic update from 0.7.0 to a **synthetic local 0.7.1 fixture** passed download, verification, install, restart and settings/header preservation. Tampered bytes were rejected. This used a local test publisher, not the production service.
- Five exact Windows files, including the installer, had no threats reported by local Defender diagnostic scans using unchanged definitions **1.459.601.0**. The binaries remain unsigned; this establishes neither cloud/download clearance nor acceptance on other machines.
- The public publisher download adjustment passed focused tests and independent review. It uses the approved release's direct bundle URL instead of depending on anonymous GitHub REST requests, while retaining signature and content verification.

## Hosted acceptance and renewal

The final runtime source passed Linux CI, and the rebuilt artifacts passed binary/payload audits, native automatic update acceptance and exact-file local Defender scans. The acceptance record identifies the tested source and scanned files; the release checksums identify the packaged downloads.

An isolated native Windows client with a synthetic older version automatically downloaded the exact public 0.7.0 package, installed and restarted while preserving settings and the full saved headers. A separate fresh 0.7.0 client automatically checked both public feeds without manual publisher setup. Its validated snapshot prefix remained unchanged while ordinary peers appended 1,034 headers. The synthetic older fixture was never published.

Independent public HTTPS verification passed against the renewed feeds at application sequence 3 and header sequence 2: both application archives and all 97 header chunks, totalling 99 complete downloads with signature, size and content checks. Application and header content remained unchanged. The service was warmed before acceptance; idle cold-start latency was not measured.

The weekly renewal workflow is configured for Mondays at 06:17 UTC. Its [manual commissioning run](https://github.com/Blockamoto/gateway/actions/runs/37816973175) passed: the renewed manifests were published and matched the live service. The current application sequence is **3**, the header sequence is **2**, and both expire on **7 November 2026 at 17:30:26 UTC**. Future scheduled runs still depend on functioning permissions and hosting; failed or disabled workflows need operator attention.

An initial upload negotiation failure left the feeds unchanged. Its correction passed regression tests, review and [full Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37815970248) before the successful commissioning run; details are in the acceptance record.

The released package and `v0.7.0` tag remain at `e7c81ea0aa059e700881eff3fd7f62c3506645f0`; validated runtime source is `98d4b508c23deb11287c42223db7c0495b79b717`. The later operator fix at `6f2c17b8d2c8ed9328f26683d989e12bea86a5bb` changes feed publishing, not the released application or header content.

The [renewal operator guide](../packaging/update-publisher/RENEWAL.md) describes the approved-release policy, failure reporting and recovery. [Feed status](UPDATE-FEED-STATUS.json) records the current publication/renewal state.

Read [release notes](RELEASE-NOTES-v0.7.0.md), [Getting started](GETTING-STARTED.md), [the QA guide](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
