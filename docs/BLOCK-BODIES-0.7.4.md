# Durable Bitcoin Blocks bodies in 0.7.4

New Blocks commits store a verified Bitcoin serialized block and a compact
ordered transaction-ID list. The position in that list is the zero-based
transaction position, including coinbase at position zero. This preserves the
transaction-location knowledge supplied by Blocks without requiring a separate
Transaction Index. Complete decoded input/output/script/witness arrays are
temporary views for builders, queries and JSON inspection/export.

## Files and compatibility

- Durable serialized body name: `indexes/blocks/bodies-v2/<first-two-hash-digits>/<block-hash>.block`.
- Small rebuildable inventory: the same path plus `.receipt.json`, containing
  schema 2, height, hash and the body receipt.
- Commit payload: the existing Bitcoin record with `body_schema: 2`,
  `verifier_version`, ordered `txids` and `raw_body`. The receipt contains
  `format: "bitcoin-serialized-block"`, SHA-256, byte length and the exact
  80-byte header encoded as hex. The durable `transactions` array is absent.
- Migrated commit/head root: `indexes/blocks/v2/`. A durable
  `indexes/blocks/storage-v2.json` selects it only after the full replacement
  has passed readback. Its schema 2 record names root `v2` and the source
  legacy commitment.
- Migration journal: `indexes/blocks/body-migration-v2.json`. Index status
  exposes it as `block_body_migration`, including state, source head, converted
  count, total raw bytes including resumed blocks, legacy commit bytes, legacy
  cache JSON/hex file counts and any deferral error.

Checkpoint envelopes, definition identifiers and their existing rules remain
compatible. The updater's existing global index/storage schemas and trust checks
are unchanged; the independent `block_body_schema: 2` describes this payload.
Legacy decoded Blocks batches remain readable. Frozen publications continue to
read their original immutable commitments; a later publication can select the
new versioned root.

Body reads recheck the selected header, block hash, proof of work, ordered
transaction Merkle tree and witness commitment. Receipt length/SHA-256/header
checks cover the exact serialized bytes. The compact record also verifies its
header and ordered IDs against its committed checkpoint/Merkle root. A query
which temporarily decodes a body reports
`body_state: "verified_raw_decoded_on_demand"`; missing bytes remain
`"unavailable"` while saved locations remain usable.

## Automatic migration and recovery

Startup runs migration before live index supervision. Opening Blocks also
retries deferred migration. The profile lock, migration mutex and Blocks
commit/reconcile guard coordinate writers. A store captured before a root
switch must reopen rather than publish into the retained old root.

Migration inventories the legacy commit chain and JSON/hex cache duplicates.
It reconstructs exact serialized bodies when the legacy decoded fields permit
it, restoring coinbase's zero-hash/ffffffff previous output and canonical
CompactSize script/witness lengths. Transaction identity, witness identity,
canonical sizes, order and total values must round-trip through the parser.
Existing verified durable/retained/cache raw bytes can recover incomplete
display fields. An unverified cache or a pruneable mounted Core reference alone
cannot replace formerly durable evidence.

Each verified replacement body is stored and read back before its compact
immutable batch and head are committed under the staged root. Interrupted
attempts resume from that root, but every resumed body is reverified and repaired
from retained source evidence if missing or damaged. A final full-prefix readback,
selected-chain check and unchanged-source-head check precede publication.
Source mode, retention and head flags are persisted even when the entire prefix
was already staged.

Missing/unreadable selected-chain authority defers migration without treating it
as a reorg or rewinding a valid progress cursor. An actual selected-header hash
mismatch or incompatible source range withdraws the staged cursor while keeping
its immutable files. Missing sources, reconstruction failures, cancellation or
destination write/space errors leave the old visible root and all old evidence
in place. No partial replacement is advertised as a complete migrated index.

Durable raw retention is separate from ordinary cache eviction. A later verified
refetch can restore an evicted/damaged committed body after matching its receipt.
Private index retention never becomes an automatic serving provider and does not
shadow a separately eligible public cache/archive/Core provider. The timeline records
present raw possession separately from saved index locations, filtering bodies
against the selected headers; full integrity is rechecked when used.

New Gateway-owned raw writes reuse a completely verified existing object through
filesystem hard links. The durable name, `blocks/raw/<height>-<hash>.block` cache
name and optional `indexes/sources/<hash>.block` retained-source name can therefore
refer to one physical body allocation. Their separate catalogs preserve original
provenance and sharing choices. Removing a cache name does not remove the durable
name or its bytes; cache capacity continues to measure the logical cache contents.
Writes and repairs replace a name atomically rather than modifying a shared inode.
Existing valid independent copies are kept intact instead of being rewritten for
deduplication. Core mounts and external symlinks are never hard-link candidates.

NTFS and Linux filesystems with hard-link support use this sharing path. FAT/exFAT,
some network filesystems, unavailable/read-only candidates or a concurrent cache
unlink can prevent reuse. The safe fallback writes and flushes an independent raw
object, preserving offline durable availability. A cache plus durable name then
has two body allocations; additionally requesting retained-source storage can
make three. This fallback is raw-byte duplication, not a single-copy result.
Damage to a shared object can affect all its linked names; full read verification
and verified reconstruction/refetch remain necessary.

## Disk behavior and limits

Migration does **not** delete or rewrite legacy commits, decoded cache JSON, hex
copies or other usable old evidence. General index deletion and duplicate cleanup
are outside this pass. Existing profiles can temporarily use more space during
conversion. New writes avoid permanent decoded arrays; reclaiming old duplicate
space still requires a future opt-in cleanup which proves durable replacement
for every file it removes.

The regression fixture using real mainnet genesis and block 1 measured 4,196
bytes of legacy durable commit JSON versus 2,534 bytes of compact commit JSON
plus 500 bytes of serialized bodies and 892 bytes of inventory receipts.
Head/journal/private locator receipts and filesystem allocation are additional;
the measurement is illustrative, not a whole-chain savings promise. All legacy
decoded/hex evidence and existing independent raw copies remain after migration;
new raw names may link to an already present verified cache body. Lean inscription coordinates and Full
selected inscription content/envelope records are unchanged.

The real genesis body is 285 bytes. The sharing regression measures cache plus
durable names as 570 logical bytes but 285 unique serialized bytes (`os.SameFile`
confirms one object); with the retained-source name it is 855 logical bytes and
still 285 unique serialized bytes. Its forced unsupported-link case measures
570 unique serialized bytes for two independent objects, or 855 with the additional
retained-source name. The Windows validation machine's C: volume is NTFS and
passed the shared-object checks; its D: volume is exFAT and exercises the
independent-copy fallback. Directory entries,
receipts, allocation rounding and legacy representations are additional.

Focused checks cover byte equality for independent historical bodies, coinbase
encoding, witness/CompactSize boundaries, cold offline restart, cache eviction
and verified refetch, missing/corrupt resumed bodies, full-stage/source-extension
resume, reorg during conversion, unavailable authority, legacy fallback,
frozen publication, private/public serving and timeline possession. Shared-object
checks additionally cover every retention policy, cache unlink, atomic repair,
unchanged existing copies and the independent-copy fallback.
