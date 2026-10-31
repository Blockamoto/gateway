# 0.8.0 integration and parity matrix

This is the current implementation boundary, not release acceptance. An actual
recipe controls derivation through the common compiler/interpreter; a native
physical codec may retain its checked storage and publication contract. Existing
native identities and data are preserved unless an explicit migration is stated.
A saved definition or graph is not proof of source coverage.

| Family | Native / saved recipe execution | Evidence and remaining boundary |
| --- | --- | --- |
| Bitcoin headers and Raw block retention | Existing native paths preserved; actual compositional recipe migration remains open. | Source admission, selected-header fences and retained body codecs stay in place. Retention/source recipe controls still need integration. |
| TX ID Locators | Actual native JSON projection and independent saved transaction recipes run through the common engine. | `080-NATIVE-LOCATOR.md`; native reverse lookup retains its physical codec and frozen binding. |
| Inscriptions Lean / Full / optional and saved conventional IDs | Actual JSON programs control native projections and saved tracks. | `080-NATIVE-INSCRIPTIONS.md`; metadata/Ordprops parsing and same-block dependencies are integrated. Explicit legacy numbering remains a separate open layer. |
| UTXO Coin state | Actual ordered JSON drives native and saved Coin state. | `080-UTXO-ADAPTER-DECISIONS.md`, `080-ORDERED-PUBLIC-INTEGRATION.md`; native COW/facts/undo, genesis prefix, finite/Live, query, reorg, archive and maintenance are integrated. This fixed Coin contract is not arbitrary table storage. |
| Address History, current and legacy payment destinations | Actual JSON controls native selection; saved sparse typed projections use the common engine. | `080-NATIVE-ADDRESS-HISTORY.md`, `080-SPARSE-STREAMING.md`; retained native reverse rows and sparse custom outputs have their distinct query contracts. |
| Current Addresses, current and legacy payment destinations | Native and saved checkpoint projections execute actual JSON against an exact declared built-in or saved Coin source. | `080-NATIVE-CURRENT-ADDRESSES.md`; public scheduling, source pinning, direct destination query, owned facts/COW, recovery, child-only archive and maintenance are integrated. |
| Sat FIFO current state and retained original output ranges | Native and saved prefix recipes execute actual current/history JSON through the typed interpreter. | `080-NATIVE-SATS.md`; physical parity, private receipt/CAS, shared cohort source/inventory, finite/Live, prefix reuse, fragment query, savepoint recovery, pause/restart, archive and collection have focused coverage. The wider regression passed in 836.739 seconds, with focused follow-up checks after the final query guards. Native recovery cadence is explicit; arbitrary old intermediate snapshots are not fabricated. |
| Retained output values and exact scripts | Actual independent JSON projections drive the shared native fact writer and saved sparse copies. | `080-RETAINED-OUTPUT-FACTS.md`; physical schema/page/reference parity, separate field policies, typed native proposals, public saved/native cohorts and inspector controls are integrated. Retention ownership and coverage remain with each native parent. |
| TXIO positional links | Existing native creation/spender derivation remains; compositional migration is open. | Preserve source lookup/proof order, duplicate incarnations, pending translations and missing-evidence semantics. No whole-index dispatch wrapper counts as migration. |
| Bitmap Districts and validated Parcels | Current native fixed profiles, proof routes and saved legacy data preserved; actual compositional recipes remain open. | Preserve first-eligible order, exact source/profile identity and validated-only Parcel publication. Deferred/legacy generations cannot be silently enabled or reinterpreted. |
| Other independent legacy/source retention layers | Explicit mapping still open. | Include legacy numbering and compact source/fee evidence in the final audit. Existing host source-fact retention is not a claim that every independent retained layer has its own editable recipe. |
| General ordered tables | Arbitrary named typed tables execute saved JSON through the common interpreter and shipping native COW pages. | `080-TYPED-TABLES.md`; declared origin, public finite/Live, prefix reuse, exact queries/large-row downloads, retained recovery, archive and corruption-aware collection are integrated. The slow staged-state and page-pack experiments remain excluded. Full-chain performance remains unproven. |
| Broader typed incoming lookups | Same-block scalar and Coin snapshot/delta contracts are integrated; wider source/state dependency kinds remain open. | General typed tables currently reject dependencies and exports until their checked coverage contracts are implemented. |
| Guided editing and hierarchy | Five source capabilities, typed instruction/expression controls, general table declarations/starter, exact parent pins, import/export and independent display hierarchy are integrated for the implemented contracts. | Remaining built-ins need the same practical authoring coverage. Unsupported forms preserve raw JSON. Editing, arming and navigation alone start no work. |
| Complete working preview and visible acceptance | Open. | Production builds and focused/headless browser checks are intermediate evidence. Visible browser control cannot initialize because its kernel-assets path is missing. Do not claim complete 0.8.0 acceptance until all required families above and the isolated preview audit are finished. |

The real block-792435 projection/retained-fact checks and bounded synthetic
reorg/state fixtures have separate roles. They are not full-chain replay or
complete consensus validation. Detailed test durations, exact semantic bundles,
performance measurements and migration decisions belong to the linked documents
and `080-ENGINE-LEDGER.md`.
