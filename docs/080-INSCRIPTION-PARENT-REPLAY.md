# Reusable checked inscription parent replay

The existing replay verifier is now an inscription relationship component in
`inscription_parent_replay.go`. Its inputs are parent identity/position, the
authenticated child occurrence, selected-header authority and bounded block,
transaction and spender providers. Bitmap supplies those through a thin wrapper.
The component does not select Districts, parse claim grammar or choose winners.
Existing proof fields, messages, content-addressed receipts and verification
behavior remain unchanged.

This extraction prepares the declared relationship-proof reader. That reader's
owned evidence/lifecycle integration and complete Parcel JSON policy are still
open; a reusable verifier alone does not complete the migration.

The previously optional positive fixture is now available locally. These raw
blocks were fetched through the documented [Esplora raw-block API](https://github.com/Blockstream/esplora/blob/master/API.md)
and independently checked by the replay test:

| Height | Block hash | Serialized bytes | Payload SHA-256 |
| --- | --- | ---: | --- |
| 818117 | `000000000000000000035ef07d180022e09a52f44e7587cbac23f25613d35fc2` | 1,827,614 | `ee936ab28fa84515d2ca5c6183aa735aca1577b922daa4cdd1f5e3d0e96a34cf` |
| 818174 | `000000000000000000033aff8cd450e7a1a26d421a1d32e0d7701b1ea1c1a7c7` | 1,511,121 | `f29fb2d54ae5019754b40a62b910a48f855141025c6046354f8bbd0615584591` |
| 818182 | `00000000000000000002407a2a6bcca709f08aa425b65de3af9b58b295c62ae5` | 1,592,974 | `28084e922f2e5d8f3425c4140610a27ea02bc93d4da3ea5dc4f4e8e75d4f1fbc` |

Run `python scripts/fetch-parcel-reference-079.py` to obtain/check the pinned
files under ignored `build/parcel-reference-079`, then run
`go test -run '^TestParcelReplayMainnetReference079$' -count=1` with the project's
Go environment. The script verifies header/payload hashes and preserves any
existing mismatched file. It does not read or index installed app data.

The positive end-to-end mainnet test passed in 8.036 seconds after extraction.
It verifies PoW, transaction/witness commitments, the Ord relationship, durable
receipt/reopen, replaced-source rejection and the capture boundary. The selected
District binding used later in that fixture is explicitly synthetic: this Ord
reference relationship itself is not a Bitmap claim, and fabricated claim
bodies are rejected. These three blocks do not constitute a full consensus or
full-chain replay audit.

The replay/attestation/same-transaction/grammar subset also passed in 4.565
seconds; the optional positive fixture was skipped in that initial run before
the files were fetched. The later mainnet run was a pass, not a skip.

The production build also passed (25,363,968 bytes).
