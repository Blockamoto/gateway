# Exploratory retained-body cache comparison

The skew broker optionally retains recently unused decoded bodies, evicting the
least recently released entry to respect both an entry limit and an estimated
inactive-byte limit. The default remains no retention. The broker checks the
block hash before returning an active or retained entry and independently checks
the static selected header immediately before each individual consumer commit.
It now reports source decodes, active overlap hits, retained hits, evictions,
active and retained estimated peaks, and the combined estimated peak. The unit
tests cover retention hits, byte eviction, redecoding and rejecting a mismatched
cached identity; the run checks full output and checkpoint digests.

These are Python object-size estimates, not a hard memory bound: decoding and
raw temporaries, interpreter/SQLite allocation and allocator overhead are not
included. The active-byte check occurs after decoding. Retained bodies can use
up to the configured inactive budget in addition to active-body storage. This
sample does not exercise a dynamically changing selected chain or establish
safe cache behavior through a real reorganization. The broker serializes decode;
read `SKEW.md` for the other fixture, concurrency and recovery limitations.

## One matched observation, 2026-10-26

The same 32 authenticated fixture blocks, eight-block initial history, 24
heights per consumer, two-slot work queues, FULL/WAL individual commits and
100 ms artificial slow-consumer delay were used for both runs. The no-retention
run was first, then the retained run; each ran shared database before separate
databases. The retained configuration allowed eight inactive bodies and 32 MiB
of estimated inactive bytes. The parent reported Go tests had stopped before
the pair; system load, OS cache and Python scheduling were otherwise
uncontrolled. These are single observations, not a speed comparison.

| Layout and policy | Source decodes | Active overlap / retained hits | Evictions | Estimated active + retained peak | Fast / slow finish |
| --- | ---: | ---: | ---: | ---: | ---: |
| Shared, no retention | 47 | 1 / 0 | 0 | 4,846,189 B | 3.831 / 6.489 s |
| Shared, eight retained | 45 | 0 / 3 | 37 | 21,253,248 B | 2.214 / 5.142 s |
| Separate, no retention | 48 | 0 / 0 | 0 | 4,381,147 B | 1.430 / 4.523 s |
| Separate, eight retained | 46 | 0 / 2 | 38 | 20,916,394 B | 1.712 / 5.083 s |

Process lifetime peak working sets in the same order were 31,977,472,
45,027,328, 33,439,744 and 46,014,464 bytes. The per-layout process logical
read/write bytes were 79,305,087 / 17,003,200; 76,552,853 / 16,999,104;
80,334,028 / 16,929,424; and 77,404,096 / 16,929,424. Process counters are
not physical device I/O and lifetime peaks are not isolated per-layout peaks.
Per-consumer checkpoint sequences and transaction/output digests matched in both
layouts and across the two runs. Fast workers still advanced ahead. In this
small skewed workload, eight retained entries saved only two source
decodes in each layout while substantially raising estimated retained memory. Consumer lead,
replacement policy, dataset, duration and cache budget can change the result.

For reproduction, run the command in `SKEW.md` twice using fresh outputs; add
`--retained-blocks 0` to the first and `--retained-blocks 8` to the second. Then:

```powershell
python experiments/storage/compare_cache.py path/to/off/result.json path/to/eight/result.json
python experiments/storage/test_skew.py
```

The comparison tool verifies matching fixture, scope, delay, output digests and
checkpoint digests before displaying metrics. All generated databases and
results remain ignored.
