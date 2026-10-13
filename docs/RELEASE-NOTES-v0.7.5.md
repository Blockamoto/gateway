# v0.7.5: UTXO set

Gateway 0.7.5 adds a genesis-first UTXO index, optional TXIO output values and ScriptPubKeys, and grouped coverage tracks. This source is being prepared as a prerelease. Exact package, public-release and hosted automatic-delivery checks remain pending until recorded in the release acceptance evidence.

## UTXO state

The **UTXO set** track processes the selected Bitcoin chain from genesis and keeps one live state at its explicit processed height/hash. It reuses local bodies and configured providers, pauses at a consistent committed head and resumes from that head. The dedicated panel shows progress, live output count/value, exact chain anchors, measured storage when available and the recovery boundary.

Live membership points to locally retained exact output values and scripts. A mandatory native-prevout bridge permits processing Bitcoin inputs after old raw bodies have been evicted. BPS lookup remains positional and needs no complete historical Transaction Locator index.

Recovery retains a fixed **288 self-contained reversible block transitions**. This is a local recovery policy, not 288 full UTXO copies, mandatory raw-body retention or Bitcoin consensus finality. Ordered processing handles same-block spends, genesis, provably unspendable outputs and historical BIP30 replacement. A deeper reorganization requires an explicit reviewed rebuild from genesis; normal Resume never silently resets. Rebuild archives preserve prior state and their fact dependencies, so replacement builds require additional disk space.

This release is **header-anchored processing, with `consensus_validated=false`**. Block commitment checks and UTXO state construction do not establish full script, maturity, subsidy or other Bitcoin consensus validation. Full node validation remains deferred to v0.8.

## TXIO output history

TXIO gains independently selectable **Values** and **ScriptPubKeys** history. Output facts can be shared with mandatory UTXO live/recovery data rather than repeated on spending inputs. Required UTXO facts remain retained when optional TXIO history is off. Each layer reports its own coverage; a saved TXIO file does not imply complete value/script history.

Resolved TXIO relationships use positional input/output links and block anchors, with transaction IDs supplied by the separate locator catalogue. Sparse unresolved native outpoints remain explicit pending translations. Version-zero and version-two records remain compatible and preserved. Existing bodies and legacy copies are not automatically removed.

## Timeline

**Blocks** expands into Headers and raw block bodies. **TXIO** expands into Positional links, Values and ScriptPubKeys. Collapsed groups retain independent colored coverage bands; expanded children keep their own identities and coverage. **Transaction Locator** remains a separate track/store. **UTXO set** has its own state panel and genesis-only build workflow.

Address, Sats/Satline, fees, Bitmap, Gateway peerhood and snapshot/state-sharing features are not added by this release. Existing Bitcoin providers and separate raw retention settings continue to apply. Saved output values and scripts alone do not establish global sat identity or complete sat history.

See [the UTXO operator guide](UTXO-INDEX.md), [indexing](INDEXING.md), [test guide](TESTING-0.7.5.md), [status](STATUS.md) and [update policy](UPDATES.md). Publication must use the existing approved signed delivery path and preserve users' update preferences; changing the source version alone does not promote the hosted feed.
