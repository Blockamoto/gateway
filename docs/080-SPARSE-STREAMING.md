# Sparse row streaming

Public independent and DAG sparse runs now stage rows to owned files, seal the
existing canonical schema-1 JSON unit, and publish its existing immutable hash
through the selected-header/run/registry and coverage-revision fence. This path
has no aggregate event, block or unit output-size quota. Source admission,
expression work, definition and decoded-source budgets still apply. Native
built-in compact stores and their semantic checkpoint mappings are unchanged.
The complete 0.8.0 preview remains unfinished for the scope listed in the main
engine ledger.

The former real-source failure is now a passing persistence/read audit:
verified block 792435, 3,139 rows and a legal 16 KiB annotation produce exactly
52,463,224 bytes with the frozen logical digest
`60d405ebf28a790218357d8196666a5d53587d539736a3cbb59028519ddce862`.
Stage, publication and verification measured 2.569 seconds here. Cumulative
allocations were 465,715,832 bytes; this is allocation traffic, not peak live
memory or full-chain throughput. Run with
`GATEWAY_RECIPE_LARGE_OUTPUT_AUDIT=1` for the optional diagnostic report.

A public coordinator test also consumes an authenticated Bitcoin genesis block
with a legal sub-256-KiB definition whose shared variable is projected into
three large rows, totaling over 64 MiB. It covers a dependent child reading the
parent's text export, output paging, complete row/unit downloads, and a second
finite run reusing coverage without a source read. The complete test passed in
46.16 seconds on this machine. This intentionally expensive projection is not
a normal-index throughput measurement.

## Physical implementation

`recipe_sparse_spool.go` owns sequential row/key files, a bounded in-memory
membership set, and disposable native `ordinalTree` pages for larger sets.
Exact duplicate bytes are compared from the owned row spool. Errors poison the
unpublished stage. Late filters, conflicting duplicates and duplicate export
type checks preserve the existing interpreter's order and meaning. Native
materialized adapters retain their own explicit bounded utility paths; public
custom sparse runs use the spool.

`recipe_sparse_sort.go` sorts fixed-size references rather than row/key
payloads. Bounded batches spill into temporary runs. Equal-size level
compaction bounds live metadata and merge fan-in bounds open files. Keys use
fixed prefixes and chunk comparisons for long common prefixes. The final
stream verifies full key identity, row digests, extents and unique order.

`recipe_sparse_json_stream.go` encodes one field at a time, with underlying
writes of at most 64 KiB. It preserves `encoding/json` ordering, escaping,
exact numbers, raw JSON, custom marshalers and nil forms. Source/expression
budgets still govern individual in-memory fields. A test streams a row larger
than 64 MiB without a whole-row result buffer.

`recipe_sparse_publish.go` verifies and installs synced immutable payload bytes
before entering the authority fence. Failed, stale or canceled publication
leaves those bytes unreachable. Existing hashes, receipt refs, native COW page
codec, durable head history and the head visibility boundary remain unchanged.
The optional unsynced tree flag applies only to disposable scratch roots.
Durable trees retain their original synced writes and flushes.

`recipe_sparse_view.go` copies a unit to an owned temporary file and verifies
its whole digest before parsing. A JSON grammar scanner records row/value byte
offsets without decoding skipped values. Bounded key metadata spills into
native scratch pages. Typed metadata is distinct from a materialized unit:
unread rows cannot become a false evaluated-empty result. Small metadata reads
reuse the existing bounded decoder for speed. Lineage/coverage/reorg proofs
retain metadata, and dependency lookups decode only the selected scalar.
Owned views and spools remove only their checked, uniquely owned directories.

## Output and remaining work

The normal output endpoint pages by immutable unit token and offset. Existing
small oversized rows retain their inline behavior. Very large rows return an
exact JSON attachment link, without rejecting durable output or requiring the
browser to parse the entire value. Saved tracks expose that link and a complete
block JSON download. Payload copying for the root output view occurs outside
selected-header/registry locks, followed by a final authority/ref recheck;
network response writes occur after those locks are released.

A 208.406-second focused consumer/DAG/lineage/output/range regression passed
before the last small-unit fast-path and export-type refinements. The final broader suite passed in 787.730 seconds. The focused sparse suite
passed in 121.637 seconds after abstracting row policy from physical storage;
the combined range-export/large-output set passed in 89.287 seconds. Canonical bytes,
long keys, scratch spill, malformed JSON, cancellation, corruption, unpublished
failure, reopen, exact scalar types and late filters have focused coverage.
The four recipe/workspace JavaScript checks pass.

Generic range row export and its GUI now consume these views; see
`080-HISTORICAL-RANGES.md`. Full maintenance/retention policy and visible Chrome
acceptance remain open. Coverage/lineage integrity checks still
read committed payloads; large graphs may repeat that I/O. Bulk export must
reuse one checked immutable header fragment rather than hash a wide interval
per row page. The private bounded secondary-index experiment remains outside
public execution. No page-pack, slow staged ordered backend, or new permanent
row-index format was introduced. The remaining built-in conversions and wider
ordered contracts remain part of the accepted release scope.
