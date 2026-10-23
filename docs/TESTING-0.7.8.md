# Gateway 0.7.8 local portable preview

This is a local development build for user testing. It does not install Gateway,
register a browser integration, or apply application updates. No release or update
feed is published by the preview build script.

Extract the ZIP into a new directory and run `Start-Preview.cmd`. It uses a separate
`preview-profile` beside the executable, opens the browser, and prints its URL.
`Stop-Preview.cmd` stops only the process authenticated by that profile's runtime
record and preserves its data. Run Start again to resume that profile.

For a separate disposable profile, use PowerShell:

```powershell
.\Preview.ps1 -ProfilePath C:\GatewayTests\bitmap-078
.\Preview.ps1 -ProfilePath C:\GatewayTests\bitmap-078 -Stop
```

Use `-Offline` on first creation for offline UI inspection. Otherwise ordinary
Bitcoin header networking is enabled. Core access and data serving start disabled
in this isolated profile. Settings remain under your control. Opening an index
view does not start indexing.

Canonical District and Parcel tracks use new identities and start at genesis to
establish the first qualifying reveal. Legacy experimental definitions and results
remain inspectable and deletable but cannot execute. They are not migrated into
canonical checkpoints. Select the canonical tracks, review a bounded range, and
use the timeline Start/Pause/Resume and independent Live and retention controls.

Chains are optional derived records. District and Parcel chains have independent
outcome histories and can consume saved authenticated outcomes. Missing history
is a gap, never an empty block. The chain scheme uses a replaceable development
codec because the upstream draft leaves mechanical encoding choices unresolved.
Consult the read-only rule/source metadata and codec documentation before comparing
hashes with another implementation.

Parcel parent declarations alone do not prove the Ordinals child relationship.
When the required dependency evidence is unavailable, the preview reports it as
unresolved and does not silently start unrelated indexing. Backend fixtures and
local browser checks are evidence of bounded behavior, not a completed current
mainnet index or full Bitcoin consensus validation.
