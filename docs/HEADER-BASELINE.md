# Header snapshots and incremental delivery

Headers are Gateway's foundational index. Fresh-install packages carry a validated mainnet snapshot at `bootstrap/headers-mainnet.bin`, with adjacent metadata. The Windows installer writes the same external files. The application executable and application-update ZIP do not contain the full snapshot.

## Building a snapshot

Supply a completed genesis-first mainnet header file with `scripts/build-release.py --headers-baseline PATH`. Tooling captures exact bytes, count, tip hash, SHA-256 and capture time. A pending append marker rejects an incomplete source. `SOURCE_DATE_EPOCH` can fix capture metadata for reproducible input handling.

The built runtime's `-verify-release-bootstrap` checks linkage, proof of work, difficulty/retarget, median-time-past and future-time rules in a disposable directory without opening a user profile or starting networking. Malformed snapshots fail the build. Development builds must explicitly use `--without-header-baseline`.

Package roles are separate:

| Package | Purpose |
| --- | --- |
| Application-only ZIP | Signed app update; no full snapshot |
| Fresh ZIP | Portable first installation, including external snapshot |
| Windows installer | Managed first installation, including external snapshot |
| Header ZIP | Snapshot and metadata for publisher-side chunk generation |

A snapshot's exact tip and capture time belong in its metadata and release evidence. Its inclusion does not claim it is the current Bitcoin tip.

## Import and catch-up

Packaged data only extends matching local history. It never replaces an ahead or divergent saved chain. Recoverable append batches preserve a validated resume boundary after interruption. A checksum identifies bytes; it is not an independent consensus trust anchor.

Ordinary Bitcoin peers provide the remaining tail and inform chain selection. Headers do not contain transactions and do not mean block bodies have been downloaded.

## Signed header feed

The publisher validates the selected GitHub release's source identity and header artifact, then divides the snapshot into deterministic compressed chunks of at most 10,000 headers. A separately domain-signed Ed25519 manifest binds network, sequence, issue/expiry times, source release, total count/tip and each chunk's range, anchor, length and digest.

Clients use an independently provisioned public key. App and header sequences are separate; the highest verified sequence and same-sequence digest are preserved per key. Header metadata expires within the client's maximum validity window of 31 days. Size and expansion bounds apply before import.

Only chunks intersecting the missing suffix are fetched. A chunk overlapping saved data is compared exactly before appending. Already complete chunks and an already-ahead store need no downloads. Every appended header still passes normal validation. A mismatched anchor preserves local history and leaves ordinary peer synchronization available.

Configured checks also check signed header delivery in the background; Settings has no separate header-update button. Header pause and outbound-network disable prevent acquisition. A header-feed error does not block app updates or ordinary peer synchronization. Reaching the publisher's tip does not establish that the Bitcoin network has no newer headers.

See [updates](UPDATES.md) for source trust and operational renewal status.
