# Approved 0.7.8 indexing performance scope

This follows reviewed local source cfde4c9 on branch v0.7.8. Implement fixed
**genesis-aligned336-block batching** and address measured persistence,
duplication, pipeline/backpressure and responsiveness costs. Keep Bitcoin
verification, authenticated per-block identities, dependency order, sparse
consent and durable publication guarantees. This is local development only:
no Gateway main push/merge, release/updater promotion or installation.

## Fixed boundaries and bounded physical work

For height h, chunk=floor(h/336), range=[chunk*336,chunk*336+335]. The end test is
(h+1)%336==0. Starts/resumes, selected ends and live tips may be partial. Bitmap
begins792435 within792288..792623; its first active portion is792435..792623
(189blocks), followed by792624..792959. Never backfill792288..792434 for batching.

Do not retain336 decoded modern blocks. Use bounded byte/record buffers and
physical subflushes within the fixed windows, overlapping acquisition with
application. The improvement must reduce actual filesystem opens, shard data
flushes, receipts and head updates, not merely change a download window/counter.
Retain every transaction locator for every covered block, including coinbase.
Reuse verified bytes, decode and shared extraction; avoid duplicate sparse and
sequential work when another armed consumer still receives all eligible facts.

Apply in correct height/dependency order. Only acknowledge progress after the
corresponding data/receipts are durable. Staged work is not coverage. Pause/cancel
may flush or discard pending work truthfully; resume starts at the first
uncommitted block. Crash/receipt/head ordering, restart/reorg, partial tip
extension, completed empty blocks, RawOff and unarmed publication remain safe.
Use compatible additive storage acceleration when needed, with explicit rollback
behavior and the original immutable per-block commitments preserved.

## Existing semantics and current presentation

Keep fixed Bitmap District/Parcel origin792435, verbatim six/seven source rules,
blocator-only Bitmap persistence, disabled chains and all previously approved
explorer/armed-track behavior. Rename the existing TXIO group to Transactions
and put the existing full Transaction Index/Locators track inside that group.
Preserve index IDs, individual controls and data semantics. Subsequent explicit
human approval settles the current two-level default order as Blocks,
Transactions, Addresses, Inscriptions, Bitmap. Blocks contains Headers and Raw
blocks. Transactions contains Transaction locators, Positional links, Values,
ScriptPubKeys, UTXO set and Sat ranges. Addresses remains a sibling parent for
current address UTXOs and historical outputs. Inscriptions stays standalone above
Bitmap in0.7.8. Bitmap keeps sibling Districts/Parcels and disabled chains. Move
both locators and UTXO into Transactions with safe saved-order migration; this
changes neither data semantics nor consent. A broader parent hierarchy and
expanded inscription indexing remain a future direction.

Every group and individual track has immediate hover/focus purpose help with
click/tap pinning, close, Escape, outside dismissal and viewport clamping. Help
must not select, expand, arm, index, delete or drag a track. A separate dependency
icon describes genuine other-track requirements only: Current Addresses needs
its matching complete UTXO checkpoint; Parcels needs source-aligned District
outcomes and conditionally exact retained pre-reveal Sat context. Parent help
summarizes those child-specific requirements. Generic source availability,
ordered own history and optional locator assistance belong in purpose/context
help, without a universal dependency icon. Published Bitmap rules stay separate.

The actual Gateway Home/workspace root presents lightweight, read-only feature
availability based on gates, providers and retained evidence, never on Arm alone.
Known block-position browsing may fetch temporary bodies with Raw Off. Arbitrary
transaction IDs still need locations. Output addresses do not need an Addresses
index. Numbered-Sat mathematics does not imply output identity or saved location.
Current Addresses requires matching snapshots; history, spenders and Bitmap are
coverage-scoped. A known reveal can work offline from saved Raw without an
Inscriptions inventory. Lean reveal locations alone do not imply content, and
the inscription-viewing setting is honored. No checklist action starts a scan,
fetch, trace or index. Availability indicators are distinct from Live-follow.

The horizontal scrollbar uses a continuous invertible nonlinear mapping between
logical span and thumb span, with progressively fine response at high zoom.
Do not substitute a larger minimum thumb or decorative animation. Both edges
remain grabbable on narrow screens, preserve the opposite exact anchor, and
retain pan, keyboard, wheel and pinch behavior. Lightweight progress polling is
coalesced, pauses while hidden and can be more frequent during active work;
idle/paused polling and detailed status inspection remain slower.

The Gears draft is read-only motivation for336 alignment. Do not implement or
modify Gears, add index/chain hashes, or conflate batching with commitments.

## Measurement and delivery

Use identical hash-pinned blocks and armed tracks in fresh disposable baseline
and candidate profiles. Local replay isolates persistence/processing and is NOT
a Bitcoin peer download or live-throughput comparison. Report elapsed/stage
measurements, physical write/flush/receipt operations and peak memory where
available, plus exact locator/output/coverage equivalence. A separate live check
must use the delivered executable; do not promise an unmeasured speed multiplier.

Inspect the active build/profile and actual coverage before interpreting track
counts. Saved per-source unions and committed run ranges may differ. A broadband
subscription is not measured peer throughput. Detailed status polling costs are
an independent measurement; keep cached presence-observation age explicit and
fresh durable progress distinct from staged work.

The human subsequently authorized disposing of the delivered temporary test
profiles/block/index data and task-owned QA data when useful. Installed Gateway,
unrelated profiles/source work/main remain protected. Verify resolved Windows
targets/process ownership before destructive operations, using one native shell.
Still deliver a separately named portable executable/ZIP and fresh profile with
working browser/start/stop links. Preserve prior artifacts unless cleanup helps
this task; do not ask routine permission questions.
