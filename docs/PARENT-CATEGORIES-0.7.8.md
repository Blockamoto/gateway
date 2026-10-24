# Local 0.7.8 parent-category interaction correction

Baseline: local `v0.7.8` at `2998faa3739526831a47c1883da63a576e724d7c`.
This focused presentation pass supersedes the previous combined-parent and
per-child expansion interaction. Previous portable previews remain separate.
No push, main merge, release/update, installation, Gears, indexing, storage or
protocol redesign is included.

## Confirmed interaction

Parents are categories and default closed. Their disclosure reveals/hides their
child mini tracks; closed parents show no child label list. Children have no
individual disclosure, click-to-expand or separate full-track mode. Selecting
a child opens its complete state, counts, ranges and settings in the existing
upper details panel. The existing right-side track zoom enlarges visual tracks.

The order remains Blocks, Transactions, Addresses, standalone Inscriptions,
Bitmap. Inscriptions matches parent styling and title indentation, reserves the
same empty disclosure slot, and has no expander or invented children. It remains
selectable with its own right-side controls and details.

Child Arm/record, Live, disabled Chain-link where applicable, dependencies and
purpose help sit in one compact right-side area. Existing parent Arm-all also
moves right. Inline OFF/status and coverage-count metadata leave the mini
headers; accurate data remains in the upper details/settings panel and accessible
pressed states/tooltips. Genuine dependency semantics, fixed help/popovers,
transparent grips and the green left selection edge remain intact. Arm ON stays
red, Live ON electric blue, OFF muted; disclosure, selection, help and zoom do
not change consumer consent or start indexing.

The one Bitmap parent target band remains visible and distinct: District claims
at Bitcoin block targets, Parcel claims at transaction targets inside a block.
Child lanes retain reveal-history coverage from 792435. Standard V1 IDs, frozen
legacy evidence and unknown/gap semantics are unchanged.

## Real vertical geometry

The right-side zoom changes actual row/lane dimensions and canvas backing
heights, not just a CSS variable. The compact initial scale remains small;
parent-style Inscriptions also responds when other categories are closed.
Zoom uses actual visible layout anchors and excludes hidden children from
geometry. Opposite-edge, keyboard and wheel anchoring remain supported without
the former uniform-row/count assumption. Labels and right-side hit areas remain
aligned and unobstructed at supported scales.

## Verification and evidence

Focused production-asset and exact compiled browser checks cover default-closed
parents, open/close, hidden child labels, mini-only children, right controls,
selection into upper details, no inline status/counts, Inscriptions indentation
and blank disclosure slot, real hover/focus/pinning in desktop/narrow iframes,
and red/blue/muted state isolation. Pointer and keyboard zoom checks inspect
actual rectangles and canvas backing heights with parents closed/open and at
mid-list scroll positions; they verify visible anchor stability and unchanged
prepared index choices.

Backend production code is unchanged. Valid prior Standard V1 identity,
legacy/provenance, target/reveal/reorg/cache, durable batching and Bitcoin peer
evidence is reused. No slow broad UTXO or 130-height setup rerun is needed for
presentation-only changes. Relevant vet/assets/layout/progress checks and
compiled asset serving are checked. No new throughput or new-tip Live-follow
claim is made.

The ZIP's explicit file list and hashes must match the exact tested executable.
Its new profile starts unarmed; updates remain disabled. Owned start/stop checks
leave the previous review at port 60651 available. Exact final source revision,
executable hash and verification results accompany the portable manifest.

## Final executable checks

Executable SHA-256:
`b0f70267f714c6bca8064887798a5955791f5ec508a22be778c8497b689792b4`.
The ZIP executable must reproduce this tested digest.

| Check | Result |
| --- | --- |
| Default categories | PASS at 1440px and 390px; closed parents hide child labels/rows, with no obsolete per-child expansion |
| Inscriptions | PASS; parent-style 32px root row, reserved blank disclosure slot, matching parent typography, title indent 46px, no fake children/expander |
| Right actions/details | PASS; compact 22px controls fit the 202px sidebar, selection shows actual counts/state above, no inline mini OFF/count metadata |
| Zoom with categories closed | Actual Inscriptions row 32→40px and canvas backing height 31→39 |
| Keyboard zoom with children open | Actual row 24→32px and canvas backing height 23→31 |
| Pointer zoom at mid-list | Actual row 72→78.65625px, canvas backing height 71→77; preserved-anchor error 0.14453125px |
| Pointer/keyboard/touch and help | PASS; category disclosure, selection, real action hit targets, focus, pinning/Escape and narrow iframe geometry |
| Consent/style | Disclosure, selection, help and zoom send zero index POSTs; four explicit prepare calls test red Arm ON/OFF and blue Live ON/OFF without starting a run |

Normal/OFF action chrome is bare and muted like purpose/dependency icons;
hover, keyboard focus, mixed, disabled and red/blue pressed states remain clear.
The actual compiled UI passes with no JavaScript errors; its owned runtime exits
cleanly. Relevant post-freeze vet passed, and focused assets/layout/progress Go
checks passed in 4.874s. Production Go sources are unchanged.

Evidence:

- `build/reviewed-qa/category-interaction-independent`
- `build/reviewed-qa/parent-categories-source-final`
- `build/reviewed-qa/parent-categories-compiled-final/result.json`
- `build/reviewed-qa/parent-categories-actual-targets/result.json`

The saved Standard target/reveal matrix passed with the category parent closed
and open: target winners 0/3/7 stay distinct from reveal coverage
792435–792442, with unchanged heads/jobs/run and no body fetch or index POST.
That matrix ran before the final Inscriptions-only font alignment; Bitmap code
and geometry were unchanged, so its valid evidence is reused. The final native
suite additionally checks the completed Inscriptions typography alignment.
Previous real Bitcoin peer, exact commit-prefix, legacy and nonempty Parcel
evidence remains applicable because backend code is unchanged. No throughput
comparison or new-tip Live-follow claim is made by this presentation pass.

## Portable use

The follow-up background polish paints unused track-area space with the existing
`--bg` theme token, matching the surrounding timeline in light and dark modes.
It changes no row sizes, zoom defaults, selection, coverage or scrollbar geometry.

Directory: `build/portable-v0.7.8-parent-categories`.
ZIP: `build/Gateway-v0.7.8-parent-categories-portable-preview-windows-amd64.zip`.
`Start-Preview.cmd` opens Home in its own fresh `preview-profile`;
`Stop-Preview.cmd` verifies/stops only that runtime. Profiles, logs and runtime
tokens are excluded from the ZIP. Installed/unrelated profiles stay untouched.
