# Published index snapshots and peer claims

Index construction is private by default. Publication is a separate, explicit action: it freezes one committed index checkpoint. Building additional blocks does not publish the new tail. Publishing again refreshes the snapshot and invalidates old pagination cursors. Unpublishing removes only the publication marker; private index data remains available locally. The global serving switch must also be enabled before manifests or records are served.

The GUI may provide an expected checkpoint when publishing. The operation fails if the current checkpoint differs from the one reviewed. CLI publication without that guard explicitly selects the current checkpoint. An older snapshot may remain published after the private index advances, so `serveable: false` on the latest local instance is not proof that no older publication exists.

Publication checks its checkpoint against the current selected Bitcoin chain, using connected Core authority or local selected headers. A stale or unavailable anchor suppresses the manifest and range service. This does not turn remote recipients into validators: their results remain **`peer_claim_replay_required`**. Neither received manifests nor received records are written into the local verified index store. Provider-supplied verification receipts are cleared from received inscription records. A peer can generate internally consistent hashes for false data; only independent Bitcoin replay can establish the derived result.

## Additive BOD wire extension

These requests use existing negotiated Gateway/BOD sessions and BOD wire version 1, with the existing 64 KiB message limit, request IDs, response correlation and serving/request budgets. Older peers may respond `unsupported`. The capabilities `index_manifest` and `index_range` are advertised only when there is an enabled published snapshot.

- `index_manifest`: no index parameters; returns at most the two implemented published snapshots, inscriptions and Bitmap.
- `index_range`: payload contains `index_range` with `index`, `rule_hash`, the published `checkpoint` commitment, and inclusive `from`/`to` heights. The initial page must end at the published checkpoint. A continuation includes the exact `cursor` and `anchor` returned by the previous checked page.

Manifests include schema, definition/version, network, rule hash, output schema, explicit coverage and checkpoint. Checkpoints include dependency and record fingerprints, the block hash and preceding checkpoint commitment. Page records appear newest first. Receivers check matching fingerprints, complete requested range, each record hash, checkpoint hashes, dependency fingerprints and continuity. These checks establish structural consistency of a claim, not Bitcoin validity, first-claim correctness or complete earlier history.

The continuation token authenticates the next local commitment using a private random publication secret. It is bound to the publication snapshot and the next height/block hash. This prevents clients from selecting arbitrary private-tail or orphan commits. Tokens are pagination authorization, not signatures attesting correctness. A token from an earlier publication is invalid after re-publication or revocation.

## Resource limits and current scope

Each page covers at most four whole committed block batches and at most 48 KiB of encoded page JSON. Each batch is limited to 32 KiB for serving; reads are bounded before parsing. Manifests and publication markers are also bounded. Requests never walk an entire index history: continuation follows only the requested next few immutable commitments.

**Large batches are not fragmented in this version.** A block containing a large inscription dataset may exceed the 32 KiB batch budget; that page returns an explicit error directing the requester to local Bitcoin replay. It never supplies a partial batch as complete coverage. The manifest exposes `max_page_blocks`, `max_page_bytes`, `max_batch_bytes` and `whole_batches_newest_first` so a consumer can understand this restriction. Metadata may be published even where a large batch cannot be exported through this bounded extension.

There is no automatic remote import or promotion. The initial range starts at the published tip; older data is reached through checked continuations, rather than arbitrary-height lookup. Ownership, historical inscription numbering and compatibility acceptance are not inferred from a peer's report. Requests that rely on those results still need their own declared dependency closure and evidence.

The local receiving flow is explicitly invoked: `indexPeerClaims` inspects at most four already-negotiated Gateway/BOD peers, in parallel within existing request timeouts, and returns each peer's checked manifest claims or error. `indexPeerPreview` accepts only an endpoint already in that negotiated inventory, fetches its compatible manifest and requests one latest block. A configured endpoint or service-bit hint alone does not qualify. These operations do not launch discovery, run during status polling, or persist received data. Peer identifiers remain self-reported labels. Even a successful preview explicitly reports `bitcoin_replay_performed: false`, `stored: false` and `peer_claim_replay_required`. A single block can still exceed the whole-batch limit; preview reports that limitation rather than retrying or scanning additional blocks.

Focused tests cover private defaults, explicit publication, frozen tails, the expected-checkpoint guard, revocation/global serving, signed continuation and orphan access rejection, missing ranges, wrong fingerprints/schema, altered records, stale Bitcoin anchors, missing files, oversized batches, false but self-consistent claims, clearing remote verification receipts, and actual BOD frame dispatch.

Client tests additionally exercise managed-session request/response framing, the four-peer cap, rejection of unnegotiated configured hints, disabled networking, successful one-block preview without store writes, invalid manifest and altered page rejection, and an oversized preview failing without retries.
