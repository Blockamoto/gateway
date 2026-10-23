# Timeline and Bitmap development preview

This local development build combines the timeline controls, persistent resource workspace and experimental Bitmap indexes. It builds on the released Sats source. Its application compatibility version remains 0.7.7; the development label and source receipt distinguish it from that published release. It is not a new published release or an updater promotion.

Use the supplied portable directory and its own data profile. Keep other Gateway installations and profiles separate. The running preview address is supplied with the build; `Start preview.cmd` can start another portable session and prints its local address. No browser or Windows integration registration is required.

## Raw storage and Core-based acquisition

The [Core alignment contract](BLOCK-FETCHING-CORE-ALIGNMENT.md) describes the current block-acquisition work. Raw Blocks is a retention choice: save verified serialized Bitcoin bytes and the small storage catalog needed to find those bytes and resume safely. It does not build new transaction-ID lookup rows or save decoded transaction/input/output arrays. Open a known block or BPS coordinate to derive its details on demand. Use the optional Transaction Locator track when you want new transaction-ID-to-location lookup coverage.

New bodies use bounded append segments. Existing legacy data remains readable without automatic conversion or deletion. New schema 3 records require this development build; use its separate portable profile when comparing with an older executable. Logical range deletion may retain shared physical segment bytes until safe compaction; the completion receipt must distinguish those facts.

Bulk and on-demand requests share the hash-correlated body-download manager. Bulk work fills a forward window while consumers decode and publish in order. The pinned Core reference supplies peer capacities, pool topology and stall policy; Gateway keeps its own bounded ephemeral-memory budget and existing source precedence. These choices do not establish a numerical speed ratio against Core.

To check the separation, start Raw Blocks alone over a small valid range, then open a block and transaction by known position. Do not expect a new bare transaction hash lookup to succeed solely because Raw stored that body. Inspect Transaction Locator if the location is unknown; inspecting it must not arm or start it. Repeat with Raw Off and another eligible consumer armed: source bytes can be read temporarily without creating Raw retention coverage.

## Shared indexing controls

Every available built-in track appears by default. Expand a group to see its layers. Plus adds custom Bitmap rulesets; displaying or creating a track does not start indexing. Track numbers are omitted. Drag the dedicated grip, or use its Alt+Up/Down alternative, to arrange sibling tracks; children stay within their parent and groups move with their descendants.

- **Arm** and **Live** are the icon buttons themselves, with a faint button outline when off, visible pressed states when on, hover explanations and accessible names. Arm selects a consumer; Live chooses following new blocks. **Play/Start** validates and starts these exact choices in one press, including a mix of finite and following tracks. It does not change Live or open a review popup. Status and unmet requirements appear in the top-left panel. A marked Out caps all consumers. Resume preserves the saved run's admitted choices.
- **Pause**, **Resume** and **Stop** act on the coordinated run, preserving committed records. Turning a consumer Off constrains its next safe publication. Turning Live off stops its following after the admitted target. Arming or changing Live during a run requests admission at its current safe processing boundary. Earlier blocks are never backfilled in that run. Pause stays paused; Stop prevents queued joins. Changing build/recovery options requires stopping and preparing a new run.
- Aligned consumers share the block read, decode and inscription extraction. Different checkpoints and missing dependencies can require later replay; this is not a promise to fetch every historical block only once forever.
- Caught-up Live consumers get turns while other consumers process historical blocks. Unchanged selected tips cause no repeated source fetch or index publication; a new selected block or reorg requires progress/reconciliation.
- Arming **Raw blocks** retains newly fetched serialized bodies. With it Off, other armed indexes use new bytes temporarily. Existing saved bodies, external source files and mandatory state/recovery facts are preserved.

Requirements appear beside the selected resource or index overview. The persistent run-status panel distinguishes obtainable source bytes from required saved state, optional lookup evidence and joins. **Inspect** opens the relevant details. Including a prerequisite is an explicit selection; missing state never silently starts a genesis scan. A blocked track shows its reason rather than requiring a second Play press.

Current Addresses projects a complete saved UTXO checkpoint. Sats and UTXO each maintain their own prefix state; neither requires a prebuilt historical TXIO index. Historical Addresses can scan independent creation ranges. Unknown or unavailable state does not mean a zero balance or an empty result.

## One global range and navigation

Use the main address bar or timeline to focus a resource, then **I** sets In and **O** sets Out. Known output-local Sat positions retain their precise coordinates; the status panel also shows the inclusive whole-block interval the builders can process. An address or an unknown/lost Sat location cannot supply a unique position. Clear In, Clear Out and Clear range are separate accessible actions. The Handles command creates both boundaries inside the current visible timeline, spaced apart to grab. Handles retain typed transaction/output/Sat positions at deeper levels; a single visible position cannot create two distinct handles. Drag a handle to adjust it; right-click or Delete clears it.

The same marks apply to every armed track. Per-track ranges are deferred. A marked Out caps Live. Sats and UTXO require genesis or authenticated prior state; Bitmap requires its declared origin and exact earlier evaluation. A later In cannot replace this prefix. Out can cap a valid continuous build. A newly armed track without the prefix remains armed and blocked at the active boundary; explicitly Stop and start a suitable run to build earlier state. Current Addresses projects a whole saved UTXO checkpoint. The [origin report](INDEX-DEPENDENCIES.md#required-origins-and-range-behavior-in-the-development-build) lists the rules for each index.

Left/Right steps one item and Shift+Left/Right skips to the previous/next ruler notch at the current zoom level, matching the bar-and-arrow skip buttons. Up/Escape returns to the parent; Down/Enter explores the focused item. Home/End selects the verified parent bounds. Ordinary typing and native control shortcuts are preserved. Navigation never starts indexing or Satline.

Skip also uses the selected track's known bounds. Before an index's positive starting block, Next/Shift+Right goes directly to that start; after a known saved end, Previous/Shift+Left returns to that end. Within its range, normal zoom-relative ruler notches apply, with encountered saved coverage edges as additional stops. Saved scope takes precedence over the declared start. At transaction or Sat depth, boundary checks use the containing block; a boundary jump returns to block level and pans into view. Unknown, group and unavailable-chain cases retain normal navigation. These actions do not alter Arm, Live or global range markers.

Ordinary wheel input scrolls vertically. Ctrl/Command+wheel or pinch zooms in the time area; Shift/horizontal wheel pans. The left label column always scrolls vertically. A centered pull-tab expands and collapses the timeline. Its compact height follows the controls instead of reserving blank space. Expanded groups hide their child-name summary; collapsed groups retain it. Navigation help scrolls within the window and dismisses on outside click or Escape. Sat ranges stays directly beneath the other TXIO layers while retaining its independent Sats engine.

## Resource workspace

Blocks, transactions, inputs/outputs, inscriptions, Sats, addresses, P2PK destinations and Bitmap results use one persistent timeline workspace. Resource details, requirements and advanced index settings live in the upper-left panel. Contextual visualization uses the upper-right panel. Settings and Peers remain separate destinations and preserve the timeline on return.

The physical backtick key maximizes the hovered workspace panel, falling back to the most recently focused panel. Press it again or use the visible **Restore layout** button to return to the saved arrangement. Escape closes an open menu, dialog or help first, then restores a maximized panel before normal timeline navigation. The shortcut is inactive while typing in editable fields. Maximization keeps the same resource and content instances and does not start indexing.

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

Expand Bitmap and use its blank **+** row to add a District comparison track. Expand a District to see its linked Parcel variants and its own blank **+** row for adding another Parcel variant. These actions create unarmed tracks and select their rule settings; they start no indexing. Each Parcel pins the exact parent District and cannot be moved into another District by dragging or changing rules. Saved sibling order follows this hierarchy, including migration from the older flat Parcel layout.

Click an individual District or Parcel track to inspect its rules in the top-left panel. Choose a profile such as **Bitmap Districts Standard** or **Bitmap Parcels Standard**, or change the supported binary answers. An unindexed custom track remains editable; saved indexing evidence locks its interpretation, and admitted or resumable work reserves its rules until that work is canceled or replaced through review. Standard/Non-standard follows the rule values, independently of a user-renamable display name and stable track ID. Added tracks receive the separate names **Bitmap Districts Custom N** or **Bitmap Parcels Custom N**, even with Standard settings; changing rules does not rename them. Editing a Standard option marks the configuration Non-standard; returning to the reference values restores Standard classification. The rules fingerprint and configuration revision bind every admitted run. Editing invalidates old admission and deletion confirmations; a new Start checks the saved configuration. Renaming does not change the rules identity. Existing fixed legacy profiles and saved data retain their original identities.

The selected track shows its full binary answer string beside its name, with Copy and the exact schema-specific ordered-rule key. This is an inspectable Gateway answer vector, not the rules fingerprint or a claimed upstream canonical encoding. A legacy fixed profile shows a separately labelled policy projection and keeps its original identity.

The [optional comparison rules](BITMAP-OPTIONAL-COMPARISONS.md) describe the latest version. Its `delegate_exclusion=1` rejects a delegate field without resolving it; `raw_utf8_body=1` requires `text=0` and strictly decodes the entire own body without media/charset interpretation. Both default to zero, preserving reference behavior. Older v2 tracks keep twelve/thirteen named answers and their original fingerprints; their editor does not inject new keys. An explicit version change on an unindexed track prepares an upgrade; saving is still required. A built track stays inspect-only.

To check Raw progress, compare **saved blocks** with the separate **Backfill cursor** in track details. Bodies fetched for Districts also count as saved while Raw is armed. An incomplete/rebuilding map is a lower bound, not a finished range or a stopped worker. The saved extent must not stop at a display sampling limit, and the backfill checkpoint must not be presented as the total retained body count.

The named rule editor shows reference values, supported alternatives, conflicts and unfinished interpretations separately. The preserved `text=0, raw_utf8_body=0` **Gateway comparison** matches the inscription's own strict ASCII body while ignoring media-type and charset metadata. It uses the unchanged whole canonical grammar and physical-order winner policy. It is not a canonical Bitmap Standard or delegate/decompression route. Other alternatives lacking an implemented interpretation/evidence validator remain unsupported; a saved draft cannot build.

Each District has its own attached Chain, bound to that instance and profile. When enabled, a slim Chain lane appears beneath the District's timeline lane without a separate named row in the left track list. The District's Chain icon is that child's Arm choice. Independent Chain Live and retention choices remain in the parent track's details. Full history retains each block's commitments; Pruned retains the current tip and validated recovery points, normally the newest two multiples of 12 and newest two multiples of 144. Displayed restore heights are actual coverage, not a guaranteed 288-block rollback window. Both retention modes calculate each completed block, including empty outcomes. Pruned-to-Full restores future retention; missing older records require explicit replay.

A Chain-only run can read sufficient retained District outcomes without raw-block reads or inscription extraction. Missing evaluation stays a gap; the current winner map alone cannot recreate each historical outcome. Chain recovery uses its own baseline and exact District receipts without rewinding a healthy District producer.

Click the Bitmap group name for comparison results; its chevron only expands tracks. Comparison setup stays separate from each track's rule editor and from timeline add rows. Compare actual saved winners, resolved mappings and candidate audits even with Chain Off. Live comparison advances through common completed source-aligned coverage, showing differences, unknowns and lag separately. A different rule fingerprint or cumulative hash alone does not increment the count of newly differing winner blocks. Delta markers and an accessible results list open paired details without moving the playhead or stealing focus.

## Delete saved index data

The selected track's Index Settings includes a bin action. Shift-click track labels to select sibling tracks; Delete opens the selected scope in the top-left panel. Typing fields and open menus/help keep their normal key behavior. Pause active writers first. Review exact dependencies and explicitly include any required dependent tracks; deletion never silently cascades. The receipt persists through refresh and distinguishes deleted, no owned data, retained sources and pending cleanup.

With no In/Out marks, built-in tracks keep their definitions while whole custom District/Parcel deletion removes the selected custom definitions and owned data. Every attached dependent definition must be explicitly selected. Clearing Chain alone keeps its attached descriptor so it can be rebuilt; Manage Chain remains available on its District even when Chain is Off.

With marks, deletion uses the displayed inclusive whole-block interval and retains custom definitions. A missing endpoint resolves to that store's saved extent and is shown before confirmation. Historical Addresses supports sparse interval subtraction; Raw Blocks removes only verified Gateway-owned serialized bodies inside the interval. Transaction locator/TXIO stores support exact suffix rewind. Arbitrary UTXO, Sats, Current Addresses and Bitmap holes are blocked with an explanation, not rounded to another checkpoint or expanded into later data. TXIO layers identify their shared parent scope rather than silently removing siblings.

Mandatory output facts, independent recovery evidence, headers, settings, external Core/configured archives and unrelated indexes are preserved. Raw deletion reports separately retained or unproven files and actual body coverage; an empty Raw index does not mean every available source file has been deleted. Header deletion requires a separate full-profile reset.

The [ruleset and comparison design](BITMAP-RULESET-COMPARISON-DESIGN.md) records the exact implementation target. Exact new-package acceptance is separate from the earlier preview receipts below.

## Verification limits

Acceptance uses bounded genuine early Bitcoin blocks, authenticated synthetic protocol fixtures, source browser checks and the exact compiled Windows app in disposable profiles. Those checks do not establish a whole-mainnet Bitmap or Sats build, production throughput, complete ownership history or full consensus validation. Per-track range editing and arbitrary holes/restore operations for cumulative state remain outside this preview. Supported selective deletion is described above. Main and live updater delivery remain unchanged.

## Performance checks and limits

A controlled excerpt of eight genuine blocks (793922–793929, about 15 MB serialized) took approximately 2.0–2.3 seconds for one District plus Chain and 3.2–3.5 seconds for two of each, with eight source reads/decodes/extractions in either case. This used fresh empty trees and an external fixture-only activation override: it measures bounded processing cost, not correct full-history winners or whole-mainnet throughput. In a separate peer sample, acquisition took about 17 seconds per block while verification took tens of milliseconds. Network conditions and peers vary.

The development pipeline prepares one upcoming block while applying the current block. Source selection avoids repeated failed peers and excludes local request/writer queue time from peer-scoring evidence. Publications remain ordered and recheck source and user intent. Progress is saved per shared cohort and unchanged reconciliation can be reused within the same adapter/authority. These optimizations do not remove index durability or make disconnected frontiers a once-ever fetch guarantee. Exact new-build timing and package acceptance are recorded separately.
