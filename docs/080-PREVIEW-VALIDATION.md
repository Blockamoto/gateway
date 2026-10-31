# 0.8.0 portable preview validation

The working local preview is `http://127.0.0.1:52108/?resolve=indexes.gateway`,
PID 9060 at validation time. It uses the disposable profile
`C:\Users\camer\AppData\Local\Temp\gateway-080-portable-api-3\profile`.
The profile contains authenticated genesis/block-1 header fixtures and the actual
215-byte mainnet block 1, plus four clearly named QA recipe tracks. It contains
no full mainnet prefix, UTXO, Sat or TXIO generation.

## Artifact identity

- Source: `f60e3537162b9c70e0c6e0a6c8e731c019ca2641`, branch `0.8.0`.
- Build manifest reports `source_dirty:false`; later documentation commits record
  this validation and do not change the packaged executable's source identity.
- Executable: `build/portable-v0.8.0-reviewed/GatewayClient.exe`, 19,311,104 bytes.
- ZIP: `build/Gateway-v0.8.0-reviewed-portable-preview-windows-amd64.zip`.
- ZIP SHA-256: `b7036da18f3e5b5a58d594ff935ca1cc9c045a1b0f5cbbb348c0641100f9e3e9`.
- All 89 packaged file hashes and their exact ZIP payloads verified; ZIP CRC
  checks pass. PE version and product resources both report 0.8.0.
- The earlier unaccepted build is preserved separately and is not this artifact.

The build disables update delivery. Runtime checks confirm offline/Core-disabled/
Core-mount-disabled settings, disabled sharing/LAN/cache/graph choices, and update
status `disabled`. The optional companion is packaged with its separate preview
identity; no extension, native host or integration was registered or installed.

## Actual process/API exercise

`scripts/test-portable-recipes.py` starts and stops the actual packaged executable
in a new disposable profile. The successful evidence is
`%TEMP%/gateway-080-portable-api-3/smoke-results.json`.

| Exercise | Observed result |
| --- | --- |
| Compile, validate, save and reload actual documents | All four saved recipes are validated and runnable. The smoke exposed and fixed the missing header/Raw registry Arm capability flag. |
| `qa-headers`, origin/In/Out 1 | Complete; one source read, zero body decodes and zero inscription extractions. |
| `qa-raw` + `qa-fees`, origin/In/Out 1 | Complete; one shared body acquisition, one owned ordered-source verification, no inscription extraction. The counters include two decode stages; no single-decode claim is made. |
| Reuse the completed Raw/fee range | Complete; zero source reads or decodes. |
| `qa-child` with exact saved `qa-fees` totals checkpoint | Complete as a child-only run; one source read, one copied typed row, no implicit parent writer. |
| Stop, restart and repeat completed Raw/fee work | Complete; zero source reads or decodes. All saved outputs remain covered and queryable. |
| Native state side effects | No native Coin, Sat or TXIO head is created. |

The earlier focused/wider family and lifecycle suites are recorded in
`080-ENGINE-LEDGER.md` and the linked parity matrix. This continuation adds 151
passing wider fee/state checks, 33 final fee/source/release checks, the corrected
registry/source subset, guided/narrow DOM round trips and both workspace browser
suites. The older transaction workspace test fixture was updated for read-only
registry/hierarchy routes and its navigation-address selector; product navigation
was unchanged.

## Remaining acceptance

Visible Chrome acceptance is **not completed**. Both the supported Windows
Computer Use runtime and the dedicated browser tool fail before JavaScript or
browser initialization with:

```
failed to write kernel assets: The system cannot find the path specified. (os error 3)
```

Kernel resets and the final attempt to open this exact preview produce the same
error. No visible click, screenshot or browser observation is claimed. The process/
API and headless source-identical checks are separate evidence. The Codex tool
runtime needs repair/restart before that acceptance can continue.

Full-chain throughput, peak memory and a positive mainnet Bitmap Parcel remain
outside the bounded fixtures. Bitcoin source checks are not full script/UTXO
consensus validation. All changes are on `0.8.0`; main, 0.7.9, Render, installed
user data and the preserved experiment were not changed.
