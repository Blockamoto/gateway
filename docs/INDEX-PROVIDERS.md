# Lower index providers

Gateway can retain an index answer or locator without retaining the underlying raw block. A locator remains a hint: the resolver fetches Bitcoin evidence and checks the claim before presenting a verified transaction or spend.

`indexProviderViews()` projects the existing providers into the local index catalog. Rows with the same `id` describe independent providers of the same definition. It does not build an index, run an address scan, traverse a sat, discover peers, change retention, or change serving policy. It reads local metadata and the existing bounded Core status probe/cache. No credentials, paths, queried addresses or peer endpoints appear in these views.

| Definition | Existing providers | What their answer means |
| --- | --- | --- |
| `headers` | Gateway selected header chain; Core active-chain RPC | A block height/hash anchor. Gateway headers establish the selected proof-of-work chain; live Core supplies its consensus-validated chain view. Neither alone proves possession of block bodies. |
| `blocks` | Raw Gateway cache; read-only Core mount; Core RPC | Retained block data, with separate recorded ranges for each provider. Pruned Core is not archival storage. Gateway verifies block bytes when reading them. |
| `tx-locator` | Gateway cache; native graph; Core `txindex` | `txid → block location`. Gateway fetches the containing block and checks transaction inclusion. Native graph preserves historical txid incarnations; an ambiguous ID needs chain position. |
| `txo-spender` | Gateway positive sightings; native graph; Core `txospenderindex` | `outpoint → spending transaction`, or a qualified snapshot answer. Absence in a partial index remains **unknown**. Mempool state is separate. |
| `address-state` | Local Core chainstate scan | Explicit `scantxoutset` request for the current address UTXOs at the returned height/hash. It is an on-demand operation, not a prebuilt address-history index. |
| `address-history` | Unavailable | No historical address activity provider is implemented. Ordinary queries do not secretly build one. A future deep history index requires explicit opt-in work. |
| `sat-state` | Existing Satline resolver, on demand | Placement at a stated snapshot can be derived from verified lineage plus terminal unspent evidence. There is no global current-sat index or implemented Ord sat-state adapter. |
| `satline` | Local per-query lineage/checkpoints; optional verified peer acceleration | Full or partial lineage for the requested sat/satpoint. Saved queries and hops do not imply whole-chain coverage. |

The inscription, inscription-numbering, Bitmap and compatibility definitions belong to the higher index catalog and their own providers. This helper does not create placeholder claims for them. Existing Ord integration supplies inscription metadata/content; its presence does not establish support for current sat state, address history or canonical numbering.

## Catalog fields

- `coverage` is recorded work or explicitly provider-reported progress. The `completeness` and `verification` fields qualify it. `recorded_ranges` is not an independently audited completeness proof.
- `public_coverage` is the subset eligible for the existing serving path. It is not permission to publish private data, and it is not a claim that every request in the range succeeds. `serveable` also applies current policy.
- `queryable` means the provider currently has a local query path/backing knowledge. Sparse providers can answer only some queries; success still depends on availability and evidence checks. A configured but disconnected Core is unavailable.
- `serveable` means the current serving implementation and policy can offer the stated kind of data. It does not mean the listener is running, public inbound reachability is proven, or every recorded entry is intact. Corruption, reorgs and missing backing bytes fail during the existing query checks.
- `snapshot` identifies the provider chain view at observation. For a partially caught-up Core index it is the **Core tip**, not an assertion that index coverage reaches that tip. `coverage` separately carries the index height.
- `provider_reported_at_snapshot` is a qualified Core claim, requiring the enabled index to report synced, index height to reach Core's tip, a usable tip hash and Core to be out of IBD. It is not a claim that Core's tip is the global latest tip.

Catalog inspection never unions a provider's trust model into another's. The Core mount is a source of Bitcoin bytes, not a substitute for `txindex` or a spender index. The native graph's ranges and private cache knowledge remain distinct.

## Existing resolution and serving boundaries

Transaction lookup uses `resolveTransactionViaOverlay` and `verifyTxLocation`; Satline's transaction backend adds the before-height context needed for duplicate historical transaction IDs. Core and peer locations are hints until the real containing transaction is checked. Raw-block eviction does not erase a retained locator.

`resolveSpender` returns `confirmed_spent`, `unspent_at_snapshot`, `unknown`, or `provider_disagreement`. Positive local graph/Core results pass through `verifyGraphSpend`, which checks the spending transaction and exact input. A native unspent answer requires a unique origin transaction, an existing output, complete coverage from creation through the snapshot, the matching tip marker and an intact queried shard. A local Core negative additionally checks the confirmed UTXO set. Neither a missing outpoint nor a failed lookup becomes unspent. Satline's Core fast path validates the input, retains Core provenance, and separately checks block membership during traversal.

Remote BOD wire v1 differs from local resolution. `txloc` currently uses synced Core or the public cache; it does **not** dispatch to native graph transaction shards. Native graph `spendloc` serves positive locator hints, not its local negative proof. Core wire negative responses remain provider claims; callers must not convert wire `Found:false` into a locally verified unspent proof. The catalog is not a new wire advertisement or authorization mechanism.

The local `coreAddressUTXOs` operation remains available. Incoming network `address_utxos` requests are explicitly unsupported and do not start Core scans. A successful local scan's returned snapshot belongs to that answer, not to an earlier catalog status observation. This capability says nothing about spent historical outputs or script satisfaction by a particular wallet.

`indexSatStateAtSnapshot` is a presentation guard for **already locally resolved or verified** Satline results. It accepts a `CURRENTLY_UNSPENT` terminal with a valid point and snapshot, sufficient verification and no pending/conflicting anchor. `UNRESOLVED`, `STEP_LIMIT`, `PAUSED` and lost states can carry a `CurrentSatpoint` field that is only the traversal frontier; they are not current placement. The helper does not authenticate arbitrary peer JSON, and a saved answer remains an answer at its historical snapshot until refreshed.

Satline serving requires the module enabled, publication serving enabled, and at least one structurally usable non-empty detached publication. Private saved lineage, enabling a preference, or an empty publication does not imply an answerable segment. Published segments can be stale after a reorg and recipients still verify their Bitcoin evidence. Deep lineage remains optional and per-query; no unconditional whole-chain commitment is introduced.

## Verification

`index_providers_test.go` covers enabled versus synced Core indexes, IBD, missing pruning boundaries, private/public metadata, locator retention after raw-block eviction, real-block verification of a locator, current-address status without scans, spender unknown/unspent boundaries, sparse native coverage and Satline frontier/publication semantics. Fixtures are local and synthetic; they do not establish production network reachability or mainnet index completeness.

`TestCoreLookupAndOverlay` also verifies the existing local Core address scan succeeds while a remote request is rejected as unsupported and starts no additional Core scan.
