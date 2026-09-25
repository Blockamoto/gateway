# Proposal 001: height-addressable block resolution

Status: experimental design note for BOD 0.3.8.

## Problem

Bitcoin P2P block transport identifies blocks by hash. Sparse/light clients often begin with a human block height and may not yet possess a complete height -> hash header index.

## Existing behavior

Once a client knows a block hash, ordinary Bitcoin `getdata`/`block` is already the preferred transport. BOD should not replace that transport gratuitously.

## Proposed BOD primitive

`block_location` (conceptually `resolve_block_height`):

Request:

```json
{"height":800000}
```

Response:

```json
{"height":800000,"block_hash":"..."}
```

The response is a location hint tied to the provider's advertised chain snapshot. The seeker then asks Bitcoin P2P for the raw block by hash and validates what it can locally.

## Sparse-peer fallback

If the BOD peer itself possesses the block, the same Bitcoin connection can satisfy an ordinary `getdata` request for that hash. No JSON/base64 raw-block message is required.

## Validation

A location answer is not truth. The seeker checks the returned block's hash and internal commitments and later anchors it to its own header chain. A Core-backed provider may itself have stronger consensus validation, which is communicated separately from the location primitive.

## Backwards compatibility

This is additive to BOD wire v1 and requires no Bitcoin consensus change.

## Resource/DoS

One fixed-size height request returns one fixed-size hash/height result. It is substantially cheaper than arbitrary range scans.

## Privacy

Asking a specific remote peer for a specific height reveals interest in that height. Future privacy work may use multi-peer queries, proxying or cover traffic; none are required for this internal version.

## Possible BIP relevance

If height-addressable historical retrieval proves broadly useful outside BOD, a future Bitcoin peer-services proposal might be appropriate. BOD should gather operational evidence first.
