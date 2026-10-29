# Gateway 0.7.9 indexing workflow proposal

This brief supersedes the provisional Parcel discovery workflow. Only checked,
canonically selected Parcel results and validated coverage are published. Missing
or unknown proof stops Parcel at the affected block; it is neither invalid nor
coverage. Preserve any experimental discovery data read-only. District and
Parcel advance as a coordinated pair based on committed validated Parcel
completion; other ready producers may advance while Parcel waits. Parcel Live
continues after catch-up when District and its selected proof producer are also
explicitly configured for Live. It waits without publishing if evidence lags.
Out remains a hard cap.

Attempting to arm Bitmap Parcels opens a concise dependency setup. Parcels is
pending only while the setup is open and remains off if setup is abandoned.
Select Bitmap Districts and exactly one of Raw blocks, TX locators, or TXIO as
the Parcel proof route. The dependency rows stage the actual producer choices;
other independently selected tracks remain selected. The single footer changes
from Disarm Parcels to Set In point to zero once both requirements are selected.
Confirming commits Parcel selection and sets In and the navigation playhead to
zero without starting work. Selection means configured, not built. The admitted
proof route and indexing scope remain explicit, and the backend checks
actual evidence, lineage, values, witness and parent proofs. There is no per-run
validation-operation quota; pause, stop or missing evidence still stop work. A missing dependency
or proof cannot become executable through a different API or publication path.

Current Addresses uses the same single-footer setup when compatible, caught-up
UTXO state with Live is unavailable. Its UTXO row requires Arm and Live. An
abandoned setup leaves Current off; confirmation sets In and playhead to zero.
Existing compatible UTXO coverage is reused without reset. Historical Addresses
retains its own dependencies. Values and ScriptPubKeys keep their inherited TXIO
Live behavior in 0.7.9; distinguishing inherited Live from unavailable controls
is deferred to 0.8.

Genesis-required selection and dependency configurations identify their scope in
metadata and the plan. Once confirmed with In zero, Play uses that scope without
another boundary chooser. Otherwise a compact chooser presents each distinct
relevant genesis, origin, explicit In, and saved continuation boundary once with
its affected tracks, using direct bottom buttons. A choice is re-planned and
admitted only with a matching current plan token. Resume retains its admitted
scope and staged edits do not mutate running work. Existing compatible coverage
and checkpoints are reused; track origins and gaps remain enforced.

The ordered sweep shares verified block reads and decoding, reuses compatible
committed intervals, and keeps holes honest. Independent ready producers may
advance while a dependency is blocked; a caught-up Live track may receive one
cohort in eight when the selected tip advances. Out is a hard cap. Reviews of more than
4,096 sparse Historical Address blocks remain explicitly unverified rather than
assuming continuity.

Use the friendly names Bitmap Districts and Bitmap Parcels in user-facing text,
without changing persisted identities. Timeline hover uses a thin preview line;
ordinary row clicks move the navigation playhead, including group rows. Shift
previews the highlighted track's origin before its start or its compatible saved
indexed end inside or beyond coverage; Shift-click commits that preview. Keyboard
transitions work with a stationary pointer, and hover never fetches, changes
range marks, or starts indexing. Sparse gaps must not be presented as continuous.
Dependency-panel refreshes preserve scroll and focus.

Verify setup dismissal and dependency invariants, plan revalidation, origin and
coverage enforcement, validation-only Parcel catch-up, proof readiness and real
Ord proof checks, as well as timeline interaction. Do not claim full-chain
acceptance from synthetic fixtures.
