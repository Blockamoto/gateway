# Independent-consumer skew experiment

`skew.py` compares one shared SQLite database and two separate SQLite databases.
Each consumer has a two-item FIFO containing height requests, a separate worker,
and a separately committed per-block transaction containing its own data and
checkpoint. Both layouts use SQLite WAL and `synchronous=FULL`; the slow output
consumer sleeps before each block while the TX consumer can proceed. Consumers
start from identical eight-block populated histories. Each source request is
read and decoded on demand, and a broker shares an active decoded body when
requests overlap. It serializes decoding and allows at most two active bodies.

For the referenced 32 authentic blocks, the decoder independently checks size,
SHA-256, selected header bytes/hash, previous-header linkage, claimed target
proof of work, transaction Merkle root and count. Immediately before each
commit it checks the selected header hash again. This is a read-only static
header file, so this is not a race-safe reorganization publication fence.
It does not independently verify the difficulty rules, full selected-chain
consensus, scripts/UTXOs or witness commitment. See `README.md` for the archive
origin and fixture limitations.

The broker tracks estimated Python sizes of retained decoded rows; it checks a
32 MiB limit after a decode and fails if exceeded. It is a diagnostic estimate,
not a hard memory allocator or RSS ceiling: it excludes raw/decode temporary
buffers, SQLite caches and runtime overhead. At most two broker entries are
active; requests queue only heights. Consumers may retain short-lived Python
references, and a future scheduler must enforce its own real memory budget.

Each run asserts per-consumer sequential completions, fast-worker progress
while the slow worker lags, exact selected-height/hash checkpoint sequences,
matching full row digests between layouts, SQLite integrity, and broker cleanup.
`test_skew.py` additionally checks wrong-anchor rejection and bounded broker
reuse/wait/release with small synthetic inputs. These are not power-loss,
injected crash/retry, dynamic reorg, consensus, large-history, index dependency,
GUI responsiveness, or production scheduler tests. The static delay and two
simple projections do not represent all index families. Single observations
with order/cache effects do not choose a storage layout; shared and separate
files still have different contention and cross-consumer atomicity properties.

```powershell
python experiments/storage/test_skew.py
python experiments/storage/skew.py `
  --manifest C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/qa-current/baseline/manifest.json `
  --archive C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/archive `
  --headers C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/profile/headers/headers.bin `
  --out experiments/storage/results/a-new-skew --count 32 --warm 8 --delay 0.1
```

Use `--order separate-first` and a different output path to reverse order.
Generated profiles and databases are ignored. No user profile is opened.

## Exploratory sample, 2026-10-26

One Windows run used the command above after concurrent baseline compilation
finished. One snapshot detected no `go`, `compile`, or `link` process during
the run; other load, cache and Python scheduling were uncontrolled. The shared
layout ran first. Fixture manifest SHA-256 was
`d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a`.

| Measurement | Shared database | Separate databases |
| --- | ---: | ---: |
| Fast TX finish | 2.545 s | 1.909 s |
| Delayed output finish | 5.628 s | 5.066 s |
| Largest TX lead | 15 blocks | 18 blocks |
| Process CPU over timed work | 3.984 s | 3.313 s |
| Logical process reads / writes | 81,013,964 / 16,999,104 B | 80,334,028 / 16,929,424 B |
| Broker peak active bodies | 2 | 2 |
| Peak estimated active decoded rows | 4,945,388 B | 4,573,068 B |
| Process lifetime peak RSS | 33,361,920 B | 34,734,080 B |

All ordered checkpoints and final row digests matched. In both layouts the 24
timed blocks were decoded **48 times**, with zero overlapping broker hits: the
fast consumer ran ahead and the broker drops bodies once no worker holds them.
This demonstrates the cost of this particular memory/reuse policy, not that
parallel indexing inherently requires two decodes. The short test does not
establish either a speedup or which database to use. Testing a bounded retained
cache or authenticated spool against this baseline is a useful next experiment.
