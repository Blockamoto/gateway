# Contributing

Start with [Getting started](docs/GETTING-STARTED.md), the [roadmap](docs/GATEWAY-ROADMAP.md) and [development notes](DEVELOPMENT.md). The inscription stage builds on Headers and Blocks; transaction enrichment and Gateway peers remain gated. Discuss an expansion of that scope before implementing it.

For a bug, include the version, operating system, browser where relevant, minimal steps, expected result and actual result. State whether Core is configured and whether the issue occurs after restart. A short redacted log or screenshot is more useful than an entire profile. Use [GitHub Issues](https://github.com/Blockamoto/gateway/issues) for ordinary bugs and [SECURITY.md](SECURITY.md) for sensitive reports.

For a code change, keep the patch focused, add meaningful regression coverage and run the relevant checks in [BUILDING.md](BUILDING.md). Explain behavior and validation in the pull request. Use disposable profiles; preserve user data, credentials and consent. Document platform or fixture limitations honestly.

Do not commit profiles, private signing keys, access tokens, service credentials, downloaded build artifacts or personal machine paths. Keep upstream attribution and license files with copied material. Gateway contributions are distributed under the [MIT license](LICENSE); third-party material retains its stated terms.
