# Isolated 0.7.8 TX and District baseline

`baseline_078.py` prepares an owned copy of the existing real-block fixture and
0.7.8 candidate client, then can run the copied client against a new disposable
profile. It only allows output beneath this checkout's ignored `build/` folder,
refuses existing replay profiles and directs the owned subprocess's temporary
files there. Source paths are read-only. The profile config disables Bitcoin
networking, Core, serving and Raw retention; the existing 0.7.8 harness starts
its copied executable with an explicit profile and loopback endpoint, then
shuts down the owned process. No installer, existing profile, or running
preview is used. Preparation does not start the executable.

The inputs are pinned by SHA-256: client
`a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137`,
reference harness `23f325a018cac636e09e7e95112f620e7f360a98ccda63c0f7861dd3e136edb4`,
selected manifest `d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a`,
and headers `534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155`.
The executable is the earlier *single-scan candidate* reported in the 0.7.8
performance evidence; its exact source revision has not been independently
established here. The reference worktree was clean at `5514485` during input
inspection, which does not by itself establish the binary's build revision.

Preparation checks all copied block bytes against the pinned manifest, their
selected headers and linkage, claimed proof of work and transaction Merkle
roots. These independent preparation checks do not establish full Bitcoin
consensus or witness validity. The actual 0.7.8 app must then acquire and verify
the staged bodies with its own code. The copied native harness arms the full TX
locator and Standard Bitmap Districts for 32 blocks at heights 792435–792466;
it checks full transaction positions, native per-height commitment chains,
District outcomes, coverage, Raw-off behavior and unchanged source archive.
The 32 blocks contain 64,767 transactions and a small number of District
winners, so this is not a representative heavily populated District tree.

The run times admission to durable completion observed via 200 ms polling;
startup, setup and three post-run status samples are separately recorded.
The fresh profile has no thousands of earlier TX browse points, no browser
rendering, no peer fetches, and no dynamic reorganization or crash injection.
The operating-system cache and other machine load are uncontrolled. Its real
TX locator and Bitmap semantics, parser, durability and input costs differ
materially from the Python/SQLite experiments: do not compare elapsed times as
an engine or database speedup.

To prepare a fresh copy on this machine:

```powershell
python experiments/storage/baseline_078.py prepare `
  --out build/baseline-078-owned-01 `
  --manifest C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/qa-current/baseline/manifest.json `
  --archive C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/archive `
  --headers C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-078/candidate-500/profile/headers/headers.bin `
  --binary C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/build/sustained-candidate/GatewayClient.exe `
  --harness C:/Users/camer/.codex/worktrees/index-performance-078/Gateway/scripts/benchmark-index-replay.py
python experiments/storage/test_baseline_078.py
```

Once a timing window has been coordinated, run the prepared copy exactly once:

```powershell
python experiments/storage/baseline_078.py run --out build/baseline-078-owned-01
```

Use a new output path for another run. `baseline-result.json` and detailed
native replay evidence are kept in the ignored build folder. A failed or
interrupted run is not automatically replaced or treated as a passing sample.

## Owned replay observation, 2026-10-26

The prepared client completed one fresh-profile TX+District run in **25.743 s**
from Start to observed durable completion; application startup took 6.761 s
outside that timer. The job read and decoded all 32 verified source blocks and
extracted inscriptions 32 times, covering 64,767 transaction positions. The
native harness passed its exact per-height commitments, coverage, TX positions,
District outcome and Raw-off checks; the staged archive was unchanged.

Windows whole-process counters during the timed interval reported 52,804,203
read bytes and 18,583,267 write bytes. The process lifetime peak working set
sampled by the harness was 229,105,664 bytes and includes startup. Three full
status calls after the timer took 0.354, 0.343 and 0.242 seconds. These process
I/O counters are logical operations, not physical device transfers or flush
counts; they are not evidence of GUI rendering responsiveness.

Another agent reported a test compilation stopping as the timing window began,
so initial CPU/I/O contention may have affected this observation. OS cache and
other system load were not controlled. The prior test profile was fresh and
not populated with the synthetic 3,000-point status history used in a different
0.7.8 diagnostic. This single observation is not directly comparable with the
other experiments, and does not establish a speedup.
