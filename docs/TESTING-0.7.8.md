# Gateway 0.7.8 reviewed portable preview

This local development build uses a separate profile and has application updates
disabled at build time. It does not install Gateway or publish a release/feed.
Keep any older preview folder and its profile intact.

Extract this ZIP into a new folder and run `Start-Preview.cmd`. It prints its
loopback URL, opens Gateway Home, and uses `preview-profile` beside this executable.
`Stop-Preview.cmd` authenticates and stops only that profile's process, retaining
data. Start resumes the same profile. A separate disposable profile is also
supported:

```powershell
.\Preview.ps1 -ProfilePath C:\GatewayTests\bitmap-078-reviewed
.\Preview.ps1 -ProfilePath C:\GatewayTests\bitmap-078-reviewed -Stop
```

Use `-Offline` for an offline profile or `-NoOpen` to skip opening a browser.
Ordinary Bitcoin networking is enabled by default; Core RPC/mounted Core access,
data serving, optional indexes, and raw retention begin disabled. Merely opening
an index view does not arm or start an index.

The separate performance preview includes its approved scope and validation in
`APPROVED-PERFORMANCE-BRIEF-0.7.8.md` and `VERIFICATION-PERFORMANCE-0.7.8.md`.
Home's feature checklist describes available sources and saved evidence; its
indicators are distinct from the optional tracks' Arm and Live-follow switches.

The fixed canonical District and Parcel tracks both start at **792435** and are
siblings. Review a bounded range, arm the chosen normal tracks, then Start.
Bitmap arming offers the existing Transaction Locators track as a companion.
Legacy/custom Bitmap profiles remain inspectable but cannot execute. Old
checkpoints are not migrated into the new canonical identities. Both chains are
disabled, including direct API and restored execution. Saved evidence is retained.

Already-armed capable tracks can retain facts from a complete verified block
obtained by ordinary browsing. Sparse facts do not imply complete history or
negative knowledge. Stateful tracks advance only on the exact next block.
Values and Scripts can be selected independently of TXIO links. Sat historical
ranges require their explicit existing retention option. Capability details are
in `SPARSE-INDEXING-0.7.8.md`.

Canonical source rules are separate from implementation limits. Parcel parent
tags alone do not establish an Ordinals child relationship: exact evaluated parent,
spend, and Sat context is required. Missing evidence remains unresolved. A local
parent-evaluation receipt preserves what this installation checked; independently
replaying that check still needs its referenced Sat evidence.

Relationship buttons explain missing local coverage or configured Core evidence.
A missing spender never establishes that an output is unspent. Core's transaction
or spender indexes are RPC services; normal Bitcoin P2P retrieves known blocks.
Hover or focus a flow strand for immediate known amount and destination details.
Unknown inputs do not hide known outputs or create an inferred fee.

The standards project is now `https://github.com/Blockamoto/blocators`.
The grammar is unchanged. Frozen compatibility identifiers such as
`gateway-sat-fifo-mainnet-bps-v2` retain their original spelling because changing
an authenticated identifier would change profile identity. Historical recipe
strings can likewise retain the old spelling; active UI terminology is lowercase
blocator/blocators.

See `APPROVED-BRIEF-0.7.8.md` for scope and `VERIFICATION-0.7.8.md` for the checks
actually completed. This is bounded local acceptance, not a full mainnet index or
full Bitcoin consensus validation.
