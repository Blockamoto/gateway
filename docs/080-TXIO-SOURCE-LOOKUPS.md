# TXIO links and declared input-creation evidence

`recipes/builtins/txio-links.json` is the complete executable native TXIO
derivation. The typed interpreter resets and increments input ordinals, reads
declared per-input evidence, chooses creation proofs in JSON, and emits either
resolved positional links or explicit pending translations. Native codecs keep
their existing v3 payload, translation catalogue, anchors, reverse pointers,
retained output layers and publication rules. The pre-composition algorithm is
retained only as a differential test oracle.

The source contract is `gateway-admitted-spending-inputs-v1`. Complete native
equivalence binds it, `gateway-bitcoin-input-creation-lookups-v1`, the v3 codec
contract and the actual program fingerprint to
`060c52f86e2983e2d5a534d34ded08f0e31f08603aef776c8e3efb82aae88052`.
Existing native rule identities and physical layouts are unchanged. The raw
input component and three independent output-fact documents remain available
as inspector variants alongside the complete link document.

## Declared point reads

A sparse `bitcoin.ordered` / `rows.json.v1` document can declare up to sixteen
`lookups`. Each has an alias, source `bitcoin.input_creation`, provider, exact
installed revision and semantic fingerprint, key `input_ref`, and result type
`creation_evidence`. Installed bindings are returned in `builtin_lookups` by
the recipe catalogue. Aliases are independent of binding identity.

An ordinary typed `read` instruction supplies a canonical input coordinate in
the current owned block. It cannot read arbitrary heights, files or table keys.
Results are `{state:"unavailable"}` or
`{state:"available",position:"…",block_hash:"…"}`. Unavailable evidence makes
no claim that an output is absent or unspent. Returned values are owned; reads
are cached per point during evaluation, bounded by the common state operation,
touched-key and aggregate byte limits. Source declarations accept no writes.

Four point providers are exposed. The complete JSON program chooses their
lazy priority: shared-cohort prevout, earlier same-block transaction, retained
native TXIO facts, then the configured positioned-parent evidence provider.
The latest eligible transaction incarnation is authoritative. An absent output
in that incarnation is an error, rather than permission to guess another one.
Non-cancellation provider unavailability remains explicit pending evidence;
cancellation aborts the whole evaluation.

Every positive result must identify the requested output, precede its spending
input, and carry a valid creation block hash. Conflicting anchors fail. Saved
recipes independently parse verified raw block bytes before constructing the
reader; caller-edited decoded input fields cannot substitute. At publication,
positive creation hashes are checked again against selected local headers
under the existing header/run/registry fence.

Point lookups differ from complete saved-parent dependencies. They neither
claim complete parent coverage nor schedule a parent writer. Outgoing exports
and point lookups in one document are rejected until a combined checked
contract exists. General table/checkpoint lookup kinds remain separate work.

The configured provider captures wholly absent offline inventories on first
use, avoiding repeated filesystem probes for every input. Exact known cached
parent IDs are still checked on each first point read. Newly created inventories
are considered by a later evaluation. Existing provider inventories retain
their regular checked lookup path; saved reads explicitly forbid locator repair.

## Saved copies, refresh and guided controls

A saved copy uses the same compiler, typed instructions and source contract;
its output uses the common sparse canonical JSON storage and generic query
interface. Native positional queries retain their own physical codec.
Finite and Live scheduling retain committed block receipts. Later evidence
availability does not silently replace already committed rows.

Source-evidence recipes expose a staged rebuild in saved-recipe controls.
Review binds the current generation's ownership/head metadata and the selected
range. Start rechecks that binding and archives the whole previous semantic
generation, then evaluates only the reviewed range. A rebuild from block 1
does not require a genesis body or a Coin/Sat prefix. Coverage outside the
replacement range remains in the archive; it is not asserted in the new
generation. A stale review cannot archive newer results. Persistent archive
markers make interrupted/resumed jobs idempotent, including Windows busy-file
retry behavior shared with the other recipe backends.

The guided editor reconstructs the complete nested program. Source controls
pin installed bindings, preserve aliases when changing providers, rename nested
read references, and refuse removal while referenced. Unsupported combinations
preserve raw JSON. Editing and staging rebuild options start no indexing work.

## Verification boundary

The native/source suite passed in 452.367 seconds before the public rebuild
extension. It compares complete v3 payloads and translation references on real
block 792435 with and without retained facts, lazy provider precedence,
unavailable/error/cancellation cases, legacy reads, restart and reorg behavior.
The real block fixture is verified serialized data; sparse selected-header
test fixtures do not claim full intervening chain or consensus validation.

The complete saved program on block 792435, native-oracle row comparison and
receipt reuse passed in 94.39 seconds, including native oracle translation
durability. A real block-170 input starts pending, remains unchanged when a
parent fixture becomes available, and resolves after reviewed rebuild with the
native producer Off. Its parent files remain byte-exact and no sidecars are
added. That check and ordinary sparse rebuild rejection passed in 3.618 seconds.
The native retained-parent fixture uses an admitted synthetic position and
selected anchor; it is not a parent consensus replay. Live follow, fork
withdrawal, pause and historical-revision restart passed in 3.43 seconds.
Reviewed range archive, stale review rejection and resumed-job idempotence
passed in 1.861 seconds after the persisted-binding validation fix. Structural
run validation remains separate from exact-definition execution admission.
The final Live/reorg/restart check starts with a nonempty reviewed original
generation and a staged rebuild, then verifies that restart preserves both the
replacement and its original archive; it passed in 3.644 seconds. The production
build passed after these checks.

Guided unit checks, source-identical DOM controls for five actual documents,
source-provider pin/alias editing, narrow layout, Apply and no network work
passed. The wider index workspace browser checks also passed. Headless editor
checks are code QA; they do not constitute visible Chrome preview acceptance.
The final wider recipe/saved/native-TXIO/075/run regression passed in 930.163
seconds: 629 checks passed and six were skipped, with no failures. No build or
browser process competed with that suite. Its session audit is
`gateway-080-txio-links-regression.jsonl` in the temporary directory.

`BenchmarkRecipeTXIOLinks` measured one iteration per implementation on the same
verified block 792435, fresh physical roots, no external provider, and optional
output-fact retention disabled. The old oracle took 91.946 seconds with
244,782,968 bytes cumulatively allocated; the recipe took 117.151 seconds with
320,177,192 bytes allocated. Both include checked source selection and durable
translation catalogue/pointer writes. They exclude raw source parsing, final
native index publication, public sparse storage/query and peak resident memory.
The 75.4 MB allocation increase is material. A single Windows durability sample
cannot establish a throughput ratio or full-chain performance. The benchmark
passed in 214.738 seconds; its raw output is in the session's temporary audit
log, not a shipped data dependency.
