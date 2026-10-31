# Spending-input recipe component

`recipes/builtins/txio-inputs.json` now drives native source-to-spending-input
rows and independent saved sparse copies. Its program resets an exact input
counter at each transaction, composes `input_ref(height, transaction, input)`,
emits raw outpoint/source identities and increments the counter. The coordinate
function is a narrow typed primitive, available in the compiler catalogue and
guided controls. Existing programs and native semantic bundles remain unchanged.

Native source admission preserves the previous raw-spend boundary: iterate the
admitted transactions, skip coinbase transactions and reject invalid spending
input evidence. The interpreter determines ordinals, coordinates and proposed
rows. The adapter checks each individual typed proposal and retains the existing
legacy raw row shape. The old loop is a test-only oracle. The binding is
`f6be769c76c8c0fe8030405dcb5069f6035843c12aa1378ed907c27f82e5423d`.
Changed semantics fail closed pending explicit migration.

The modern TXIO writer now executes the complete `txio-links.json` program,
including creation-proof selection and pending translations. This raw-input
component remains independently editable and supplies the legacy raw-spend
boundary. See `080-TXIO-SOURCE-LOOKUPS.md` for the complete composition and its
declared, lazy input-creation evidence. Neither program establishes unspentness
or complete history from missing evidence.

The TXIO inspector exposes the actual input document beside the independent
retained output value/script programs. A saved copy runs with authenticated
raw-owned events through the existing sparse executor, stores generic JSON rows
and uses its normal finite/Live, range, query and publication controls. It needs
no hidden genesis or UTXO build. The native and saved programs have the same
counter/field logic, with their source-admission/physical boundaries stated
separately.

Focused checks compare every native raw row on verified block 792435, reset
ordinals across multi-input transactions, preserve evaluated empty coinbase
blocks, reject invalid input evidence and overflowing coordinates, and return no
partial rows on cancellation. Existing TXIO creation/pending/legacy-reader tests
remain in the regression. A reviewed saved run on block 792435 uses an explicit
sparse selected-local-header fixture and real raw block bytes. That test checks
local publication authority and stored source identities, not validation of all
intervening mainnet headers. Actual documents round-trip through guided controls
and the inspector/browser fixtures.

`BenchmarkRecipeTXIOInputs` isolates source-to-input rows on the same reference
block. A three-iteration local sample measured 1.97 ms and 2.92 MB allocated for
the old direct loop, versus 50.95 ms and 24.42 MB for the recipe component. This
is a material CPU/allocation increase for this small component. Creation lookups,
translation/page writes, source parsing and generic saved storage are excluded;
these numbers are not complete TXIO throughput or peak-memory measurements.
Complete-derivation measurements and the preserved proof/coverage semantics are
recorded separately in `080-TXIO-SOURCE-LOOKUPS.md` and `080-PARITY-MATRIX.md`.
