# Replaceable Bitmap Chain development codec v1

The optional District and Parcel chains follow the shape of the Bitmap chain
draft pinned at revision `577bae5e5810cb2389f1e02f322438df567f0089`, README blob
`5a67f8b37a12bd0ce9968c67f8c8ab8d40488a27`. The draft leaves mechanical encodings
unresolved. **`gateway-bitmap-chain-dev-v1` is a Gateway preview codec, not an
upstream interoperable encoding.** It does not decide Bitmap claim validity.

Each producer keeps its own authenticated completed outcomes. Chains consume
those outcomes without loading raw blocks or extracting inscriptions. District
and Parcel chains have separate registries, outcome chains and final chains.
The Parcel chain never consumes the District chain hash. The first height is
the reveal of the selected valid `0.bitmap`, established by the canonical
District index. Before that evidence exists the chain waits. Its producer may
run alongside it or may already have retained the required history.

The replaceable boundary is `bitmapChainCodec` in `bitmap_chain_codec.go`:
`Outcome`, `Registry`, and `Step`. Scheduling, producer truth, retention and
publication use its outputs. A replacement must use a new codec ID and fresh
chain identity. Saved state binds the codec, exact immutable definitions,
resolved origin, source instance/profile and previous hashes. Unsupported or
changed identities cannot resume silently. Existing truth/outcomes remain
available to a replacement chain.

Mechanical choices in v1:

- SHA256 throughout, without domain prefixes. Hash strings decode as their
  displayed big-endian hex bytes. Initial previous outcome and final hashes
  are 32 zero bytes.
- An inscription ID encodes as the 32 displayed txid bytes followed by its
  uint32 inscription index in big-endian order. Sort these fixed 36-byte
  records lexicographically, reject duplicate IDs, concatenate, then SHA256.
  A completed empty block is SHA256 of zero bytes. A missing or unresolved
  producer block supplies no outcome and cannot advance either chain.
- One initial registry slot contains the exact compact JSON bytes emitted by
  Go `encoding/json` for `{"encoding":"gateway-bitmap-rule-definition-json-v1",
  "profile":<the pinned source profile>}` (no newline or whitespace). The
  actual bytes, including field ordering and JSON escaping, are stored as
  base64 `definition_bytes`, so reproducing them does not depend on a future
  serializer. Profile reference, implementation and parser identities are
  included. The initial slot is introduced/activated at the chain origin.
  This preview exposes no rule mutation/editor.
- Registry slots have permanent array positions. Their first activation is
  their introduction; later activations must follow a prior retirement.
  Activation includes its height; retirement excludes its height. Introduced
  active slots contribute SHA256(exact definition bytes); introduced inactive
  slots contribute 32 zero bytes; not-yet-introduced slots contribute no bytes.
  SHA256 of the concatenation is the registry hash. Future rule definitions
  must append slots and preserve exact previous definition/history bytes.
- `outcome_chain = SHA256(previous_outcome_chain || outcome_hash)`.
- `final = SHA256(previous_FINAL || source_Bitcoin_hash || registry_hash ||
  outcome_chain)`; each input is exactly 32 bytes, in that order. An evaluated
  empty block advances both chains. Retention policy does not change hashes.

Fixed vectors (also checked by `TestBitmapChainDevCodecFixedVectorsAndRegistryHistory`):

| Input/result | Hex |
|---|---|
| Empty outcome | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| Outcome ID: txid bytes `00..1f`, index 513 | `0c0da5df7750e2b98b672bad3684eee982780de8833015c53097e8cc56966413` |
| One active slot, definition ASCII `example immutable definition` | `a127d493cae621b50a734ab9563fdcde2cf88f4926da4400fe4bd5cb3d531b60` |
| First outcome chain for that ID | `1eba01470bba7a0401ed057d9f93c05bb1c3e4cdd2513e9159e6a0a48e36ee9b` |
| First final: source Bitcoin bytes `20..3f`, preceding registry/outcome | `1de86e72c6f1b182cdf69639c8008d4a321e2b5ae6b99c9de1cc8806133845bd` |

Full/Pruned storage and selected-branch restore points reuse the existing
authenticated publication/recovery path. Missing history is reported and
requires an explicit producer replay/rebuild decision. If a reorg changes or
removes the initial selected `0.bitmap`, the derived chain requires an explicit
rebuild; the canonical producers retain independent recovery/inspection state.
