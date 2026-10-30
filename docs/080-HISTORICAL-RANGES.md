# Historical recipe receipt ranges

The read-only local endpoint `/api/v1/index/recipes/coverage` enumerates saved
sparse receipt anchors for independent recipes or a complete same-block DAG.
It requires `id`, `revision`, `semantic_hash`, `from` and `to`. Optional `limit`
is 1–32. Continue with the returned `next` as `after` and the exact returned
opaque `snapshot` token. There is no automatic backfill or indexing operation.

An anchor represents an authenticated committed unit, including an evaluated
unit with zero rows. A missing height is uncovered. Exhausting the enumeration
does not assert that all heights in the interval have coverage. Unavailable,
stale, fenced or malformed parent evidence rejects the page; it is never an
empty result. This endpoint enumerates anchors, not row exports.

The stateless snapshot binds the exact root recipe revision, semantic identity,
profile, interval, every transitive ancestor's published immutable receipt head,
and a digest of the selected local header bytes for precisely that interval.
Old heads use the existing content-addressed `heads` history; no new backend,
snapshot files, run or checkpoint generation is created by reading. The token
is an inspectable snapshot descriptor, not a signature or publication lease.
Every use checks it against current state and local authority.

Current receipt trees are compared with the saved trees over the entire
requested interval, including previously returned heights. Identical immutable
subtrees reuse their digest. Differing boundary leaves compare exact entries;
an append outside the range may split a leaf without changing the historical
view. One leaf's entries are compared with the other tree incrementally, never
by materializing the full opposing subtree. A changed referenced ancestor in a
prior page rejects continuation. An append within the requested interval also
changes its view, including previously absent coverage.

Header bytes are read in 64 KiB chunks under the selected-header fence. The
whole requested byte interval is checked on each page. This detects changes to
previous-page headers even when the range's last header and the current tip
remain unchanged. Appends beyond the requested interval do not affect its hash.
Registry identities, reconcile fences, complete current per-page lineage, unit
bytes and selected anchor hashes are rechecked before exposing a response.
Unchanged prior unit bodies are not decoded again merely because Live advanced;
their immutable receipt identities are compared through the saved tree roots.

The chain claim remains selected local header bytes as of this read, without an
independent Bitcoin consensus claim. No mutable latest-head revision is used
as the historical continuation identity. Existing coordinator/reconciliation
coverage keeps its stronger current-head/run revision fence; the historical
query path does not weaken that admission machinery.

Tests cover three-level lineage, reopen, unrelated receipt and header append,
changes within the interval, a prior-page transitive ancestor replacement,
prior-page header replacement with an unchanged endpoint, exact prefix restore,
independent empty units, unavailable header range, graph/profile/scope changes,
missing published-head history, malformed queries and boundary leaf splitting
in both directions. The focused range/store/DAG coverage set passed in 16.188 s. The broader
DAG/lineage/dependency/store/coverage/output/coordinator/saved-recipe and ordered
public suite passed in 146.488 s after a missed output-route return was fixed.
The production build passed.

An opt-in synthetic warm-file measurement (`GATEWAY_RECIPE_RANGE_MEASURE=1`)
used repeated real block-one header bytes, not a valid chain. Five reads averaged
5.301 ms for 10,000 headers (0.8 MB), and 257.534 ms for 1,000,000 headers (80 MB),
allocating about 69.8 KB per read. This measures header-interval hashing only,
not full range-query, chain-validation or export throughput. Interactive page
checks are linear in header range width; bulk row export should reuse one
checked immutable header fragment and verify the current interval again at
completion instead of repeating that wide scan for each row page.

General custom row-range export, its UI and bulk performance remain open release
work. The experimental generic sparse unit/decoded-byte budgets also remain an
open persistence boundary. This receipt-range path does not declare those tasks
complete or reduce the required built-in/custom indexing scope.

## Generic committed-row range export

`GET /api/v1/index/recipes/export` accepts the immutable recipe `id`, `revision`
and `semantic_hash`, plus inclusive `from` and `to` heights. It consumes only
committed sparse reveal/transaction receipts. It does not fetch source blocks,
start a run, backfill gaps or build a secondary index. Saved recipe tracks expose
From/To inputs and a JSONL download using the same current or historical identity
as the output controls. Ordered state has its separate snapshot query; this row
export does not misrepresent an ordered checkpoint as sparse output.

The JSONL format begins with a `gateway-recipe-rows` version-1 manifest naming
the immutable identity, range, local-header authority and receipt-range snapshot.
`unit` records include height/hash, logical unit digest, row count and lineage;
zero rows explicitly mean an evaluated empty block. `row` records stream the
original stored key/value JSON. `uncovered` records coalesce inclusive missing
intervals. A final `complete` record reports rows, covered units and uncovered
blocks. A complete artifact may have uncovered blocks: coverage is explicit,
and the HTTP Complete/Partial headers distinguish complete range coverage from
successful artifact preparation. Exact integer JSON bytes are preserved.

The exporter captures the existing immutable heads for the root and every
ancestor, then copies the selected header interval once into an owned temporary
fragment. It walks receipt keys with radix pruning, verifies owned root payloads
and every declared ancestor at each covered anchor, and streams row spans with
bounded buffers. It retains only a block's graph metadata, not a whole range of
rows or refs. A final fence checks exact current headers, saved graph revisions
and the entire receipt interval for all ancestors. Appends outside the interval
are allowed. It does not rehash a wide header interval per output page.

The entire artifact is staged before response attachment headers are set.
Late corruption, changed prior rows/ancestor refs, unavailable published-head
history, canceled requests or changed selected headers discard the artifact.
The HTTP response includes SHA-256, exact length, row/coverage counts and an
artifact-complete flag. Network copying occurs after authority locks release.
Temporary files belong only to the request and are removed on completion/error.
Owned verified bytes are a coherent snapshot as of their final authority check;
this is selected-local-header evidence, not independent consensus proof.

Focused tests cover exact values, evaluated-empty versus uncovered records,
transitive ancestor changes, prior-header changes, missing history, cancellation,
allowed outside-range append, and late HTTP failure without an attachment.
The focused set passed in 43.953 seconds before the public route/UI wiring.
The large-output coordinator test also exercises range export of its >64-MiB
committed projection. The final route/large-output set passed in 89.287 seconds; the four recipe/
workspace JavaScript checks and production build passed. The wider pre-export
regression passed in 787.730 seconds. Full built-in parity and visible preview acceptance
remain open.
