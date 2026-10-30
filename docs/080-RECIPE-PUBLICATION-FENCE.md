# Recipe publication fence: design note

Status: the local publication guard and guard-before-store lock order have a
focused implementation and tests. They are not connected to a custom recipe
scheduler and do not establish a production or external-Core guarantee.
Scheduling, read-side fencing and reorg integration remain open.

An isolated local consumer adapter now verifies its target-height/hash token
against the selected local headers before and after planning, checks every
reused anchor under that same selected prefix, and rechecks it inside the
publication guard. It exclusively owns a fresh sparse consumer, so a generic
caller cannot seed its cache with an unauthenticated observation. Its order is
**consumer → selected headers → run state → recipe registry → recipe store**;
never enter the consumer while holding a later lock. This adapter is not wired
to the scheduler, and its result is an as-of local-header observation.

The token stays valid across chain extensions above the same target. A later
Live plan with a higher target currently opens a fresh consumer and rechecks
saved coverage. Safe transfer of an authenticated prefix across expanding
plans has not been implemented; Live efficiency and recovery remain open.

## Local selected headers and admission

Make the authority guard surround store locking, not the reverse. The intended
order for a guarded mutation is:

1. Acquire the app-owned selected-header read lock with
   `lockSelectedHeaders051()`, which first recovers any interrupted append.
2. Acquire `indexRunMu`, and check the current run and track admission.
3. Acquire `recipeRegistryMu` and validate the exact admitted immutable recipe
   revision and fingerprint, even if the registry head has since changed.
4. Invoke the guarded callback, which acquires `recipeStoreMutex`, checks the
   current head or planned revision, and atomically publishes the new head.
5. Release in reverse order after publication returns.

This order is **selected headers → run state → recipe registry → recipe store**. Audit every
new read, publication, deletion, settings and reconciliation path against this
order before enabling it. In particular, timeline code already takes a header
read lock before `indexRunSnapshot()` takes `indexRunMu`. Do not take the
header lock while holding `indexRunMu`, or call a store guard while holding
`recipeStoreMutex`. Go's writer-preferred `RWMutex` can otherwise deadlock even
when both header acquisitions are reads. Do not acquire `indexLiveControlMu`
inside a store callback: control and planning paths take that lock before
other work. Do not invoke `readSelectedHeader` or `currentChainAuthority`
while already holding the header lock; use `readHeaderAt` against the held
snapshot and compare its computed hash directly.

With both outer locks held, check cancellation; exact run ID and running or
waiting state; the current track's armed selection, admitted options and
effective start; finite Out or the admitted Live rule; the immutable recipe
fingerprint; and the selected block hash at the intended height. Validate all
other explicit evidence and consent required by that track. Ordinary progress
updates change the document revision, so they must not by themselves revoke a
valid admission. The guard fences a nonzero, monotonic per-track admission
generation, so unrelated track amendments do not invalidate it and an Off/On
cycle cannot reuse stale work. The scheduler must durably record both that
generation and the recipe ID, fingerprint and revision binding before this
guard can admit publication. Keep the locks until the callback completes, so a stop,
amendment or local header replacement linearizes wholly before or after the
durable head swap. A pause waiting for an already-entered commit can only be
acknowledged after that commit finishes.

The simple implementation holds both outer locks during file I/O. A shorter
critical section requires a separate staging and compare-and-swap design with
the same crash and concurrency guarantees; merely checking a revision and
releasing the lock before the head swap is insufficient. Mutex waits and I/O
must remain bounded and cancellation-aware where possible. No callback may
recursively call a locked store or reacquire an outer lock.

Read paths must also verify each returned anchor against a held selected-header
snapshot before presenting current-chain coverage. Reorg plans must pin both
the store revision and the selected-header snapshot across their pages, then
reconcile under the same authority. A changed revision or incompatible header
snapshot requires retry. Preserve unaffected sparse receipts and distinguish
an evaluated empty unit from an uncovered unit.

## External Bitcoin Core

`headerChainMu` protects Gateway's own header file, not the active chain inside
an independently running Bitcoin Core process. A Core `getblockhash` response
before or after a head swap is an observation, not a lock on Core: Core can
reorganize between that response and publication. A local mutex, an RPC retry,
or two equal RPC responses cannot create an atomic cross-process chain fence.

An initial implementation can fail closed unless Gateway has an app-owned
selected-header snapshot through the candidate height that agrees with the
Core-observed target. Its guarantee is then explicitly relative to that held
local snapshot; it is not a claim that Core cannot have changed independently.
Do not report a lagging or disagreeing local mirror as the current Core chain.
Core-only operation needs a separately designed observation protocol, including
as-of labeling, read gating, change detection and reconciliation, or a properly
synchronized app-owned authority mirror with an honest freshness boundary.
Full external atomicity would require cooperation from the external authority.

## Required concurrency evidence

- Race a blocked commit with pause, cancellation and track amendment in both
  orders; rejected work must not advance rows or coverage.
- Race a blocked commit with local header replacement, and reject a replacement
  that won before guard admission. Exercise timeline header → run locking with
  a waiting header writer to detect lock-order deadlocks.
- Check missing headers, interrupted append recovery, wrong block hash, stale
  recipe generation, expired admission, finite Out and cancellation before
  publication. Failures preserve the previous head.
- Change the store revision or selected snapshot while paginating a reorg
  plan; require retry, then remove only affected anchors. Verify empty receipts,
  unaffected sparse coverage and restart behavior.
- Simulate external Core changing during a commit to demonstrate that RPC
  observations are not an external lock; verify that any observation-based
  design does not silently serve stale results after a detected change.
