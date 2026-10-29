# Gateway 0.7.8 reviewed portable verification

This record concerns the final approved scope in `APPROVED-BRIEF-0.7.8.md`.
The earlier preview remains intact; its acceptance does not establish acceptance
of this revised artifact. Gateway main is not pushed or merged, no installer or
update delivery is promoted, and no application update is installed.

Base: Gateway `origin/main` `7e1d1d9b731b689586309df8f287316a614e1934`.
Canonical Bitmap main: `335958311eb1c8ce4d14c48bc5c21d23c7a4b134`;
README blob: `cbd10d75feac8636dd553c10ab52995e38e55514`.

## Standards repository

The authorized private repository rename completed, preserving repository ID
1406477590, privacy, history, default branch, permissions, and settings. New URL:
`https://github.com/Blockamoto/blocators`. Terminology changes are on the separate
`codex/blocators-terminology` branch, commit
`77257bc8fd478f3c05eec5d5dba0cc0f67e8f29a`. Main was checked unchanged at
`e3c4c93a92f8b454e8ddc3245a142e8e37191af1` after that branch update.
The changes preserve positional grammar, hash examples and conformance vectors.

## Backend and browser checks

Go 1.27.1, Windows amd64, `GO111MODULE=off`, `CGO_ENABLED=0`.
Focused canonical/v2 tests cover the 792435 origin, exact six/seven published
rules, frozen v1 hashes and unchanged readable heads, ID-free persisted Bitmap
records/parent receipts, evaluated empty outcomes, selected-chain replay and
exact-next/admission fences. Compiled offline native API checks pass for fixed
profiles, origin planning, disabled chains, custom authoring/comparison refusals,
and preserved legacy identity.

Sparse-ingestion checks cover complete local/cached bodies with Raw Off,
independent Values/Scripts-only explicit runs, sparse TXIO pending translations,
independent query/collection/deletion owners, deferred dependency bodies while a
state writer holds its lock, exact-next UTXO/Sats/Current Addresses, historical
empty scans, Full/Lean/conventional-ID choices, and late-Off publication fences.
Relationship/Coinbase checks pass for usable saved and Core routes, historical
creation identity, selected anchors, exact coinbase bytes, later availability
pages, cancellation, and no automatic locator-sidecar reconstruction.

Browser checks pass for Bitmap sibling rows/rules/limits/disabled controls,
TXIO backward/forward actions and paged availability beyond127, stale/anchor
rejection, direct same-block targets, shell navigation, Satpoints and numbered
Sats, workspace resources, immediate flow output rendering and hover/focus help,
and 27 pinned geometry vectors including zero/unknown/fee behavior. Narrow and
zoomed tooltip wrapping is checked. `go vet . ./internal/... ./packaging/...`
passes.

The broad `go test ./... -count=1 -timeout 20m` run is **not a pass**: its aggregate
limit expired while the existing 10,000-coin/321-transition UTXO maintenance
fixture was collecting pages. It also exposed obsolete expectations and real
integration fences, which were corrected and rerun in focused groups. The
remaining short UTXO and Bitcoin wire/protocol group passes (24.961s), as do
internal/cleanup after its unrelated random-TempDir-name false-positive was
fixed, plus datadir, updateapply, updatepublisher, updates, launcher and native
host packages. The large maintenance fixture and a complete race suite are not
claimed complete.

## Real executable checks

The separately owned QA executable used real Bitcoin P2P, with Core RPC/mounted
Core access, data serving, raw retention and application updates disabled. A
read-only header bootstrap was copied into its new profile, and it then received
fresh headers from a Bitcoin peer to height972920. Block792435 was fetched from
`100.6.76.66:8333`: 1,834,669 serialized bytes, 3,139 transactions, hash
`000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c`.
The receipt verifies P2P checksum, header hash/chain, proof of work, Merkle root,
transaction parsing and witness commitment; full consensus validation is false.

An explicit one-block District plus normal Transaction Locators shared run
completed at792435. Its worker counters report one source read/decode/extraction;
it did not index a genesis prefix. Windows locator-file writes took about127s.
Only the actual792435 coverage is claimed.

Actual browser checks use no mocked routes. They verify exact published rules,
desktop/narrow layout, disabled updates and no hidden index controls. They also
verify both direct `0.bitmap` and block0's sticky Bitmap tab reuse the existing
inscription viewer for `953i0.792435`, preserve district focus0, and render the
exact eight bytes `0.bitmap` from the separate isolated content origin. Browser
fetched content SHA-256 is
`df16aa5fafa5bbbc3036c992169cc287e704b1ea6d6b01aa7c71d8b3f34d81e2`, matching
the saved winner. The real check caught an empty legacy-panel collision; that
was fixed and the real check rerun successfully.

## Final portable acceptance

The final implementation source was committed locally as
`efa8cda0b157a721caf751be92f120cfebb5a414`. A final combined affected-backend
regression run passes (14.985s), followed by `go vet` and the final canonical,
approved-explorer and TXIO browser suites. The broad-run failures were corrected
and rerun; they are not silently treated as broad-suite success.

The separately named executable is
`build/portable-v0.7.8-reviewed/GatewayClient.exe`, using its own
`preview-profile`. Its SHA-256 is
`4b380d1d34a0eb15a39f045b2464f371034b3403d49250d5d20408d7c1e5a363`.
This exact executable passes the compiled native API check and both actual
portable browser scripts, with no mocked routes.

Its own fresh profile fetched both Bitcoin blocks792435 and972920 from
`101.108.106.56:8333`, checking P2P checksum, selected header/hash, proof of work,
Merkle root, transaction parsing and witness commitment. The recent block is
`00000000000000000000c4773f6fb7a15cf9d516a0afb95fda1e27adc3ca405f`,
1,576,260 serialized bytes and3,735 transactions. Core RPC/mounted Core access,
Raw retention and serving were disabled. The final executable's explicit
District-only792435 run completed with one source read, one decode and one
shared inscription extraction (858ms source,7ms apply). Its coverage is exactly
792435 and its district0 winner is `953i0.792435`. Browsing972920 afterward did
not advance across the missing Bitmap prefix.

Final actual-browser acceptance checks exact six/seven rules, separate limits,
sibling tracks/start792435, grey chain controls, updates disabled, transaction
count in the heading, desktop/narrow rendering and no hidden starts. A separate
actual check verifies direct0.bitmap and block0's sticky Bitmap tab, exact isolated
content bytes/hash and district focus0. Jobs, prepared choices and the completed
shared run remain unchanged by these read-only checks.

Final evidence is saved outside the archive under
`build/reviewed-qa/final-live`, `final-native-api`, `final-portable-browser`, and
`final-mapped-browser`. The older executable/profile/ZIP and running preview are
preserved. The new launcher prints its current loopback URL; restart may change
the port. Start/Stop operate only on the new profile and preserve its data.

The final ZIP is
`build/Gateway-v0.7.8-reviewed-portable-preview-windows-amd64.zip`. Its manifest
records the final local source revision, clean state and each shipped file hash.
Profile data, logs, screenshots, runtime tokens, installers and update helpers are
not included. No Gateway remote branch/main push, merge, release or update-feed
promotion was performed.

## Scope limits

Sparse positive records do not establish contiguous completeness or unspentness.
Stateful projections require exact predecessor state. Parcel child evaluation
needs referenced Sat context; a local evaluation receipt is not a portable proof
that independently reconstructs missing Sat history. Legacy state remains readable
under frozen identities. Chains are disabled, and future chain-retention,
reproduction, and index/outcome hashes are not implemented. This preview does not
claim full Bitcoin consensus validation or a completed mainnet Bitmap index.
