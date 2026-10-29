# 0.7.9 Parcels

Gateway 0.7.9 adds a validated Bitmap Parcels track and a revised indexing workflow. See [indexing](INDEXING.md) for evidence requirements and operational limits.

- Bitmap Parcels uses checked Ord parent provenance and the selected Bitmap District interpretation. A declaration of a parent alone does not prove the relationship. Unknown or incomplete evidence remains pending and does not advance validated Parcel coverage. Saved experimental Parcel data remains available without being presented as validated results.
- Setting up Parcels selects Districts and one proof route: retained Raw blocks, TX ID Locators, or Transaction Input/Output (TXIO) links. The route and indexing scope authorize the needed evidence work; selection does not itself start a build. Existing compatible coverage and checkpoints can be reused.
- District and Parcel processing advances together on committed validated completion. Live following waits for compatible District and proof-producer evidence; a marked Out remains a hard cap. There is no per-run validation-operation quota.
- Index setup and start controls clarify dependencies, genesis and continuation boundaries, and selected tracks. Timeline navigation and Bitmap relationship views expose locally saved evidence without implying complete coverage.

Gateway verifies block identity and selected-chain context but does not provide full Bitcoin consensus or script validation. Bounded tests do not establish full-mainnet indexing time, storage, memory, or whole-chain Parcel and Live performance. Some parent proofs remain pending when required evidence or supported proof bounds are unavailable. Bitmap Chains, Satline history tracing and Gateway peerhood remain disabled. See [testing](TESTING-0.7.9.md), [status](STATUS.md), and the packaged RELEASE-ACCEPTANCE.json for evidence and remaining release checks.
