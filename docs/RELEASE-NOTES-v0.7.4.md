# TXIO and serialized block bodies

Gateway 0.7.4 adds the Transaction Input/Output index, durable Bitcoin serialized block bodies and focused workspace improvements. It is prepared for publication as a prerelease. The acceptance record distinguishes completed checks from final hosted promotion.

## Input and output navigation

The **TXIO index** stores one committed spend relationship set with automatic lookup in both directions: an input links to its original output, and an output links to its spending input. Positions are zero based; coinbase is transaction zero. Known positional navigation needs no transaction-hash locator. Sparse missing creation evidence remains unresolved, and an unknown spend is never labeled unspent.

Both endpoints are checked against the selected chain. Restart and private accelerator rebuild use immutable canonical facts; reorganization withdraws disconnected relationships. Saved links remain useful after source-body eviction, although opening the target may still need its verified block bytes. The compatible internal identifier remains `txo-spender`. See [the schema and measured overhead](TXIO-INDEX.md).

## Block storage and migration

New Bitcoin Blocks records keep ordered transaction IDs, a compact body receipt and a durable verified Bitcoin serialized raw block. Builders, BPS navigation, inscriptions and JSON inspection share temporary decoding. Blocks continue supplying transaction-location knowledge independently of the Transaction Index.

Automatic compatible migration stages a separate versioned root and verifies replacements before activating it. Interrupted conversion resumes without discarding usable legacy evidence. Old committed batches, decoded cache JSON and hex copies are retained; this version does not delete them automatically. Existing profiles therefore do not reclaim those duplicate bytes during migration. Full selected inscription content and Lean positional records remain distinct.

## Workspace

- Input flow and output details navigate through saved TXIO links first, with explicit verified fallback when needed.
- Map controls use a reserved toolbar and remain accessible in narrow panels.
- The inscription potential-range ghost follows the catalog's suggested mainnet start, **767430**, while explicit earlier ranges and coverage remain valid.
- The newer same-block hierarchy navigation correction from local 0.7.3 is included.

Gateway peerhood, address, Sat/Satline, Bitmap and later feature stages remain locked. A known relationship is not a claim of current ownership, complete history or locally available bytes. Broader Details/Inspector redesign and destructive index cleanup remain separate work.

## Delivery and validation

The intended delivery path is the existing Gateway signed update channel. Eligible automatic-install profiles can receive this approved prerelease normally; check-only, manual, offline, custom-source and explicit opt-out preferences retain their behavior. Publisher signatures, validity, sequence, package digests and compatibility checks remain effective. Render automatic deployment is enabled, but signed release approval remains a separate publication step.

See [the acceptance record](RELEASE-ACCEPTANCE-0.7.4.json), [tester guide](TESTING-0.7.4.md), [status](STATUS.md), [update policy](UPDATES.md) and [indexing](INDEXING.md). Windows executables remain unsigned; local antivirus diagnostics do not establish clearance on every other machine.
