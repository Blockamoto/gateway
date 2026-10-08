# Gateway 0.7.0 status

The first public testing release is being prepared. The source targets **Headers and Bitcoin Blocks**, with the explorer/schema, ordinary Bitcoin serving, optional local providers, Windows browser access and signed updates. Additional indexes and Gateway sharing remain locked.

This repository's documentation describes the intended 0.7.0 behavior. It does not yet claim that release artifacts, live hosted delivery or renewal automation have passed final acceptance. Exact source/package hashes and test results must be recorded before publication. The repository remains private during preparation; its owner controls the public visibility change.

## Remaining release acceptance

- Build the exact release source and validate Windows/Linux packages, bundled notices, fresh startup and restart.
- Run native, browser, packaging and relevant race checks, keeping skips and platform limitations explicit.
- Verify installer update/source choices, real `.bitcoin` browser registration and data preservation in disposable profiles.
- Exercise ordinary Bitcoin block retrieval, bounded index work, privacy/serving controls and locked backend paths.
- Verify the live signed app/header feed, application install/restart and incremental headers against the published artifact hashes.
- Verify the renewal workflow and its failure visibility before calling renewal automatic.

Windows binaries remain unsigned; no general antivirus-clearance claim is made.

Read [release notes](RELEASE-NOTES-v0.7.0.md), [Getting started](GETTING-STARTED.md), [the QA guide](TESTING-0.7.0.md) and [the roadmap](GATEWAY-ROADMAP.md).
