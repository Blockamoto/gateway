# Bitcoin on Demand protocol — experimental wire v1 (BOD app 0.4.1; wire unchanged from 0.3.9)

Status: internal experiment. This is not a public allocation or stable protocol release.

## 1. Design rule

Bitcoin defines the canonical objects and consensus rules. BOD makes partial Bitcoin data discoverable and requestable. Peer answers are knowledge, not truth; positive claims are independently checked against Bitcoin data before being promoted to verified local knowledge.

BOD does not require Bitcoin Core. Core is an optional local validator, index provider and block backing store.

## 2. Transport and discovery

BOD uses ordinary Bitcoin P2P framing.

Discovery pipeline:

1. Obtain ordinary Bitcoin peer addresses using normal Bitcoin discovery (DNS seeds/manual peers and `getaddr`).
2. Treat experimental service bit **25** (`1ULL << 25`) as `POSSIBLE_BOD_PEER`.
3. Connect to the advertised endpoint.
4. Confirm Gateway support using BIP 434, exchange the Gateway protocol list, and confirm the Bitcoin on Demand protocol ID/version before any BOD application message is sent.

Bit 25 is an unauthenticated internal hint only. It is not proof of Gateway or BOD support and is not a permanent allocation. Re-check experimental-bit usage before any public network test.

LAN UDP discovery and manual peers remain stage-zero development fallbacks in 0.4.1. They are not the BOD wire transport.

A serving node may optionally use `-advertise-gateway host:port` so BOD can announce that reachable endpoint to ordinary Bitcoin peers using a standard `addr` record carrying bit 25.

## 3. BIP 434 feature

BOD advertises Bitcoin protocol version >= 70017 and negotiates BOD between `version` and `verack`.

Experimental feature ID:

`9f944881b78ac02e6fc82fe4582ec97c3dc0c3473897be8758a648c896af20cb`

It is the SHA-256 digest of:

`Bitcoin on Demand protocol | experimental BIP434 feature identity | protocol family | pre-1.0`

Feature data is UTF-8 JSON and currently advertises supported BOD wire versions:

```json
{"wire":[1]}
```

The feature ID identifies the protocol family. Application versions and BOD wire versions are separate concepts.

## 4. BOD application message

After successful BIP 434 Gateway On Demand negotiation and Gateway On Demand protocol-list exchange, BOD uses one Bitcoin command:

`bodmsg`

Maximum BOD payload accepted by v0.4.1: 64 KiB. Gateway `gwmsg` is separately capped at 16 KiB.

Envelope:

```json
{
  "wire": 1,
  "kind": "request",
  "type": "status",
  "id": "random-request-id",
  "payload": {}
}
```

Response statuses:

- `ok`
- `unknown`
- `unsupported`
- `invalid`
- `busy`
- `error`

`unknown` never means "does not exist" or "unspent". It means only that the answering peer cannot establish the requested fact from its advertised state.

## 5. Status

The first application-level query used for discovered BOD peers is `status`.

Status separates possession from index coverage and binds claims to a chain snapshot.

Representative shape:

```json
{
  "wire": 1,
  "feature_id": "...",
  "experimental_service_bit": 25,
  "snapshot": {
    "network": "mainnet",
    "height": 967000,
    "hash": "...",
    "source": "core_consensus"
  },
  "capabilities": ["blockloc", "blockdata", "txloc"],
  "profiles": ["history"],
  "block_ranges": [{"from":0,"to":967000}],
  "tx_location_coverage": [{"from":0,"to":967000}],
  "spender_coverage": [{"from":0,"to":780000}],
  "spender_lookup": false,
  "extensions": []
}
```

Range endpoints are inclusive.

At most 256 intervals are returned in a status response. `block_range_count` and `ranges_truncated` tell the seeker whether more intervals exist. A later paginated range query can extend this without changing wire v1.

Status is an offer, not a forensic dump of the peer's disk. A client may keep locally retrieved objects private. v0.4.1's reference client has separate local policy switches for public-cache broadcasting and privacy mode; private cache objects and private derived lookup knowledge are excluded from peer advertisements and serving. These are client policies, not new BOD wire states.

## 6. Derived BOD profiles

Profiles are computed from actual state. They are not user-selected identities.

- `partial`: possesses one or more incomplete ranges.
- `history`: possesses continuous block history from genesis through the advertised snapshot.
- `graph`: history plus complete authoritative spender lookup through the same snapshot.

A pruned Core-backed node can have authoritative spender lookup without being a BOD Graph profile because it does not possess complete history.

## 7. Initial query types

### status

Returns current status/capabilities/ranges/profiles.

### block_location

Input: block height.

Output: block hash and height.

This resolves a human height into the native Bitcoin block identifier. Raw block transport should then use ordinary Bitcoin `getdata`/`block` whenever possible.

### hash_location

Input: block hash.

Output: height and hash when known.

### tx_location

Input: txid.

Output when known: txid, block hash, height and optional transaction index.

### spender_location

Input: outpoint (`txid`, `vout`).

Positive output: spending txid, block hash, height and optional input index.

A negative `found=false` is only authoritative when the provider has complete spender lookup through the response's chain snapshot. Sparse BOD spend indexes otherwise answer `unknown` for absence.

### address_utxos

Current-state convenience capability currently provided by Core-backed nodes. It is not an address-history protocol.

## 8. Raw block transport

BOD does not wrap normal raw block transfer inside JSON.

Once a block hash is known, use Bitcoin `getdata` -> `block`.

A sparse BOD peer can serve canonical serialized block bytes over the same Bitcoin P2P connection in response to `getdata` if it possesses and can validate that block.

## 9. Retrieval versus validation

Retrieval is not gated by local header synchronization.

A seeker may:

1. obtain a height -> hash hint from a BOD peer,
2. request that hash from Bitcoin/BOD peers,
3. check the block hash, proof-of-work target and transaction Merkle root,
4. retain/display it immediately,
5. anchor it later when local headers catch up.

Current validation states are:

- `structurally_checked`: hash/PoW/Merkle checked; chain position not locally anchored.
- `pending_header_validation`: height is known but local headers have not reached it.
- `header_anchored`: the block matches BOD's locally validated header chain.
- `consensus_validated`: a configured local Bitcoin Core supplied the block from its consensus-validated active chain.

Header anchoring proves chain commitment, not full transaction/script consensus execution. `consensus_validated` is deliberately a separate stronger label.

## 10. Storage

Core-backed and standalone BOD are both first-class configurations.

- If Core has a block, BOD can use/serve Core's existing copy without duplicating it.
- If Core is pruned/missing or absent, BOD can store canonical serialized blocks in its own sparse cache.
- BOD indexes and coverage metadata remain BOD-owned.
- BOD does not edit Core's private databases.

## 11. Coverage

Possession and derived-index coverage are separate facts:

- header coverage
- block possession ranges
- indexed block coverage
- tx-location coverage
- spender-event coverage

A positive spender sighting is independently verifiable. Absence requires complete coverage to a named chain snapshot.

## 12. Extensions

BOD status reserves an `extensions` list. An extension advertises a stable protocol-family ID plus a separate version.

Human names are labels only. Machine identity should be collision-resistant and fixed by the extension specification. Module execution/installation is not part of v0.4.1. Satline remains planned as a later staggered 0.4.x module rather than part of the 0.4.1 storage build.

## 13. Resource limits

v0.4.1 bounds:

- BOD `bodmsg`: 64 KiB maximum
- featuredata: BIP 434 maximum (512 bytes)
- 64 BOD requests per inbound connection in the experimental server
- 128 inventory vectors per `getdata` handled by the BOD listener
- 256 advertised range intervals in status
- one request ID may be used only once per connection
- BOD request timeout: 18 seconds
- ordinary Bitcoin frame maximum: 32 MiB in this experimental client

Malformed messages must fail closed without poisoning indexes or crashing the process.

## 14. Compatibility

BOD 0.4.1 remains pre-1.0. It does not promise compatibility with the experimental 0.3.7 feature identity/discovery behavior. Storage schema 2, header schema 1 and raw canonical block files remain reusable.
