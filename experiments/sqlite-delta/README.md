# Incremental SQLite transaction experiment

This isolated experiment proposes a `Delta` contract: a producer prepares a
selected-chain/consent/revision fence, reads the specific coins it needs, then
submits changed coin upserts and deletions plus one complete anchored receipt.
`CommitDelta` applies those mutations, the consumer head, and the receipt in
one SQLite transaction after rechecking the fences. A missing requested coin
is explicit; a stale lookup or publication fails. Consumer state is separate,
consent defaults off, and stale saved coverage is not presented as current.

`PrepareDelta` does not materialize the full state in Go, and `LookupCoins`
reads requested outpoints only. Coverage validation still scans prior receipt
anchors in SQL; the experiment does not establish constant-time publication.
`WriteStats` counts logical rows changed, not disk writes or bytes. One fixture
inserts two coin rows, then updates one, inserts one, deletes one and records an
empty receipt; an explicitly unchanged upsert causes no coin-row update.

The current common-engine `Backend` requires a complete candidate View. Its
adapter here therefore reads and compares the full state before submitting a
delta. Using that adapter does **not** avoid whole-state copying or prove
indexing throughput. It is provided to compare correctness against the earlier
whole-state SQLite prototype. A real incremental engine must produce its own
deltas and selected lookups without that adapter.

Run `go test ./...` and `go vet ./...` in this directory. Tests cover insertion,
updates, deletions, empty outcomes, independent index instances, stale chain and
consent fences, injected rollback, reopen, and comparison with the whole-state
SQLite backend. A child process is terminated after coin mutations but before
receipt insertion and commit; reopening preserves the prior state and a retry
succeeds. Process termination is not a power-loss test.

The optional `TestDeltaAndWholeBackendOnDistrictExport` uses the pinned
`GATEWAY_SQLITE_DISTRICT_FIXTURE` export from the separate 0.7.8 differential
harness. It compares every ordered District receipt from this adapter and the
earlier whole-state SQLite backend to the native-matched export, then closes and
reopens both halfway through. It does not independently authenticate the export
or demonstrate whole-chain parity; the common-engine adapter remains full-View.
The local run passed on all 32 exported outcomes, including 30 empty outcomes
and three winners; the input SHA-256 was
`d3f01991fcfff748fe15aed0c1828d16f18c6e9fb4e8f307011541a02d7ef12d`.

This is a synthetic, non-production experiment, not a storage selection or a
performance benchmark. SQLite uses the separately pinned pure-Go driver. It
does not authenticate Bitcoin data, implement reorg rollback, migrate schemas,
provide a complete index engine, prove full 0.7.8 parity, or measure physical
I/O, memory, latency or throughput. Producer semantics and source evidence
remain outside this storage contract. A runtime View-shape check fails closed
if the supported persisted structures or their serialization tags change.
In particular, the first head's required genesis or District origin is a
producer responsibility, and an upsert can update an already present coin.
A Bitcoin UTXO producer must reject disallowed live-outpoint collisions and
handle historical exceptions before submitting a delta; this store does not
establish those rules.
