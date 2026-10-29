# Gateway 0.7.9 indexing workflow proposal

The new Start flow reviews the armed tracks and an explicit In/Out window against
the current selected chain, compatible committed coverage and each track's
required origin or next checkpoint. Group equal candidate heights and show the
tracks affected. For each candidate, report the actual bounds and whether each
track is ready, starts later, already covered, or needs earlier state. A changed
range or selection requires explicit review; Resume retains its admitted scope.
Changes staged during a run apply to a later Start. The older immediate
amendment endpoint is disabled until a reviewed active-revision flow exists;
stop and review a new Start to change the active scope.

Execution makes an ordered sweep of ready missing work using the shared verified
block source and decode. Compatible committed intervals are reused, holes remain
missing, and a contiguous format that cannot fill a hole is reported rather than
silently rebuilt. Complete-prefix state starts at genesis or resumes an exact
compatible checkpoint. Out is a hard cap. Blocked dependencies retain their owed
work while independent ready producers continue. Staged/source progress remains
distinct from committed coverage.

The current read-only review checks at most 4,096 saved sparse Historical
Address blocks individually against the selected chain. A larger reuse review
is reported as unverified; review smaller windows rather than treating a
highest saved height as proof. Resumable large-range verification is not yet
implemented.

Districts and Parcel discovery form a dependent group. Admission binds a fixed
compatible complete District ceiling, capped by Out. A lagging Parcel discovery
frontier catches up to that ceiling while District publication is held,
including incidental publication. Once aligned, each District outcome precedes
the authenticated Parcel candidate scan for that block; the pair does not move
on until the scan is durable. Other tracks may advance. With Districts off,
finite catch-up ends at available complete District coverage.

Discovery and validation are separate durable frontiers. A verified source
block can yield provisional claims and an ordered revalidation backlog without
fetching ancestry or starting prerequisites. Missing proof remains pending and
is visibly labelled not yet validated. Provisional claims never reserve a
position, establish final winners, enter canonical exports or count as verified
children. Earlier unresolved contenders prevent later candidates becoming final
winners. Relevant new committed evidence triggers ordered revalidation under the
original route, range and finite allowance; unchanged evidence does not cause a
retry loop. Reorgs and interpretation changes invalidate the affected state.
The user sees discovery coverage separately from validation completeness and can
distinguish rejected claims from unresolved ones. Live Parcel following remains
unavailable until separately implemented and verified.
When District Live is selected with a finite Parcel run, the paired District
work is capped at that Parcel run's reviewed finite boundary; this does not
imply that Parcel follows later blocks.
Raw and TX ID Locator evidence can advance by reviewed phases. The current IO
Link audit requires the complete finite admitted horizon; it does not yet reuse
an authenticated incremental audit prefix.
The current retry signal follows relevant committed route-prefix and District
changes. Arrival of previously unavailable source bytes alone is not tracked
precisely; review a new validation attempt in that case. Discovery reorg
reconciliation is bounded to 4,096 checkpoints per attempt and reports a deeper
rewind for explicit recovery. Discovery totals are cumulative observations,
not a count of claims still awaiting validation.

Arm notices derive from actual dependencies and coverage: Current Addresses can
offer its UTXO prerequisite, Historical Addresses retains its distinct rules,
and Parcels explains District and alternative parent-evidence routes. Arming and
review do not start work. Dependency panels preserve scroll and focus across
progress refreshes. Verify the planner, scheduler, all publication paths and
browser behavior with focused restart, gap, mismatch and blocked-evidence tests;
do not claim full-chain validation from synthetic fixtures.
