# Exact audited Linux client: offline hosted check

This separate tooling branch proposes a fail-closed reproducibility experiment.
It checks out full branch history, verifies that exact release source 6c33011 is
an ancestor, and archives it locally into the Windows-derived GOPATH-off package
identity. It uses Go 1.27.1 and the release build flags, and
checks the public embedded update descriptor. It executes the rebuilt client
only if its full SHA-256 equals the independently audited Linux candidate
`64a9baddba41f0e0233c62de3b21b34ce5bcac80b579055e5f16d9ad3e9727c2`.
Cross-platform reproduction is not yet established; a mismatch is a failed
check, not permission to execute a merely equivalent binary.

After that gate, tests run as a non-root user with no-new-privileges and without
inherited credentials in a Linux network namespace containing only loopback.
This is network isolation, not an operating-system filesystem sandbox. They use newly generated genesis-only
profiles beneath runner temporary storage, exercise startup, shutdown, restart,
offline settings, and the existing compiled Bitmap API test. The workflow has
no protected environment, signing secret, artifact upload, deployment or
publication. GitHub checkout/setup actions use network and may use ephemeral
read-only action/runner tokens; the test subprocesses receive a clean env.
No existing user profile is copied or opened.

The historical header baseline is a package sidecar and is not embedded in the
client. This check uses only the genesis-only fixture; it does not validate the
969,480-header baseline, a populated index, live network behavior, performance,
release acceptance, or publication. Run an independently gated check if those
are needed. The workflow is local only and must not be pushed or dispatched
without the requested release coordination.
