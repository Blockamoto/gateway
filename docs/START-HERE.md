# Gateway: start here

Sparse by default, deep by request. One headless Bitcoin-derived knowledge engine with CLI and GUI interfaces. Top-down intent, bottom-up dependency resolution. Bitcoin evidence anchors knowledge; peer responses remain claims until checked.

Read [STATUS](STATUS.md), then the [master implementation brief](MASTER-IMPLEMENTATION-2026-09.md). The September master brief supersedes earlier audit-only constraints and the temporary plan to remove Bitcoin discovery or automatically add libp2p. Preserve the existing stack and evolve it. The separate gateway-bitcoin-discovery repository is a research lab, not the main product.

Priority: indexing, P2P capability/serving, discovery, Probe. Core/Ord are optional read-only providers; never alter external data or wallets. Ephemeral source data, cache and retained Gateway-owned data are independent of derived index retention. Possession never implies publication.

Build/test: see BUILDING.md at the repository root; current code uses standard-library Go with GO111MODULE=off and CGO_ENABLED=0. Build script: scripts/build-release.py. Run native tests against a freshly built runtime using GATEWAY_TEST_BINARY. Known Windows symlink fixture limitations must be reported, not mistaken for passing tests.

Protocol specifications: spec/gateway-protocol.md and spec/bod-protocol.md. Historical evidence: docs/audits/2026-09-26. Until v1.0.0, integrate and validate coherent slices, update STATUS, commit and push directly to main; versioned prereleases at meaningful runnable checkpoints.
