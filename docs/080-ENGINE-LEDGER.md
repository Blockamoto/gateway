# Gateway 0.8.0 engine ledger

Last updated: 2026-10-30. Baseline: 0.7.9 `76cc8d5efa3c3d75a1531774fc2af1dff44dba51`; the 0.8.0 branch starts there. This is a working ledger, not a claim that 0.8.0 is implemented or that baseline behavior has already been reproduced.

## Current implementation boundary

The initial interpreter evaluates sparse reveal recipes against complete verified extraction. Independent recipes can be saved, armed, reviewed, run over a finite range or Live and queried through the existing coordinator and a selected-local-header check. Live target rebinding and journalled sparse reorg reconciliation have focused backend tests. A visible isolated Live test followed a real new block and paused; Computer Use stopped after restart because it could not establish Chrome's current URL, so visual Resume remains unverified. This is **not a complete indexing release**: a narrow sparse same-block dependency path is implemented for reveal and transaction sources, while in-run re-admission and broader dependency forms remain open. The language cannot yet describe all built-ins or their ordered mutable state. Native TX ID Locators, UTXO transitions, Lean/Full inscription retention, optional/saved inscription locators and Address History destination rows now derive through actual JSON compositions with explicit native semantic-equivalence bindings. Custom ordered Coin/count recipes reuse isolated native-format generations through guided authoring, reviewed finite/Live runs, query, rebuild and maintenance. Other built-ins retain their original derivation; the complete baseline matrix and wider custom contracts remain open. Legacy native equivalence does not relabel a custom generation.

The CBOR reader retains typed data and duplicate entries. The initial Ord properties adapter handles uncompressed and bounded Brotli data; malformed/unknown-field equivalence against the pinned Ord implementation remains unproven. Definitions that use interpreted properties receive a new semantic profile for Brotli; prior property generations remain retained and need an explicit new generation rather than being silently relabelled. Synthetic tests cover metadata and properties. The real Bitcoin genesis fixture proves only the verified empty-reveal case. An optional raw block at height 908657 verifies one real reveal with metadata and metaprotocol, including a changed-predicate check; its standalone header is not authenticated as selected chain. Broad differential parity remains outstanding. The prototype's narrowly supported operators are not a release-scope reduction; extend or replace them to satisfy the full matrix below.

Definition and sparse-store initialization fail closed if a published head is lost; a crash between initialization marker and first head also requires explicit recovery. Page paths receive best-effort reparse-point checks, not protection against hostile concurrent filesystem replacement. Production integration still needs complete chain, admission and dependency fencing, reorg recovery and read-side validation. The generic store guard is a trusted synchronous caller contract, not an independent authority check.

The sparse consumer and local-header publication guard have focused tests, including cancellation, stale revisions, gaps and concurrent stop/header replacement. Independent recipes use them through the coordinated worker and per-height query path. The local fence cannot lock an external Core process; the Live backend reuses an unchanged authenticated prefix within a running consumer, while restart rechecks saved anchors. See `080-RECIPE-PUBLICATION-FENCE.md` for lock order and remaining authority boundaries.

Offline, disposable Chrome smoke tests authored, validated, saved and reloaded metadata and property-trait drafts; they ran a finite recipe over the verified Bitcoin genesis block and observed its committed empty result. A separate real-block run at 792435 returned a reveal. At 908657, guided metadata and metaprotocol recipes ran against the verified block and validated header prefix: the metadata predicate selected `2201i0.908657`, a changed value produced a completed empty result, and the metaprotocol predicate returned 38 rows including that reveal. A combined guided metadata-and-metaprotocol recipe returned that same reveal with transaction and block-hash projections. These are bounded checks, not full acceptance or a user-ready preview. Full guided recreation, child dependencies, all built-in recipes and parity remain open.

## Repository brief

Gateway is an existing Go application with a local HTTP management interface and an embedded HTML/JavaScript explorer and indexing timeline. It obtains Bitcoin headers and bodies from available sources, verifies source identity and commitments, and writes selected indexes to profile-local stores. The selected header chain and body verification are not full Bitcoin script/UTXO consensus validation. Blocators are primary positional identifiers; transaction hashes and conventional inscription IDs are translations backed by evidence. `BUILDING.md` describes the offline Go build (`GO111MODULE=off`), including its locally vendored Brotli decoder, checks and packaging. Use an isolated data directory and preview; never point development runs at installed user data.

Key starting points: `index_catalog.go` (definitions, identity and planning), `release_availability.go` and `bitmap_capabilities.go` (availability), `index_run*.go` (admission, coordination and lifecycle), `index_ingestion_capabilities.go` (incidental writes), `index_layers.go` and `index_*storage*.go` (coverage and persistence), `ui/shell/index-timeline.js` and `index-workspace.js` (workspace), `docs/INDEXING.md` and `docs/RELEASE-NOTES-v0.7.9.md` (behavior). The archived 0.8 experiment is separate and read-only evidence; its fixed handlers and limited constructor are not the release scope.

## 0.7.9 inventory and parity matrix

Status below refers to baseline execution policy, not whether the user has armed a track or saved data. Sources and dependencies summarize operative behavior, not a proposed JSON grammar. Every enabled row and retained layer needs an executable recipe in the common engine, guided reconstruction, and differential tests. Baseline parity remains **unproven** until those tests pass. The distinction between source evidence, a display group, a true dependency, and independent coverage must survive migration.

| Track / identity | Baseline status and coverage | Source, outputs, constraints and parity obligations |
| --- | --- | --- |
| `headers` | Enabled chain authority; continuous validated header history | Selected chain and proof-of-work context; not block-body possession or full consensus. Preserve header retention, synchronization, chain identity and reorg behavior. |
| `blocks` / Raw | Enabled sparse source and separately armed raw-body retention | Verified block bytes and compact metadata; Raw Off permits temporary processing, On retains verified bodies. Existing legacy raw locators remain readable; new Raw alone does not create a full txid lookup. Physical body coverage, provider availability and processing progress are distinct. |
| `tx-locator` | Enabled sparse, arbitrary start | Complete ordered transaction positions per covered block; txid to blocator translation. Keep independent opt-in and coverage, verified membership and legacy lookup behavior. |
| `inscriptions` | Enabled sparse Lean/Full; effective new mainnet scan origin 767430 even though historical definition identity says zero | Ordered witness envelopes, positional occurrences and block counts; Full retains reveal contents; optional related conventional-ID locators can be built or enriched independently. Empty scans count. Neither mode alone proves global numbering, ownership or parent provenance. |
| `txo-spender` / `txio-links` | Enabled sparse positive relationships | Input-to-creation-output and output-to-spender positional links; unresolved predecessor translations remain unknown, not unspent proof. Independent authenticated link coverage; source evidence and translation bridge remain separate. |
| `txio-values`, `txio-scripts` | Enabled optional independent historical layers | Exact creation values and scripts from shared output facts, with independent coverage, enrichment and retention. These optional histories do not imply links or complete UTXO membership; required UTXO/Sat facts survive their removal. |
| `utxo-state` | Enabled ordered state from genesis or exact compatible checkpoint | Consume inputs/create outputs in transaction order; current live set, amount/script, creation positions, outpoint translation and atomic state/undo. Preserve 288 reversible transitions; deeper reorg requires explicit reviewed rebuild with old data retained. Header anchored, not full consensus. |
| `address-state` | Enabled snapshot projection | Depends on matching complete `utxo-state` checkpoint; supported payment-address and exact P2PK destination reverse map. Bind UTXO commitment and snapshot; stale/missing cannot become a zero balance. No implicit UTXO build. |
| `address-history` | Enabled sparse independent history | Supported output destinations, retained shared facts and anchored empty scans; keep P2PK-aware coverage, revision-bound queries/pagination and reorg exclusion. Historical received totals do not establish a current balance. |
| `sat-state` / Sats | Enabled ordered genesis state | Issuance, input FIFO, output allocation and coinbase subsidy/fees; explicit lost, unspendable and BIP30 behavior. Current location binds checkpoint; 12/144-height savepoint policy and selected-chain recovery remain distinct from UTXO undo. |
| `txio-sat-ranges` | Enabled optional Sats historical retention | Historical output ranges during ordered Sats processing, off by default; independent from Raw and current Sat state, and selected ranges do not imply full block coverage. |
| `bitmap-districts-standard-v1` | Enabled ordered origin 792435 / compatible checkpoint | Pinned published Standard V1 profile, authenticated complete reveal order, MIME/grammar/target checks, first eligible winner and retained candidates. No ownership or global numbering implied; source evidence may be shared without retained Inscriptions. |
| `bitmap-parcels-standard-v1-replay` | Enabled coordinated validated track | Bound exact Standard District profile and same-height evaluation. A configured Raw, TX Locator or IO Links proof route supplies verified parent provenance and target transaction count; missing proof stays pending and earlier unresolved candidates prevent premature winners. Commit validated coverage only, preserve route-specific audit/recovery, Live and finite Out. No per-run validation quota. |

Additional catalog identities are preserved, not silently enabled or treated as equivalent:

- `bitmap-parcels-standard-v1`: older saved Standard Parcel data is readable, but new execution is disabled in favor of replay identity.
- `bitmap-districts`, `bitmap-parcels`, `bitmap-chain`: legacy development profiles and saved evidence; current release gating prevents new execution. `bitmap` and `bitmap-compatibility` are earlier locked identities. Do not rewrite their semantics.
- `bitmap-districts-canonical`, `bitmap-parcels-canonical`, `bitmap-districts-canonical-v2`, `bitmap-parcels-canonical-v2`: previous fixed generations, retained for inspection; not the current execution identities. Custom profile and comparison execution is gated.
- `bitmap-district-chain-dev-v1`, `bitmap-parcel-chain-dev-v1`: draft chain execution disabled; saved evidence remains inspectable. `satline` tracing is disabled, with saved trace data retained. Gateway peerhood is gated and is not an index to enable as a side effect.

Relevant implementation details can exceed this summary; the catalog, route admission and tests are authoritative when a conflict occurs. In particular, enabled availability is distinct from nonempty user coverage, UI grouping, `Buildable` in a historical definition, and incidental browse permission. Baseline has separate storage/checkpoint generations and private evidence; compatibility cannot be inferred from similar labels or hashes.

## Milestones and evidence

All milestones are open until actual code and checks establish them. A milestone may be developed in parallel, but downstream acceptance cannot be claimed from a schema or fixture-only prototype.

1. **Contract and inventory:** reconcile this matrix with code and private bindx draft; settle typed sources, expressions, joins, ordered state, output tables, dependencies, coverage and evidence. Establish a versioned executable JSON contract and semantic identity independent of display ID; add actionable validation errors.
2. **Vertical authoring path:** one shared typed model for guided controls and raw JSON import/edit/export; create a category or child without an implied execution dependency, author a custom recipe, run it on verified evidence, and query saved rows. Include inscription metadata, metaprotocol, properties/traits and metadata key/value filters, and a dependency-backed child.
3. **Complete baseline conversion:** express every enabled index and retained layer in real compositions interpreted by the engine. Use reusable typed domain primitives with explicit state, inputs, filters and outputs; do not dispatch an entire legacy index by recipe ID or disguised whole-index opcode. Show built-in recipes in settings. Validate same-source differential results against 0.7.9 including optional modes and independent layer coverage.
4. **Scheduler, evidence and storage integration:** share acquisition/decoding while preserving independent committed frontiers and backpressure. Test sparse gaps, empty outcomes, pre-origin fences, explicit prefix/checkpoint requirements, dependency backfill, durable atomic completion, restart, pause/stop, Live, finite Out, lineage/reorg recovery, audit reuse and cancellation. Parcel tests must cover validated winner ordering and unavailable proofs.
5. **Compatibility and integrated preview:** either prove saved formats/identities/checkpoints compatible or present an explicit non-destructive migration/rebuild boundary. Exercise ordinary authoring, import/export, queries and existing navigation visibly in Chrome. Run appropriate native and browser suites and the established build/audit process with a disposable profile; record source revision, commands, artifacts and limitations. Do not publish a release or modify main, 0.7.9, Render or installed user data.

Validation must include synthetic adversarial cases and bounded authenticated real Bitcoin/Ord fixtures with their precise coverage disclosed. The preserved experiment's 32-block District comparison is useful but not 0.7.9 parity; its inscription sample contains no nonempty metadata/metaprotocol/parent tags. New meaningful vectors are needed. No untested milestone is complete merely because its schema validates.

## Decisions, defaults and open questions

- **Usage guard (lifted):** the human explicitly instructed “Continue until the working preview is ready” on 2026-10-30. This overrides the prior 70%-used weekly pause guard. Continue the complete accepted scope without another go-ahead question; use the remaining account budget efficiently and keep tested progress synced. The earlier 50%-used one-time pause also remains lifted.
- **Scope (decided):** all enabled 0.7.9 behavior above is required, plus user-authored custom indexes. Built-ins and custom definitions share the actual executor. Maintain existing explorer/timeline UX; avoid broad restyling.
- **Representation (default):** keep definition, run state and presentation separate. A recipe ID and title identify an instance for users, but do not change execution meaning; predicate or other semantic changes change results and semantic identity. Define canonical encoding and versioned behavior before sharing checkpoints.
- **Dependencies (decided):** a visual parent/category is only layout. Explicit execution dependencies describe exported inputs, coverage, provenance and lineage. An unavailable dependency cannot be inferred from a display relationship or silently built.
- **Evidence and coverage (decided):** unknown, unavailable, absent, invalid and verified empty are distinct. Sparse receipts cannot fill gaps; stateful first publication needs genesis or a compatible authenticated predecessor. No writes before an operation's origin. Keep evidence/consent/selected-chain fences at publication and revalidate on reorg.
- **Persistence (open):** exact adapter strategy and compatible reuse of each existing generation remain to be demonstrated. Default to retaining old data and requiring a clearly described rebuild if equivalence cannot be proven; no silent reinterpretation or destructive migration.
- **CBOR (default pending implementation profile):** preserve raw field bytes and typed values; metadata tag 5 chunks concatenate before decoding and can contain arbitrary CBOR. Define and test traversal, scalar conversion, duplicate keys, tags, numeric bounds, invalid and incomplete values without conflating unknown with non-match. RFC 8949 permits indefinite-length forms and non-preferred encodings in general; Ord properties are a more restricted profile. Reference [RFC 8949](https://www.rfc-editor.org/rfc/rfc8949.html) and [Ord metadata](https://docs.ordinals.com/inscriptions/metadata.html).
- **Ord properties (default):** treat tag 17 properties and tag 19 encoding separately from arbitrary metadata conventions; concatenate properties chunks, follow pinned Ord rules for definite CBOR, structured attributes and optional Brotli bounds. See [properties](https://docs.ordinals.com/inscriptions/properties.html) and [pinned Ord implementation](https://github.com/ordinals/ord/blob/b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414/src/properties.rs). Decide supported paths, value types and error presentation in the constructor; do not silently drop unsupported values.
- **Parent evidence (decided):** a declared parent or display hierarchy is not proof of an Ord child. Validate provenance from required authenticated source and state; keep unresolved proofs pending. [Ord provenance](https://docs.ordinals.com/inscriptions/provenance.html).
- **Resources and verification (default):** retain existing proof checks and use explicit bounded memory/cancellation without arbitrary result quotas. Reuse a previously authenticated unchanged prefix when extending it; recheck changed source and dependency lineage. The [BIP 141 witness commitment](https://github.com/bitcoin/bips/blob/master/bip-0141.mediawiki#commitment-structure) is distinct from a transaction Merkle root and selected-chain authority.
- **UI and run intent (decided):** configuration, navigation and hover do not start historical work or alter run bounds. Arming controls permitted incidental sparse capture; Start admits a run. Apply monotonic revisions so stale asynchronous responses do not override later user intent.
- **Open implementation choices:** operator catalogue and evidence profiles, generic durable row/query representation, efficient state and prefix validation, exact legacy migration paths, real metadata/properties fixture provenance, and full baseline differential coverage need design and measurement as work proceeds. Record changes to these defaults and unresolved blockers here rather than silently narrowing scope.

### Presentation hierarchy: current boundary

The existing profile-owned `timeline-layout.json` is the single owner of both built-in track order and the new display-only hierarchy. A hierarchy node can be a category or a track, and either can be the visual parent of another node; a track stores a display reference, including an unknown or legacy reference. These relationships never create execution dependencies, enable a recipe, arm a consumer, or alter jobs. `GET /api/v1/index/layout/hierarchy` is read-only; a `POST` requires the exact current layout revision and replaces the hierarchy deliberately while preserving the stored built-in order and unknown saved identities. Existing reorder writes preserve the hierarchy. Concurrent stale hierarchy writes fail rather than replacing newer layout changes.

Reading an existing schema-1 or schema-2 layout leaves its bytes unchanged. The first explicit hierarchy save writes schema 3 to the same file, preserving its existing order and data. **The 0.7.9 layout reader cannot read a modified schema-3 layout**; use a separate profile for 0.7.9 or retain a copy of the former layout before explicitly saving a hierarchy. This is a layout compatibility boundary, not an index data migration. No navigation or hover performs that hierarchy save. The timeline now renders nested display-reference rows and lets the user explicitly add categories and track references. Known unlocked references draw their existing coverage on the shared ruler; unavailable references remain labelled placeholders. Built-in lanes retain their existing order. These display references do not themselves execute recipes; a complete hierarchy editor and executable custom lanes remain open. The creation control shows the schema-3 downgrade boundary, and display placement stays independent of dependency/admission state.

### UTXO and Current Addresses: planned state primitives

UTXO processing needs ordered keyed state with read-after-write behavior within a block and a first-write before image for every touched key. Its exported input is an authenticated complete UTXO snapshot plus net before/after deltas bound to the exact source commitment; amounts and exact scripts remain mandatory shared facts. Current Addresses projects that snapshot and its deltas without implicitly starting a UTXO build. A missing or stale projection cannot imply a zero balance, and unsupported destination totals must be retained separately. Preserve historical BIP30 destruction handling, the 288-transition recovery window, and an explicit rebuild boundary beyond available undo. These are design requirements, not completed recipe parity.

**Physical adapter decision:** reuse the existing UTXO COW/fact/journal/atomic-head machinery behind generic ordered operations. A new page-pack or staged-store format is not a release requirement. Keep large historical scripts in authenticated mandatory facts and stage model mutations in program order; avoid duplicating the physical undo batch in memory. `080-UTXO-ADAPTER-DECISIONS.md` records the native integration and measured adapter choice. Public custom ordered execution remains in progress.

**Final handoff requirement:** deliver a plain-English debrief with the completed preview, including changes and reasons, decisions/tradeoffs and storage paths actually shipped, recipe/GUI usage, guided recreation, built-in parity and migration status, test/performance evidence and limits, preview/start instructions, defaults and any genuine unanswered questions. Experimental options are implementation decisions; the user need not select them. This document is not that final acceptance handoff.

### Independent transaction recipe increment

The ordinary coordinator accepts reviewed independent `bitcoin.transaction` recipes for finite and Live runs. A private per-block transaction snapshot copies ordered IDs only after checking the authenticated raw-header Merkle root and parser receipt transaction count; multiple recipe tracks reuse it without another block decode when the private parser receipt is present. Legacy decoded views without that receipt must be reconstructed and verified. Outputs use the generic block-scoped recipe store and selected-header query fence. Filtered blocks produce explicit empty outcomes. This does not migrate compact TX ID Locators, its native reverse lookup, historical stores, or complete native locator migration.

The source profile is `gateway-recipe-transaction-merkle-v2`; earlier experimental `gateway-recipe-transaction-verified-v1` receipts are preserved but incompatible and require an explicitly reviewed new generation. Existing reveal semantic fingerprints are unchanged. Focused coordinator coverage includes finite mixed reveal/transaction runs, empty output, extraction-failure isolation, Live extension and selected-chain reorg, pause/restart with pinned revision, and output query. The 0.7.9 differential fixture covers genesis, block 1, synthetic positions and, with the optional local raw fixture and generated oracle, the 3,113 transactions of block 908657. These checks do not establish native storage or reverse-query parity.

### Narrow sparse dependency increment

Reviewed sparse `bitcoin.reveal` and `bitcoin.transaction` recipes may declare the supported same-block exported-row lookup. Admission checks the historical dependency graph and its saved revisions; selected parents are dispatched before their children. Missing saved coverage is unknown and waits, with no implicit parent job. A selected revision with the same instance and full semantic fingerprint can supply the shared generation even if a name-only edit changed its revision; the pinned historical definition must still exist. A different fingerprint cannot substitute. Same-block cross-source lookups are supported when export contracts match. Other dependency forms remain unavailable.

Focused tests exercise public planning and Start, reverse-order selected parents, saved-only parents, missing coverage and backfill, historical pause/reload/resume, a registry change at the final locked admission check, mixed transaction runs, and Live selected-local-header replacement. The latter is a local synthetic reorg test, not external Core authority or full consensus validation. This slice is not evidence that all built-ins, stateful dependencies, arbitrary joins or full-release acceptance are complete.

Transaction dependency validation also includes a nonempty public transaction-only run, saved-parent reuse, real block 792435 lookups in both source directions, and stale-parent rejection. The combined targeted lifecycle suite passed in 168.343 seconds after this extension. Guided drafts now select either source and use its field catalogue; metadata and trait filters require reveals.

### Native locator and interpreter row sink

The native TX ID Locators paths execute `recipes/builtins/tx-locator.json` in the common interpreter. The compact physical adapter validates and encodes the resulting rows. The original derivation exists only as a test oracle. Frozen v1/v2 rule identities are explicitly bound to the compiled semantic fingerprint; an unversioned JSON or interpreter-profile change cannot reuse their native generations. Settings exposes the executable document and can create a separate guided copy without a write or run. See `080-NATIVE-LOCATOR.md` for exact authority, compatibility and performance boundaries. Custom compact-backend selection and general reverse-query integration remain open.

A reusable row-proposal sink removes the block-wide row map and repeated JSON encoding for transaction adapters. It retains event atomicity, late filters, export type checks, duplicate detection, exact byte accounting, completeness and cancellation. The combined recipe/saved-recipe/coordinator/timeline/native-locator/store/integration suite, including the optional verified block 908657 fixture, passed in 504.359 seconds after the change. Native locator, recovery and timeline checks also passed separately in 117.805 seconds. Browser-side editor and workspace tests passed. These results cover this increment, not the whole release.

### Historical dependency pagination still open

Per-block output tokens bind immutable unit bytes and recheck their complete same-height lineage; unrelated appends do not change that unit identity. Coordinator dependency coverage retains current ancestor-head vectors for its run/admission fence. A separate historical range endpoint now pins existing immutable published heads and compares only the requested receipt intervals, with a digest of the exact selected header interval. It survives unrelated Live append while rejecting changes to prior-page ancestors or headers. General custom row-range export, its UI and bulk validation remain unfinished acceptance work; see `080-HISTORICAL-RANGES.md`.

### Native UTXO composition

Ordinary UTXO `ApplyBlock` executes `recipes/builtins/utxo-state.json`
through the common ordered interpreter. Its typed state interface also has a
JSON adapter for the existing generic state transactions. The native adapter
performs individual keyed coin mutations and checks logical counters against
derived physical counters; it does not execute a whole index operation. The
old loop is test-only. Physical checkpoint, fact, pin, undo and recovery formats
are preserved, and an explicit semantic-equivalence guard binds the actual
program and profiles to schema 2 before any native read/write.

The native path streams complete admitted blocks without the experimental
50,000-event/global-work quota. Scripts remain in authenticated shared facts.
Failed programs, cancellation and interruptions before the head leave the old
checkpoint intact. Differential vectors cover exact checkpoints and journals,
intra-block spends, money rejection, restart/reorg, real genesis/block 1, both
BIP30 exceptions, partially spent originals, invalid replacements and undo.
Historical-height substitutions are synthetic state vectors, not chain replay.

The native UTXO, address, coordinator, generic ordered/state/resume suite passed
in 133.875 seconds after switching `ApplyBlock`. Editor and workspace JavaScript
checks passed. The actual program appears read-only in Settings; custom ordered
document/registry/GUI execution and namespaced fact ownership remain open.
The typed adapter cut a 2,048-output synthetic fixture's allocations from about
97 MB to 42 MB; measured apply times were approximately 1.065 s and 0.950 s in
separate noisy paired runs. Native baseline was approximately 0.84–0.88 s and
11 MB. See the adapter decisions document for commands, exact counts and limits.
This is one native conversion, not full 0.8.0 acceptance.

### Native inscription retention compositions

Lean positions, Full source records and browse conventional-ID projection now
execute actual JSON recipes through the sparse interpreter and streamed row
sink. Settings exposes all three documents separately, preserves the selected
document during observation, and can copy each into a separate custom draft.
Lean and Full survive guided-editor round trips. Custom Full output serializes
the exact source occurrence, while individual source fields remain available
for filters. Additive reveal fields bind a profile only when used, preserving
previous recipe identities and the native locator fingerprint.

Native v1/v2 generations have an explicit semantic-equivalence mapping for the
three-program bundle. Differential fixtures match exact native payloads for
empty genesis, synthetic multi-envelope reveals and real blocks 792435/908657.
The original retained projection is test-only. The internal inscription-locator
hash now excludes inherited executable display metadata, preserving the
pre-display definition rule. Legacy explicit numbering conversion remains open;
the subsequent retained-ID increment below covers retroactive sparse locators.

The broad recipe/saved-recipe/sparse/positional/inscription suite, including the
optional pinned metadata fixture, passed in 188.404 seconds after routing native
retention through JSON. Subsequent admission-reuse, catalog variants, native
identity, source/sink rejection and durable Full-record checks passed with
dependency/sparse lifecycle checks in 72.358 seconds. Editor/workspace checks
also passed. See `080-NATIVE-INSCRIPTIONS.md` for source
authority, physical compatibility, authoring and engineering bounds. Large
generic Full records still face the experimental 16 MiB durable-unit bound;
that remains open release work, not an accepted scope reduction.

### Saved inscription IDs and explicit table distinctness

The retroactive saved-inscription locator now executes
`inscriptions-saved-conventional-ids.json`. Its source adapter retains the old
saved-checkpoint/record-commitment checks, Full-without-fetch behavior, verified
block requirement for nonempty Lean data and explicit empty outcomes. Every
saved position becomes an admitted logical event. The interpreter's declared
`on_conflict: identical` table operation retains a transaction row once and
rejects different values at the same key. Deduplication is no longer hidden in
the source/physical derivation loop. The old projection is a test-only oracle.

Identical means exact deterministic encoded row bytes. Default tables continue
rejecting duplicates, with unchanged hashes. New policy semantics have their
own profile, participate in immutable recipe identity, validate duplicate export
types and preserve late-filter event atomicity. A shared row stream applies
the operation to both reveal and transaction sources with exact unique-byte
accounting. Comparison buffers are owned independently of the sink. The guided
editor exposes the policy, and Settings includes the saved projection document.
Native current/legacy locator generations have an explicit program/source
equivalence map. Arbitrary custom native-source imports and compact-backend
selection remain open release work.

Focused parity/identity/sparse/catalogue checks passed in 14.422 seconds; the
broader recipe/saved/sparse/positional/coordinator-recipe set passed in 163.427
seconds after the generic table operation. Final duplicate-export, real saved
source, custom durable row and four-variant presentation checks passed in
14.957 seconds. Guided-editor/workspace checks and the production build passed.

### Complete ordered documents and isolated native-format generations

The complete UTXO document now compiles through the common registry/compiler;
native AST identity/checkpoints remain compatible. Exact structural JSON field
spelling is enforced while literal object keys retain their spelling. Ordered
capabilities expose the actual typed events, functions and instructions.

Private custom generations reuse native COW/facts/first-before undo with explicit
origin/definition ownership and an authority-first CAS. The private consumer
binds selected headers, the admitted run and exact revision through publication,
supports restart/finite scope and authenticated local reorg recovery, and
rejects stale work. A cohort shares one owned raw-verified ordered source;
its extra authentication decode is counted rather than hidden.

State queries page committed coins/counts without materializing the full tree.
An immutable historical checkpoint token survives unrelated appends within the
retained recovery window, while changed selected ancestors reject continuation.
The output UI supports either sparse offsets or ordered physical cursors.

Public ordered execution remains gated pending scheduler/rebuild/maintenance
and guided-authoring integration. Other built-ins, generic ordered contracts,
large sparse persistence and full historical DAG ranges remain open. This is
an intermediate engine increment, not the requested finished preview.
See `080-ORDERED-PUBLIC-INTEGRATION.md` for implementation and test evidence.

### Public ordered Coin/count lifecycle and guided program editor

The first ordered physical contract now uses public recipe save/resolve, explicit
arm and reviewed Start, finite/Live coordination, prefix checks, reorg recovery,
pause/process restart, historical queries and staged rebuild. Pure planning
blocks missing lead-in without writing or fetching a prefix. Rebuild review
binds current metadata and archives the complete prior generation with a
durable per-job intent; retry/resume retains the replacement's progress.

Maintenance reuses native COW/undo retention and fact sweeping at completion and
Live committed boundaries, with private generation ownership and without native
UTXO mutexes. Current/recovery facts survive undo, old unowned facts can expire,
and native-root facts/archived generations remain independent. The mixed run
exposed a prepared fact-cache assumption; its key now binds the physical owner,
and private generations avoid retaining per-track maps in that shared cache.

The guided ordered editor represents the actual complete UTXO program, supports
nested instructions and typed expressions, preserves exact uint strings and
refuses unsupported forms without rewriting raw text. Copying/viewing starts no
work; Apply precedes validation/save. Saved recipe tracks can stage/clear a
rebuild for the next reviewed Start. Existing flat sparse controls remain.

Focused public coordinator tests passed in 13.753 seconds after the ownership
fix, alongside earlier private lifecycle/GC/rebuild checks. All four recipe
editor/tracks/workspace JavaScript checks passed. The broad recipe/saved-recipe/UTXO/address/run regression passed in
499.889 seconds, including the pinned metadata fixture and stale rebuild review.
The production build passed. No finished preview is served; wider state contracts, remaining built-ins and acceptance are open.

### Immutable historical receipt ranges

A separate read-only range-coverage endpoint now pins existing immutable
published receipt heads for the root and every ancestor. Stateless continuation
checks the complete requested receipt interval against current trees, including
prior-page ancestors, and the exact selected local header byte interval.
Unrelated receipt/header appends beyond the range preserve the token. Boundary
leaf splits are compared by entries rather than raw page shape. Existing
coordinator current-head/admission fences remain intact. No snapshot files,
index job, hidden backfill or additional persistence backend is created.

The focused range/DAG/store coverage set passed in 16.188 seconds. The wider
DAG/lineage/dependency/store/coverage/output/coordinator/saved-recipe and ordered
public suite passed in 146.488 seconds after correcting a missed output-route
return. Synthetic warm header interval hashing measured 5.301 ms/0.8 MB and
257.534 ms/80 MB, using about 69.8 KB allocated per read. The production build
passed. Details and scope are in `080-HISTORICAL-RANGES.md`. General custom row
export, its GUI and bulk validation are still open; bulk export should reuse a
single checked immutable header fragment rather than hash a wide interval for
every small page. The requested complete preview is not yet served.

### Large sparse-output evidence and streaming primitives

A verified block-792435 transaction projection with a legal 16 KiB annotation
completed 3,139 rows / 52,463,224 canonical JSON bytes, then hit the provisional
16 MiB durable-unit cap. This confirms a real accepted custom-program output
case which must be supported before release. The opt-in boundary audit passed
in 4.921 seconds and currently records that rejection explicitly.

Private streaming JSON and external metadata-sort helpers now preserve legacy
bytes/order without a whole-row/block buffer. Sorting uses bounded batches,
level compaction and bounded fan-in; long common keys compare in chunks. Tests
stream a row beyond 64 MiB, check exact JSON bytes and numbers, exercise long
keys/multiple merge levels, cancellation, malformed runs and cleanup. The
combined set passed in 4.492 seconds. Public persistence is still unchanged;
these primitives are not an integration milestone or a finished preview.

The chosen direction retains canonical JSON units and logical digests, existing
atomic receipt heads and native COW primitives. It does not require a new
persistent index format or the slow private staged/page-pack experiments. See
`080-SPARSE-STREAMING.md` for remaining source/consumer/lookup/query/export work.

### Public sparse row streaming

Independent and DAG sparse consumers now stage and sort owned rows, preserve
schema-1 canonical bytes/logical hashes and publish through the existing
coverage CAS and authority guard. Public custom execution no longer imposes
whole-event, block or durable-unit output quotas. Native compact sinks and
semantic mappings remain unchanged. Temporary native COW pages bound large
key sets; no new persistent format or experimental storage backend is used.

Coverage, lineage and reconciliation retain typed metadata; owned large-unit
views verify bytes, index row offsets and provide on-demand scalar exports.
Output pages retain exact unit tokens; very large rows and complete units can
be downloaded through saved-track links. Small historical output behavior is
preserved. Root payload copying and network output occur outside authority
locks, with an exact receipt/header/registry check before exposure.

The real 52,463,224-byte block-792435 projection now persists and reads with its
frozen old-format digest. Its optional stage/publish/verify audit measured
2.569 seconds and 465,715,832 cumulative allocated bytes (not peak memory).
A public >64-MiB genesis projection, dependent scalar lookup, downloads and
source-free prefix reuse passed in 46.16 seconds. The focused broader set
passed in 208.406 seconds; the complete recipe/saved-recipe/ordinal-tree/UTXO/run
regression passed in 787.730 seconds. The row-policy abstraction and focused
sparse suite passed in 121.637 seconds. The four
recipe/workspace JavaScript checks pass. Details and current I/O limits are in
`080-SPARSE-STREAMING.md`. Generic row-range export/GUI, maintenance, remaining
built-ins, wider ordered contracts and visible full-preview acceptance are
still open; no finished preview is claimed.

### Generic sparse range row export

Saved recipe tracks now offer a local JSONL download for inclusive From/To
heights and current or historical immutable identities. The file streams exact
committed rows, explicit evaluated-empty units and coalesced uncovered intervals;
no source fetch, run, backfill or secondary build occurs. Root/ancestor published
heads and one owned selected-header fragment are shared throughout the export.
A final full-interval header/graph/receipt check precedes any attachment headers.
Unrelated appends remain valid. Artifacts are request-owned temporary files with
SHA-256, exact extent and separate artifact/coverage completion metadata.

The initial focused export set passed in 43.953 seconds, including ancestor and
prior-header changes, missing history, cancellation, allowed outside append,
exact integers and corruption without an attachment. The public route/GUI and combined
export/large-output checks passed in 89.287 seconds; the four JavaScript checks
and final production build also passed. The wider recipe/saved-recipe/ordinal/
UTXO/run suite passed in 787.730 seconds before the export route was added. Remaining built-ins, wider
ordered contracts, maintenance and visible complete-preview acceptance remain
open. See `080-HISTORICAL-RANGES.md` for the artifact protocol and authority.

### Stateless typed output projections and native Address History

The typed event source now supports sparse `rows.json.v1` projections with an
arbitrary origin, existing spooled receipts and outgoing same-block exports.
It shares the raw-owned cohort source and does not borrow UTXO state. Guided
controls support output declarations, exports and typed event composition;
state reads/writes and incoming typed lookup expressions fail explicitly for
this contract. Focused public checks passed in 6.360 seconds, and the wider
ordered/admission/resolution/DAG/output/range set passed in 192.711 seconds.

Native Address History now selects destinations through actual current and
legacy-payment JSON documents in the same interpreter, using its existing
complete retained fact source. Its physical reverse rows, counters, COW roots,
coverage/pins and publication paths remain intact. Frozen semantic bundle
checks protect schema-1 native data. Exact small physical differentials,
reapplication, retained P2PK/payment rules and native mapping checks passed in
7.952 seconds. The broader address suite and real block-792435 fact/raw differentials passed
in 141.628 seconds; both legacy/current JSON compositions matched retained facts
and owned raw source. The production build and four JavaScript checks passed. See `080-NATIVE-ADDRESS-HISTORY.md`.
Current Addresses, broader ordered contracts and dependencies, other remaining
built-ins, maintenance and complete visible preview acceptance remain open.

Final checks for this increment: the broader address/real-reference suite
passed in 141.628 seconds; the ordered/native-UTXO/address-recipe regression
passed in 265.475 seconds. The real block-792435 retained-fact/public raw JSON
differential passed for both destination policies. A typed >16-MiB row with a
9,000-byte key passed staging, publication and lookup in 6.588 seconds, alongside
individual field/work-budget rejection. Complex fields stream separately and
repeated byte values share encoding. The final production build and four
JavaScript checks passed. This does not close Current Addresses, broader
ordered dependencies, remaining built-ins or visible full-preview acceptance.

### Native Current Addresses and private checkpoint dependency storage

Native full traversal, updates and recovery now execute actual current and
legacy-payment JSON in the common typed interpreter. Existing COW roots,
reverse rows, counters and publication paths retain their native layout and a
frozen equivalence bundle; the prior mutation loop is test-only. Settings
exposes both actual documents. The exact built-in UTXO recipe is declared as a
typed coins snapshot/delta dependency, rather than inferred from display order.

Read-only parent resolution also supports exact saved Coin generations without
creating or starting them. Private child ownership, immutable checkpoints,
receipt-head CAS, existing COW materialization and checked net-delta/undo paths
are implemented. Full traversal saves its exact dependency/cursor every 512
outputs. Child fact links survive source unlink. The combined native Current
Addresses, mapping, immutable dependency and private projection suite passed in
89.057 seconds, including durable cancel/restart and distinct JSON policies.

This remains an intermediate increment: custom copies validate but report
`source_scheduling_unavailable`. Their public consumer, query, guided controls,
rebuild and maintenance are still open, alongside the wider remaining built-ins
and full visible preview acceptance. See `080-NATIVE-CURRENT-ADDRESSES.md`.

The final catalogue/API/native identity checks passed in 11.475 seconds after
updating their expected source catalogue. A saved empty Coin dependency beside
nonempty native state passed in 6.812 seconds without substitution. The
production build and four recipe/workspace JavaScript checks passed.

### Private checkpoint query and owned COW maintenance

Checkpoint projection queries now have an internal read-only adapter with exact
revision/semantic/parent/checkpoint tokens, physical member paging, empty versus
unbuilt distinction and historical selected-header/source receipt checks. The
response explicitly reports that exclusions retain counters rather than rows.
Public routing and scheduling remain gated pending the remaining lifecycle/UI.

Each child owns copied native fact-pin COW branches as well as immutable fact
links. Incremental copy reuses unchanged branches and existing fact ownership;
initial/resumed work restores complete pins before publication. Independent COW
files keep the native collector's timestamp marks isolated. The private marker
profile advances to v2 and rejects an earlier internal binding for reviewed
rebuild, without changing native data or its frozen semantics.

Private maintenance now pins retained history and staged members, visits shared
COW pages once, uses the existing bounded page/fact collectors and expires only
unreachable snapshot metadata. Corruption defers fact sweep. Focused projection,
graph and mixed ordered/sparse DAG dispatch checks passed in 21.230 seconds;
maintenance/copy checks passed in 11.917 seconds. The dispatch change also fixes
independent ordered writers being rejected when a sparse DAG shares their run.
The broader dependency/DAG/lineage/ordered-public/projection regression passed
in 122.557 seconds and the production build passed. A final focused projection
and COW set passed in 14.578 seconds after adding owned-child-ancestor recovery
following source branch reclamation. Without a retained selected child ancestor
or required source transition, rebuild remains explicit. This does not complete
public checkpoint integration or the full preview.


### Public checkpoint projection lifecycle and guided source pinning

Current Addresses copies now use public `dependency.utxo` finite/Live execution,
true built-in/saved Coin checkpoint dependencies, arbitrary admitted snapshot
height, private COW/fact ownership, selected-source/header/revision/CAS fences,
state cursor query, maintenance and explicit child-only archived rebuild. The
consumer stays outside the body queue, waits on missing saved prefixes without
creating either generation and reuses complete inputs with producers Off.
A saved earlier child point can be read/reused without rewinding a later view;
unretained older views and expired source evidence require explicit review.

Start review now distinguishes built-in dependency identity from saved registry
identity. Checkpoint and ordered recipes share the existing archive/resume
implementation with unchanged ordered metadata wire bytes and a shared target
binding helper. Native Current Addresses keeps its frozen equivalence bundle,
physical data and lifecycle. The read-only catalogue supplies the actual native
Coin binding; guided controls represent both actual Current Addresses documents,
can pin a saved Coin parent, preserve raw unsupported forms and reject stale
asynchronous parent responses. Saved tracks distinguish state snapshots from
sparse row downloads and stage a child rebuild without requiring child genesis.

The public mixed selected-parent/saved-only/rebuild check passed in 12.320
seconds; the combined public Live/local-fork/pause/historical-restart checks
passed in 15.871 seconds. Shared rebuild/ordered-public checks passed in 21.488
seconds; dependency-only consumer checks passed in 7.190 seconds; planning and
catalogue checks passed in 5.821 seconds. Four JavaScript checks and the
production build passed. The broad recipe/saved-recipe/ordinal/UTXO/run/current-address regression
passed in 822.032 seconds with an explicit 30-minute test timeout. General state contracts,
remaining built-ins, custom reverse-query integration and complete visible
preview acceptance remain open; no finished preview is claimed.


The isolated intermediate UI smoke server is available at
`http://127.0.0.1:61235`, using only the temporary profile
`C:/Users/camer/AppData/Local/Temp/gateway-080-ui-766132ffd25b4e959db871d654751474/profile`.
Core/RPC, mounted Core files, network transport, header sync and serving are
explicitly disabled. It is not the finished preview and still carries the base
0.7.9 version label. Production/installed user data and integrations were not
changed. Both browser-control runtimes failed to initialize with
`failed to write kernel assets: The system cannot find the path specified`,
including after a kernel reset. No visible Chrome acceptance is claimed.


### Custom checkpoint destination lookup

Saved Current Addresses projections now expose direct destination-bucket lookup
through the committed output route and track controls. Mainnet addresses,
SEC public keys and exact supported scripts resolve to a canonical script key.
The recipe/source-relative scope, exact count/balance strings, cursor identity,
covered zero, source loss, filter changes and stale GUI responses are explicit.
No scan or parent work starts on lookup. Focused projection query checks passed
in 8.237 seconds; all four recipe/workspace JavaScript checks and production
build passed. General state contracts, remaining built-ins and complete visible
preview acceptance are still open. Details are in `080-NATIVE-CURRENT-ADDRESSES.md`.


### Native Sat FIFO and retained original range JSON

Actual current-state and retained-history documents now drive native Sat FIFO
through the common typed interpreter. Their source explicitly orders ordinary
transactions before coinbase; JSON composes immutable interval concatenation,
splits, fee carry, lost remainders, output state, legacy duplicate destruction
and inscription pointer/unbound identity. Individual physical writes retain the
native COW/fact/reverse-location codec, savepoints, checkpoint identity and GC.
The old derivation is a test-only differential oracle. A frozen equivalence
bundle protects existing layout-1 native data.

Both actual documents round-trip through guided controls, including range
functions, exact interval literals and the optional history declaration. Saved
copies compile but remain scheduler-gated pending their own public lifecycle.
No private staged-state or page-pack experiment is adopted. Details and the
precise source/physical boundary are in `080-NATIVE-SATS.md`.

Complete physical current/history parity and existing same-block/duplicate
checks passed in 7.918 seconds. The broader native Sat, savepoint recovery and
registry suite passed in 274.525 seconds. Fragmentation/semantic checks passed
in 9.603 seconds, including more historical intervals than the expression-node
quota and unchanged-tail sharing. Final source-fact/compiler rejection and
native/range checks passed in 6.669 seconds. All four JavaScript checks passed;
the final production build passed after the source-field follow-up. Saved Sat execution/query/lifecycle, general
state contracts, remaining built-ins and visible complete-preview acceptance
remain open. Browser initialization still fails with the missing kernel-assets
path; no visible acceptance is claimed.
