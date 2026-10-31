# Declared inscription parent replay points

Ordered transaction/reveal tables can declare `bitcoin.inscription_parent`
lookups. The installed catalogue supplies exact revision and semantic pins for
three explicit providers: `retained-raw`, `tx-locators`, and `txio-links`.
The key is `current-child-ref;parent-ref`, using canonical positional inscription
coordinates. `text_concat` composes the key without changing either reference.

The provider requires its complete selected genesis prefix through the child
block before proof work. A declaration starts no producer. The run plan and
guided editor explain that reads can scan the parent-to-child interval or audit
the prefix. An unavailable prerequisite or replay remains unavailable; it is
never evidence that a parent was not consumed.

Complete authenticated inventories with an absent endpoint or no matching parent
declaration return typed missing. Otherwise the existing checked Ord replay
returns `verified` or `rejected`, exact endpoint references and the unchanged
proof payload. Claim grammar, District recognition and winner selection are
outside this input primitive.

Every used result is retained in the child's imported-fact COW root. A positive
or checked rejected relationship also owns independent copies of its receipt,
raw proof blocks and any local no-spend attestation under `source-blobs`.
Producer files are copied through bounded streaming I/O, never linked directly.
Removing or modifying producer files cannot change the child's evidence.
Proof blocks stay outside the VM's single-value limit. Existing replay limits
remain 128 proof blocks and 64 MiB of aggregate raw proof data; the interpreter
does not add a separate per-block claim quota.

Fact checks bind every owned extent and digest to the receipt, proof and selected
source anchors. Final publication rechecks all used anchors, including movement
blocks. Maintenance verifies all required evidence before deleting anything,
hashes shared source blobs once per pass, retains referenced blobs, and collects
old unreachable blobs after the existing grace period. Whole-generation rebuild
archives retain these files with their facts and state. Existing facts that do
not use this source keep their previous serialized representation.

Local no-spend attestations retain their original limitation: they record this
installation's checked scan, rather than a detached consensus proof. The local
signing key is not copied into child evidence. Independent replay can still
require the complete raw interval. This source does not migrate the native
Parcel's remaining JSON policy by itself.

Validation: the focused mainnet point/ownership/key checks passed in 9.441
seconds. The wider table/import/identity/text/list/replay/attestation suite passed
52 checks, without skips or failures, in 63.904 seconds; its log is
`gateway-080-parent-points-regression.jsonl`. It includes saved output reads,
producer mutation/removal, owned-fact reuse, intermediate-header replacement,
corruption-before-collection, orphan collection and reviewed rebuild archives.
The authentic three-block Ord fixture is deliberately injected at the point
boundary; it is not misrepresented as a complete genesis-prefix provider or a
Bitmap claim. Production factories separately reject missing prefixes for all
three providers.

Guided unit and network-free headless DOM checks cover exact provider pins,
alias preservation, pair-key defaults, read-only targets and narrow layout.
These are code checks, not visible Chrome acceptance.
The wider workspace browser check and production build also passed
(25,448,960-byte executable).
