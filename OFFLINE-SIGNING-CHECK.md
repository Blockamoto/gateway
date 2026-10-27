# Offline signing checks on a separate tooling branch

This isolated branch contains the otherwise inactive signing draft solely to run
synthetic, offline tests on an ephemeral Ubuntu runner. It does not enable the
protected release-signing workflow and does not sign or publish a release.

The push-triggered workflow `signing-offline-check.yml` requests only read-only
repository permission, no protected environment, no configured secrets, no
publish or upload step, and does not persist checkout credentials. GitHub may
still supply ephemeral runner/action tokens to checkout and setup actions; these
are not the protected signing key. Every test and build is launched with an
empty environment and a narrow allowlist of nonsecret paths and offline Go
settings; their subprocesses inherit that environment or a synthetic fixture
environment. The POSIX gate invokes
exactly the real permission-rejection test and fails if it skips. The full fake
origin suite and a synthetic-key integration with the real local publisher run
before the job completes. Package checkout and tool installation do use network.
The test fixtures replace production origin requests and use local files, but
the runner has network access and this workflow does not enforce an OS-level
network sandbox. Go module proxy and checksum lookups are disabled for the build.

The branch starts at release source 6c33011a9cb3878d6246031f23938ca11ec5ba81.
The branch and its four new scripts plus this workflow are local only.
If separately authorized to run, push only `codex/signing-offline-check` to the
intended GitHub remote; the push itself triggers the workflow. Inspect the
job log for `Required POSIX permission rejection passed without skips`, the full
unit suite, and the real synthetic-key operator integration. Do not merge or
promote this branch as a release signing workflow. Final protected release pins
remain empty and their checks continue to fail closed.
