# Gateway 0.7.4 implementation brief

Gateway 0.7.4 adds the Transaction Input/Output (TXIO) index, makes Bitcoin serialized raw blocks the default durable body storage, and improves positional navigation. Publish this version as a GitHub prerelease and deliver it through the existing signed Render updater to installations whose update preferences permit it. Existing explicit opt-outs and all integrity checks remain effective.

## TXIO relationships and navigation

The Input index maps a spending input's BPS position to its original output's BPS position. The Output index maps that original output to the input which spent it. Both are views of one logical spend relationship, committed together by spending block. Build fast output lookup automatically. Use the existing zero-based BPS grammar, including coinbase transaction zero.

Raw inputs contain a previous transaction hash and output ordinal. Construction must locate and verify the creation transaction; supplied BPS navigation itself needs no transaction-hash locator. Reuse existing location providers and bounded origination knowledge, including same-block spends and duplicate transaction-hash incarnations. Preserve unresolved predecessors in sparse ranges. Missing knowledge is never proof that an output is unspent.

Keep canonical spend facts after source-body eviction. Store chain identity and checkpoints at block level, with rebuildable private lookup accelerators. Document canonical and accelerator schemas and measured storage overhead. Existing txo-spender identifiers, APIs and committed facts remain compatible; old byte layouts must not be reinterpreted as new ones.

Recovery withdraws disconnected spending records and their reverse entries, restores origination state, and follows the replacement branch. Both directions refer to a coherent selected-chain snapshot. Integrate backward input-to-original-output and forward output-to-spending-input navigation into the current workspace, preferring saved BPS links and using existing verified resolver fallbacks where appropriate. A known relationship does not imply that its block bytes are already local.

## Raw block bodies and compatible upgrade

New Bitcoin Blocks writes retain verified Bitcoin serialized raw blocks and compact ordered transaction-location metadata, rather than permanent complete decoded transaction arrays. Keep the exact header store and the transaction knowledge formerly supplied by Blocks independently of the Transaction Index. Builders, BPS queries and JSON inspection/export share verified raw loading and temporary decoding, with a readable legacy compatibility path.

Automatically inventory and migrate legacy data resumably. Verify and durably store raw replacements before publishing versioned records and a consistent visible head. Coordinate writers; withstand restart, insufficient space, unavailable sources and reorgs. Never edit immutable batch/checkpoint hashes in place or discard an unresolved usable legacy copy. If raw reconstruction is used, validate byte-exact transaction order, coinbase previous-output encoding, witness and canonical sizes; otherwise reacquire and verify raw data through existing sources.

Preserve privacy, publication, provenance, coverage, retention and independent offline availability. A pruneable mounted Core reference alone cannot replace formerly durable evidence. Ordinary cache eviction remains separate from durable retention. Lean inscription coordinates and Full selected content/envelope records continue to work.

Reuse verified Gateway-owned raw objects across newly created cache, retained-source and durable Blocks names using filesystem hard links where supported. Each name keeps its independent retention and privacy policy; cache eviction removes only that reference. All writes replace names atomically, so a cache repair cannot mutate another retained reference. Preserve existing valid independent copies. When hard links are unavailable, retain a separately flushed copy for durable offline availability and report that additional raw-byte allocation explicitly.

Legacy duplicate decoded/hex bodies remain until the user chooses verified cleanup. General index deletion is deferred. A narrowly scoped cleanup option is allowed only if it is opt-in and proves each removed duplicate has a durable verified replacement. Existing profiles realize disk savings only after selected cleanup; new writes use raw storage by default.

## Interface corrections

The inscription potential-range ghost currently uses a stale 481824 boundary. Use the catalog's existing suggested mainnet inscription scan start, 767430, for the projection and fallback. Keep the semantic lower bound at zero, preserving explicit earlier scans and coverage. Clip potential ranges correctly under zoom and when the chain ends before that boundary. The ghost represents potential indexing, not stored evidence.

Move map zoom and navigation controls into accessible reserved space so they do not obscure the map, including narrow windows. Related focused usability corrections discovered during implementation are authorized; broader Details/Inspector and destructive management redesigns remain separate.

## Validation and delivery

Verify bidirectional consistency, sparse unknowns, same-block and duplicate-hash cases, restart and accelerator rebuild, reorgs, eviction/refetch and zero-based navigation. Exercise interruption and legacy compatibility, offline cold restart, Lean/Full inscriptions, privacy and retention. Add focused regressions for ghost projection/fallback/clipping and unobstructed responsive map controls. Run repository Go, Node, browser and supported platform build/package checks appropriate to these changes.

Update version, compatibility descriptors, release notes and an acceptance record. Publish new 0.7.4 artifacts as a prerelease; do not republish the previous 0.7.3 review draft. Approve and sign the new release through the existing publisher workflow, preserving its trusted key and user preference modes. Prerelease status must not prevent delivery to the intended existing channel. Verify live Render signatures, validity, sequences, checksums and downloadable artifacts, then exercise update selection/application from an earlier client in an isolated profile where feasible.

Publication is authorized. Report concrete credential or enforced approval blockers, and distinguish verified outcomes from remaining limitations. Do not claim GitHub publication alone proves automatic updater delivery.
