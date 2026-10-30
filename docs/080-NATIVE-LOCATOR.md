# Native TX ID Locators recipe conversion

The native TX ID Locators derivation executes `recipes/builtins/tx-locator.json`
through the common recipe interpreter. The compact physical adapter validates
the resulting transaction rows and encodes their ordered positions; it does
not derive them from an index-name opcode. Both sparse browsing and historical
native ingestion use this path, including the older prefix payload layout.
The executable document appears in index settings and can be copied into a
separate guided custom draft without saving, arming or starting it.

## Compatibility and authority

The physical payload, checkpoint bytes and native reverse-query implementation
are retained. Exact output equivalence is pinned to semantic fingerprint
`509d0084357660b8168611045fa2f05c3fccef619ec406436a3925168b9389ac`
and the explicit frozen v1/v2 native rule hashes in
`recipe_builtin_locator.go`. Opening, reading or committing a native generation
checks that binding. A changed recipe, parser or interpreter profile cannot
reuse that identity without an explicit migration decision. Display recipe
fields are added after computing the frozen native rule hash and are excluded
when reconstructing its legacy v1 definition.

Native source admission retains the existing selected-header and verified
decoded-inventory contract. Parser-owned raw receipts are rechecked when
present. A legacy decoded inventory is not upgraded to raw-byte proof merely
because the recipe consumes it. Recovery tests with synthetic already-admitted
decoded inventories explicitly discard unrelated raw receipts after relabeling
their fixtures. New externally supplied custom recipe sources still require
their authenticated complete bytes. Existing publication, consent, reorg and
physical-store guards remain necessary after evaluation.

## Validation and performance evidence

The frozen 0.7.9 derivation exists only in tests. Comparisons cover genesis,
block 1, synthetic positional vectors and the committed 0.7.9 JSON oracle;
the optional local block 908657 fixture compares all 3,113 transactions through
both custom rows and the native physical adapter. Fixture-only source admission
does not establish selected-chain membership or full consensus validation.
Native locator recovery/packed-store/lookup/timeline tests passed in 117.805s;
the additional restart/store/integration and real-fixture pass took 232.374s.
The later shared row-sink refactor passed the combined recipe, saved-recipe,
coordinator, timeline, native locator, store and integration suite in 504.359s,
with the optional block 908657 fixture enabled. Browser editor and workspace
checks passed as well.

`BenchmarkRecipeBuiltinLocator` compares the same parsed real block 792435
(3,139 transactions). Parsing and disk fixture loading are outside timing.
The 0.7.9 oracle is the old projection loop; the new production path additionally
rechecks its private source receipt and Merkle inventory. A short 20-iteration
run measured 19.6 microseconds and 82,020 allocated bytes for the oracle versus
44.6 milliseconds and 8,764,738 bytes for the materializing recipe path.
A reusable interpreter row sink removed the full block row map and a duplicate
JSON encoding. Three further 20-iteration samples measured 26.0–26.4 milliseconds
and about 6.67 MB allocated per block. The fixed loop remained 12.4–16.4
microseconds in those samples. These small warm measurements are not full-chain
throughput or a general speed ratio; dynamic expression/JSON work still has
material CPU and allocation cost. The sink keeps only staged compact entries,
duplicate tracking and one event's proposals live, and retains exact byte,
duplicate, export, late-filter, completeness and cancellation checks.

This is one converted enabled built-in. Inscriptions, TXIO, ordered UTXO and
Sats, address indexes, Bitmap and source-retention recipes remain required
before the complete Gateway 0.8.0 preview can be called ready.
