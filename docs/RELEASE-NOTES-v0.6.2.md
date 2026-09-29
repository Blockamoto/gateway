# Gateway 0.6.2 — sparse index fixes

- Compact inscription coordinates: `TXiINDEX.HEIGHT` and `TX.iINDEX.HEIGHT`, with optional `.bitcoin` suffix. Resolve the reveal from Bitcoin block evidence without an Ord server or Gateway locator peer. The local inscriptions module remains enabled; no global sat ownership or canonical inscription numbering is implied.
- Bitmap jobs retry temporarily unavailable block sources with bounded backoff, preserve checkpoints, and expose an explicit resume/retry action. Validation and storage failures remain visible errors.
- Blocks checked and unique districts found recover from committed state after resume/reorganization.
- Bitmap's fresh starting height is fixed internally. Only the final block is editable.
- New Bitmap builds default to lean district/inscription strings. Full mode retains candidates/evidence. Existing full instances resume as full; no automatic destructive migration. Lean peer publication is not yet supported by the existing full-record peer format.
- Settings offers a password-gated development visibility mode for Satline controls. This is a UI testing gate, not API authorization. No plaintext development password is included.
- The testing discussion and future browser/WebRTC/IndexedDB/OCI/comparison ideas are recorded in GATEWAY-ROADMAP.md with scope and unresolved questions.

Validation: Windows local build and Linux cross-build; installer/portable payload audit; application Go tests and vet; real Chromium shell navigation and shipped-script index interactions; lean restart/duplicate/rollback tests. Three existing cleanup tests require Windows symlink privilege. Native Linux execution, installer execution, a new full historical scan and browser-to-peer transport are not claimed.

The previous 0.6.1 release is preserved. Packages are unsigned prereleases. Core, Ord and user profiles are not modified during release validation.
