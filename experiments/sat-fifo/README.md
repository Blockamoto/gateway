# FIFO Sat-range operation experiment

This fixture-only Go experiment executes one constrained JSON operation plan
for an ordinary transaction: concatenate input Sat ranges in input order,
allocate each output's value from that stream, and retain the remaining ranges
as the transaction's fee. Intervals are half-open and stream order is preserved;
it need not be numerical order. Every input must supply a value, ranges and an
anchor string. Missing or inconsistent evidence, duplicate inputs, overlapping
ordinal ranges, overflow and overspending return an error without a result.
Anchors and input identities are caller assertions, not verified evidence.

Run `go test ./...` from this directory. The tests use synthetic fixtures to
check output splitting, ordering, zero values, fee remainder, conservation and
failure behavior. This is a single allowed operation sequence, not a general
index language or proof that a constructor can express the Sats index.

The 0.7.8 reference is `sat_core_077.go` and `sat_index.go`: its ordinary
transaction processing validates input range values, allocates ordered output
ranges and adds leftovers to a later fee stream. This experiment does **not**
settle fees to coinbase, generate subsidy ranges, model lost or burned sats,
genesis outputs, duplicate coinbases/BIP30, historical Ord rules, inscription
binding, Sat issuance, real block parsing, persistence, reorgs or recovery. It
does not validate Bitcoin transactions, authenticate chain anchors, or claim
parity with the complete Sats index.
