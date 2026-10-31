# Header and complete-block source recipes

Two new typed sparse sources expose one event per selected height:

- `bitcoin.header` exposes exact 80-byte headers and their decoded fields. A
  header-only run reads selected local headers without block-body acquisition,
  inscription extraction or a UTXO prefix.
- `bitcoin.block` exposes complete independently parsed verified block bytes,
  their SHA-256 and checked size/transaction metadata. A cohort shares one owned
  source with other typed consumers. Raw bytes remain separate from optional
  native Raw retention.

`recipes/builtins/headers.json` and `raw-blocks.json` are actual executable
projections, using ordinary typed fields, decimal keys and emitted row objects.
They run through the common interpreter and existing sparse spool, publication,
query and reorg contracts. Header-only work shares the existing coordinator's
no-body path; mixed cohorts can derive a header projection from the same checked
body as their other consumers. Header carriers explicitly lack complete-body
verification and cannot satisfy a body-consuming recipe.

Saved results have independent coverage and owned row bytes. Their declared
origin can be block 1; there is no implied current coin state. Exact header
bytes establish an input identity, while the existing selected-header/run/
registry fences supply authority. The source adapters do not replace consensus
validation or authorize an unrelated writer.

The catalogue exposes both contracts. The builder provides New header projection
and New Raw block projection starters, typed fields, output declarations, nested
instructions and JSON import/export. The actual documents round-trip through
the guided editor and fit its narrow layout.

This increment enables saved/custom sources. Native header synchronization and
Raw retention still need their own JSON projection/codec integration and frozen
equivalence audits; these two source capabilities alone do not complete that
native migration or the overall preview.

Focused checks passed in 3.543 seconds. They include authentic genesis/block-1
header execution with zero body decodes/extractions, exact owned Raw output,
altered decoded inventory rejection, immutable header bytes, cancellation and
the shared interpreter/spool. The broader source/registry/coordinator/Live suite
passed in 25.906 seconds; its audit is
`gateway-080-whole-source-regression.jsonl`. Guided unit and network-free
headless DOM checks passed for both actual documents and starters.
The wider suite contains 26 passing checks, with no skips or failures. The
workspace browser check also passed after adding the two starter controls.
The production build passed (25,814,528-byte executable).
