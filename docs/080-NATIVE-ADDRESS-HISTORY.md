# Address History JSON composition

Status: implementation and acceptance work in progress. This is not the complete
0.8.0 preview. Current Addresses, broader ordered dependencies and the remaining
built-ins remain open in the main engine ledger.

## Executable behavior

`recipes/builtins/address-history.json` selects supported output destinations
through the common typed event interpreter. It explicitly selects output events,
classifies their exact script with the `destination` primitive, filters unsupported
scripts, constructs the positional output reference and emits destination/type,
exact sats, script and block hash. There is no whole-address-index opcode.

`address-history-payment-legacy.json` uses the explicit `payment_destination`
primitive for older payment-only projections. Both documents have guided typed
representations. Settings exposes the current document and retained legacy
variant. A copied draft uses independent generic sparse rows; it does not rename
or take over the native store.

The source and result are separate contracts. Custom `bitcoin.ordered` programs
may use sparse `rows.json.v1` storage from an arbitrary origin. They consume the
cohort's owned, reparsed verified block events and publish ordinary immutable
sparse row units. They require no UTXO/genesis build. Typed uints become exact JSON
numbers, bytes become the existing hex object form, and output rows are spooled.
They can export scalar columns to existing same-block children. Incoming typed
lookup expressions remain unsupported and fail validation explicitly. The typed
stateless language forbids ordered state reads/writes, rather than silently
creating or borrowing a state generation. Existing stateful Coin/count programs
retain their exact semantic fingerprints.

The guided typed editor supports this sparse shape, arbitrary origin, output
tables/keys, duplicate policy, exports, typed expressions and nested event
instructions. State mutations are unavailable for this contract. The saved-track
UI distinguishes sparse event output/downloads from ordered UTXO rebuild and
snapshot controls.

## Native storage and retained facts

The native adapter supplies complete admitted output-fact page rows to that same
interpreter. It does not claim raw-block possession or add chain authority. Only
the fields used by the frozen compositions are supplied by this fact adapter.
The exact fact refs, value, script, position and hash are checked against emitted
rows. JSON controls destination selection and classification; the existing
physical store maintains reverse rows, COW buckets, derived counts/sums,
coverage, pins and head publication. No new physical generation is introduced.

Current commits and retained-page replay use these compositions. Version 0 keeps
its original payment-only behavior; version 1 retains exact valid P2PK
identities as well. Complete-map admission, selected-header checks, sparse known
empty coverage, replacement/reorg accounting and source fact retention remain in
their original native ownership paths. The old selection/commit loops exist only
in a test oracle.

Native schema 1 is explicitly bound to the two actual program fingerprints and
`gateway-address-history-complete-facts-v1`, through the frozen bundle digest
`5c5ef3a981053bd26e79ed4f9e91fb53a88cbbc3e9a96d5e85e7bb9fe6fc4398`.
A different bundle or physical schema fails closed and requires explicit
migration. Existing stored heads, roots, fact references and query identity are
not relabelled as custom output.

## Evidence and remaining checks

The stateless public path passed arbitrary-start execution at real block 1,
shared owned source accounting, a dependent child, selected-local-header output
and generic range export. Focused projection/rejection/public tests passed in
6.360 seconds. The wider ordered/admission/resolution/DAG/output/range suite
passed in 192.711 seconds before the native fact adapter was added. All four
recipe/workspace JavaScript checks passed, including both actual address JSON
documents round-tripping through guided controls.

Native differential tests passed in 7.952 seconds for exact physical initial
heads/roots and same-anchor reapplication on real genesis/block 1 and synthetic
mixed destination scripts. Retained payment-only and P2PK rules matched the
old oracle, and unknown native mappings were rejected. The broader address suite, including the real block-792435 raw-versus-facts
differential, passed in 141.628 seconds. The real reference projection itself
took 2.14 seconds. Complete retained-fact reuse without Raw, incomplete facts,
Live gaps, legacy P2PK coverage, cancellation, reorg and GC checks passed. The
production build passed. Commit status belongs in the engine ledger.
These tests are bounded evidence, not full-chain replay or consensus proof.

The final ordered/native-UTXO/address-recipe regression passed in 265.475
seconds after the resource checks. A typed projection with a >16-MiB row and a
9,000-byte key passed staging/publication/query in 6.588 seconds. Complex fields
are encoded individually, repeated byte fields share their encoded value, and
individual decoded-field/structural-work budgets prevent expression expansion
from becoming an unbounded in-memory graph. These are source/expression resource
limits, not aggregate row/block output quotas. The final production build and
four JavaScript checks passed. Remaining Current Addresses and state-contract
work is explicitly outside this conversion's completed evidence.
