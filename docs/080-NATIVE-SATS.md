# Native Sat FIFO recipes

Native and saved Sat recipes now have their execution/query lifecycle. The
complete 0.8.0 preview, wider ordered contracts and remaining built-ins are
still open. This remains an intermediate increment.

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
publication. Saved copies use the same compiler/interpreter with public
`ordered_finite_and_live` execution. Their isolated generation and receipt
head are described below.

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


## Saved Sat prefix generations and queries

Each saved Sat instance owns `indexes/recipe-sat-v1/<instance hash>/<semantic>/`
with immutable recipe ownership and one atomic `prefix-head.json` receipt.
Native COW, facts and checkpoints live beneath this private profile; no native
Sat head is written there, and neither reading nor rebuilding it changes the
profile's native Sat generation. Missing receipt metadata cannot become an
empty generation. First initialization can finish only an exact owned empty
origin; unexpected payload without ownership requires explicit recovery.

The existing prefix scheduler now uses a small storage interface over the Coin
and Sat adapters. Both bind exact saved revisions and compare the observed
state token at publication. Sat staging uses its own compiled program. Native
Sat writes keep their original mutex/authority/write/sync path, while saved Sat
staging avoids native global locks and publishes only through its generation
CAS inside the selected-header/run/registry fence. A source body is parsed once
per cohort for ordered recipes; an admitted cohort's complete inscription
inventory is shared by native and saved Sat consumers. Standalone source parsing
continues to reject caller-edited decoded facts.

Start review requires an explicit genesis prefix. A compatible prefix can be
reused with no body reads. Finite and Live follow the common reviewed target,
marked range, pause and restart behavior. Reorg recovery restores a retained
selected Sat savepoint before replay. Expired recovery needs a reviewed rebuild.
The shared persistent rebuild intent archives the entire prior private profile,
including its receipt and facts; restarting the same job preserves replacement
progress. Native data and other instances remain independent.

Saved queries use the committed output route with a state cursor. The response
labels its format `gateway-sat-output-range-fragments-v1`: current outputs then
retained historical outputs, each in physical-key order and FIFO interval order.
A row contains a bounded interval fragment, exact value/count/index/offset
strings, positional output identity and state. Both row types use creation-position keys, so historical duplicate coinbase
incarnations remain distinct. Current rows also identify their native outpoint. This is a range
view, not a reproduction of every native query field. It does not repeat the
locking script in each interval fragment. Large fragmented outputs therefore
remain pageable. The cursor's interval index is checked against the saved row;
its sats offset comes from authenticated segment prefixes.

Tokens bind instance, requested revision, semantic identity and exact immutable
checkpoint. A current or retained savepoint is queryable; an older intermediate
point that is not retained is rejected even if an obsolete commit file remains.
Unbuilt state is explicitly uncovered. Selected-header loss/forks, missing heads,
changed ownership and corrupt facts/pages fail instead of supplying zero.
The current checkpoint and newest two savepoints at each native cadence (12 and
144 blocks) define this recovery/query boundary. There is no claim that every
historical height remains a saved state snapshot.

Maintenance authenticates retained metadata, fact pins and complete required
fact payloads, then completes the checked native COW mark before any fact sweep.
Damaged member subtrees or facts defer orphan deletion. Native bounded page
collection, fact grace and commit pruning are reused within the private profile.
Unrelated native data and archived generations remain untouched.

Focused generation/guard/native parity and existing ordered-consumer checks
passed in 14.332 seconds. Public mixed current/history/native runs, source and
inventory sharing, body-free prefix reuse, rebuild archive, Live extension,
local fork, pause and historical-revision restart passed with fragment queries
and the existing ordered public tests in 30.276 seconds. The final focused
Sat/ordered set passed in 29.466 seconds, including the actual HTTP query route,
missing-head/fork rejection and corruption deferral before fact deletion. The
four JavaScript checks and production build passed. The wider regression passed in 836.739 seconds. Final optional-field and
malformed-segment checks passed with the Sat query suite in 30.946 seconds;
duplicate-coinbase creation-key, public and fragment checks passed in 14.441
seconds. The native range-page reader now rejects a short nonfinal segment
immediately. The catalogue marks transaction fields optional during block
initialization. The source-identical workspace and dedicated FIFO editor
headless browser checks passed, with inspected wide/narrow captures. Visible
Chrome acceptance is still blocked by browser-control initialization.
