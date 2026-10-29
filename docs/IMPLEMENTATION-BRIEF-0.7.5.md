# v0.7.5: UTXO set

The release establishes complete UTXO state at a processed Bitcoin chain tip, preparing Gateway for full validation in v0.8. It also adds optional output values and ScriptPubKeys to TXIO and makes the timeline's coverage layers expandable. This is an authorized pre-release. Development stays on `codex/v0.7.5-utxo-set`; main and the live Render publisher are promoted only after the implementation and exact release packages pass verification.

## UTXO state

- Build from genesis, process blocks and transactions in order, prefer local raw/Core/cache bodies, and fetch missing bodies through existing providers. No arbitrary range is presented as complete state.
- Build, pause, and resume from the last consistent committed head. A complete prefix is complete at its processed height; it is current only when its identity agrees with the selected target tip.
- Persist live membership and exact output facts: satoshi amount, scriptPubKey bytes, creation BPS position and shared creation-block hash. BPS supplies creation height and coinbase status after structural checks.
- Keep the native outpoint-to-position bridge required to process Bitcoin inputs using the separate transaction-locator machinery. Full historical transaction-locator coverage is not required for a supplied resolved BPS lookup.
- Publish membership, facts, locator changes and recovery references atomically. Use bounded, content-addressed pages and retain referenced facts locally.
- Retain a fixed 288 complete reversible block transitions. This is a recovery policy, not a consensus finality rule and not 288 full UTXO copies. Raw bodies may be pruned once the transition is durably committed because recovery is self-contained.
- Undo transactions in reverse order; handle outputs created and spent within one block and historical BIP30 displaced coins. Handle genesis and provably unspendable outputs correctly.
- Reconcile to the common ancestor and replay the selected branch. Beyond locally retained recovery, fail closed with rebuild required. Corrupt or incomplete records must not be reported as current state.
- Explicitly report that full consensus validation has not been performed. No snapshot trust mode, background validation chainstate, new peer protocol or configurable recovery window in this release.

## TXIO and shared facts

- Keep Transaction Locator and TXIO as separate tracks and separate JSON stores. The earlier merged per-block catalogue proposal is superseded.
- Resolved TXIO links contain BPS input/output positions and shared block anchors, without duplicate transaction IDs. The locator translates native transaction references while building. Preserve unresolved sparse evidence in a distinct pending-translation representation, without inventing a position or discarding old records.
- Read old version-zero and version-two data compatibly. Any migration must be resumable and preserve original committed data until a verified replacement is available; cleanup remains user controlled.
- Add optional Values and ScriptPubKeys history on outputs. Do not copy predecessor amounts/scripts onto every input.
- Store output facts once where practical. Retention is the union of optional TXIO history, live UTXO coins and recovery pins. Turning optional history off cannot erase live/recovery dependencies. Recently spent coins may originate years before the recovery window.
- Each layer reports independent coverage. A file's existence does not imply that all of its layers are complete.

## Timeline and build UI

- Blocks is an expandable group containing Headers and raw block bodies. This is UI grouping; existing storage formats remain distinct.
- Transaction Locator stays separate from TXIO. TXIO expands into Positional links, Values and ScriptPubKeys.
- Collapsed groups show independent coverage bands within one row. Expanded children have stable distinct colors and labels.
- UTXO has its own state track and build panel showing processed height, target, live output count/value, measured storage and recovery boundary. Distinguish building, paused, waiting, current, reconciling and rebuild-required states.
- Use “Pruned” for the UTXO retention policy. Optional raw body retention remains separate. Mandatory UTXO output/recovery facts are retained automatically even when TXIO historical enrichment is disabled.

## Deferred

Sats, historical sat ranges, fees, address indexing, snapshots, peer-to-peer state sharing, full consensus validation and advanced recovery settings remain future work. No general index deletion redesign or automatic removal of legacy duplicates.

## Acceptance and publication

Test meaningful ordered-state transitions, same-block spends, BIP30, pruning/offline undo, reorgs, deep-reorg failure, damaged-state recovery, interrupted resume, independent optional history, published-profile compatibility and UI coverage. Run the appropriate Go/JS/browser checks and review the resulting implementation. Build exact Windows/Linux packages from a clean neutral checkout, verify contents and versions, exercise those executables with old data and ordinary providers, then publish `v0.7.5: UTXO set` as a GitHub pre-release. Promote the signed Render updater feed and verify automatic delivery to the previous published client. Report actual results and limitations.
