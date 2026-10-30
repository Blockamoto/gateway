# Native Sat FIFO recipes

This is an intermediate native migration. The complete 0.8.0 preview, saved Sat
execution/query lifecycle, wider ordered contracts and remaining built-ins are
still open.

The actual `recipes/builtins/sat-state.json` and `sat-ranges.json` documents now
control native Sat derivation through the common typed event interpreter.
Current state and independently retained original output ranges have distinct
semantic identities. The native history option selects the corresponding actual
document; opening existing data does not select a policy or start work.

The declared `bitcoin.fifo` source emits block initialization, ordinary
transactions in Bitcoin order and then coinbase. It supplies source facts,
output totals and inscription claims. Input values/offsets are authenticated by
the exact saved outputs consumed by the program. There is no UTXO build or
substitution for a missing Sat prefix. Genesis-first admission, complete source
inventory, selected-header checks and the existing final publication fence
remain outside the interpreter.

JSON explicitly accumulates input intervals, splits each output's allocation,
carries transaction fees into the coinbase stream, records its unclaimed
remainder and selects inscription pointers versus input offsets. The program
also chooses burned/genesis state, preserves legacy duplicate destruction and
emits inscription identities. The storage adapter performs individual keyed
writes, fact pinning, reverse location updates and counter bookkeeping. It does
not dispatch a whole-index algorithm. The old derivation loop is test-only.

`ranges` is an immutable typed interval sequence. `range_empty`, `range_subsidy`,
`range_concat`, `range_split`, `range_value` and `range_at` are reusable narrow
operations. Wire endpoints are exact decimal strings. Numeric sorting or
coalescing never changes FIFO sequence. Exact split boundaries share unchanged
interval spans; interior splits copy their boundary spans. Historical
fragmentation is independent of the expression/event node budget. Operations
check cancellation, while literals and generic JSON storage retain their own
byte limits. The native sink uses its existing segmented range pages instead
of imposing a generic JSON-row quota on historical fragments.

Native profile `gateway-sat-fifo-mainnet-bps-v2`, layout 1, RuleHash, output
facts, COW roots, savepoints, queries and collection remain unchanged. Their
explicit JSON/source/physical equivalence bundle is
`cf32e338d8057228d9246db0d538c076f5f462314f732e31ae47cd220eb7699d`.
Unknown bundles/layouts cannot silently adopt native data. The native historical
height policy for duplicate coinbases is preserved, including its existing
synthetic fixture boundary; this migration does not assert new consensus rules.

The read-only native catalogue exposes both actual documents. Guided controls
round-trip every instruction and field, edit range functions and exact interval
literals, and add/remove the optional history table. A history declaration needs
one original-row write for every source output; incomplete programs fail before
publication. Saved copies validate under the same compiler but currently report
`source_scheduling_unavailable`; their public lifecycle is the next integration
step. That limitation must not be represented as a finished working preview.

Physical differential checks compare complete batch/checkpoint/head identities
with the frozen old derivation for current and retained history. They cover
same-block spends, fees, lost sats, burned and zero-valued outputs, inscription
pointers/unbound identity, source facts and restart reads. Native duplicate
fixtures also pass. The broader native Sat/recovery/registry suite passed in
274.525 seconds. The following focused fragmentation/semantic check passed in
9.603 seconds, including an interval sequence longer than the expression-node
quota and no copy of an untouched exact-boundary tail. All four recipe/workspace
JavaScript checks and a production build passed. The final source-field/compiler
follow-up passed in 6.669 seconds and the final production build passed.

These are bounded deterministic fixtures and existing native recovery tests,
not a full-chain replay or visible Chrome acceptance. Browser control still
fails before initialization with a missing kernel-assets path.
