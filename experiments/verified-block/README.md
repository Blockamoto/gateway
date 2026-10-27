# Standalone verified-block experiment

This isolated Go package tests whether maintained btcd components can form a
reusable block-integrity boundary for Gateway 0.8. It pins
[`github.com/btcsuite/btcd` v0.27.0](https://github.com/btcsuite/btcd/tree/652ed42247660daec248d2cfb3a3a87cbeb50592)
and its transitive versions in `go.mod`/`go.sum`; it does not import Gateway's
`package main` or copy its parser. The candidate uses btcd's `wire` decoder,
`blockchain.CheckBlockSanity`, `ValidateWitnessCommitment` and `GetBlockWeight`.

`Verify` accepts a raw mainnet block, caller-supplied height, and exact header
bytes. It checks byte bounds, header equality, complete decoding, btcd's
context-free sanity checks (including header PoW, transaction checks and Merkle
root), post-SegWit witness commitment and block weight. Before mainnet SegWit
activation it rejects witness data and does not treat a commitment-looking
output as an activated commitment. Read-only accessors return detached
transaction inputs, witness items and outputs.

The height and header are caller assertions. Matching a header does **not**
authenticate its ancestry, selected-chain membership, header difficulty
transitions or current chain status. `SelectedChainProven` and
`FullConsensusValidated` remain false. No UTXO/script consensus, ownership,
inscription parsing, peer fetching, durable receipt or reorg handling is
implemented. btcd sanity performs additional context-free checks beyond the
0.7.8 commitment checks and includes a time-source-dependent future timestamp
check; exact historical acceptance and rejection parity remains to be studied.
The mainnet-only activation decision is valid only when the supplied height is
independently trustworthy.

For comparison, the 0.7.8 `parseBlockDetailed` and `integrityVerified` perform
header/hash/PoW, transaction Merkle and witness checks, enforce an exact
transaction order, reject mutated Merkle trees and retain a process-local raw
body receipt. The btcd candidate adds its own transaction-sanity, legacy sigop
and timestamp checks, but this experiment has not proved that its rejection
behavior is identical across Bitcoin history. It also does not yet expose all
transaction and header fields required by the existing explorer or provide the
existing private extraction receipt. Adding a selected-chain authority contract
and a complete detached block view is necessary before integration can be judged.

Tests use a self-contained genesis vector and adversarial header/body, trailing,
noncanonical CompactSize and size inputs. To additionally test three read-only
historical fixtures, set `GATEWAY_VERIFIED_VECTOR_DIR` to the directory
containing `blocks/<block-hash>.blk`, and `GATEWAY_VERIFIED_HEADERS` to the
header snapshot from [the vector evidence](../INSCRIPTIONS-REAL-VECTORS.md).
The test checks pinned file digests, block hashes and transaction positions and
changes witness bytes to require commitment rejection. The pinned snapshot is a
fixture, not proof of current selected-chain membership.

Run `go test ./...` from this directory. This is a candidate for evaluation,
not a finalized dependency, conformance claim or replacement of 0.7.8.

## Independent review notes

The input is copied before both header comparison and parsing, so those checks
operate on one owned snapshot. A caller must still avoid concurrently writing
the input while the copy is being made. `WitnessRulesChecked` describes the
rule branch selected by the **caller-supplied** height; it is not proof that the
height or chain anchor is authentic.

The pinned btcd decoder bounds declared counts against its message limits and,
for the byte-backed reader used here, checks available bytes before large
preallocations. Targeted tests reject impossible transaction and witness counts.
However the 4,000,000-byte serialized ceiling is **not** a 4 MB decoded-memory
ceiling: a large witness containing many empty items can require a slice header
per item. For example, on a 64-bit Go implementation roughly four million
three-word slice headers alone are on the order of 96 MB, before block and
runtime overhead. That is a source-based estimate, not a measured peak. A
production acquisition scheduler must enforce an aggregate memory/concurrency
budget; this experiment does not yet do so. No arbitrary lower witness count
has been introduced, because doing so would require a separate compatibility
and consensus analysis.

On the real 825334 fixture, a regression duplicates the final transaction of an
odd-sized tree. The mutated transaction Merkle root remains identical, but
btcd's duplicate-transaction rule rejects the block. The test also checks that
witness-bearing bytes are rejected when the caller asserts a pre-SegWit height,
and that a different caller-asserted post-SegWit height does not magically
prove chain membership. These checks cover the demonstrated case, not every
historical block or mutation pattern. btcd's future-timestamp rule depends on
local time; an incorrect system clock can cause an otherwise authentic block to
be rejected, so callers must surface the error rather than mark the block or
source as definitively invalid.
