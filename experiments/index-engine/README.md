# Executable index definitions: small experiment

This isolated prototype checks a versioned JSON definition, runs its typed
field and lookup operations against transaction or input rows, and returns an
anchored result for one supplied block. The two definitions in `definitions/`
produce transaction locations and positive spend references through the same
executor. Missing or ambiguous lookups retain the native previous outpoint with
an explicit unresolved state; provider errors abort the run. Zero rows describe only the supplied block, never an entire
range or unspentness.

Run the tests from this directory:

```sh
go test ./...
go run ./cmd/index-demo -definition definitions/spends.json -fixture fixtures/block.json
go run ./cmd/index-demo -definition definitions/transactions.json -fixture fixtures/block.json
```

`host/` adds a common versioned definition envelope and `Host.Execute` entry
point for this sparse interpreter and the neighboring state-machine experiment.
The host checks recipe and input kinds and runs each instance independently;
it dispatches to two different operation interpreters. Missing lookup evidence
stays unresolved or aborts; missing state prevouts abort the transition. This
is an orchestration experiment, not proof that one operation vocabulary can
express all built-in indexes. It does not yet share decoded blocks or perform
cross-index atomic publication. Its local Go module dependency points to
`../state-machine`.

The supplied block and resolver are **fixture contracts**, not Bitcoin
verification. An anchor string is required but is not authenticated here.
The prototype has no durable storage, real block decoding, safe reorg/restart,
parallel scheduling, actual custom-index UI, or compatibility guarantee. It
does not implement stateful UTXO/Sat, inscription extraction or Bitmap and does
not demonstrate their parity. Production code must bind an authenticated
selected chain, stable definition identity, coverage and retained evidence to
atomic publication, resource limits and proper recovery before use.
