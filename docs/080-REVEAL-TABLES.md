# Ordered state from authenticated inscription reveals

Saved `bitcoin.reveal` recipes can use `history: "ordered"` and
`storage: "tables.cow.v1"`. They execute the common typed instructions against
the existing arbitrary named-table COW backend. The declared origin determines
the required continuous prefix. Every evaluated block advances its receipt,
including blocks with no reveals. Finite/Live scheduling, exact queries, prefix
reuse, retained root recovery, reviewed archive/rebuild and collection share the
table lifecycle.

`recipes/reveal-count-state.json` is the executable guided starter. The editor's
**New reveal table draft** opens it, with origin 767430 and a counter incremented
for each authenticated reveal. Origin is editable. A Bitmap claim-state recipe
can explicitly begin at 792435; an isolated later In requires an existing prefix
or an explicitly admitted lead-in. Choosing a source or editing a draft starts
no indexing.

## Owned source and capability modes

The source profile is `gateway-typed-owned-reveal-events-v1`, bound to the
installed inscription parser/reference and block verifier. The table semantic
fingerprint includes that binding. Existing transaction-table and native
semantic fingerprints retain their old bindings.

The host independently parses verified raw block bytes and admits a complete
authenticated inventory. It visits occurrences in transaction position and
transaction-wide envelope order. Caller-edited decoded transactions and forged
inventory bodies cannot substitute. A cohort shares its owned source and
admitted parser inventory across recipes. Own body and content-type bytes are
preserved without delegate resolution, decompression, charset interpretation,
trimming or claim selection.

| Fields | Typed representation |
| --- | --- |
| `kind`, `block`, `txid`, `ref`, `content_hash` | Text; kind is `reveal`, ref is the positional inscription coordinate. |
| `height`, `tx_index`, `reveal_index`, `input_index`, `parent_count` | Exact unsigned integers; reveal index is transaction-wide. |
| `body`, `content_type` | Exact bytes, including malformed text declarations. |
| `has_body`, `has_content_type`, `incomplete_fields`, `duplicate_fields` | Parser facts as booleans. |

The catalogue describes this history/storage-specific view in
`state_sources`. Existing sparse reveal recipes retain their generic fields and
functions, including metadata and properties. Resolving an ordered definition
returns its typed source fields/functions. A transaction script field cannot be
used in reveal mode, nor can a reveal body field be used in transaction mode.

## Reusable text operations

The typed executor and guided function selector include:

| Operation | Behavior |
| --- | --- |
| `bytes_length`, `bytes_slice`, `bytes_concat` | Exact byte length, owned half-open slice, bounded concatenation. Invalid slice bounds return missing. |
| `bytes_equal`, `bytes_find`, `bytes_find_fold_ascii` | Exact equality and byte offsets; folding changes ASCII A–Z only, without modifying source bytes. An absent match returns missing. |
| `bytes_ascii` | Returns text only for ASCII bytes, preserving whitespace/control bytes; otherwise missing. |
| `decimal_parse`, `decimal_format` | Canonical unsigned decimal bytes and exact text formatting, through uint64 max. No signs, whitespace, exponent, leading zeroes or overflow are accepted. Invalid input returns missing. |
| `media_type` | Parses declared content-type text or bytes and returns the normalized media type, or missing for malformed syntax. Parameters do not decode a body. |
| `inscription_ref` | Constructs a positional coordinate from bounded height, transaction and transaction-wide envelope indices. |

Existing `eq` behavior is unchanged. Programs choose byte equality explicitly.
These operations perform no state lookup, evidence acquisition or index
dispatch. They allow a program to compose decoding decisions, canonical claim
grammar, eligibility, counters and keyed first-winner state in ordinary JSON.

## Verification

Exact decimal/byte/malformed-input/cancellation tests and a public table run
exercise the primitives through compilation, execution, storage and query.
Frozen native semantic checks still match. On real block 792435, two ordered
state recipes and a sparse reveal control share one source fetch, one independent
raw-ownership verification and one inscription extraction. The pipeline records
two decodes: initial source admission and the shared independent owned view.
Query, reuse and reviewed rebuild pass. Forged inventories are rejected before
events reach the program; caller-edited decoded identities are ignored. An empty
block is covered with zero entries.

Actual transaction and reveal starters round-trip through guided controls.
Source-specific fields, byte/decimal functions, table edits, exact uints, Apply,
narrow layouts and no network work passed the headless DOM check. These checks
do not constitute visible finished-preview acceptance. Native Bitmap rule
composition and broader typed incoming lookups remain open.

The final focused table/reveal/text/catalogue/saved-recipe suite passed in
28.223 seconds, including Live crossing the declared origin, pause/restart,
retained-root reorg recovery and maintenance. The broader coordinator,
dependency, transaction, table, saved-recipe and frozen-native suite passed all
92 checks in 174.582 seconds. Its temporary session audit is
`gateway-080-reveal-tables-regression.jsonl`.
The production build passed after the final source changes.
