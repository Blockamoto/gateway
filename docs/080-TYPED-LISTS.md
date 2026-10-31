# Typed lists and parent declarations

The common ordered value model now retains lists of typed items, including
nested objects/lists, exact uint64 values and bytes. Empty lists remain `[]`;
missing is a separate value. Existing value encodings and native semantic
bindings are unchanged when lists are unused.

`list_length` returns the item count, `list_at` returns an item or missing for
an out-of-range unsigned index, and `list_contains` compares exact scalar types
without coercion. Membership supports text, uint, bool, bytes and missing; use
`list_at` and `get` to inspect containers. Each membership comparison consumes
interpreter work. Depth, structural work, decoded bytes, state transaction
budgets and cancellation apply to nested values. Imported values are cloned
recursively so retained list bytes cannot alias the source cache.

Typed ordered reveal events expose `parents`: the parsed parent declarations
in parser order, alongside `parent_count`. These are claims from the admitted
own-envelope inventory. They do not establish consumption, ownership or an Ord
parent relationship. The parser/reference source binding remains explicit.
Existing sparse reveal fields and metadata/properties behavior are unchanged.

`recipes/declared-parent-mentions.json` demonstrates a continuous reveal table
that uses `list_contains` and retains the matching declarations. Replace the
placeholder parent ID and review the origin before running it. This is a mention
tracker, separate from the checked provenance required for validated Parcels.

Lists round-trip through saved typed state and exact output JSON. Sparse typed
projections can emit lists through their ordinary array representation. The
guided editor supports nested list literals, exact item values, insertion,
removal, reordering and the list function selector. Existing unsupported shapes
remain raw JSON without silent rewriting.

Verification covers mixed exact scalar membership, max-uint JSON, order, empty
and malformed lists, bounds, work/byte budgets, cancellation, recursive owned
copies, a public saved-table query and admitted parent declaration events.
Unit and headless real-DOM checks cover actual example round trips, nested item
controls and narrow layout. Visible preview acceptance and the remaining Parcel
proof-source composition are still open.

The wider ordered/reveal/import/District/catalogue suite passed 83 checks in
55.675 seconds. The added coverage caught a prior scheduler regression: a
history-mode guard intended to avoid reparsing sparse reveals had also excluded
sparse transaction projections from their shared owned source. Restoring those
transaction cohorts passed the existing exact source-sharing test three times
(13.901 seconds for that focused set). The audit is
`gateway-080-typed-lists-regression.jsonl`.
The final production build passed (25,241,088 bytes).
