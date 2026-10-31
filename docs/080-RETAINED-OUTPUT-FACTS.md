# Retained output value and script recipes

Three actual JSON documents now control the shared retained output layers:

- `recipes/builtins/output-values.json`
- `recipes/builtins/output-scripts.json`
- `recipes/builtins/output-values-scripts.json`

They use the same ordered-event compiler/interpreter as saved sparse recipes:
select output events, compose an output position, and emit the declared exact
value/script columns. They do not invoke a whole native index. The native
adapter checks one proposed row against its admitted source output; the existing
codec retains page packing, byte/row limits, content addresses, durability,
repair, references and collection. The old writer is a test-only oracle.

Native callers include optional TXIO values/scripts and their independent
enrichment path, shared address-history facts, and mandatory Coin/Sat facts.
Retaining both fields does not silently enable either optional TXIO history
policy. Existing owners and their coverage/pin metadata remain authoritative.
Neither reading a recipe nor copying it starts a writer. TXIO and Bitcoin block
inspectors expose these actual layer documents as separate variants after their
frozen native rule hashes are computed.

Native source admission remains an explicit boundary: the helper projects the
complete admitted output inventory, preserving its old coinbase-position,
output-order and exact-script checks. It does not upgrade a legacy decoded
inventory to parser-owned raw evidence or selected-chain authority. Independent
saved copies use the public raw-owned sparse source and publication fences.
They save generic JSON rows, with exact numeric values and byte representations;
they do not become native output-fact owners or change native layer policies.

The schema-1 physical/source/program bundle is pinned to
`e2d02c262d402fa108e3f98166d9fc50abb50ba0df27e64c72da57feecfdf830`.
Changed program semantics or an unknown physical schema fail closed pending
explicit migration. Native rule hashes, page bytes and references stay unchanged.
The interpreter has an internal typed proposal sink so native codecs avoid
converting numbers/bytes into generic JSON rows and decoding them again. Public
sparse output keeps its existing conversion and field budgets. Both execute the
same compiled program and machine.

Tests compare all three policies on verified block 792435 with the old writer,
including every physical reference and distinct immutable page. Additional
fixtures cover page splitting, a large exact script, empty script, zero and
maximum unsigned values, cancellation, rejected noncanonical script hex and
value-only processing without demanding an unused script. A public reviewed
block-1 cohort runs all three saved programs beside native TXIO. Compiler/GUI
round trips and inspector variant checks use the actual documents. The tests
also preserve the pre-existing native rule identities and corruption/repair
behavior.

`BenchmarkRecipeOutputFactWriter` compares the same verified block and fresh
physical roots for the old oracle and recipe writer. It reports elapsed time
including durable page writes and cumulative allocations, not peak resident
memory or full-chain throughput. Timings are sensitive to Windows filesystem
and background activity; allocation overhead remains an explicit cost of typed
interpretation. These figures must not be promoted to full-chain parity.

The final three-iteration sample on the local i7-10510U workspace was:

| Layer | Old / recipe elapsed per block | Old / recipe allocated bytes per block |
| --- | --- | --- |
| Values | 895 / 685 ms | 3.52 / 19.34 MB |
| Scripts | 626 / 883 ms | 4.25 / 20.24 MB |
| Values and scripts | 499 / 558 ms | 4.55 / 20.52 MB |

The contradictory elapsed ordering across samples shows why these fresh-directory
durable-write timings are not a throughput guarantee. Reusing the synchronous
source envelope and accepting exact typed proposals reduced the initial recipe
allocation totals from roughly 31–35 MB to 19–21 MB on the same fixture. The
remaining allocation increase over the old direct loop is material and recorded
here. Neither an unmeasured peak-memory bound nor full-chain equivalence is
claimed.

This completes the retained value/script derivation layer, not TXIO positional
creation proofs or Bitcoin header/raw retention migration. Those remaining
families, broader typed dependencies and complete visible preview acceptance
remain tracked in `080-PARITY-MATRIX.md`.
