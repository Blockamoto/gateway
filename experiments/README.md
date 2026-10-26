# Gateway 0.8 reconstruction experiments

These experiments are isolated from Gateway's production runtime and its
profiles. They are a first engineering increment, not a 0.8 preview or a claim
of feature parity. The starting reference is 0.7.8 commit `efc5b13`; the separate
performance worktree at `5514485` is evidence, not the base of this branch.

- `index-engine/` tests executable, typed sparse index definitions on fixture
  data. Its input anchors are not verified by the prototype.
- `common-engine/` tests sparse projection and UTXO-style state operations in
  one interpreter on a shared fixture input. Its two fixed recipes do not yet
  constitute a general constructor.
- `storage/` compares candidate layouts using an independently checked real
  block corpus and simplified projections. Its README states the important
  qualifications and reproduction inputs.
- `pipeline/` explores bounded parallel consumers and their scheduling
  behavior. It is not a durable or chain-aware production pipeline.
- `state-machine/` tests a small executable UTXO-style transition recipe,
  atomic failure and bounded rollback on synthetic data.
- `sat-fifo/` checks ordinary-transaction range allocation and fee remainder
  with synthetic evidence; block-level coinbase and historical rules are not
  implemented.
- `PROTOCOL-AND-CONTRACTS.md` records pinned protocol evidence, operation
  obligations and unproven behaviors.

`gateway080_verified_adapter_experiment_test.go` bridges the existing 0.7.8
verifier to the sparse experiment for pinned mainnet genesis. Its optional
second test accepts a local raw block through `GATEWAY_080_BLOCK_792435` and
checks it against an independently pinned hash. These are test-only adapters,
not a general selected-chain authority or a production integration.

The production target remains on-demand Bitcoin peer acquisition, the current
explorer and enabled indexes, and the familiar timeline, backed by a common
engine and an approachable executable index constructor. Full stateful index,
Ordinals, migration, reorg, privacy and recovery parity are not demonstrated
by these experiments. Physical storage and the definition runtime remain open
until representative measurements and correctness tests justify a choice.

Existing profiles and data must not be silently modified or interpreted as
compatible by the new engine. A migration or read-only import requires its own
explicit compatibility, evidence and recovery policy; no such policy has been
implemented here. Gateway peerhood and related 0.9 concepts are future scope.
