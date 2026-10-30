# Ordered UTXO integration decisions

Status: native integration under validation. Ordinary UTXO processing now
executes the embedded JSON program. Public custom ordered authoring, scheduler
and generation-owned fact retention remain unfinished release work.

The physical backend is the existing UTXO COW trees, mandatory output facts,
first-before journal, 288-transition recovery policy and atomic head. The
recipe derives ordered coin changes; the adapter stages individual keyed
changes through the existing physical maintenance mechanics. Page packs and
the separate experimental staged store are excluded from this path.

The interpreter accepts a typed keyed-state interface. Its JSON state adapter
preserves the existing experimental memory/durable transaction paths. The
native adapter writes coins into private COW roots and the physical journal.
Logical counters remain independent during the composition and must match
derived physical counters before publication. This removes a second logical
JSON undo batch and repeated value serialization. The earlier staged-JSON
visitor prototype was removed. Failure or cancellation discards the staged
view and retains the old head. No adapter operation derives an index by ID.

Coin values reference mandatory fact pages instead of copying each spent
output's full script into transaction memory. Input-heavy blocks can reference
many large historical scripts. Facts preserve exact amounts/scripts and native
page pins. The model retains fact-reference slot changes and counter/transaction
identity metadata in addition to native undo, never a whole UTXO snapshot.

BIP30 uses the old live count and each replacement's matching outpoint. Only
the exact historical exception can displace an older live coin, and every old
live output must be replaced. This follows native mutation/journal order and
avoids the experimental 4,096-range bound. A captured incarnation bridge keeps
untouched old outputs reachable while the native bridge selects new outputs.
Tests cover both exception identities, partly spent originals, unspendable or
absent replacements, wrong height/hash, non-coinbase duplicates and undo.
Historical-height substitutions are labelled state fixtures, not chain replay.

Prevouts offered to other armed consumers are explicit recipe output proposals
backed by the same mandatory facts. The old derivation exists only as a test
oracle. The original admitted decoded inventory contract remains; this does
not upgrade legacy decoded fixtures into parser-owned raw evidence. Native
source admission, publication fences, optional address catch-up, recovery and
garbage collection remain in the existing host machinery.

The native path streams the complete admitted source, with expression work
bounded per event. It has no experimental 50,000-event/global-work quota and
does not use an old-output range loop. Existing physical page/journal bounds
remain. Custom generations now reuse that format with independent fact
ownership, CAS, native maintenance and explicit archive-on-rebuild policy.

The normalized actual program and interpreter, typed-stream, Bitcoin primitive,
native source/fact and physical profiles bind schema 2 to semantic fingerprint
`b0c3c2ff4df3a9cd7cd2203aa70a914e779ac949d4be0991e21dc2e99e994d96`.
An unmatched program/profile/schema fails closed and requires an explicit
migration. Native checkpoint and journal bytes remain the compatibility test.
Settings displays the complete executable JSON document and can copy it into
a separate custom draft. The common compiler/registry validates that document,
and guided controls represent its complete nested program. Custom Coin/count
recipes can now be explicitly armed and reviewed through finite/Live scheduling;
saving a definition starts no work. Other state contracts and full release
acceptance remain open; see `080-ORDERED-PUBLIC-INTEGRATION.md`.

The opt-in paired `TestRecipeNativeUTXOApplyMeasurement` measures total apply
cost on the same synthetic 2,048-output block and existing physical backend,
not chain throughput. The staged-JSON prototype took 1.065 s and allocated
96,603,136 bytes / 1,116,163 allocations versus 0.878 s and 11,766,320 bytes /
118,292 allocations for the old loop. The typed adapter measured 0.950 s and
42,059,000 bytes / 277,369 allocations versus 0.843 s and 10,882,128 bytes /
118,317 allocations. These short sequential runs are noisy; dynamic expression
allocation still adds cost. Page-pack storage and the slow experimental staged
store are not involved.

Using a local three-argument evaluation frame reduced the typed run further
to 38,498,848 bytes / 252,771 allocations. That pair took 1.123 s recipe versus
1.173 s legacy (11,737,368 bytes / 118,368 allocations); filesystem timing
variation makes a throughput improvement unsupported. The focused ordered,
native-equivalence, primitive and resume suite passed in 17.393 s. The broader
native UTXO/address/coordinator suite passed in 133.875 s; editor/workspace
checks passed separately. A production `go build` completed successfully.
