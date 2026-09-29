# Inscription occurrence extraction

Gateway separates a bounded scan of inscription envelopes from canonical inscription numbering, sat movement, and ownership. A scan can identify content in selected Bitcoin blocks without reconstructing earlier inscription history. It cannot infer canonical inscription numbers, curse/vindication outcomes, reinscription eligibility, parent ownership, or current satpoints from that scan alone.

## Shared-block interface

```go
extractInscriptionOccurrences(block blockView) (inscriptionBlockResult, error)
```

The caller supplies an already decoded complete block from Gateway's block pipeline. The helper performs no RPC, peer request, filesystem operation, decompression, delegate fetch, publication, or persistence. Multiple index consumers can reuse the same decoded block.

The input needs a current local integrity receipt, valid block/transaction hashes, a known height, and transaction positions matching array order. Any witness-bearing input additionally requires an authenticated witness commitment, even if the receipt incorrectly omits its `WitnessPresent` flag. These receipts are an internal pipeline contract, **not a proof accepted from peer-provided JSON**. Before committing verified range coverage, the index engine must independently require the block's current selected-chain anchor or trusted local Core consensus authority. Extraction can report `pending_header_validation`; it never promotes that evidence.

`inscriptionBlockResult.Occurrences` is `[]inscriptionOccurrence`. Results have this order:

1. Bitcoin transaction position in the block.
2. Input position in the transaction.
3. Recognized envelope position in that input.

`Index` is the transaction-wide zero-based suffix in `txid+iN`. It resets for each transaction and advances across its inputs. `EnvelopeIndex` is input-local; `BlockOrder` is block-local. Neither is a global inscription number. `CanonicalNumberKnown` is always false, and there is no synthetic canonical-number field.

Each occurrence includes the reveal transaction/block, input and envelope positions, witness script position, script byte offset and digest, raw body and body SHA-256, ordered payload pushes as hex, parsed envelope fields and flags, parser profile, and the supplied verification evidence. `Envelope.Fields` and `PayloadHex` preserve raw field bytes; `PayloadHex` also retains an incomplete trailing tag. Display strings do not replace those bytes as evidence. `HasContentType` distinguishes an absent tag from the older field decoder's display fallback of `application/octet-stream`. `Envelope.HasBody` distinguishes no body delimiter from a present empty body.

Content remains exactly as encoded in the envelope. Compressed content is not expanded, delegates are recorded without being resolved, and parent fields are claims. A consumer such as Bitmap must declare its own handling of encoding, delegates, flags, and eligibility rather than treating every occurrence as an accepted claim.

## Pinned interpretation

The profile is `gateway-inscription-occurrence-v1`, pinned to Ord commit `03c9b87b14fe1e2c38191b860a08988345ec4e5f` for reference envelope semantics. It does not silently follow upstream updates.

- The reference selects an unversioned witness leaf by stack position, removing an optional trailing annex. This does not prove the spent output was P2TR, and it does not require a particular control-block shape or leaf version. [Ord witness selection](https://github.com/ordinals/ord/blob/03c9b87b14fe1e2c38191b860a08988345ec4e5f/src/lib.rs)
- Envelope parsing follows empty-push/`OP_IF`/`ord`, recognized push-number encodings, and `OP_ENDIF`. A malformed data push anywhere discards the input's envelopes. An unfinished or non-push envelope does not consume an ID. Body delimiters occur only at even payload positions; duplicate and incomplete fields remain flags. [Ord envelope parser and fixtures](https://github.com/ordinals/ord/blob/03c9b87b14fe1e2c38191b860a08988345ec4e5f/src/inscriptions/envelope.rs)
- Known pointer, parent, delegate, and chunked metadata interpretation reuses Gateway's existing field decoder. Additional odd fields are preserved as raw evidence without implementing their higher-level protocols. [Ord field tags](https://github.com/ordinals/ord/blob/03c9b87b14fe1e2c38191b860a08988345ec4e5f/src/inscriptions/tag.rs)
- Historical numbering and inscription state transitions require the separate updater's prior state. Parser flags alone are not curse, Jubilee, binding, or ownership verdicts. [Ord inscription updater](https://github.com/ordinals/ord/blob/03c9b87b14fe1e2c38191b860a08988345ec4e5f/src/index/updater/inscription_updater.rs)

The existing known-ID `parseOrdEnvelopes` path remains unchanged. It requires a Taproot-shaped control block and uses the older `gateway-envelope-v1/...` profile. Consumers must not mix those profile identities or assume equal results for unusual witnesses. The new bounded extraction profile removes that narrower recognition gate explicitly, rather than changing the old adapter's behavior during this release.

## Limits and failures

Work is bounded to 4,000,000 decoded witness bytes including at least one length-prefix byte per item, 100,000 recognized occurrences, and 250,000 payload items per block. These are implementation work limits, **not Bitcoin consensus rules or inscription validity rules**. A limit or input-integrity failure returns an error and a zero result; callers must pause/report the dependency error and must not mark that block or range complete. Supporting a block beyond these limits requires an explicit resource-budget change.

Malformed scripts and rejected envelope syntax are reference parser outcomes, reported through deterministic transaction/input/offset diagnostics. They do not trigger any network repair or hidden retry. No timestamp, local path, current owner, canonical number, or source endpoint is invented by the extractor.

## Verification performed

Focused native Windows Go tests cover transaction/input/envelope ordering, deterministic repeated serialization, annex and unversioned leaf selection, key-path exclusion, raw binary content, PUSHDATA1/2/4 boundaries, nonminimal empty pushes, malformed/truncated pushes, discarded-input ID numbering, missing terminators, disallowed envelope opcodes, field flags, chunked metadata, evidence rejection, and fail-closed work budgets. Existing known-ID parser fixtures continue to pass unchanged.

These are synthetic decoded-block fixtures with explicit synthetic integrity receipts; they test extraction semantics, not the validity of invented Bitcoin blocks. Primary source comparison was performed by inspection and fixture construction. No full upstream Ord executable differential run or mainnet range scan is claimed by this layer's unit tests.
