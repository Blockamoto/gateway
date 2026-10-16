# Timeline and Bitmap development preview

This local development build combines the timeline controls, persistent resource workspace and experimental Bitmap indexes. It builds on the released Sats source. Its application compatibility version remains 0.7.7; the development label and source receipt distinguish it from that published release. It is not a new published release or an updater promotion.

Use the supplied portable directory and its own data profile. Keep other Gateway installations and profiles separate. The running preview address is supplied with the build; `Start preview.cmd` can start another portable session and prints its local address. No browser or Windows integration registration is required.

## Shared indexing controls

Every available built-in track appears by default. Expand a group to see its layers. Plus adds custom Bitmap rulesets; displaying or creating a track does not start indexing. Track numbers are omitted. Drag the dedicated grip, or use its Alt+Up/Down alternative, to arrange sibling tracks; children stay within their parent and groups move with their descendants.

- **Arm** and **Live** are the icon buttons themselves, with visible pressed states, hover explanations and accessible names. Arm selects a consumer; Live chooses following new blocks. **Play/Start** reviews these exact choices, including a mix of finite and following tracks. It does not change Live. The review explicitly explains the second press that starts work. A marked Out caps all consumers. Resume preserves the saved run's admitted choices.
- **Pause**, **Resume** and **Stop** act on the coordinated run, preserving committed records. Turning a consumer Off constrains its next safe publication. Turning Live off stops its following after the admitted target. Other changes wait for a new reviewed run.
- Aligned consumers share the block read, decode and inscription extraction. Different checkpoints and missing dependencies can require later replay; this is not a promise to fetch every historical block only once forever.
- Caught-up Live consumers get turns while other consumers process historical blocks. Unchanged selected tips cause no repeated source fetch or index publication; a new selected block or reorg requires progress/reconciliation.
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

The Bitmap group has three consumer kinds, with separate saved state for each interpretation:

| Consumer | What it establishes |
| --- | --- |
| Districts | Candidate decisions and first eligible District claims under the declared development interpretation. |
| Parcels | Transaction coordinates and checked selected-District parent provenance under a separate experimental interpretation. |
| Bitmap Chain | Per-block and cumulative commitments to completed District winner outcomes, including evaluated empty blocks. |

These consumers share source decoding and authenticated inscription extraction. A separately retained Inscriptions index is optional. Parcel parent metadata alone is not proof. The implemented proof path uses matching pre-block Sat evidence and exact reveal input consumption; missing evidence and unsupported same-block transfer context remain unresolved. The preview does not start Sats or historical tracing to fill that gap. Independent District/Chain work can continue while Parcels waits.

Use `H.bitmap` for a district, `T.H.bitmap` for a parcel, and `H.bitmapchain` for a chain block. `bitmap.gateway` and `bitmapchain.gateway` open introductory views. The panel separates source, profile, coverage, gaps and evaluated outcomes. An evaluated block with no winners advances the chain; incomplete evaluation does not. Exact commitments and raw evidence are available in Advanced.

The pinned reference revision and explicit development semantic profiles appear in the evidence. They are not claimed to be final canonical Bitmap standard profiles, ownership proofs or Bitcoin consensus validation. The legacy locked Bitmap recipe remains separate. Parcel outcomes do not silently enter the District-only audit commitment.

## Custom Bitmap tracks and comparison

Add a District or Parcel ruleset through Plus. District creation optionally adds a linked Parcel track; the same shortcut is available later. Parcel pins the exact selected District interpretation. Saving a supported profile creates independent unarmed, non-Live tracks. Changing executable rules creates a new profile/track; renaming preserves identity. The new registry preserves the older preview profiles and their saved data.

The named rule editor shows reference values, supported alternatives, conflicts and unfinished interpretations separately. This pass includes an explicit **Gateway comparison** with `text=0`: match the inscription's own strict ASCII body while ignoring media-type and charset metadata. It uses the unchanged whole canonical grammar and physical-order winner policy. It is not a canonical Bitmap Standard or delegate/decompression route. Other alternatives lacking an implemented interpretation/evidence validator remain unsupported; a saved draft cannot build.

Each District has its own Chain child, bound to that instance and profile. Chain On is its Arm choice. Its Live choice follows completed District outcomes. Full history retains each block's commitments; Pruned retains the current tip and validated recovery points, normally the newest two multiples of 12 and newest two multiples of 144. Displayed restore heights are actual coverage, not a guaranteed 288-block rollback window. Both retention modes calculate each completed block, including empty outcomes. Pruned-to-Full restores future retention; missing older records require explicit replay.

A Chain-only run can read sufficient retained District outcomes without raw-block reads or inscription extraction. Missing evaluation stays a gap; the current winner map alone cannot recreate each historical outcome. Chain recovery uses its own baseline and exact District receipts without rewinding a healthy District producer.

Click the Bitmap group name for Overview/Compare; its chevron only expands tracks. Compare actual saved winners, resolved mappings and candidate audits even with Chain Off. Live comparison advances through common completed source-aligned coverage, showing differences, unknowns and lag separately. A different rule fingerprint or cumulative hash alone does not increment the count of newly differing winner blocks. Delta markers and an accessible results list open paired details without moving the playhead or stealing focus.

## Delete saved index data

The selected track's Index Settings includes a bin action. Review its saved-data scope and dependencies, then explicitly confirm deletion. Pause active indexing first. Saved dependent indexes must be cleared before their source; deletion never silently cascades. The track/ruleset remains available for an explicit rebuild. Shared output facts, independent recovery archives, source caches, external blocks, settings and unrelated indexes are preserved. Header deletion requires a separate full-profile reset. TXIO layer deletion identifies the whole parent-store scope rather than silently removing sibling data.

The [ruleset and comparison design](BITMAP-RULESET-COMPARISON-DESIGN.md) records the exact implementation target. Exact new-package acceptance is separate from the earlier preview receipts below.

## Verification limits

Acceptance uses bounded genuine early Bitcoin blocks, authenticated synthetic protocol fixtures, source browser checks and the exact compiled Windows app in disposable profiles. Those checks do not establish a whole-mainnet Bitmap or Sats build, production throughput, complete ownership history or full consensus validation. Advanced selective deletion/replay tools and per-track range editing remain outside this preview. Main and live updater delivery remain unchanged.
