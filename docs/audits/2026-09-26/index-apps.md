# App/provider and index audit notes

Scope: audit of the verified Gateway source baseline supplied by the parent audit, focusing on brief deliverables E/F/G, index-related H, and direct questions 5–14. Production code was not changed. Line references are to the local pinned source. Findings below are source-derived unless explicitly identified as a controlled reproduction, proposal or test to run.

Audit-only Go overlay fixtures in `index_apps_repro_test.go.txt` ran successfully under Go 1.27.1 with `GO111MODULE=off`, `-vet=off` and `-overlay audit-working/index-apps-overlay.json`. The log is `index-apps-test-output.txt`. These are PASS-on-bug-observation tests for the baseline, not evidence of repairs. They confirmed all three corrupt-spend-shard modes (delete/truncate/CRC), private graph/BOD publication bypass, and manifest advertisement ignoring consent and compatibility. The graph fixtures use the repository's synthetic graph test setup to isolate storage/policy semantics; they do not claim validity of synthetic Bitcoin transactions. No public-network test was performed in this subtask.

## Conclusions that should survive into the main audit

1. Gateway already separates raw block possession from some derived knowledge. Preserve that work. It does **not** yet have an extensible app registration contract, generic index definitions, deterministic range fingerprints, or Bitmap indexing.
2. The module API is a descriptive manifest/UI boundary, not a runtime provider boundary. A directory containing `module.json` does not make an app executable or remotely usable.
3. The best minimal extension is an authenticated local provider registry plus an opaque rendezvous protocol, after shared sessions work. A plugin marketplace and generic execution runtime are unnecessary.
4. Treat TRIP as a discipline for reproducible rules and data, not as a mandate to build four large frameworks. Add a small descriptor/checkpoint/coverage contract around one useful index first.
5. The relevant distinction is independent evidence versus a claim. A fingerprint proves equality to committed bytes; it does not prove correct execution, complete history, a valid checkpoint, current chain membership, or data availability.
6. Bitmap's minimum state depends on the exact requested answer and pinned interpretation. Current Gateway Ord parsing cannot substantiate canonical Bitmap ownership, global inscription ordering, curse history, or reinscription eligibility.

## Concrete findings and existing strengths

### P1 — private derived spend knowledge bypasses the cache publication policy

`block.go:302–322` marks newly retrieved remote block data private in Privacy Mode. `cache.go:127–153` retains private flags for transaction/spend knowledge, and private-aware serving functions exist. However `cache.go:159–184` also queues every integrity-checked, anchored block into the native graph; `graph.go:428–459` stores transaction/spend records without visibility or publication metadata. `overlay.go:672–678` serves a native graph hit directly before consulting private-aware cached spend lookup. `bod_status.go:170–175` advertises the entire persisted spender coverage.

With graph indexing and serving enabled, privately fetched block-derived spend hints can therefore be disclosed through BOD. This is a current path, not merely a future registry concern. Underlying transactions are public; the private fact being disclosed is the node's observed/indexed interest and possession. Serving the same fact independently from a public Core dataset need not be forbidden, but a private-only acquisition must not itself confer publication consent.

Smallest repair: carry explicit publication eligibility into graph records/range metadata, or initially prevent private acquisitions from entering the publicly served graph and use private local storage separately. Gate every advertisement and request through a common policy decision; a public flag on the cache map cannot protect a second store. Do not retroactively publish legacy records of unknown provenance. Controlled reproduction `TestAuditObservePrivateGraphKnowledgeServedByBOD` confirms that the exact BOD handler and status disclose the graph-derived fact while the private-aware cache refuses serving it. A later repaired integration test should exercise the same policy over a connected peer.

### P1 — damaged graph storage can turn a known spend into a negative answer

`graph.go:490–518` treats a missing spend shard, read error, truncated trailing record, and CRC-invalid record as no spend found. `graph.go:570–593` then checks origin existence, coverage intervals and tip marker; it does not establish that all committed spend records are still intact. `graph.go:731–732` can promote this absence to `unspent_at_snapshot`.

The existing commit marker is valuable: graph records are synced before the block hash marker (`graph.go:468–478`), and positive records are canonical-chain filtered (`graph.go:510–513`). But a later missing/corrupt shard leaves the coverage/markers intact. CRC detects a malformed record; silently skipping it does not preserve negative completeness. This is especially relevant before selling derived-only storage as independently answerable.

Smallest correction must distinguish missing/corrupt/unavailable evidence from known empty evidence. Merely returning an error for CRC mismatch fixes one case; missing or truncated files also need expected segment/shard metadata. Proposed durable layout: completed bounded segments record expected files, sizes, record counts and content digests; advance coverage only with that manifest. Invalidation makes affected negatives unknown until repaired. Controlled reproduction `TestAuditObserveGraphDamageBecomesUnspent` confirms all three variants: deleting, truncating and corrupting a committed spend shard produces `unspent_at_snapshot` while markers/coverage remain intact. Repaired tests must instead require unknown/repair.

### P2 — external manifest discovery becomes an unsolicited remote inventory

`extension.go:85–120` reads local `modules/*/module.json`, marks host API compatibility, and reports metadata. It neither starts code nor registers a handler. Yet `bod_status.go:145–153` publishes any discovered `protocol_id`/`protocol_version` as an extension capability; it ignores `network_advertised`, host API compatibility, and actual runtime support. `bod_status.go:245` sends the resulting list in BOD status.

This is separate from root `gwmsg`, whose current list is hard-coded BOD plus opted-in Satline. Nevertheless it leaks local installation inventory and suggests unavailable extensions. Repair by deriving public capability metadata only from active, eligible provider registrations. A passive manifest must never be the authority to publish a network claim. Controlled reproduction `TestAuditObserveManifestAdvertisementIgnoresConsent` creates a passive manifest beside the temporary test executable and confirms publication despite incompatible host API and `network_advertised=false`.

### P2 — loopback content isolation does not mean cache-query privacy

The separate read-only Ord content listener is a useful isolation boundary (`ord.go:357–377`), and delegates are cache-only with depth/cycle limits (`ord.go:406–418`). However the listener sends `Access-Control-Allow-Origin: *` (`ord.go:376`) and reads any cached known inscription ID (`ord.go:388–403`) without a publication policy. An unrelated site that discovers the ephemeral port and can make permitted loopback requests can probe cached-ID presence and read cached content/metadata. Browser local-network restrictions and port discovery affect exploitability; they do not make the server's intended cross-origin disclosure private.

Preserve separate-origin rendering, but make access deliberate: an explicit origin/capability policy for the current viewer, no general cache enumeration/probing oracle, and no assumption that a CSP protecting rendered content protects against outsiders fetching it. Add a hostile-origin test. Do not automatically turn locally viewed Ord records into remotely published data.

### Other current constraints worth stating accurately

- `gateway.go:60–62` returns only BOD in the base protocol list; `satline_network.go:74–84` adds opt-in Satline. The generic protocol support struct exists but does not imply app hosting or message routing.
- `extension.go:20–38` is a useful existing manifest vocabulary. Versions are strings in module manifests and integers on `gatewayProtocolSupport`; a provider registry must explicitly separate app release, host API, storage schema and protocol wire versions.
- `MODULE-API-v1.txt` and `modules/README.txt` are stale relative to three bundled manifests in `extension.go:157–160`; the latter still says reserved/non-advertised Satline. `BOD-WIRE-v1.txt` also retains an old BOD BIP434 feature identity. Consolidate the effective compatibility/spec table before extensions.
- `cache.go:425–436` deliberately retains transaction/spend knowledge after raw-cache eviction. `block.go:250–252,339–347` reuses local Core data and permits indexing without duplicate raw storage. This directly supports the brief's retention principle.
- `cache.go:25–33,88–97` still keeps all incidental transaction/spend knowledge in maps serialized to a JSON file. Dedup state in `cache.go:167–173` grows with observed block hashes. These are not scalable full-chain index stores. The sharded graph is a better starting point, but its linear shard scans and per-shard fsyncs need measured budgets, not claims of arbitrary scale.
- `coverage.go:16–22` has separate sparse intervals for indexed blocks, transaction locations and spenders. `coverage.go:69–78` avoids advancing spender coverage merely because a block was fetched. `performance.go:103–130` uses a bounded graph queue and drops work rather than inventing coverage. Preserve this honest incompleteness.
- The transaction-location coverage can advance before its asynchronously saved cache index is durable: `sync.go:195–211`, `performance.go:88–101`. Generic index coverage must mean a committed queryable result, not only that bytes were decoded in the process. Crash/restart tests should check this boundary.
- `bod_status.go:193–228` still derives the `graph` profile partly from full raw block history and requires genesis coverage for its negative claim metadata, whereas local `graphCanProveUnspent` needs creation-to-snapshot coverage. These are different semantics. A future explicit index service should advertise its queryable state directly and not require raw archive ownership; keep v1 interpretation stable during transition.
- `integrity.go:13–15` explicitly distinguishes commitment checks from full script/UTXO consensus validation. `block.go:324–331` inherits a live trusted local Core's validated active-chain authority. Mounted files alone remain header anchored. Keep both evidence levels visible in index dependencies.
- `graph.go:716–734` correctly distinguishes local Core-backed unspent, native coverage-backed unspent, and unknown. Positive spend results check the actual spending input (`graph.go:596–635`). The older `txresolve.go:134–191` remote spender helper labels a peer negative `index_backed_unspent`, but repository search found no caller; treat it as obsolete code to remove, not an active Satline trust path.
- `txresolve.go:205–223` checks only a remote current-UTXO report's chain anchor, not the report's membership or completeness. The note admits provider reporting. A valid best-block hash is not an authenticated UTXO set. Address history is a separate absent index.
- `txresolve.go:59–77` tries the single-entry incidental cache before the graph's duplicate-txid ambiguity check. Keep historical transaction positions in future index keys; `txid` alone is not a universally unique historical location.

## Minimal app/provider architecture (proposed)

Maintain separate local and public objects. Local provider registration contains app/provider ID, local principal, protocol family ID, supported wire versions, handler binding, active lease, capability dependencies, transport modes, resource quotas and publication consent. The public direct-peer descriptor contains only protocol ID/version and permitted session/rendezvous features. Do not serialize local handler paths, tokens, filesystem storage, RPC details or installed-app inventory.

The minimal peerhood selector is `(protocol_family_id, compatible_version, optional realm_namespace + opaque realm_selector)`. Network/chain genesis may be a protocol-context field when meaningful. A realm does not need a global name service or a universal database schema. An index definition digest can identify an index-oriented context, while a game world or invite token can identify a live context. A hash of a guessable private realm name does not hide membership. Private discovery needs a secret/capability scheme and app-level authorization; Gateway must not invent privacy by hashing names.

Use leases/withdrawal: a crashed local app must stop being advertised; revoked consent must remove new advertisement and block new sessions. Validate conflicts and exact ID syntax; one local provider must not steal another principal's protocol slot. A protocol ID is a routing/matching name, not authentication of an author or peer. A signed manifest can attest a publisher, never honest behavior or index truth.

The smallest runtime proof should use an already-running local test app over an authenticated named pipe or loopback IPC endpoint, not execute downloaded code. Expose conceptual operations register/unregister, find/watch compatible peers, request/open a session or rendezvous, and obtain evidence/health. App-required capabilities resolve through a local broker with explicit permissions. Merely naming `chain_authority` in a manifest must not grant direct Core credentials or arbitrary RPC access.

Gateway knows bounded identifiers, supported versions, transport/reachability, consent, status, quotas and evidence types. Payloads, events, business meanings, game state and realm-specific authorization belong to apps. A handler can validate its own message semantics without teaching root Gateway to recognize every application.

Default is a per-protocol hybrid: BOD and small bounded control messages may use the existing shared session; bulk traffic, WebRTC media/game state, BitTorrent transfer and normal Nostr exchange should use their own negotiated transport. Gateway supplies rendezvous only where that protocol benefits from it. Full-duplex session and request cancellation/backpressure are prerequisites even for small shared messages. External transport still needs reachability/NAT/relay/authentication solved by that transport; discovering a peer does not make a direct path work.

Do not change wire v1 solely for this local registry. Old peers already understand an array of protocol IDs and ignore families they do not use. Keep direct-only bounded hello semantics; new rendezvous, peer exchange and index-description functionality should have their own advertised family and explicit versions. This still requires an explicit provider dispatcher/session binding: the current hard-coded BOD/Satline message routing will not become generic merely by loading registry metadata. The first external-transport proof can keep app payloads outside Gateway; shared app payloads need their own bounded negotiated carrier protocol. Do not append large realm membership lists to hello. Once measured need exceeds 16 KiB, negotiate a separate bounded catalog/query protocol; preserve initial hello semantics and do not assume old peers understand a truncation marker or a new mandatory interpretation.

Future Gateway Apps is only distribution/search/trust UX over this local contract. It does not allocate or authorize protocol existence. Avoid a marketplace, sandbox runtime, dependency installer and universal broker until two independent local apps have exercised the small contract.

## TRIP definition/instance design (proposed)

Theory is human explanatory material. Rule Set is the precise executable interpretation with pinned semantics and normative fixtures. Index is the generated state with declared inputs and coverage. Product chooses a definition/instance and presentation policy; it may display compatibility warnings but cannot silently change the underlying result.

Use one small versioned `IndexDefinition` manifest plus an `IndexInstance` metadata record. Do not embed arbitrary execution DSLs.

| Object | Required fields |
|---|---|
| Definition | Stable family ID and version; normative rule-set digest; theory/reference links; input/dependency definitions and required semantics; start mode; transition/output/checkpoint schema versions; canonical encoding; query kinds; range semantics; compatibility profile; fingerprint algorithm |
| Instance | Local instance ID; definition digest; network/genesis; committed sparse coverage intervals and gaps; chain anchor; start/end checkpoint references; dependency fingerprints; output roots; evidence status; local queryability; explicitly published/serveable subset; retention policy and available proof material |
| Segment commit | Exact interval; parent/pre-range block anchor; end block anchor; input and dependency commitments; start/end state digests; canonical ordered output count and digest; files and integrity receipts; committed/invalid/repair-needed state |
| Observation/claim | Source and time, endpoint, claimed definition/coverage/roots, direct compatibility observation, verification performed, reason for uncertainty; kept outside canonical semantic identity |

Reject ambiguous optional dependencies: whether absent or present and the semantics of either choice must be part of the definition/input commitment. An implementation version or binary digest can aid reproduction, but should not prevent independent conforming implementations from agreeing on the same normative rule definition. A changed rule or canonical encoding makes a new definition digest; storage-only layout changes need not fork semantic identity.

A possible precise fingerprint construction, to be finalized by fixtures before wire use:

```
definition_digest = H(tag("gateway/index-definition/v1") || C(normative_definition))
output_digest = H(tag("gateway/index-output/v1") || C(ordered_output_records))
range_fingerprint = H(tag("gateway/index-range/v1") || C({
  definition_digest,
  network_genesis_hash,
  interval: [start_inclusive, end_inclusive],
  start_parent_block_hash,
  end_block_hash,
  starting_checkpoint_digest,
  dependency_commitments: sorted_unique[(definition_digest, interval, root)],
  input_commitment,
  output_schema_digest,
  canonical_encoding_id,
  record_count,
  output_digest,
  resulting_checkpoint_digest
}))
```

Here `C` is a specified deterministic, length-delimited encoding; absent/null, integer widths, byte order, string normalization, map ordering, record ordering, duplicate behavior and empty ranges must be normative. `tag` is unambiguous domain separation. Chain hashes must have one representation. A source checkpoint hash belongs in the commitment even when the outputs happen to coincide. For event-only indexes, explicitly bind an empty stateless start rather than silently omit it.

For a sparse instance, commit to the ordered list of **actual segments**; never hash just outer min/max and imply gaps are complete. Keep storage path, peer identity, download time and local instance ID outside this semantic fingerprint, otherwise identical results on two nodes cannot compare. Provenance and validation receipts can be separately signed/hashed. Verification status is not conferred by a remote signature or included label.

A flat per-segment digest is sufficient for build/resume/compare initially. Add a Merkle tree only if partial verification/range lookup requires it, with an explicit ordered-leaf/tree/odd-node algorithm. Bitcoin does not commit to these arbitrary derived roots. A proof under a root supplied by the same untrusted peer proves internal membership only; independent replay, a justified checkpoint or a separately accepted trust assumption is needed for truth/completeness. Multiple agreeing Sybil peers are not independent validation.

### Which range builds are actually independent?

| Index/query | Can start at arbitrary verified block range? | Extra context/limits |
|---|---|---|
| Block-position transaction locator / output occurrence | Yes | Active-chain anchors; key by block and tx position as well as txid; completeness limited to interval |
| Spend-edge events in an interval | Yes | Inputs identify spent outpoints; a missing edge is not globally unspent |
| Historical output/script occurrence | Yes | Explicitly output-only; complete debit/input-address history also needs prevout script/value evidence |
| Address current UTXOs at snapshot | No | Validated prior UTXO state or complete replay; spendability/consensus scope and all intervening transitions |
| Unspent-at-snapshot query | Conditionally | Existing output plus complete valid spend coverage from its creation, including same-block spends, through snapshot; handle historical duplicate IDs and definition-specific exceptional outputs |
| Satpoint's forward movement | Conditionally | Locally established source satpoint, all needed prevout values and verified spender hops; no need to index every sat, but unavailable intermediate history gives unresolved |
| Absolute sat current state | No arbitrary unanchored start | Birth/issuance and verified lineage, or correct sat-state checkpoint; a UTXO value snapshot alone does not assign sat ranges |
| Known-ID inscription envelope/content | Yes for reveal evidence | Locate the transaction/block, verify witness commitment; not global numbering, prior reinscription, ownership or complete provenance |
| Full Ord semantics / historical numbers | No | Pinned implementation semantics and prior inscription/UTXO state, counters and ordering behavior; checkpoint or necessary history |
| Bitmap candidate occurrence | Conditionally | Exact pinned envelope/parser/input semantics; candidate occurrence is weaker than winning/eligible claim |
| Bitmap accepted claims/current ownership | No arbitrary unanchored start | Prior winning claims, required Ord semantics and prior state; current ownership also tracks transfers to snapshot |

A starting block height is an acquisition range, not proof that an index is mathematically range-local. Require the start precondition to be satisfied before publishing completeness.

### Shared decode without coupled indexes

Reuse the verified `blockView` and a narrow normalized event view. A bounded coordinator dispatches immutable block/events to only active indexers. Each indexer owns its schema, commit checkpoint, coverage and publication policy. Input validation occurs before fan-out. Persist each index's durable result before advancing its coverage; a cancelled/failed consumer records a gap or stops independently. An Ord-dependent Bitmap consumer waits for the corresponding committed Ord result, not a best-effort side effect.

The current coalesced four-block decoded cache (`performance.go:48–86`) and two-entry async graph queue (`performance.go:103–130`) show useful limits, but there is no generic dependency-aware pipeline yet. Initial extraction should adapt the graph builder to a tiny `ApplyBlock/Commit/Checkpoint/Rewind` interface and prove crash safety, reorg rollback and range determinism. Do not replace all current resolution/storage with a framework at once.

Retention needs separate controls for raw bodies, decoded transient objects, derived event/output data, start/end checkpoints, undo data, proof receipts and public snapshots. Discarding raw bytes can preserve an answer but impair independent replay if no provider later serves them; advertise availability of actual retained verification material. Keep enough undo state or a known rollback checkpoint to respond to reorgs. Imported state is not automatically trusted or immediately publishable.

Peer-serving should initially use one small index-descriptor/range-transfer family for interoperable metadata and bounded requests; semantic query handlers and evidence verification stay with index families. Avoid a universal query language. A descriptor can say definition digest, exact public intervals, snapshot/start checkpoint, fingerprint scheme, supported queries, limits and evidence kinds. Publication is explicit and separate from local coverage. Request work budgets, max compressed/uncompressed bytes, max dependencies, bounded recursion and cancellation are necessary; peers must not trigger unlimited rebuilds.

## Bitmap proof architecture and external evidence

Current code's Ord scope is intentionally modest. `ord_envelope.go:12–14` identifies `gateway-envelope-v1/ordinal-handbook-2026-09-23`. `ord.go:129–160` verifies the witness commitment and extracts a known-ID envelope, while expressly declining global numbering, curse/vindication history and complete provenance. `ord.go:174–179` refuses flagged or ambiguous pre-jubilee binding. `ord.go:242–290` offers only a loopback Ord adapter and labels its unverified responses `provider_reported`. `ord.go:451` says Ord is not network advertised. There is no Bitmap implementation in these files.

Primary external sources inspected on 2026-09-26:

- [Original author's Bitmap theory](https://github.com/Blockamoto/gitbook/blob/main/bitmap-theory-whitepaper/theory.md) states district claims by the first inscription of an existing block height and a separate parcel parent/child concept. It supplies the broad intention, not a complete byte-level resolver for every historical corner case.
- [Original author's TRIP methodology](https://github.com/Blockamoto/gitbook/blob/main/bitmap-theory-whitepaper/methodology.md) separates the four layers and calls for repeatable rules. This supports freezing explicit definitions; it does not answer missing parser/eligibility details.
- [Ordinal Handbook inscriptions](https://docs.ordinals.com/inscriptions.html) distinguishes inscription IDs from global numbers, documents fee-related historical ordering and mainnet jubilee height 824544, and describes reinscriptions as additions rather than replacements.
- [Ord inscription updater source](https://raw.githubusercontent.com/ordinals/ord/master/src/index/updater/inscription_updater.rs) uses input UTXO inscription state for reinscription classification, prior entry status for cursed/vindicated handling, counters and fee/coinbase state, and optional sat-range input. These moving source URLs are audit research evidence, **not** a pinned future normative dependency. Implementation must select a concrete commit/version and differential test corpus.

Do not invent a supposedly historical base Bitmap rule-set hash yet. First freeze a small base-district definition with attributable choices for exact text bytes/encoding, MIME treatment, numeric grammar (leading zero, sign, whitespace, bounds), existing-block timing, candidate ordering/tie-breaks, accepted inscription semantics, reinscription/negative-number treatment, delegates/compression, burns/transfers, and reorg handling. If original sources do not decide a case, label it unresolved or an explicit proposed new profile. A market's current accepted list cannot silently settle the original rule.

### Minimum upstream state: answer by requested product

For **candidate text extraction**, the relevant reveal blocks plus a pinned witness/envelope parser can identify candidate bodies. That is not enough for a canonical winner or ownership claim. If the defined base rule intentionally ignores historical numbers/reinscription eligibility, some classification dependencies may be omitted, but this is a declared definition choice that must not be marketed as canonical historic Bitmap behavior.

For **historically compatible accepted claims**, minimum state is the dependency closure of those rules: verified input values, tracked inscriptions on spent outputs and offsets, the prior inscription properties needed to classify reinscriptions/curses/vindication, deterministic reveal/fee ordering, necessary counters if numbers affect acceptance, and the already accepted district/parcel state. Because non-Bitmap inscriptions can change whether a later Bitmap candidate is a reinscription or its classification, a filter that drops every non-Bitmap inscription before state processing is generally unsound. This is an engineering inference from the reference updater and proposed compatibility requirements, not proof that one fixed smallest state representation has been derived.

For **current ownership**, also track accepted inscription movement through every required transfer to the declared snapshot, including fee/coinbase and lost/unbound cases. Parcels additionally need authenticated parent/provenance semantics if included. Full arbitrary inscription media, explorer search tables, wallets, Rune indexes and full sat-number inventory are not inherently required. Ord's optional sat-range input demonstrates that inscription tracking and full absolute-sat indexing are separable; do not require all-sat retention unless the selected output needs it.

There are two honest initialization paths: replay the needed history with a legitimate prevout/block source, or import a specifically defined and independently validated/trust-labelled checkpoint at H-1. An ordinary UTXO snapshot lacks prior inscription state; a snapshot hash from an untrusted peer alone does not solve validation. Starting at the first visible Bitmap claim without such context cannot prove historical completeness. This audit cannot provide a single safe earliest start height without an approved definition and pinned upstream dependency contract.

### Base and compatibility interpretations

Produce a canonical base result and separate namespaced compatibility outputs, each bound to its own definition, upstream dependency version and historical provenance. A disagreement record should contain district/parcel key, base/compatibility definition digests, candidate IDs and chain positions, byte/content evidence references, responsible rule/decision, dependency checkpoint/range fingerprints and observed historical source. Keep uncertainty separate from disagreement: a validator with missing evidence returns unknown rather than fabricating a yes/no.

Base results stay stable when a compatibility validator is installed or changed. Validators can run on the same preserved canonical inscription events when sufficient, but some interpretations require a different upstream Ord profile; do not force every validator to inherit one already filtered inscription stream. Retain enough raw candidate/decision evidence or replay references to revisit a compatibility dispute. Do not implement historical exclusion lists until sourced and versioned.

The minimal future Bitmap demonstration is a bounded corpus containing ordinary claim, duplicate district claim, reinscription/history-sensitive candidate and an explicit compatibility disagreement. Verify independent builds yield identical base roots; compatibility disagreement creates separate evidence and does not rewrite the base root. This proves the architecture without claiming a complete mainnet Bitmap index.

## Direct answers to brief questions 5–14

5. Gateway knows IDs, versions, bounded selectors, reachability/transports, publication consent, health and evidence metadata. Apps own payload meanings and authorization.
6. A control plane with opt-in per-protocol shared data paths. Heavy protocols normally establish their own transport; discovery is not a NAT traversal or privacy guarantee.
7. Protocol family ID, compatible wire version and optional namespaced opaque realm selector. Add only context demanded by that family, with separate privacy policy.
8. None for the first local registry/symmetric-session proof. Add separate advertised protocol families for rendezvous/catalog/index/PEX. A change to hello's required semantics needs explicit new negotiation/version, not silent reinterpretation.
9. Evolve from inspection-only manifests to active, leased, principal-bound registrations with handlers, permissions, withdrawal and publication policy; never execute a downloaded manifest as code.
10. Content-addressed normative definition plus per-node built-instance metadata. Same definition, distinct ranges/checkpoints/evidence and independent publication/retention are normal.
11. Domain/encoding version, definition/rules, network genesis, exact interval, chain anchors, start checkpoint, all semantic dependency/input commitments, canonical ordered output/count, and end checkpoint. A digest is not evidence of correctness by itself.
12. Block-local events/locators can start at arbitrary verified ranges; balances/current UTXOs, full sat state, Ord history and Bitmap winner/ownership state require their declared prior state. Conditional sparse forward sat tracking can avoid a full global sat database but cannot skip required evidence.
13. No universal minimum is established until exact Bitmap/Ord semantics are fixed. Keep the inscription/UTXO/prior-eligibility/ordering/winner dependency closure, plus transfers if ownership is required; full media/UI/all-sat tables can be excluded where not semantically needed. Initialize by replay or an explicitly validated/trusted checkpoint, not an assumed first Bitmap height.
14. Pin original/base behavior only where evidenced, specify remaining choices openly, and run independently versioned compatibility validators alongside it. Separate outcomes/disagreement evidence; do not mutate the base or fabricate historical authority.

## Stage and proof recommendations for the root roadmap

- Networking/0.5.x floor: fix private graph publication, manifest inventory consent and damaged-storage negative claims alongside session correctness. Tests are concrete regression fixtures; no generic framework needed.
- Provider proof: a local authenticated test provider registers an unfamiliar ID/realm, discovers a compatible counterpart, withdraws on expiry, and opens a separate mock transport. Old v1 peers continue ignoring unfamiliar families. Rollback disables the registry and retains bundled modules.
- Index proof: adapt one block-local locator/event index to definition/instance metadata, two adjacent ranges and a deliberate gap. Independent replay roots match; incompatible checkpoint roots differ; incomplete or corrupt segments never acquire coverage. Rollback leaves existing graph/cache intact and treats new metadata as optional sidecars.
- Stateful proof: checkpoint/resume with exact dependency fingerprints, reorg invalidation and cancellation/crash tests; separate positive evidence from snapshot negatives. No remote checkpoint becomes trusted solely because its digest matches its own manifest.
- Ord/Bitmap proof: choose/pin semantic sources and edge-case corpus; first prove candidate extraction, then historic eligibility and optional ownership. Existing known-ID Ord functionality stays labelled as such. No full-mainnet completeness claim until differential scans and historical fixtures justify it.

## Packaged reproduction runner

The scratch overlay paths in the original research command were local work files. From the repository root, run `python docs/audits/2026-09-26/run-reproductions.py --go <path-to-go>` to generate a temporary overlay for the packaged fixtures. See [verification.md](verification.md) for the complete environment and limitations.
