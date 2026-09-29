# Implementation decisions — September 2026

- Keep one existing engine and shared Bitcoin decoder; index jobs select source
  retention explicitly instead of toggling global settings.
- Bound inscription occurrence extraction independently of historical Ord
  numbering. Unknown global context is not fabricated.
- Pin Bitmap's project origin to 792435 and label Gateway's chronological raw
  district profile explicitly. Source research does not justify calling it OPI
  equivalent or a complete universal historical standard.
- Store immutable block batches behind one atomic selected head. Local verified
  history can resume; peer claims cannot promote themselves into this store.
- Publish frozen index snapshots only after an explicit local action. Later
  private appends do not automatically extend what peers may retrieve.
- Reuse the current peer transport, remembered-member scheduler and Bitcoin
  discovery ledger. Add configurable alpha bootstrap sources without inventing
  a project-operated seed or introducing libp2p infrastructure.

Detailed evidence and limitations live in STATUS, INDEXING, INDEX-PROVIDERS,
INSCRIPTIONS and BITMAP-RESEARCH. The master brief sets the release acceptance
criteria; these notes do not waive uncompleted criteria.
