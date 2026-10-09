# Gateway 0.7.1 status

Gateway **0.7.1 is prepared for public testing; publication and hosted update activation are pending**. The tested build replaces index cards with a timeline workspace and brings Inspector and Block Explorer tabs into its upper pane. Headers and Bitcoin Blocks remain the enabled indexes. Additional indexes and Gateway-to-Gateway sharing remain locked.

The [acceptance record](RELEASE-ACCEPTANCE-0.7.1.json) distinguishes completed runtime validation from the remaining package and delivery checks.

## Completed validation

Validation on **9 October 2026** used runtime source `e508085f34c406f41ded7eadbc33d5ee0ab4324b`.

- Windows Go suite: **858 passed, 54 expected skips, no failures**. Windows and Linux development payloads built successfully.
- [Linux CI](https://github.com/Blockamoto/gateway/actions/runs/37944091190): all 14 stages passed, including Go tests, vet, race, helper, packaging and browser checks.
- Real-data testing fetched and retained blocks **840000, 840001 and 850000** from ordinary Bitcoin peers with Core RPC and mounted block sources disabled. Headers synchronized beyond the bundled snapshot. The stored blocks appeared as two exact coverage ranges and survived restart.
- Native browser checks used **no API mocks**. They exercised block 840000's 3,050 transactions, pagination, a known transaction, correct previous-output index zero, range selection, independent playhead movement, delayed scrubbing and narrow layout. Browsing and modified selection did not start indexing jobs.

## Publication and hosted delivery

Final release packaging, exact-file installer checks, uploaded asset verification and publication remain pending. Windows executables remain unsigned. Completed runtime checks do not establish antivirus or browser-download clearance for the final installer or other machines.

The existing public update service is `https://gateway-updates.onrender.com`. It still serves the approved 0.7.0 release until the 0.7.1 signed delivery bundle is accepted and activated. **No live 0.7.1 update or fresh-install acceptance is claimed yet.**

Weekly renewal is configured for the explicitly approved release; it does not automatically approve a newer version. The [renewal operator guide](../packaging/update-publisher/RENEWAL.md) explains that policy and recovery. [Feed status](UPDATE-FEED-STATUS.json) records the existing feed state. The earlier completed public release checks remain in the [0.7.0 acceptance record](RELEASE-ACCEPTANCE-0.7.0.json).

Read [release notes](RELEASE-NOTES-v0.7.1.md), [Getting started](GETTING-STARTED.md), [the timeline tester guide](TESTING-0.7.1.md), [the setup/browser checklist](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
