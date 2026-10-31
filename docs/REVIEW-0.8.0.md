# Gateway 0.8.0 engine review

The enabled native indexes and optional retained derivations now execute actual
compositional JSON through a common compiler/interpreter. Saved and custom
recipes use that engine with their own declared source, origin, state, inputs
and outputs. A recipe never dispatches an entire legacy index by ID or a hidden
whole-index operation. Native physical adapters validate proposals and preserve
existing formats, readers, release gates and reviewed semantic bindings.

## Shipped behavior

- Typed sources: inscription reveals, transaction positions, ordered transaction
  events, Coin snapshot/deltas, Sat FIFO facts, selected headers, complete blocks
  and body-by-hash facts. Header-only recipes do not acquire bodies. Unknown Raw
  height never becomes genesis or prefix coverage.
- Actual native documents: headers/Raw, TX locators, Lean/Full inscriptions and
  conventional IDs, TXIO links and output values/scripts, Coin, current/historical
  addresses, Sat current/history, Standard Districts and validated replay Parcels.
  Independent fee and checked Core-prefix programs use explicit amount/fee facts.
- General ordered COW tables: typed keyed values, lists/bytes/exact uint64s,
  conditionals, read/put/delete and byte/MIME/decimal/FIFO primitives. Sparse row
  spooling supports large committed output and bounded streaming downloads.
- Explicit incoming data: same-block exports, exact Coin snapshots/deltas, typed
  table checkpoints and point facts for block context, inscription identity,
  checked parent replay, input amount and Core fee. Missing coverage, known
  absence and rejected proof remain separate. No declared input starts a producer.
- Independent evidence: used values and required proof blobs remain with the saved
  child through query, producer removal, archive and corruption-aware collection.
- Lifecycle: reviewed finite/Live jobs, independent frontiers, source sharing,
  publication fences, pause/restart, retained prefix/recovery, reorg exclusion,
  owned queries and reviewed generation replacement. Display hierarchy is separate
  from execution dependencies.
- Guided authoring: the actual built-in programs round-trip through the same
  typed instruction/expression controls and raw import/export. Native variants
  and independent retained layers can be copied into custom tracks.

## Compatibility and storage

Native rule identities and byte layouts remain frozen through explicit program,
source and physical semantic bindings. Changed unreviewed native programs fail
closed. Custom results use private semantic generations and cannot inherit
native coverage by name. Old identities/readers and gated historical numbering,
Bitmap/Chain and Satline stores are preserved; conversion enables no old writer.
See `080-RETAINED-LAYER-AUDIT.md` for the explicit inventory.

Saved state is under profile-local `indexes/recipe-*` generations; typed tables
use `indexes/recipe-tables-v1/<instance digest>/<semantic>`, COW pages, immutable
points, owned imports and optional source blobs. Native Coin/Sat and sparse
layouts retain their dedicated codecs. Do not edit these stores by hand.

## Evidence and limits

The family documents and `080-ENGINE-LEDGER.md` record the individual focused and
wider regressions, exact semantic pins and frozen 0.7.9 writer comparisons. Real
Bitcoin blocks and the positive Ord parent pair verify authenticated source and
proof behavior. Controlled synthetic fixtures isolate positive policy, recovery
and adversarial transitions. The real saved Parcel fixture is covered-empty;
a positive mainnet Bitmap Parcel has not been claimed.

These are bounded checks, not full-chain throughput or complete Bitcoin script/
UTXO consensus validation. The measured native Raw path adds allocation overhead
for its independent owned parse. Existing benchmark observations are scoped;
no noisy single-case timing establishes a speed improvement. Local negative
attestations and explicit Core fee facts retain their respective trust sources.

The obsolete staged-state/page-pack experiments remain excluded. Experimental
recipe fingerprints changed where documented; preserve old generations and use
an explicitly reviewed new generation rather than relabelling incompatible data.

## Preview status

`TESTING-0.8.0.md` gives isolated start/stop and the practical authoring/acceptance
path. The portable build disables update delivery and includes all executable
JSON documents, review notes and a hashed source/artifact manifest. It neither
publishes a release nor modifies main, 0.7.9, Render or installed user data.

Headless source-identical editor/workspace checks pass. Visible Chrome control
currently fails before JavaScript initialization with Windows path error 3 while
writing kernel assets, including after a kernel reset. That acceptance remains
pending; this review does not label it complete. No new design decision is
required from the user for the implemented contracts.
