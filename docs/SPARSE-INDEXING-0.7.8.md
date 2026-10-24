# Verified-block ingestion in the 0.7.8 local preview

Every operation that obtains a complete, verified, selected-chain block uses
the same ingestion fanout: ordinary navigation, generic resolution, internal
dependency resolution, explicit indexing, and reuse of local/cached bodies.
Only tracks already armed in the saved prepared timeline may publish. No
operation arms another track, starts a preceding scan, or treats a gap as an
empty result. Raw may remain Off while derived facts are retained.

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
| Bitmap Parcels | Exact-next fixed-standard state | Matching District outcomes, target-block evidence, and checked parent provenance; missing dependencies remain gaps. |
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
