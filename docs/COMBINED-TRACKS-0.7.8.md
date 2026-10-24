# Local 0.7.8 combined-track correction

This small correction starts from local design-preview commit
`5b3fcdbb294c2fd73c31326afc7e406013e10627` on `v0.7.8`. It replaces that preview's
separate compact child rows with the approved combined-parent presentation.
The previous design preview remains separate and usable. No push, main merge,
publication/update, installation, Gears or global navigation change is included.

## Corrected presentation

Each parent defaults to one combined track row. Its compact child labels align
vertically beside their own mini lanes inside that same parent row. The parent
has no expand-all action. Clicking a child's own label/disclosure expands only
that child into a full lane/control row; all other children stay inside the
parent. Collapsing the child returns it to its original ordered mini position.
There is no duplicate mini/full representation. Multiple independent expansions
and the all-expanded state keep the organizational heading visible.

This is a dedicated condensed overview, not full compact track headers merely
nested in a group. The six-child Transactions parent has an explicit maximum
180px height budget at the 202px label width. Short contextual mini labels have
full accessible names/help; full metadata, status and controls belong to the
individually expanded track. Mini strips and the parent heading remain small
while preserving real pointer/focus targets.

The existing order remains Blocks, Transactions, Addresses, standalone
Inscriptions, Bitmap. Preserve saved user order and deliberate per-child choices;
older prototype group-default flags do not expand children automatically. New
portable profiles demonstrate the combined-parent default.

Green left selection accents, compact sizing, 202px redesigned headers,
right-side purpose/dependency controls, popover fixes, red record-style Arm,
transparent drag grips and the disabled Chain-link glyph remain. Live ON gets a
vivid electric blue pressed state in both themes, with accessible pressed/focus
and disabled states. OFF stays muted. Colour does not enable Live or change Arm,
range caps or indexing behavior. Disclosure, selection, help and dragging do
not start indexing or change consumer settings.

The single Bitmap parent target band remains distinct and visible: District
targets on the Bitcoin-block axis, Parcel targets on the transaction axis inside
a block. Child lanes retain reveal indexing coverage from 792435. Standard V1
IDs/fresh stores, read-only frozen generations, bounded target queries and
unknown/gap semantics are unchanged.

## Verification

Backend production sources are unchanged from the previous verified design
preview. Existing Standard V1, legacy identity/provenance, target/reveal,
nonempty synthetic Parcel, cache repair and reorg evidence remains applicable.
The 130-height target setup and broad UTXO maintenance suite are not repeated.
Relevant vet and embedded-asset/progress/layout/provider checks passed (6.150 s
for the focused Go group).

Production-asset and exact compiled browser checks cover combined defaults,
aligned label/lane pairs, A-only expansion with B/C still inside the parent,
collapse order, multiple/all-expanded states, no parent expand-all, no duplicate
representations, selection/navigation and unchanged independent consent. Real
pointer/focus/pinning checks cover the 202px header and narrow iframe. Reorder
grips remain transparent and usable. Both themes verify independent red Arm,
vivid blue Live and muted OFF without execution side effects. Existing parent
target, Standard/legacy, progress cadence and terminal-refresh checks remain.

The separately packaged executable is checked against the ZIP manifest, actual
compiled assets and live Bitcoin access. Start/stop ownership checks use only
its new profile. Timings are acceptance observations, not a new performance
comparison or universal speed claim. The exact final executable/source revision
and evidence are recorded with the portable manifest and final test results.

## Final exact-binary checks

Executable SHA-256:
`5ae602f49c7c6ae822887cf8616d1d0a34817b7148c499dfe8c3190ddd0a4ccf`.
The packaged executable must match this tested digest.

| Check | Result |
| --- | --- |
| Condensed combined default | PASS; Transactions 178px at 1440px and 390px viewport widths, 32px heading and six 24px miniature strips |
| Mini readability/hit areas | PASS; 202px label column, single-line unclipped contextual labels with full accessible names, complete buttons fit within each strip |
| Independent expansion/order | PASS; one child, multiple children and all-expanded states; collapse restores original mini position; no duplicate representations or parent expand-all |
| Pointer/keyboard/appearance | PASS; real hover, focus, pinning and Escape in desktop/narrow iframe, transparent grips, green selection edge, red Arm, blue Live and muted OFF |
| Live colour/independence | Light `#0062ff` with white foreground (5.01:1 contrast); dark `#00b7ff` with `#00152a` foreground (8.08:1); explicit Live leaves Arm OFF |
| Compiled side-effect checks | Home/help/disclosure/selection cause zero index POSTs; exactly four explicit prepare calls test Arm ON/OFF and Live ON/OFF; no run starts |
| Actual saved Bitmap target/reveal UI | PASS; parent winners 0/3/7 versus child reveal coverage 792435–792442, unchanged heads/jobs/run, no paint-triggered body fetch or index POST |
| Actual peer access | PASS; one authentic historical block 792435, 1,834,669 bytes, all 3,139 transactions including Coinbase, fresh Standard identities and target/cache checks; Raw OFF |
| Frozen legacy inspection | PASS; 40 evidence files unchanged, explicit V2 reads, execution locked, no implicit Standard creation |
| Reused-output proof | All three actual stored first-commit files match the first commits from the earlier verified eight-block Standard run byte-for-byte |

The one-block peer acceptance took 17.794s. It checks historical Bitcoin P2P
access and verification, not new-tip Live-follow or a throughput multiplier.
Go production sources, execution transport, active polling and terminal-refresh
logic are unchanged; earlier eight-block, provenance, reorg, batching and target
coverage evidence is reused. Relevant source regression suites, vet and the
focused Go checks remain passing. No slow broad maintenance run was repeated.

Evidence:

- `build/reviewed-qa/combined-parent-independent`
- `build/reviewed-qa/combined-tracks-compiled-clear`
- `build/reviewed-qa/combined-tracks-actual-targets`
- `build/qa-078/combined-tracks/live-color-contrast.json`
- `build/qa-078/combined-tracks/peer-one-block`
- `build/qa-078/combined-tracks/legacy-v2`

All owned QA runtimes stopped cleanly. The previous review preview at port 51285
remained untouched throughout this correction.

## Portable use

New directory: `build/portable-v0.7.8-combined-tracks`.
New ZIP: `build/Gateway-v0.7.8-combined-tracks-portable-preview-windows-amd64.zip`.
`Start-Preview.cmd` opens Home using its own fresh `preview-profile`;
`Stop-Preview.cmd` verifies/stops that profile's runtime. Updates are disabled.
Profiles, logs and runtime tokens are excluded from the ZIP. Installed and
unrelated profiles remain untouched.
