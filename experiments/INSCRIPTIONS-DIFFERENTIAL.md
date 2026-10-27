# Inscription projection real-block comparison

`inscription_projection_real_080_test.go` is an opt-in adapter from Gateway's
existing verified block parser and complete inscription extractor to the
experimental common engine's `inscription-fields` recipe. It reads the isolated
0.7.8 fixture archive and selected header copy; it does not start the client or
write to the fixture, baseline profile or reference worktrees.

For each block, the test verifies body bytes against the pinned manifest and
header, invokes the existing Gateway block verifier, checks consecutive header
linkage, extracts the complete block inventory, and validates its extraction
receipt. Only then does it mark every transaction's reveals complete and send
all payload pushes to the engine in physical transaction and inscription order.
Each emitted row is compared with the existing extractor for blocator, parser
profile, ordered raw pushes, raw tag/value map, metadata hex, metaprotocol and
parent-declaration bytes, dangling-tag state and incomplete flag. A declaration
is represented as unresolved, never as a proven parent-child relationship.

The test passed on 2026-10-27 with **32 blocks, 64,767 transactions and 12,457
complete extracted reveals** at heights 792435–792466. **None** of these
reveals contained a metadata tag, metaprotocol tag, parent declaration or
incomplete field. Consequently this real-block run checks the absence of those
fields; it does not validate their nonempty or malformed cases. Those cases are
covered separately by synthetic common-engine tests and have not been proven by
this corpus. The test uses the existing extractor as its comparison source; it
is not independent Ord conformance validation.

To reproduce, first prepare the owned fixture described in
[storage/BASELINE-078.md](storage/BASELINE-078.md). Then, from the checkout root
with a complete Go toolchain:

```powershell
$env:GO111MODULE = 'off'
$env:CGO_ENABLED = '0'
$env:GATEWAY_080_078_FIXTURE = (Resolve-Path 'build/baseline-078-owned-01').Path
go test -tags gateway_district_diff -run '^TestInscriptionRealBlockProjection080$' -count=1 -v -timeout=10m .
```

The shared build tag enables the fixture helpers in the District differential;
the `-run` pattern selects only the inscription test. Without the environment
variable the test skips. The fixture is header-anchored evidence, not proof of
full Bitcoin consensus or present-day selected-chain membership. Results do not
establish broad Ord compatibility, parent provenance, reorg handling,
production storage behavior, or user-defined index completeness.
