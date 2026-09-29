# Sparse indexes

Gateway distinguishes a definition (recipe and dependency graph), a provider
(where lower-level evidence can be obtained), and a local instance (the range
actually derived). The catalog never implies that every listed capability is
materialized. Unsupported historical numbering remains explicitly unavailable.

The first derivation executors are `inscriptions` and `bitmap`. A fresh Bitmap
instance begins at mainnet block 792435. Inscription occurrence scans may begin
at a chosen height. Each executor currently has one contiguous instance per
profile; resume with its original start height and a larger end height to extend
it. Multiple disjoint inscription instances are not yet implemented.

Build requests select an explicit end height or snapshot a verified tip. With no
Core and no local headers, the job requests the necessary independent Bitcoin
headers and reports `waiting` until it can anchor each block. It never treats a
peer's height-to-hash response as proof. A user-paused header sync or disabled
network remains paused; the job reports its unmet dependency.

Core is optional. The existing source pipeline uses local Gateway data,
read-only Core providers, ordinary Bitcoin block requests and Gateway fallback.
Each fetched body is checked for hash, proof of work, transaction Merkle root,
witness commitment and selected-chain membership before derivation. Independent
header anchoring is not full UTXO/script consensus validation.

## Source retention

- `ephemeral` adds no raw source-block cache; derived records and checkpoints
  remain. It does not delete pre-existing caches or external data.
- `cache` places newly fetched source blocks in the bounded Gateway cache,
  privately. Existing cached visibility is preserved.
- `retain` keeps fetched source blocks under Gateway's `indexes/sources`, outside
  cache eviction. Existing external Core/archive blocks are read in place and
  are not duplicated simply because they were used.

An index job does not silently enable unrelated graph/backfill indexes or change
global cache settings. Retention is applied prospectively to blocks processed by
that request; changing it does not retroactively refill or delete earlier bytes.

## Checkpoints and recovery

Each block produces an immutable batch. Its commitment binds the definition,
rule hash, network, range start, height, block hash, dependency profile, previous
commitment and canonical record hash. Empty optional arrays have one canonical
representation, including blocks with no matching candidates. Provider names, local verification labels
and retention choices are excluded from semantic fingerprints.

The batch is persisted before atomically replacing the selected head. An orphan
batch left by interruption cannot advance coverage. Restart validates the linked
history and resumes at the next block. Corrupt or incompatible checkpoints fail
closed. Interrupted jobs become paused rather than silently restarting expensive
work. The profile's existing operating-system lock prevents competing processes
from writing the same index. CLI pause requests bind to one persisted job ID.

Before continuing, the engine checks its previous anchor against the selected
chain. An actual disagreement rewinds to a common committed checkpoint; an
unavailable provider does not trigger a destructive rewind. Old branch batches
remain for audit but are not selected coverage. Queries expose whether their
checkpoint is on the selected chain, unavailable or stale.

## Bitmap profile boundary

The Gateway base profile uses chronological reveal order and a canonical raw
ASCII decimal body followed by `.bitmap`. It does not infer global inscription
numbers, ownership, boundness, curse classification or historical indexer
agreement. MIME/flags do not filter this explicitly named base interpretation;
delegates and compressed content are not resolved. The theory and pinned OPI
implementation differ in details: see [Bitmap research](BITMAP-RESEARCH.md).

First eligible claim wins; later candidates retain a rejection reason and the
earlier winner. Districts cannot refer to future Bitcoin heights. Compatibility
metadata is separate and cannot rewrite base results. A real historical
conflicting-winner fixture remains an open acceptance requirement; synthetic
rule tests are not presented as historical proof.

Queries also expose a pinned OPI necessary-condition comparison. An absent or
incompatible MIME can establish candidate exclusion under that profile; a
matching MIME yields `historical_context_missing`, never invented agreement or
an alternative winner. This comparison is computed separately from immutable
base records. Synthetic differing-MIME tests do not satisfy the historic-case
acceptance requirement. Bitmap output schema 2 records MIME and parser flags
needed for this inspection.

## Validation so far

Focused tests cover provider reporting, ordered extraction, malformed and bounded
inputs, real origin-block extraction, deterministic commitments across provider
labels, collisions, restart, orphan writes, corruption, reorg rewind, unavailable
anchors, durable pause identity and explicit source-retention choices over a
loopback ordinary-Bitcoin transport. The origin fixture test does not perform a
live mainnet header sync. Public-network zero-state acceptance is tracked
separately from these deterministic tests.
