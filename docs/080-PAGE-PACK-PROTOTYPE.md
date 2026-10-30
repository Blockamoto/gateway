# Private immutable page-pack prototype

Status: isolated experiment in `recipe_page_pack_experiment.go`, not wired into `ordinalTree`, staged checkpoints, any scheduler, public store or migration. Existing state generations and all existing page roots remain untouched. It has no measured end-to-end performance benefit.

## Current staged-store behavior

`recipeStageStore.tree` uses `ordinalTree`: each canonical JSON page is independently SHA-256 addressed, stored under a hash-derived physical path, path-checked and checksum/structure-validated on first uncached read. A flush syncs changed page directories before the staged chunk checkpoint references the new roots. Old roots and their immutable pages remain readable across interrupted stage work. This design is simple to authenticate but can incur many path checks and file opens on Windows.

## Experimental format

A single bounded pack starts with the eight-byte magic `GPSPACK1`, a big-endian page count, then sorted unique `(32-byte SHA-256, 32-bit big-endian length)` entries and the concatenated original page bytes in that order. Offsets are implicit and contiguous, so no offset overlap or arithmetic-controlled seeking is accepted. The whole encoded file is addressed by its SHA-256. Maximum input and pack extent are each 16 MiB, at most 256 input pages, and each page remains subject to the existing 8 MiB ordinal page bound. Aggregate input bytes are checked before deduplication so repeated large pages cannot evade work limits.

Opening one pack uses one checked physical path/descriptor, bounds and reads the whole file, checks its complete digest, parses the bounded directory, checks sorted uniqueness, exact extents and trailing bytes, then verifies every original page digest and the same ordinal page structural rules. The in-memory reader returns a copy of the exact original bytes. It never infers selected-chain, state, checkpoint or publication authority from pack integrity. Loading eagerly authenticates every page, which can increase read amplification; a real integration needs operation-scoped reuse under a strict memory budget and actual stage benchmarks.

## Publication and recovery boundary

The caller supplies an already existing, durable, application-owned pack directory and is responsible for its ancestor directory durability. The prototype writes a temporary file, flushes it, links it immutably under its content hash, and syncs the pack directory before returning the ID. Existing files must match and verify; they are never overwritten to repair corruption. Errors or cancellation after linking may leave a valid unreachable pack; cancellation during a filesystem call has an uncertain physical outcome. Temporary files left by process termination are not published. The prototype does not publish or update a stage checkpoint, so a caller must not treat an unreferenced pack as committed state.

The interruption tests inject failures after writing, syncing and linking, then retry and verify exact immutable contents. They exercise logical recovery boundaries and orphan handling, not a real power-cut or a filesystem durability certification.

## Requirements before staged-store integration

- Design an immutable authenticated page-hash-to-pack/extent mapping whose root is bound to each staged chunk/checkpoint. A mapping must preserve direct access to previously published page roots, including roots that predate a pack. Verify both mapping and requested page identity; detect duplicates, collisions, corruption and missing pages without substituting an empty state.
- Flush every new pack, mapping page and necessary directory ancestor before atomically publishing a chunk/checkpoint that references them. A failed write must leave the prior checkpoint and its full root graph readable. Coordinate GC, interrupted staging and readers before removing any old page; no implicit conversion or deletion.
- Bound the mapping, opened-pack cache, concurrent operations and cancellation latency. Measure read amplification, number of opens, bytes read/written and peak memory for realistic growing staged commits on Windows, including both cold and hot access. A format-only test or one-pack round trip does not establish an append-throughput improvement.
- Compare state roots, first-before images, net changes, restart/replay and reorg behavior with the existing page-per-file staged store under failure injection and actual process interruption before enabling an integration.
