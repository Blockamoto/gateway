# Bitcoin Core baseline for block acquisition and Raw storage

This development change follows the user's Core-first requirement: use Bitcoin Core's established block-download design before introducing a separate fastest-peer policy. It also separates retaining raw block bodies from building the optional Transaction Locator index. The development branch is the implementation target; existing running previews, main, published releases and updater feeds are outside this change.

## Pinned reference and scope

The reference is [Bitcoin Core v31.1](https://github.com/bitcoin/bitcoin/tree/v31.1), commit `9be056a8a72b624dae9623b2f7bded92c2a21c91`. Relevant code is [net_processing.cpp](https://github.com/bitcoin/bitcoin/blob/v31.1/src/net_processing.cpp), [net.h](https://github.com/bitcoin/bitcoin/blob/v31.1/src/net.h), [blockstorage.cpp](https://github.com/bitcoin/bitcoin/blob/v31.1/src/node/blockstorage.cpp), and the `getblockfrompeer` RPC. Do not mix newer master-only manual-peer cooldown rules into this version's contract.

Core's acquisition model tracks blocks by hash, shares a global outstanding-request inventory, requests different missing blocks from available peers and refills capacity as replies arrive. Its bounds include 16 block requests per peer and a logical 1,024-block download window. Its outbound topology has eight full-relay and two block-relay-only connection targets. Gateway does not implement Core transaction relay, so the corresponding Gateway roles must be described as general header/block and block-focused connections.

Core's short stall timer concerns a blocked download window; it is not a deadline for every block. The initial two-second stall timeout can increase to 64 seconds. Its separate ordinary block timeout accounts for block spacing and the number of other downloading peers. Copying only the short timer would change Core's behavior and incorrectly classify local or aggregate bandwidth limits as peer failures.

Core's explicit `getblockfrompeer` request schedules a known-header block from a named connected peer. Gateway's on-demand resource navigation still chooses its configured sources automatically. It should use the same hash-correlated body manager as bulk work without silently requesting missing history or modifying the user's indexing range.

## Gateway integration contract

- Verified retained bodies, cache bytes and configured local/Core storage remain reusable before a remote request. Peer selection does not override existing verified local evidence.
- Bulk acquisition can download several serialized blocks while decoding and index publication remain ordered. Ready consumers at the same height share one prepared block.
- Block replies are correlated by hash independently of header replies. Simultaneous callers for the same body share acquisition; cancellation removes the caller's interest without publishing stale or canceled work.
- A logical lookahead window does not imply retaining 1,024 decoded blocks. Gateway requires a bounded serialized reservation budget for ephemeral operation, plus its bounded prepared-block cache. This is a documented memory adaptation.
- Selected-branch checks, Pause/Stop/Off fences, explicit Out limits and required index origins remain in force. Download completion alone never advances a derived checkpoint.
- Transfer measurements distinguish queue delay, received bytes and acquisition/verification. Measurements support diagnosis and acceptance; this pass does not introduce a custom fastest-peer ranking algorithm in place of the Core baseline.
- Explorer navigation and indexing enter the same acquisition and validation path. Concurrent callers share one in-flight verified base, then receive private views for their own cancellation, presentation and publication. The existing acquisition coalescer is reused; a second persistent decoded-object cache is not introduced.
- The persisted timeline is the sole authority for new optional writes. Raw Blocks On retains a requested verified body even during ordinary navigation; Off releases newly acquired bytes after current consumers finish. Existing saved bodies remain usable. An unconfigured or damaged timeline never implicitly enables new retention, and old cache/retain arguments or settings cannot override this choice.
- Locator and Inscriptions Arm choices also authorize sparse facts for explicitly viewed blocks. They never launch a prefix build, advance a contiguous checkpoint or implicitly enable other indexes. Existing complete block results are reused. Older browse APIs alias the same Arm controls rather than maintaining a second consent file.

## Raw Blocks is body retention

Arming Raw Blocks chooses retention of verified serialized Bitcoin block data and minimal block-level metadata. It must not create, repair or backfill a transaction-ID reverse index. The explicit Transaction Locator track owns new general transaction-ID-to-block lookup. Known block/transaction coordinates continue to work directly from a block body.

New raw bodies use bounded append segments instead of one raw file and receipt per body. Block-level metadata identifies the segment, byte offset, length and authenticated body identity. A completed checkpoint becomes visible only after its referenced data is durable. Interrupted unpublished tails cannot become advertised coverage or invalidate already published records.

An independently requested body is catalogued by block identity and, where known, height without inventing intervening coverage. Raw run progress remains separate. For sequential runs Gateway retains one small immutable checkpoint JSON per block and its compact height-to-commit table for compatibility with existing authenticated explorer, export, reorg and deletion APIs. This is a Gateway metadata adaptation, not the Bitcoin Core block-index disk format. New Raw records contain no per-transaction arrays or reverse-locator rows. Raw overviews use metadata; explicit block details verify and decode the body. Legacy formats remain readable, and their existing lookup evidence may be read without generating new reverse records. Continuing an old store must not silently rewrite or delete its historical data.

Range deletion must distinguish logical coverage removal from physical segment reclamation. Removing one selected block must not damage neighboring retained blocks. Any segment bytes retained pending safe compaction must be reported explicitly. Shared recovery facts and external/Core-owned data remain independent.

## Measurement and acceptance

The initial live observations covered two newly completed Raw-only blocks. Source counters increased by 9.433 seconds, Apply by 111.674 seconds and progress persistence by 12 milliseconds, while District and Chain work remained Off. Source includes acquisition, local reads and parsing; Apply includes verification, serialization, lock waits and storage. Prefetch overlaps stages. These values establish application-side cost in that sample, not an additive wall-clock rate, disk-only attribution or a Core speed ratio.

The previous Bitmap excerpt benchmark kept Raw Blocks Off and cannot establish Raw persistence throughput. This change requires separate same-fixture observations of the old and new Raw paths, using genuine contiguous blocks, fresh disposable profiles, exact body hashes and independent cold-store-reopen checks. A store reopen in the same test process is distinct from process restart; the exact packaged client must separately pass restart acceptance. Peer pipeline tests must cover out-of-order replies, multiple requests per peer, coalescing, header/block coexistence, availability and witness constraints, byte bounds, stalls, cancellation and obsolete branch results.

Final acceptance must identify the frozen source and exact packaged client, distinguish source and compiled-app checks, and report any unmeasured performance claims or remaining adaptations. No numerical speed parity with Core is established merely by adopting its scheduler constants.
