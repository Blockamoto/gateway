# Experimental recipe dependencies

Status: a bounded executor and verified read path exist for the narrow case
below. Dependency-bearing recipes are not admitted by the coordinated
scheduler, the generic sparse consumer, or the guided editor yet. This is not
a claim of complete engine or built-in index parity.

A parent may export one scalar column from a keyed table with an explicit
`"kind": "same_block"` and type `text`, `bool`, `integer`, or `null`. A child
declares a named dependency containing the parent instance, exact saved
revision, semantic hash, export name, type, and explicit
`"coverage": "same_block"`. A `dependency.lookup` expression takes the
declared dependency name and a text key. The compiler rejects unknown names,
missing coverage kinds and unsupported kinds.

The parent must have a completed receipt at the child's exact height and
block hash. A missing key in that complete receipt is absent. A missing,
damaged, differently anchored or incompatible receipt is unavailable and
blocks child completion. A present `null` is distinct from an absent key.
This slice currently rejects transitive parent dependencies.

The child's unit records each exact parent receipt digest and semantic
identity. The executor rechecks those receipts under the same store mutex as
the child coverage compare-and-swap and head publication. The lineage-aware
read repeats that check under caller-supplied selected-chain authority. Generic
store reads and coverage queries reject dependent units rather than report
them as current without validation. The aggregate retained parent-read budget
is an operational limit; exceeding it does not mean a key is absent.

The export kind is deliberately explicit so future authenticated snapshots
and net before/after deltas can define their own coverage, identity and access
contracts. The present scalar same-block lookup does not implement UTXO state,
Current Addresses, snapshot commitments, BIP30 handling or undo.
