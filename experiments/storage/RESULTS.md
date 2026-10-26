# Exploratory observation, 2026-10-26

One three-layout run on Windows completed with other Go compilation/extraction
possibly competing for resources; cache and system load were not controlled.
The fixture was 32 consecutive Bitcoin blocks at heights 792435–792466, with
64,767 transactions and 246,531 outputs. Sixteen blocks were populated before
the 16-block timed section. SHA-256 of the supplied 0.7.8 replay manifest:
`d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a`.

| Layout | Timed seconds | Synthetic polling p95 | Logical process read / write bytes | Files after replay |
| --- | ---: | ---: | ---: | ---: |
| Shared coordinated block commit | 1.266 | 0.442 ms | 7,018,148 / 12,523,720 | 10,240,000 B |
| Shared independent concurrent commits | 2.215 | 0.431 ms | 7,392,484 / 12,692,424 | 10,240,000 B |
| Separate independent concurrent commits | 2.115 | 0.450 ms | 7,389,840 / 12,581,784 | 10,248,192 B |

The three process lifetime RSS peaks were 64,167,936, 65,650,688 and
65,937,408 bytes respectively; these are not per-layout isolated peaks. Both
projection row digests agreed across layouts; each passed a pre-commit injected
rollback, two-height deletion/replay with matching row digests, and SQLite
integrity check. The timing does not include decoding, history construction or
the subsequent verification. Reader polling is not browser UI. Different
layouts were measured sequentially and use different numbers of connections,
threads and atomic commits. This is not a controlled comparison of equivalent
failure semantics, a power-loss test, or a basis for choosing a store. See the
README for the full qualifications and instructions to reproduce.

A second observation reversed the order (separate, shared independent, shared
coordinated), after the parent reported its baseline compilation complete. No
`go`, `compile`, or `link` process appeared in two snapshots during the run;
other system load and cache were not controlled. Results follow in execution
order; these are still single short samples and polling p95 has only 14–20
observations per layout.

| Layout | Timed seconds | Synthetic polling p95 | Logical process read / write bytes |
| --- | ---: | ---: | ---: |
| Separate independent concurrent commits | 0.836 | 0.258 ms | 7,159,112 / 12,581,784 |
| Shared independent concurrent commits | 1.004 | 0.243 ms | 7,331,044 / 12,680,136 |
| Shared coordinated block commit | 0.671 | 0.188 ms | 7,009,956 / 12,523,720 |

The corresponding process lifetime RSS peaks were 64,364,544, 65,642,496 and
65,642,496 bytes. Projection/checkpoint equality, injected rollback and
synthetic deletion/replay checks passed again. The change from the prior sample
illustrates why neither observation establishes a production speedup.
