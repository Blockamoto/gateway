# Timeline and Bitmap development preview

This local development build combines the timeline controls, persistent resource workspace and experimental Bitmap indexes. It builds on the released Sats source. Its application compatibility version remains 0.7.7; the development label and source receipt distinguish it from that published release. It is not a new published release or an updater promotion.

Use the supplied portable directory and its own data profile. Keep other Gateway installations and profiles separate. The running preview address is supplied with the build; `Start preview.cmd` can start another portable session and prints its local address. No browser or Windows integration registration is required.

## Shared indexing controls

Every available built-in track appears by default. Expand a group to see its layers. Plus is disabled and reserved for future custom indexes; displaying a track does not start it.

- **Arm** is a record-symbol switch selecting a consumer; **Live** is a separate switch for following new blocks. The symbol toolbar has hover explanations and accessible names. **Play** stages Live off for armed tracks and reviews a finite pass. **Record** initially stages Live on for eligible armed tracks and reviews catch-up plus following. Adjust individual Live switches and review again to retain a mix of finite and following tracks; repeated Record reviews preserve those choices. Confirm with the same symbol after reviewing dependencies and range; changing action requires a fresh review. A marked Out caps either mode.
- **Pause**, **Resume** and **Stop** act on the coordinated run, preserving committed records. Turning a consumer Off constrains its next safe publication. Turning Live off stops its following after the admitted target. Other changes wait for a new reviewed run.
- Aligned consumers share the block read, decode and inscription extraction. Different checkpoints and missing dependencies can require later replay; this is not a promise to fetch every historical block only once forever.
- Arming **Raw blocks** retains newly fetched serialized bodies. With it Off, other armed indexes use new bytes temporarily. Existing saved bodies, external source files and mandatory state/recovery facts are preserved.

Requirements appear beside the selected resource or index overview. The run review distinguishes obtainable source bytes from required saved state, optional lookup evidence and joins. **Inspect** opens the relevant details. **Include in run** explicitly stages a prerequisite and requires another review. No missing prerequisite silently starts a genesis scan.

Current Addresses projects a complete saved UTXO checkpoint. Sats and UTXO each maintain their own prefix state; neither requires a prebuilt historical TXIO index. Historical Addresses can scan independent creation ranges. Unknown or unavailable state does not mean a zero balance or an empty result.

## One global range and navigation

Use the main address bar or timeline to focus a resource, then **I** sets In and **O** sets Out. Known output-local Sat positions retain their precise coordinates; the review also shows the inclusive whole-block interval the builders can process. An address or an unknown/lost Sat location cannot supply a unique position. Clear In, Clear Out and Clear range are separate accessible actions. The Handles command creates both boundaries inside the current visible timeline, spaced apart to grab. Handles retain typed transaction/output/Sat positions at deeper levels; a single visible position cannot create two distinct handles. Drag a handle to adjust it; right-click or Delete clears it.

The same marks apply to every armed track. Per-track ranges are deferred. A marked Out caps Live. Complete-prefix engines still require genesis or authenticated saved state; a deep marker cannot bypass that requirement.

Left/Right steps one item and Shift+Left/Right skips to the previous/next ruler notch at the current zoom level, matching the bar-and-arrow skip buttons. Up/Escape returns to the parent; Down/Enter explores the focused item. Home/End selects the verified parent bounds. Ordinary typing and native control shortcuts are preserved. Navigation never starts indexing or Satline.

Ordinary wheel input scrolls vertically. Ctrl/Command+wheel or pinch zooms in the time area; Shift/horizontal wheel pans. The left label column always scrolls vertically. A centered pull-tab expands and collapses the timeline. Its compact height follows the controls instead of reserving blank space. Expanded groups hide their child-name summary; collapsed groups retain it. Navigation help scrolls within the window and dismisses on outside click or Escape. Sat ranges stays directly beneath the other TXIO layers while retaining its independent Sats engine.

## Resource workspace

Blocks, transactions, inputs/outputs, inscriptions, Sats, addresses, P2PK destinations and Bitmap results use one persistent timeline workspace. Resource details, requirements and advanced index settings live in the upper-left panel. Contextual visualization uses the upper-right panel. Settings and Peers remain separate destinations and preserve the timeline on return.

Address-bar navigation, Back/Forward and supported browser handoffs keep their resource meanings. Inscription content retains its isolated origin and sandbox. Satline remains disabled. Saved Sat ranges, locations and compatible known holdings retain their existing coverage and checkpoint rules.

## Experimental Bitmap indexes

The Bitmap group has three independent consumers and stores:

| Consumer | What it establishes |
| --- | --- |
| Districts | Candidate decisions and first eligible District claims under the declared development interpretation. |
| Parcels | Transaction coordinates and checked selected-District parent provenance under a separate experimental interpretation. |
| Bitmap Chain | Per-block and cumulative commitments to completed District winner outcomes, including evaluated empty blocks. |

These consumers share source decoding and authenticated inscription extraction. A separately retained Inscriptions index is optional. Parcel parent metadata alone is not proof. The implemented proof path uses matching pre-block Sat evidence and exact reveal input consumption; missing evidence and unsupported same-block transfer context remain unresolved. The preview does not start Sats or historical tracing to fill that gap. Independent District/Chain work can continue while Parcels waits.

Use `H.bitmap` for a district, `T.H.bitmap` for a parcel, and `H.bitmapchain` for a chain block. `bitmap.gateway` and `bitmapchain.gateway` open introductory views. The panel separates source, profile, coverage, gaps and evaluated outcomes. An evaluated block with no winners advances the chain; incomplete evaluation does not. Exact commitments and raw evidence are available in Advanced.

The pinned reference revision and explicit development semantic profiles appear in the evidence. They are not claimed to be final canonical Bitmap standard profiles, ownership proofs or Bitcoin consensus validation. The legacy locked Bitmap recipe remains separate. Parcel outcomes do not silently enter the District-only audit commitment.

## Verification limits

Acceptance uses bounded genuine early Bitcoin blocks, authenticated synthetic protocol fixtures, source browser checks and the exact compiled Windows app in disposable profiles. Those checks do not establish a whole-mainnet Bitmap or Sats build, production throughput, complete ownership history or full consensus validation. General index deletion/replay tools and per-track range editing remain outside this preview. Main and live updater delivery remain unchanged.
