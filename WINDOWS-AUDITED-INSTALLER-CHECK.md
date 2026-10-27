# Draft: exact audited Windows installer check

A qualifying push to
`codex/windows-installer-check` will run the check, including a quiet install
and uninstall on a fresh GitHub-hosted runner. It does not publish or promote
any release, and must never run against a real user's account, profile,
registry, Start Menu, or installation.

The proposed check is restricted to a fresh GitHub-hosted Windows runner. It
reconstructs candidate `e439525617ed5ec742cbcda97aecfaa3a36ee7be` at its
original neutral source path. It fetches the public v0.7.7 header archive and
requires its pinned size and SHA-256, checks ZIP CRC and exact member names,
and checks both approved header and metadata digests and the header tip.
Public update channels, all four embedded Windows executables, generated header
metadata, and the original fixed GOPATH-off installer staging path are checked.
The rebuilt client is hash-gated before the build's bootstrap verification;
the installer and copied uninstaller are hash-gated immediately before each
execution. The audited installer hash is
`af8befc5701548e32014dcefce15535ba79f442c89d68ebdf6989431a512554a`.
The final digest gate is mandatory: differing toolchains, paths, line endings,
staging or other build inputs must produce a failed check, not a substitute
installer. Earlier candidate ad8b48c reconstruction passed on hosted runs 40456943290 and
40457787932; corrected e439525 requires a new hosted check.

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

Run 40457787932 observed the earlier ad8b48c uninstaller remove itself after
33.625 seconds on a fresh hosted runner. The original 30-second acceptance check failed. The bounded acceptance budget
is now 90 seconds to allow cold PowerShell initialization; remaining files still fail
the check. The corrected e439525 pins include the production-only companion payload.
Installation must contain exactly the nine released-updater-compatible files,
with source-matching hashes, and uninstall must remove that directory.

For diagnostics, the check samples process IDs and records PowerShell children
of its own uninstaller. The check retains the 30-second observation and waits up to 90 seconds for
self-deletion, then requires all cleanup and preservation assertions to pass.
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
