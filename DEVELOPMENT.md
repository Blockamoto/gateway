# Development

The published baseline is Headers and Bitcoin Blocks. The working inscription stage adds positional indexing and content viewing, while transaction enrichment and Gateway peers remain gated. See [the roadmap](docs/GATEWAY-ROADMAP.md) and [index behavior](docs/INDEXING.md) before changing feature availability.

The Go application owns resolution, validation, local storage, peer connections and the local API. The browser UI presents that state. The Windows installer, native host, browser companion and update helper are separate entry points with narrow responsibilities. Use [BUILDING.md](BUILDING.md) for build and check commands.

## Working principles

- Keep block/header validation independent of UI and publisher claims. Signed metadata authenticates the publisher; it does not establish Bitcoin consensus validity.
- Enforce release feature locks in APIs, CLI entry points and background jobs as well as the UI. Opening a card must not start a scan.
- Use explicit bounded ranges in tests. Preserve committed records, header history and privacy classifications across restart and cancellation.
- Keep Bitcoin serving independent of Gateway sharing. Never turn a missing inbound block request into an unsolicited outbound fetch.
- Keep local providers read-only. Do not alter Bitcoin Core files or wallets.
- Protect update source/key authority, per-key sequence history and authenticated recovery. Apply installer preferences once, and keep later Settings choices authoritative.
- Make cancellation and failures visible. Colour, text, focus and switch position should communicate state together.

Use task-owned test profiles. Do not test installation, removal, DNS registration or update restart against someone's active profile. Networking tests must distinguish a controlled local peer from a live Bitcoin peer; browser tests must distinguish a fake native handshake from an authenticated real exchange.

Keep documentation aligned with the shipped behavior. Retained code for future indexes is not an available capability, and a future roadmap item is not a compatibility promise. Submit focused changes with relevant tests and explain untested platform paths. See [CONTRIBUTING.md](CONTRIBUTING.md).
