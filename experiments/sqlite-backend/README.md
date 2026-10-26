# SQLite backend experiment

This isolated module implements the `experiments/common-engine` Backend
interface using `database/sql` and the pure-Go `modernc.org/sqlite` driver.
The SQLite transaction checks the consumer revision, selected-chain epoch and
anchors, and consent epoch before publishing that consumer's full derived
state and coverage together. Separate consumers have separate revisions and
recipe identities; a changed anchor fences writes and prevents stale receipts
from being displayed as current. Consent is off by default.

The reference stores a complete gob-encoded engine View per commit for clarity,
not performance. A versioned recursive View schema fingerprint rejects an
incompatible stored schema rather than silently dropping fields; it provides
no automatic migration. Chain anchors are fixture assertions and not verified.
Tests use temporary files, inject failure after the SQL write but before commit,
reopen the database, change chain/consent fences, and exercise independent and
simultaneously competing consumers. A child-process test terminates a writer
after its SQL write and before commit, then reopens and checks recovery. Run
`go test ./...` in this directory.

An optional `TestSQLiteVerifiedDistrictDifferentialFixture` accepts the
`GATEWAY_SQLITE_DISTRICT_FIXTURE` path exported by the separate 0.7.8
differential harness after its 32 comparisons pass. It compares every ordered
block outcome, including empty outcomes, closes and reopens the SQLite file
halfway, and verifies the retained prefix before resuming. It reports the
fixture and source fingerprints; it does not independently authenticate the
export or run the 0.7.8 binary itself.

The recorded run used the exporter output at
`build/district-verified-32-01.json`, SHA-256
`d3f01991fcfff748fe15aed0c1828d16f18c6e9fb4e8f307011541a02d7ef12d`.
It compared all 32 exported native-matched outcomes: 30 complete empty blocks
and 3 winners across 64,767 transactions and 12,457 reveals. The SQLite test
closed and reopened after the first 16, checked the saved receipts, and resumed.
The fixture is an ignored local build artifact and must be retained or recreated
by the separate verified exporter for this opt-in test to run. Its pinned hashes
and limited block range do not establish whole-chain parity.

The successful local PowerShell invocation was:

```powershell
Set-Location 'C:\Users\camer\.codex\worktrees\gateway-080-reconstruction\Gateway\experiments\sqlite-backend'
$env:GOMODCACHE = 'C:\Users\camer\.codex\worktrees\gateway-080-reconstruction\Gateway\build\sqlite-go-mod'
$env:GOCACHE = 'C:\Users\camer\.codex\worktrees\gateway-080-reconstruction\Gateway\build\index-demo-cache'
$env:GATEWAY_SQLITE_DISTRICT_FIXTURE = 'C:\Users\camer\.codex\worktrees\gateway-080-reconstruction\Gateway\build\district-verified-32-01.json'
& 'C:\Users\camer\.codex\worktrees\d43b\Gateway\build\_tools-0.6.6\go\bin\go.exe' test -count=1 -run '^TestSQLiteVerifiedDistrictDifferentialFixture$' -v ./...
```

This is an experiment, not a storage selection, real block index, throughput
benchmark or production implementation. Process termination is not power loss;
the experiment does not demonstrate power-loss
durability, concurrent-workload performance, disk-space efficiency, schema
migration, broader crash scenarios, chain
authentication, reorg reconciliation or a full Gateway release. Every commit
rewrites the complete view and coverage checks scan saved receipts. A future
backend must establish its own atomicity and durability guarantees.
