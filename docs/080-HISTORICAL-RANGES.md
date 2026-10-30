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
