# Timeline–engine contract experiment

This is a proposed logical boundary, **`gateway.workspace/1`**, between a
replaceable engine and the existing Gateway workspace. It is not a deployed API,
a replacement UI, or a claim that the 0.8 engine is wired to the 0.7.8 shell.
`adapter.mjs` is a small compatibility sketch for the current 0.7.8 wire shape;
it preserves the entire original document for fields not yet normalized. A
real adapter must cover the remaining existing explorer, index-specific
inspectors, settings, navigation and accessibility behavior before GUI parity.

Evidence was read from the separate 0.7.8 worktrees: `v0.7.8` at `efc5b13` and
`codex/index-performance-078` at `5514485`, the latter with unrelated ongoing
uncommitted export work. The performance branch's `index_status_poll.go`,
`index_run.go`, `index_run_admission.go`, `index_timeline.go`,
`ui/shell/index-transport.js`, `index-timeline.js` and `timeline-explorer.js`
are the primary evidence. The optional test reads a saved status from an owned
0.7.8 replay; it does not launch Gateway or modify either reference worktree.

## Minimum logical surface

- **Status and coverage.** Return definition and provider identity, current run
  revision, independent prepared choices and admitted run, selected-chain
  context, per-index progress/dependencies, errors, and per-track coverage.
  Distinguish committed records, Raw bodies actually retained, local archive or
  Core sources, and provider-reported hints. Keep exact intervals and gaps;
  preserve verification and coverage-enumeration completeness separately.
  `coverage_complete: true` can mean the coverage enumeration is complete while
  most chain heights remain uncovered. Unknown or partial is not an empty,
  authoritative range. Staged progress is separate from committed coverage.
- **Observation freshness.** Progress may combine a fresh run/job snapshot
  with older coverage details. Carry detail observation time, age and refresh
  state; do not present old coverage as freshly checked. Apply monotonic run
  revisions independently of detail age so a late response cannot revive an
  older run. A reorganization or incompatible stored snapshot must not be
  represented as current selected-chain evidence.
- **Choices and explicit work.** Persist Arm, Live, options and In/Out without
  starting a historical scan; distinguish a prepared selection from admitted
  work. Arm is meaningful consent: on-demand browsing can still write sparse
  facts for appropriately armed tracks, and Raw On can retain a browsed source
  body. Planning checks source, dependencies, required prefix/checkpoint,
  selected anchors and capability; no plan may silently fill a missing prefix.
  Starting is explicit. When a reviewed plan token is provided, stale review
  must be rejected. The existing 0.7.8 UI can also start with a request and no
  `plan_id`; its backend replans at Start, so a compatibility adapter cannot
  simply require a new token without changing that interaction.
- **Run lifecycle.** A run has an identity, immutable admitted range/options
  unless a separately admitted change succeeds, per-consumer progress and
  explicit pending/blocked/off states. Pause can report `pausing` before a safe
  boundary, then `paused`; Resume continues the saved admitted run, not a newly
  staged range. Cancel preserves committed records. Active Arm/Live changes use
  `run/amend` with `run_id`, `admission_revision` and consumer changes; report
  each change's effective boundary and blocked reason. No consumer's progress
  implies another's coverage.
- **Browse and errors.** An explicit on-demand block request may acquire a
  Bitcoin body from ordinary peers or an enabled local/Core source. It returns
  source and verification evidence with missing/unavailable distinct from
  negative proof. Fetch/decode and each permitted incidental retention/index
  write must recheck relevant consent and selected-chain context; display,
  status and configuration alone do not trigger a historical build. Preserve
  structured blocked-plan details and source/dependency/recovery problems.
  The existing API often emits `{error: message}` and a 409 body may also carry
  `plan`; a proposed machine-readable 0.8 error code must retain that message
  and plan for the legacy UI rather than infer codes from English.
- **Management boundary.** The current local API checks loopback remote/host,
  cross-site fetch and Origin, uses no-store, and bounds/validates JSON request
  bodies. GET browse handlers may have consented retention side effects; they
  cannot all be assumed read-only. Preserve these controls and update/restart
  drain semantics when exposing any new transport.

## Actual 0.7.8 mapping

| Existing wire action | Behavior to preserve |
| --- | --- |
| `GET /api/v1/index/status`, `/progress` | Detailed observation versus fast progress with explicitly aged coverage details. |
| `POST /api/v1/index/run/prepare` | Save prepared consumer/range choices; no historical run. |
| `POST /api/v1/index/run/plan` | Review current scope, dependencies and readiness. |
| `POST /api/v1/index/run/start` | `{request}` or `{request, plan_id}`; server rechecks prerequisites. |
| `POST /api/v1/index/run/amend` | `{run_id, admission_revision, consumers}`; admission at safe boundaries. |
| `POST /api/v1/index/run/control` | `{id, action}` with `pause`, `resume`, or `cancel`. |
| `GET /api/v1/index/block-map?block=…` | Explicitly request a block/map; current Arm/Raw policy can permit sparse writes. |

Other 0.7.8 operations—including header Live, settings, export, repair,
individual indexes and explorer navigation—remain outside this minimal sketch;
they are required compatibility work, not silently retired features.

## Executable checks and limits

From the checkout root:

```powershell
$env:GATEWAY_078_STATUS = (Resolve-Path 'build/baseline-078-owned-01/replay/00-baseline/status.json').Path
node --test experiments/ui-contract/adapter.test.mjs
```

Without the environment variable the optional saved-native-status test skips.
The seven checks passed against that owned 0.7.8 status. They test interval and
source separation, unknown coverage, staged versus committed display, stale run
revision handling, preservation of 0.7.8 command shapes and blocked-plan error
payloads, plus malformed data rejection. Command builders do not send HTTP and
cannot prove server-side execution or security. No current UI or backend was
modified.

Before treating the 0.8 interface as implemented, run a reusable integration
suite against a real engine and existing shell: verify Arm/browse/Raw consent,
plan and stale-token conflicts, amendment races, pause/restart/resume/cancel,
independent coverage during skew, missing source and incomplete evidence,
reorg and corrupt checkpoint behavior, cached detail ages, out-of-order polls,
local-only/Origin enforcement, and existing GUI flows. Include a bounded
responsiveness test with populated history and the UI polling. This experiment
does not yet validate those end-to-end behaviors.
