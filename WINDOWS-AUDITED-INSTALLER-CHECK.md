# Draft: exact audited Windows installer check

This tooling branch is local and has not been pushed. A qualifying push to
`codex/windows-installer-check` will run the check, including a quiet install
and uninstall on a fresh GitHub-hosted runner. It does not publish or promote
any release, and must never run against a real user's account, profile,
registry, Start Menu, or installation.

The proposed check is restricted to a fresh GitHub-hosted Windows runner. It
reconstructs candidate `ad8b48c4a1335e0c992ad21507d9a5553f75ee28` at its
original neutral source path. It fetches the public v0.7.7 header archive and
requires its pinned size and SHA-256, checks ZIP CRC and exact member names,
and checks both approved header and metadata digests and the header tip.
Public update channels, all four embedded Windows executables, generated header
metadata, and the original fixed GOPATH-off installer staging path are checked.
The rebuilt client is hash-gated before the build's bootstrap verification;
the installer and copied uninstaller are hash-gated immediately before each
execution. The audited installer hash is
`c8e3d7b90b624d55e2270d5411ec498b2ffebc52a0277d86585ea7df2ea76234`.
The final digest gate is mandatory: differing toolchains, paths, line endings,
staging or other build inputs must produce a failed check, not a substitute
installer. Byte-for-byte reconstruction on the hosted runner is unproven.

Before installation, the test rejects pre-existing Gateway uninstall,
startup, protocol and browser-native-host registrations and the runner's real
Start Menu shortcut. It creates fresh app-data, temp and install directories,
uses the dedicated hosted user's HKCU, and invokes quiet CLI installation with
startup, protocol, browser setup and launch disabled. Installation still writes
the hosted user's uninstall registration and a shortcut under the disposable
APPDATA. The check validates the registration target and the shortcut's stored
local LinkInfo target with a bounded binary parser; it does not resolve alternate
IDList targets through the Windows shell. It validates the installed executable and header
digests, then performs quiet uninstall with no purge flags. It waits for delayed
self-deletion and requires the test registration and shortcut to be removed and
synthetic application/profile sentinel files to be unchanged. The ephemeral VM
is discarded after the job; no generic local HKCU sandbox is assumed.

For diagnostics, the check samples process IDs and records PowerShell children
of its own uninstaller. If the executable remains after 30 seconds, the check
continues observation up to 120 seconds but still fails the 30-second assertion.
It cannot capture the audited child's discarded standard error, and a missed
sample does not prove that no child was started. Findings are in the job log.

The work is not network-sandboxed. Checkout, tool setup and the public header
download use network; the test does not launch setup or enable peer networking.
It does not test upgrades, existing installations, existing user profiles,
GUI choices, browser approval, NRPT, real network use, SmartScreen, antivirus,
or full release acceptance. A failed build or hash mismatch is inconclusive;
never weaken the digest check to make it pass.

The non-installing guard suite uses synthetic data and mocked subprocesses:

```powershell
python scripts/test-audited-windows-installer-guards.py -v
```

No binary, archive or profile is uploaded. On success only a small sanitized
result may be retained as a workflow artifact. No protected secrets are used.
