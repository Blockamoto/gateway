# v0.7.7: Sats

Develop on `v0.7.7`, building on the now-published v0.7.6 Addresses release. Incorporate its applicable fixes while preserving the Sat-aware collector. This brief covers the working Sats preview; publication and updater promotion still require the user's release instruction. See [the feature breakdown](SATS-FEATURES-0.7.7.md) for the implemented behavior and its limits.

## Sat identity and output ranges

Adapt the existing FIFO engine to the shared output-facts model. Calculate ordered sat ranges from genesis, processing transactions and outputs in Bitcoin order, including issuance, fees entering coinbase, unclaimed rewards, burned outputs and historical duplicate coinbases. A current UTXO snapshot supplies amounts and scripts, but does not establish sat identities. Use local blocks first and the existing on-demand providers for missing evidence; no new peer-state or snapshot protocol is added.

Retain current output ranges and a sat-number-to-output lookup at one exact selected-chain checkpoint. Use BPS positions and shared amount/script references rather than duplicating full transactions. Store fragmented ranges in bounded pages; FIFO processing still materializes input ranges, so paging does not establish a peak-memory bound for arbitrary fragmentation. Mandatory state also represents lost, destroyed and unspendable sats, which are absent from ordinary live UTXO membership. Missing or corrupt dependencies cannot produce a known location or an empty result.

Add a Sat ranges subtrack beneath TXIO, with independent coverage. Display retained live ranges and explicitly retained historical ranges without inferring complete block coverage from selected outputs. Full historical range retention is optional and off by default. Satline on-demand tracing is deferred for this version; saved historical ranges remain an independent optional layer.

Cache authenticated per-transaction fee totals and block fee prefixes for on-demand fee traversal. Ordered input/output amounts and spend links remain necessary. A cached path for one sat does not prove the full contents of an old output. Missing historical evidence remains unresolved; full historical ranges and raw block retention remain separate choices.

## Savepoints and recovery

Keep the newest two Sat savepoints at heights divisible by 12 and the newest two at heights divisible by 144. Matching heights share one state. The live head is independent of these at most four savepoints. Publish replacement references atomically and keep all reachable range pages, inscription identity pages and shared output facts pinned before releasing old dependencies.

After a reorg, select a retained savepoint on the replacement chain at or before the common ancestor and replay forward. A savepoint is a restore point, not a proof of full history or a guarantee that replay data is local. Missing chain evidence cannot trigger a destructive reset. Once established, the oldest deep savepoint is 144–287 blocks behind the processed tip; recovery deeper than the available saved heights requires a reviewed rebuild, preserving old data. The UTXO index's existing 288 reversible transitions remain unchanged.

## Explorer and address integration

Provide a Sat explorer with issuance, ordinal properties, saved location, output BPS position, output-relative offset and a clickable destination derived from the shared locking script. Conventional addresses remain conventional addresses; P2PK uses the exact compressed or uncompressed public key and does not aggregate same-key P2PKH outputs or alternative key encodings. Other non-address scripts and lost sats show their actual script/location state.

The address page's UTXOs expand into paginated ordered Sat ranges, with navigation to individual sats. Show ranges only at compatible exact checkpoints; missing or lagging state is unavailable or catching up. Include known indexed inscription holdings by joining inscription identity to its Sat location and output script. Expose identity coverage and distinguish unknown holdings from zero. This stage does not establish wallet ownership, spendability, mempool balances or universal inscription completeness.

Satline history tracing is explicitly disabled for this version at the user's request after testing exposed unclear control over long on-demand scans. Remove Trace/Follow initiation from Sat and transaction views; direct/API/CLI entry points must refuse new and resumed work. Keep any saved trace evidence intact. Saved Sat locations, ordered ranges and known indexed inscription holdings remain available; do not launch identity-discovery traces implicitly. Existing active traces may be cancelled through the targeted job API without stopping other index work.

Transaction inputs retain their known BPS positions and verified original-output positions; native outpoints remain evidence or an unresolved-position fallback.

## Build controls and compatibility

Use reviewed Build, Pause, Resume and Live controls for genesis-first Sat state. Raw block retention is pruned and independent of mandatory Sat state/savepoints. Show exact processed/target anchors, saved checkpoint heights, recovery limits and honest progress/storage measurements. Preserve incompatible legacy experimental Sat evidence for a reviewed rebuild. Existing Addresses, UTXO, TXIO and locator data remain compatible.

Full Bitcoin consensus validation remains deferred to v0.8. Bitmap and Gateway peer/state-sharing remain locked. Test bounded authentic blocks and synthetic fragmentation/reorg cases; measure and qualify fixture costs rather than claiming a full-mainnet storage or speed benchmark.

## Deferred until after Bitmap

The user has explicitly deferred shared timeline Play/Pause, per-track arming, the broader track-local configuration/review workflow and general per-index deletion, replay/rescan and refresh/upgrade controls until after Bitmap. They are not prerequisites for this Sats preview or proposed release. Keep the existing reviewed per-index controls. Existing compatible Blocks outputs can share a scan; that is not the deferred transport/arming redesign across all index types. The Inspector continues to host build controls and deeper evidence in this version.

## Recorded Sats testing follow-up: wheel input boundaries

The user reports that vertical wheel scrolling over the left track-name/index-label column scrolls normally in some areas but zooms the timeline in others. They subsequently expanded the rule to the actual track lanes: ordinary vertical wheel input should scroll vertically across labels and tracks, with no unmodified wheel zoom anywhere in the timeline workspace. Timeline wheel zoom must require **Ctrl** (or **Command** where Meta is supported), confined to the timeline time/data area and its deliberate navigation controls. **Shift + wheel** is the existing horizontal-pan gesture, not the zoom key.

Status: the authorized wheel correction is implemented and passes source-browser and exact Windows preview checks, including trackpad and touch pinch zoom. The corrected preview also verifies the actual composed timeline-to-Sat navigation path. Work stays on the Sats development branch; publication and main promotion remain held. Earlier previews and their receipts remain preserved under their original identities; see the acceptance record for the current candidate.

The correction routes wheel input by the area under the pointer: the whole left label column scrolls consistently, while plain vertical wheel input over the tracks also scrolls instead of zooming. Explicit Ctrl/Command zoom preserves the existing pointer anchor inside the timeline; preserve intentional horizontal navigation and native editing/picker behavior. Regression checks cover label text, badges, disclosure controls, padding and empty column space; expanded/collapsed groups, the collapsed timeline and narrow layouts; vertical scrolling without zoom over both labels and lanes; diagonal trackpad input; and deliberate modifier zoom over the time/data region. This narrow Sats follow-up does not bring forward the broader post-Bitmap transport/arming/workflow redesign.

The original grouped-heading/default-zoom fallthrough was corrected by classifying the complete rendered left column before modifier handling and replacing unmodified zoom with vertical scrolling. Explicit modifier zoom retains pointer anchoring on time surfaces. The browser suite covers group headings, gutters, divider boundaries, header buttons, compact/collapsed views, diagonal wheel input and pinch gestures.

## Address entry and browser companion

Bare integers in Gateway classify against the selected authoritative chain tip: at or below it opens a block, above it opens a Sat. Do not use the peer-advertised estimate or a fixed issuance boundary. If authority is unavailable, ask for an explicit suffix instead of guessing. Explicit `N.sat` and `N.bitcoin` preserve intent, and successful shorthand is stored canonically in navigation history. Preserve numeric transaction hashes and valid public-key destinations before number classification. Read-only classification must not fetch blocks, create traces or build indexes.

Ordinal names resolve locally to Sat numbers, using the inverse of the existing name display. Accept names with or without `.sat` inside Gateway; preserve existing resource/address/hash precedence, enforce the theoretical supply bound and do not start a trace. An explicit named `.sat` destination is also eligible for companion/native handoff.

The browser companion accepts explicit `.sat` destinations through native validation/open and optional local DNS/HTTP routing. Keep its existing dot/privacy filter: bare browser-number searches remain normal browser searches. Testing does not register or change the user's browser/OS configuration.

## Acceptance checks

Check FIFO ordering, fees/prefixes, same-block spends, genesis/lost/burned/BIP30 states, live range/value agreement, shared-fact pins after eviction, savepoint divisibility/rotation/deduplication, interrupted publication, common-ancestor eligibility, replay/resume and deeper recovery failure. Check sparse historical coverage, cursors, stale responses and unknown results. Exercise real explorer/address navigation, known inscription holdings and coverage, range pagination, reviewed genesis controls, paused queries, restart/reorg cache reuse, narrow light/dark layouts and no implicit indexing. Run focused regressions, full Go/static/race checks, packaging consistency and browser checks on the final branch source.

The [development test guide](TESTING-0.7.7.md) records source-specific evidence and remaining checks. [Release notes](RELEASE-NOTES-v0.7.7.md) must not present development preview acceptance as public package or updater acceptance.
