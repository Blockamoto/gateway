# Addresses

The v0.7.6 development source adds **Current UTXOs** and **Historical outputs** under an expandable Addresses timeline group. Each child has its own coverage and color. Transaction Locator and TXIO keep their existing stores. Raw blocks remain the complete serialized block representation.

## Choose what to retain

**Current UTXOs** builds an address lookup from the complete saved UTXO set. Review and start it in the Current UTXOs panel; there is no arbitrary block range. If UTXO state is missing, the panel offers its existing build workflow and does not start that scan automatically. Queued work uses the latest complete saved UTXO checkpoint when it starts. The address build processes live outputs with bounded memory and publishes only a complete result. Pause and restart resume its saved traversal when the source commitment is unchanged. A changed source starts a fresh traversal and preserves the earlier staged evidence.

The lookup is an exact script-to-output reverse map. Logically, an address identifies a list of output BPS positions. Physical membership also retains a reference into the shared immutable output-facts store, so values and scripts remain available after raw-block eviction. Small memberships are inline; larger memberships use bounded pages. A cached count and total per address make the balance independent of output-list pagination.

Current membership follows UTXO commits and its 288-block rollback window. An interrupted projection can replay authenticated source transitions. If that recovery data is unavailable or the projection is damaged, queries report the state as unavailable until a reviewed replacement build succeeds. An address error does not undo or strand an otherwise successful authoritative UTXO update.

**Historical outputs** scans explicitly reviewed creation ranges. Ranges can be disconnected or precede existing coverage. Empty evaluated blocks count as scanned coverage. The build reuses complete, anchored TXIO output facts; missing facts require a verified raw block from an existing source. It retains references and pins facts after those outputs are spent. It does not require a full UTXO set.

Historical source retention applies to fallback blocks that are actually fetched. Reusing complete facts does not fetch raw blocks just to populate a cache. Changing source retention does not refill reused or previously scanned bodies. Historical records and their required facts remain durable regardless of that source-body choice.

Live historical processing fills missing ranges from its reviewed start before following later blocks. Progress counts those gaps, rather than treating the highest saved block as proof that all earlier work is complete. Pause, explicit retry and the single-writer queue use the existing indexing controls.

## Explore an address

Enter a supported mainnet address or P2PK public key in the explorer, or click a destination on an output. The page leads with its balance at the saved UTXO checkpoint, including the exact processed height and block identity. Expand Current UTXOs for a paginated output list, or Historical outputs for retained creation history. Clicking an output opens its BPS position; viewing details may fetch the raw block if it has been evicted.

A complete current index can return zero for an address with no live outputs. A missing, stale or damaged index returns an unavailable balance instead. Current results describe the saved checkpoint and can be behind the network tip. They exclude mempool activity and do not establish wallet spendability.

Historical results show received-output totals within their explicit scanned ranges and report gaps. Those totals are not a current balance or a complete address transaction history. Outputs have unknown spend status unless separate evidence establishes it; creation history alone never proves that an output is spent or unspent. Pagination cursors bind the canonical address and exact revision, so changed checkpoints require a fresh first page.

Supported destination scripts are P2PKH, P2SH, P2WPKH, P2WSH, P2TR and P2PK. Input addresses require valid mainnet encoding, checksum and witness-version rules. P2PK lookup accepts a valid compressed or uncompressed secp256k1 public key and shows a public-key destination label. It matches the exact bare public-key locking script, without combining P2PKH outputs or other encodings of the same key. A key mentioned inside a multisignature script does not make the whole script's funds a balance of that key. OP_RETURN and nonstandard scripts retain their output BPS and script views.

Inscription holdings wait for the Sat update. Address records do not infer ownership, public keys hidden behind hashes, full consensus validation or peer-shared state.

## Storage and compatibility

Current and historical reverse maps use separate versioned stores beneath the profile's indexes directory. They share output facts rather than copying the values, scripts or raw blocks into address rows. Historical commits bind scanned block anchors and coverage. Reorg reconciliation removes disconnected outputs from active results; retained prior revisions are preserved and continue to pin their evidence. Automatic cleanup of those revisions is not implemented.

Existing profiles need no destructive migration. Addresses are optional new projections; upgrading does not start an address or UTXO build. Existing output-facts collection recognizes address dependencies and fails closed when their retention evidence is damaged. Turning an index off stops work without deleting its committed data.

Profiles built before P2PK support keep their conventional-address evidence. Public-key lookups require an explicit Current UTXOs rebuild or historical range rescan before the new destination coverage is available. Historical Live includes those destination-coverage gaps. Addresses also defers shared-fact reclamation whenever newer Sat state or rebuild archives are present, preserving unknown owners without blocking healthy UTXO/address commits; a Sat-aware build can reclaim those facts safely later.

Address processing remains header-anchored, with `consensus_validated=false`, matching the underlying UTXO stage. Full consensus validation, full-mainnet performance and whole-chain storage costs have not been established by the focused development tests.

## Local API

`GET /api/v1/address/view` accepts `address`, `mode=current|historical`, `limit` from 1 to 100, and an optional `cursor`. It uses the local management interface and does not start a scan. Address search returns the same current-view envelope. Generic index lookup can also query `address-state` or `address-history` by address.

Responses include `state`, `snapshot`, `outputs`, `next_cursor`, `has_more`, `coverage` and `gaps`. Current results supply `current_balance_sats` and `current_output_count` only when the source is usable. Historical results supply `received_sats` and `received_output_count` within scanned coverage, and leave the current balance null. `complete` refers to a complete current membership at its snapshot; sparse historical results remain incomplete.

Build requests use the existing reviewed index workflow:

```json
{"index":"address-state"}
```

```json
{"index":"address-history","mode":"lean","from":840000,"to":840010,"retention":"ephemeral"}
```

These are request examples, not claims that those ranges are already indexed. Current rebuild uses the explicit `rebuild` flag and preserves prior data. Publishing this development version or changing the live updater requires a separate user instruction.
