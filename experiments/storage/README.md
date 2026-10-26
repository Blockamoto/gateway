# Storage layout experiment

This is a narrow prototype, not the Gateway engine or a database selection. It
compares a single SQLite database committing two projections together, one
shared database with independently committing workers, and two SQLite databases
with independently committing workers. All receive
the same transactions and output values decoded from real Bitcoin blocks, use
WAL and `synchronous=FULL`, and commit at block boundaries. Sixteen blocks are
inserted before timing the following sixteen. A reader polls simple checkpoint
queries while inserts run. Existing reference files are read-only; results must
be written to a new directory.

The parser checks fixture size and SHA-256, each header hash and selected header
bytes, consecutive header linkage, the claimed compact-target proof of work,
transaction Merkle root, and transaction count. This does not independently
verify difficulty transitions, complete selected-chain consensus, script/UTXO
validity, or witness commitments. The source archive was previously replayed
through Gateway's verifier. Two projection row digests must match across
layouts; injected failure before commit rolls back, two heights are removed and
replayed with matching complete row digests, and SQLite integrity is checked.
These checks do not simulate power loss, filesystem failure, a real replacement
branch, or independent consumer crash recovery.

On the local reference worktree, for example:

```powershell
python experiments/storage/replay.py `
  --manifest C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/qa-current/baseline/manifest.json `
  --archive C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/archive `
  --headers C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/profile/headers/headers.bin `
  --out experiments/storage/results/a-new-run
```

Add `--order separate-first` on another fresh output to reverse the layout order.

Timings are single observations, order and cache sensitive. They exclude block
decoding and the initial history, include Python, connection and commit costs,
and do not model all index families, large populated history, slow or skewed
consumers, shared decoded graph lifetime, or browser UI. The query latency is a
synthetic checkpoint query, not UI responsiveness. Windows process I/O counts
are logical process counters, not measured physical storage writes or fsyncs;
process peak memory includes parsing and all earlier phases. Store sizes are
measured after the rollback/replay check. Connection creation is deliberately
identical per index/block but is not an optimized production write path. The workers synchronize at every
block; this does not demonstrate independent consumer progress under skew. The
separate databases offer per-consumer atomic commits, not shared cross-index
atomicity. Do not infer production throughput or select a database from these
samples. Compare more repetitions, alternate ordering, mature DBs, real output
semantics, larger histories, interruption/reorg and full UI before deciding.
