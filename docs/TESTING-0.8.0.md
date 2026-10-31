# Gateway 0.8.0 portable preview

This local development preview exercises the shared JSON recipe engine. It is
not an installer, update feed or deployment. Its build disables update delivery.
The preview companion has its own extension/native-host identity and is optional;
this test does not register or install it.

## Start and stop

Extract the complete archive into a new directory. Keep `portable.marker` beside
`GatewayClient.exe`. In PowerShell, from the extracted directory:

```powershell
.\Preview.ps1 -Offline -NoOpen
```

The launcher prints a loopback URL, PID and profile directory. Open the printed
URL in Chrome. The default profile is `blocks-on-demand-data` beside the preview;
use `-ProfilePath` to name another disposable directory. Core and Core mounting
are disabled for a new profile, and offline mode also disables network/header
sync. No installed profile is needed. Preserve the printed profile for restart.

```powershell
.\Preview.ps1 -Stop
```

`Start-Preview.cmd` starts the ordinary preview and opens the default browser.
For this offline acceptance exercise, use the explicit PowerShell command above.
Do not point the preview at an installed profile. Changing a draft, opening a
resource, arranging tracks or arming a row does not start its index producer.

## Author, inspect and run

1. Open Indexes. Select a native track and inspect its executable Recipe, including
   independently retained variants. Create a separate copy to customize it.
2. Use New draft for sparse inscription/transaction projections, New state table
   draft for ordered typed tables, or the header/Raw projection starters. Raw JSON
   import/export and guided controls edit the same document. The archive includes
   the exact executable documents under `recipes/builtins`.
3. Choose an independent ID/name and origin. Block 1 is valid for header, Raw,
   sparse transaction and general ordered-table recipes when their declared inputs
   are available. Coin and Sat state require their genesis prefix or an exact
   compatible retained checkpoint. District/Parcel source origins and proof
   requirements remain explicit.
4. Guided sparse filters cover metadata, metaprotocol, Ord properties and traits.
   Ordered controls expose expressions, typed bytes/lists/ranges, branches, reads,
   puts/deletes, output declarations and exact source/checkpoint pins. A display
   parent/group is navigation; an execution input must be declared in the recipe.
5. Validate and save. Arm the track, choose In/Out or Live, then review the plan.
   Start the reviewed plan only when its source/dependency requirements are met.
   Missing coverage waits; an empty evaluated result remains distinct from a gap.
6. Inspect committed rows and coverage. Queries, range exports and large-row
   downloads retain their applicable snapshot/selected-chain guards. Stop/restart
   the preview, reload the saved recipe and check reuse. Rebuild creates a reviewed
   new generation/archive; an edited name does not rewrite old results.

A fresh offline profile contains the Bitcoin genesis header but has no mainnet
body or historical index coverage. To run a nonempty mainnet recipe, configure
appropriate explicit source access or supply an isolated reviewed fixture. Do
not mistake offline missing evidence for a broken recipe or a zero balance.
Header-only work needs no block bodies or UTXO history.

## Suggested acceptance path

- Check home/resource navigation, the shared address field, index settings,
  timeline range/arming, display hierarchy and narrow layouts.
- Copy a built-in, edit a predicate/projection, export/import, validate/save/reload
  and compare the exact guided/JSON definition. Include a metadata/trait draft and
  a typed ordered table draft.
- On explicit verified source data, run a bounded finite recipe, an evaluated-empty
  predicate and a child with a separately reviewed exact parent. Verify missing
  parent coverage waits, then backfill explicitly and retry.
- Exercise stop/restart, prefix reuse, finite Out, Live/pause and saved row queries.
  The automated fixture suites also cover reorgs, corruption, stale publication,
  independent owned evidence, archive and maintenance.

See `REVIEW-0.8.0.md`, `080-PARITY-MATRIX.md` and the family notes for implemented
behavior, exact source/semantic/storage boundaries and bounded test evidence.
`artifact-manifest.json` records the source revision, dirty flag and SHA-256 of
every packaged file. Visible Chrome acceptance is still pending until its actual
observations are recorded; headless DOM checks alone do not complete it.
