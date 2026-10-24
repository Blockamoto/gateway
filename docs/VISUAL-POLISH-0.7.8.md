# Local 0.7.8 final visual polish

Baseline: local `v0.7.8` revision
`edc0172323eebf887aa9c1ed54ad09886fb8a40d`.
This presentation pass follows the parent-category correction and supersedes
its disabled Chain mini icon and missing collapsed coverage overviews.
The prior preview on port 54950 remains separate and available.

## Approved behavior

- Empty timeline space uses the existing muted `--bg` theme surface. Rows and
  zoom retain their current dimensions; there is no stretch or automatic fit.
- Inscriptions keeps its parent-style header, alignment and blank disclosure
  slot. Its standalone data lane uses the ordinary data-track surface.
- Collapsed parent lanes compress each child's truthful saved and potential
  coverage into their existing fixed height. Blocks has two bands,
  Transactions six, Addresses two, and Bitmap its child reveal bands plus a
  distinct target mapping band. Unbuilt children retain their own potential
  projection; unknown coverage never becomes available coverage. Expanding a
  parent reveals the existing mini-only children. Headers contain no child
  label list, and individual full-track expansion remains absent.
- Bitmap gains the existing right-side Arm-all pattern for the two active
  Standard V1 consumers only. Off/mixed/on states reflect individual choices.
  Live, range limits and companion consent stay independent. Chain action
  icons leave the mini headers; backend Chain execution remains disabled.
- Block-map transaction position and sats appear immediately in the existing
  bottom caption during pointer hover or keyboard focus. Exit restores the
  normal block summary. There is no duplicate map tooltip or delayed title.
- The same pointer-transparent playhead is visible across parent and child
  lane surfaces, excluding the header controls. Axis coordinates remain exact
  through disclosure, horizontal navigation, vertical zoom and scrolling.

## Validation and limits

Focused source and exact compiled browser checks cover the approved surfaces,
fixed parent overview geometry, independent child projections and potential
bands, Bitmap target/reveal distinction, Arm-all consent isolation, removed
Chain icons, real pointer/focus caption changes, and visible playhead geometry
and pixels. Light and dark screenshots inspect the new surfaces.

No production Go code changes. Prior valid Standard V1, legacy, target/reveal,
reorg/cache, batching/durability and Bitcoin peer evidence is reused. This pass
does not claim a new indexing benchmark or Live-follow result and does not
rerun broad backend/network suites.

The portable ZIP is sealed with an explicit manifest and exact file hashes,
including the tested executable and assets. Runtime profiles, logs and tokens
are excluded. Its own fresh profile starts unarmed, with updates disabled; the
owned launcher start/stop check leaves previous previews untouched.

## Completed focused checks

The exact compiled executable has SHA-256
`7c9a6cfc33471ac1f2ce1dfb2a1331b3f88471982934c93c78a2ed761fdc7898`.

| Check | Evidence/result |
| --- | --- |
| Parent geometry | PASS: all four parent lanes and headers stay 32px; Blocks 2 bands, Transactions 6, Addresses 2, Bitmap 2 reveal ribbons plus a separate target region |
| Theme surfaces | PASS: light empty/header `rgb(244,241,233)`, data `rgb(255,253,248)`; dark empty/header `rgb(14,20,27)`, data `rgb(22,30,39)` |
| Bulk/individual consent | PASS: Bitmap Off/mixed/On follows only the two active Standard consumers; source fixture excludes frozen/Chain rows and preserves Live, hard range and optional companion choices |
| Compiled UI actions | PASS: seven explicit prepare requests exercise Arm-all/individual Arm and Live; no run starts, no read-only interaction changes consent, no JavaScript errors |
| Footer | PASS: real compiled genesis pointer/focus readout and restoration, absent overlay/native titles, stable narrow caption/stage/map/camera geometry; source fixture also checks mixed two-tile ownership and long labels |
| Playhead | PASS: visually inspected compiled chain-axis pixels with parents closed/open in both themes; exact ruler-aligned geometry and pointer/header exclusion |
| Real saved Bitmap data | PASS on this exact executable: target winners 0/3/7 remain distinct from reveal coverage 792435–792442 with parent closed/open; saved heads/jobs/run unchanged, no body fetch/index POST |

Compiled evidence is under `build/qa-078/visual-polish/compiled-home`,
`compiled-viewport-verified` and `compiled-targets`. Source hierarchy fixtures
cover deeper transaction/output coordinate guards and independent child models;
compiled chain screenshots are not presented as deeper-axis pixel proof.
The source results and sampled saved/ghost/gap/playhead pixels are recorded in
`build/visual-bundle-timeline-qa/result.json` and `pixel-result.json`.
Previous backend/network evidence is reused, not rerun or claimed as a new
performance measurement. All owned compiled QA runtimes stopped cleanly.

## Portable delivery

Directory: `build/portable-v0.7.8-visual-polish`.
ZIP: `build/Gateway-v0.7.8-visual-polish-portable-preview-windows-amd64.zip`.
Verification output: `build/qa-078/visual-polish`.
Exact executable SHA-256 and source revision are recorded in the delivered
`artifact-manifest.json` and the verification JSON files.
