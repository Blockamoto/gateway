# Sparse streaming work

Status: in progress. The public sparse writer still has the provisional 16 MiB
whole-unit limit, and the interpreter still has event/block output buffers.
The new private helpers below are not yet wired into public persistence. No
finished preview is implied by their tests.

The opt-in real-source audit (`GATEWAY_RECIPE_LARGE_OUTPUT_AUDIT=1`) reproduced
the problem on verified block 792435: 3,139 transaction rows with a legal 16 KiB
annotation produced 52,463,224 canonical unit bytes. Interpretation completed,
but the existing writer rejected the unit at 16,777,216 bytes. This is a valid
custom projection, not an accepted output-scope restriction.

The implementation direction is to preserve the existing canonical JSON unit
and logical unit digest, stream staged rows, and use bounded temporary metadata
for duplicate checking and sorting. Native built-in compact stores remain in
place. No page-pack, slow experimental ordered staging backend or new persistent
storage framework is required for this change. Prefer owned byte snapshots and
temporary native-COW lookup indexes for large-unit reads before introducing a
new persistent index/receipt format; measure any later need for that format.

`recipe_sparse_sort.go` now sorts spool references rather than row/key payloads.
Small bounded batches spill to temporary fixed-size metadata runs. Equal-size
levels compact during ingestion, bounding live run metadata logarithmically and
avoiding repeatedly merging a growing run with tiny runs. Merge fan-in bounds
open files. Keys use fixed prefixes and chunk comparisons for longer common
prefixes; they preserve legacy bytewise ordering without a key/result quota.
These run records are temporary working data, not a new store generation.

`recipe_sparse_json_stream.go` preserves `encoding/json` row bytes while
encoding one field at a time and writing at most 64 KiB per underlying call.
It preserves key order, HTML escaping, exact `json.Number` values, raw JSON,
custom marshalers and nil forms. It drops only Encoder's final newline.
Cancellation, short writes and encoding failures abort staging. A test streamed
a row exceeding 64 MiB without a whole-row result buffer.

Combined canonical encoding/sort tests passed in 4.492 seconds, including long
keys, multiple merge levels, exact row identity, source-prefix changes,
truncated runs, empty input, cancellation, sink errors and cleanup. The audit
passed in 4.921 seconds and intentionally records the current persistence gap;
replace its expected rejection with end-to-end success when integration lands.

Next implementation work:

1. Add an owned row/key spool and exact duplicate-byte checks. Keep only bounded
   reference metadata in memory and spill keyed membership to `ordinalTree`.
2. Seal the canonical, sorted JSON unit by streaming; reuse immutable file and
   atomic head publication, with the existing selected-header/run/registry CAS.
3. Give sparse consumers and DAG execution a streaming sink, preserving late
   filters, duplicate/export checks and existing small-result bytes. Remove the
   provisional output-size result quotas from that path.
4. Add context-aware metadata/row iterators and owned large-unit views for
   paging and dependency scalar lookups. Do not materialize large units or
   represent an unread materialization as an empty row array. Preserve schema-1
   data and normal native checkpoint identities.
5. Wire public queries, dependency/reorg audits, lifecycle and generic range
   row export to those views. Bulk export should reuse one checked immutable
   header fragment and verify current range receipts/headers at completion.
6. Measure allocations, I/O and restart/cancellation behavior on the same real
   source, then finish maintenance and visible end-to-end acceptance.

The remaining built-in conversions and wider ordered contracts continue in the
main engine ledger. This work does not reduce their accepted release scope.
