# Gateway

Gateway is a Bitcoin block explorer that retrieves the blocks you ask for. Explore blocks, transactions, inputs, outputs and positions within outputs without downloading the whole blockchain. Bitcoin Core is optional.

**0.7.1 is a public testing release with a timeline workspace.** Its active indexes are **Headers** and **Bitcoin Blocks**. Additional indexes remain locked while they undergo further validation. See [current status](docs/STATUS.md) for validation and hosted delivery status.

The working source is **0.7.2**, preparing the inscription prerelease. It adds positional inscription indexing and an isolated content viewport; related transaction locators, the complete Transaction Index and Gateway peerhood remain gated. This source version has not replaced the published downloads or hosted update feed. See [the inscription test guide](docs/TESTING-0.7.2.md).

## Start here

Use the Windows installer for a managed installation, or extract a Windows/Linux **fresh** ZIP for a portable first run. On Windows, launch **Gateway On Demand**; `GatewayClient.exe` is the application, not the installer. Fresh packages include a validated header snapshot separately from the executable.

The installer has a dedicated Updates page. The default checks Gateway's hosted service automatically and lets you choose when to install. You can instead choose manual checks or explicitly enable automatic download and installation. Advanced source settings are optional.

Read [Getting started](docs/GETTING-STARTED.md) for setup, browser address entry and your first block. Packages are distributed through [GitHub Releases](https://github.com/Blockamoto/gateway/releases). Review [known issues](docs/KNOWN-ISSUES.md) before testing.

## Explore Bitcoin

Try `0.bitcoin` in Gateway's address field. It opens the genesis block. `0.0.bitcoin` selects its first transaction and `0.0.0.bitcoin` its first output. Positions are zero-based; suffix addresses put the containing block height on the right.

Headers establish the selected proof-of-work chain; they do not contain block bodies. Gateway fetches blocks from ordinary Bitcoin peers or optional local sources. Transactions inside a known block need no additional index. An unknown transaction ID may require a containing-block hint.

The Indexes workspace combines a zoomable timeline with Inspector and Block Explorer tabs. The playhead controls exploration; selected ranges prepare indexing work for explicit review and start. The schema, bounded Blocks jobs, source retention, peer controls and optional Windows browser integration remain available. [Indexing](docs/INDEXING.md) explains coverage and storage. [CLI.txt](CLI.txt) lists supported command-line examples.

## Connections and privacy

Ordinary Bitcoin serving is separate from Gateway-to-Gateway sharing. Bitcoin serving is enabled for new profiles; advertised coverage depends on the actual ready provider. Gateway sharing remains locked. Private cached objects are not made public by turning Private cache off later.

The Peers page uses aliases until you reveal an address. Aliases are a display convenience: direct peers can still see your IP address and requests. Browser search providers can receive bare `.bitcoin` text before the companion redirects it. See [Privacy](docs/PRIVACY.md).

## Scope and limits

Gateway verifies block identity, proof of work, transaction and witness commitments and selected-chain context. It does not replace Bitcoin Core's full script and UTXO consensus validation. Missing data is not proof of nonexistence, and a position inside an output is not proof of current ownership.

The inscription source stage enables Lean positional records and optional Full reveal records. It does not infer global inscription numbers, sat identity, current ownership or parent-child provenance. Standalone transaction-location, related inscription transaction locators, spender/address, Sat/Satline, Bitmap and **Bitmap: Terrain Claim Chains** features remain gated. Their presence in the source does not make them supported features. The [roadmap](docs/GATEWAY-ROADMAP.md) separates available work from future work without promising release dates.

## Test, contribute and build

- [Timeline tester guide](docs/TESTING-0.7.1.md), plus the [setup and browser QA checklist](docs/TESTING-0.7.0.md) for installation, browser registration and real `.bitcoin` address entry.
- [Release notes](docs/RELEASE-NOTES-v0.7.1.md), [updates](docs/UPDATES.md) and [header delivery](docs/HEADER-BASELINE.md).
- [Building](BUILDING.md), [development](DEVELOPMENT.md) and [contributing](CONTRIBUTING.md).
- [Security reporting](SECURITY.md).

Gateway is licensed under [MIT](LICENSE). Included third-party material retains its own terms in [Third-party notices](THIRD-PARTY-NOTICES.md).
