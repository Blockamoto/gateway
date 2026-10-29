# Verified-block ingestion in the 0.7.8 local preview

Every operation that obtains a complete, verified, selected-chain block uses
the same ingestion fanout: ordinary navigation, generic resolution, internal
dependency resolution, explicit indexing, and reuse of local/cached bodies.
Only tracks already armed in the saved prepared timeline may publish. No
operation arms another track, starts a preceding scan, or treats a gap as an
empty result. Raw may remain Off while derived facts are retained.

Parcels execution is disabled in the October 26 availability follow-up pending
further Ordinals and parent-provenance validation. Its track is omitted from
the normal interface, including saved-index cards and status selections. All
canonical, legacy and custom Parcel versions remain off. Existing definitions,
rules, identities and saved records are preserved, with passive backend read
and export compatibility; restored choices cannot enable hidden work.

| UI lane | Ingestion capability | Required context |
|---|---|---|
| Headers | Selected header authority | Header subsystem validates its own prefix; a body does not fabricate missing headers. |
| Raw blocks | Sparse verified bodies | Raw Arm controls durable body membership only. |
| TX ID Locators | Sparse complete block positions | Complete verified transaction order and selected Bitcoin anchor. |
| IO Links | Sparse positive relationships | Input/outpoint evidence is retained; unavailable creation-position translations stay unresolved. |
| Values | Independent sparse creation facts | Its own retention flag, complete verified block, and selected anchor; IO Links may be Off. |
| ScriptPubKeys | Independent sparse creation facts | Its own retention flag, complete verified block, and selected anchor; IO Links may be Off. |
| Sat ranges | Exact-next optional historical retention | Armed Sats, its complete prior FIFO state, and the historical-retention option. |
| UTXO | Exact-next complete state | Mainnet genesis or the next block matching the complete prior UTXO checkpoint and live inputs. |
| Current Addresses | Matching complete-state projection | Matching UTXO transition and prior complete address projection; initial genesis projection is permitted. |
| Historical address outputs | Sparse scanned block | Complete verified output facts; explicit empty scans are retained too. |
| Inscriptions | Sparse Lean or Full | Current mode and conventional-ID choice; extraction uses the block's own inscription envelopes. |
| Bitmap Districts | Exact-next fixed-standard state | Its distinct authenticated implementation identity and required preceding claim state. |
| Bitmap Parcels | Disabled | Implementation and saved results retained for later Ordinals and parent-provenance validation. |
| District Chain | Disabled | Saved chain artifacts remain inspectable; this preview does not execute the provisional codec. |
| Parcel Chain | Disabled | Saved chain artifacts remain inspectable; this preview does not execute the provisional codec. |

The machine-readable `browse` array in index status exposes all fifteen lanes
as `index`, `enabled`, `available`, `capability`, and `reason`. Sat ranges are a
retention option of the complete Sats state, rather than a separate state
consumer. Values and ScriptPubKeys are separate consent flags on the TXIO
selection. Either can be armed while IO Links remain Off. An explicit
shared source pass with only these flags uses a facts-only adapter; it creates
no IO Links head and does not change the IO Links Arm choice.

Sparse receipts live separately from contiguous range checkpoints. Each
singleton commits its actual height and Bitcoin hash; neither a receipt nor a
later singleton implies coverage of intervening blocks. TXIO pending
translations preserve the original outpoint until a checked creation blocator
is available. Queries merge current sparse positive records with current
contiguous records. A miss never establishes unspentness.

Values and ScriptPubKeys each own their sparse manifests and shared immutable
fact-page references. They can be queried outside an IO Links range.
Collection enumerates these owners alongside UTXO live/recovery pins, Sats,
TXIO historical layers, and address history. Switching a track Off preserves
saved data. A missing or damaged owner fails closed during collection.
Whole-layer deletion of Values or ScriptPubKeys clears only that independent
sparse owner and its consent flag. Sibling layers, IO Links, and legacy
authenticated facts owned by parent records are preserved and stated in review.

Lean inscription receipts retain blocators and block metadata. Full adds
authenticated occurrence content. When conventional IDs are requested and the
shared TX ID Locators track is armed, that index owns the native-ID translation.
Otherwise the inscription receipt may own a reveal-scoped translation.
Ordinary resolution never downgrades an existing Full receipt to Lean or
silently discards an already retained conventional-ID projection.

Publication rechecks current consent/options, caller cancellation, and the
selected Bitcoin hash. Complete-state consumers never append an arbitrary
later block, reconcile merely to fit an incidental request, or initiate an
unrelated prerequisite build. The coordinated worker refreshes durable heads
before applying a block already consumed by the common fanout.

Internal fetches made while an index writer holds its state lock queue their
complete verified blocks in the caller's context. The queue drains after the
outer mutation returns and releases its locks. This preserves dependency-block
ingestion without recursive state-writer entry or a hidden scan.

Focused tests: `go test -run '^TestResolvedIngestion' -count=1 .`. These cover
local verified bodies with Raw Off, independent child-only shared runs,
ownership through collection and Off, sparse TXIO translation gaps, deferred
dependency ingestion, exact-next state, Full-mode retention, and late-Off
publication fencing.

## Shared source and inventory follow-up

Each complete verified body consumed by the coordinated worker is offered to
all applicable armed tracks. Only the actual same-height native cohort and
already staged same-body TX anchors reserve that body; a distant admitted
frontier does not suppress sparse capture. TX work can overlap the ordered
stateful group. UTXO precedes IO derivation. The retained Parcel implementation
orders District before Parcel and uses the pre-transition Sat context; Parcel
execution is currently disabled. Sats advance afterward. Every worker
joins and releases its store gates before dependency bodies drain.

Dependency facts share the already decoded body and sealed extraction. The
operation retains at most eight resident bodies within a 32 MiB serialized-fact
budget, spilling excess facts into an owned private temporary directory outside the data
profile. Checksums, identities, raw verification receipts and extraction
receipts are checked when restoring facts. Cancellation and errors clean up
the spill. Queued facts have a 1 GiB aggregate operation quota, and failed
spills or consumer publication errors are reported rather than silently
dropping a body. Independently committed checkpoints remain valid.

A complete saved TX point may supply only its exact next native prefix block.
Promotion checks its full ordered transaction payload against the selected
header Merkle root and ancestry, then computes normal native commitments.
The singleton commitment is never used as a range checkpoint. Promotion
preserves the point receipt while the native batch is awaiting durable save.
Raw On still requires the normal body-retention path. Queued dependencies
re-establish the actual idempotent producer acknowledgement before fanout;
verified bytes alone never stand in for retained Raw membership.

Future TX points share a bounded batch in the genesis-aligned 336-block window,
with at most 16 MiB of serialized facts and 120,000 transaction rows. The batch
retains no decoded bodies. Its private pending lookup can supply native catch-up,
but public queries and Available coverage see only acknowledged saved points.
Immutable per-block records are flushed before one checksummed window manifest
publishes the group. Pause or Off discards volatile facts and preserves saved
windows. The native TX prefix keeps its existing 336-block, 32 MiB, 400,000-row
limits and its independent durable checkpoint.

Ordinary point captures retain their packed reverse hints. Coordinator future
batches avoid the per-body packed-bucket flushes, historical transaction-array
rewrites and whole-history membership rewrites. Old point maps, height receipts
and locator shards remain readable. A bounded read-only fallback reports
uncertainty when it cannot exclude duplicate transaction identities. A proved
durable native ancestor can discharge covered reverse-lookup work, while all
saved point records remain separately inspectable and deletable. Staging or a
failed native flush cannot create that acknowledgement. Range and whole-data
deletion reviews bind the relevant window and shadowed height receipts.

Inscriptions coverage represents a complete envelope inventory. Lean retains
all recognized positions; Full additionally retains their content. Prepared
inventories require the actual extractor's sealed receipt, so a known positive
occurrence or Bitmap claim cannot publish whole-block coverage. Authenticated
legacy Full v1 scan recipes remain readable without changing their bytes or
requiring a denominator they never stored.

Inscriptions export review and download enforce the same inventory predicate
on every emitted range and browse batch, including older batches under a
complete head. A positive-only source cannot produce a complete inventory
attachment or advertise complete export coverage. This check uses the export's
existing source walk. Home uses a bounded, process-cached prefix certificate;
it never performs an unbounded history scan or writes an Off-side repair.
Transient read failures retry after a bounded backoff. A successful explicit
writer/resume walk can certify a longer prefix without changing legacy bytes.

Off queries may read existing locator acceleration or use a bounded,
authenticated immutable history walk. They do not rebuild missing locator
sidecars. Explicit known-ID content caching under the separate ord viewer
cache remains available. The TX details distinguish saved progress from
processed blocks awaiting batch save; pending work never fills Available
coverage and is discarded by Pause or Off according to the existing batch
policy.
