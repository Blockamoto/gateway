Gateway Client native Windows GUI installer source

Build with scripts/build-release.py. Same payload as portable.
Normal invocation is graphical. --cli installs with explicit command-line
defaults; --no-startup, --no-god, --no-bitcoin, --no-launch and
--no-browser-companion-setup customize it. --dir overrides the install folder.
--update-mode=keep, notify, automatic, or manual chooses update behavior on the
next launch. The default is keep for existing installations/profiles, otherwise
notify. The GUI has a separate Updates page with the same options, followed by
a review before installation. Back and Next preserve the selected choices.
Gateway's pinned Render channel is ready from first launch. Notify checks
automatically but asks before download/installation; automatic also installs;
manual checks only when asked. An explicit mode selection applies once to an
existing profile, preserving publisher trust and accepted update history. Keep
retains existing settings. Later Settings changes survive subsequent launches.

The Updates page has a keyboard-accessible Advanced: update source section.
Keep current source preserves an existing publisher; an unconfigured profile
uses Gateway's bundled, pinned hosted channel. Gateway hosted service explicitly
selects that bundled channel. Custom publisher accepts an HTTPS address and a
base64 Ed25519 public key, with explicit confirmation that the key was verified
outside the update feed. Editing either value clears that confirmation. The
installer never accepts a publisher access credential; add one later in
Settings > Updates when needed.

CLI equivalents are --update-source=keep (default), bundled, or manual.
Manual additionally requires --update-publisher-url, --update-public-key and
--update-key-confirmed. HTTPS is required except for an explicit loopback IP
using HTTP for local testing. Invalid or conflicting source settings are
rejected before any installation writes. The schema-2 install preference records
only the mode, public source choice and one-shot request ID. Runtime validation
applies the selection once while preserving trusted-key rollback history.

--uninstall opens the uninstaller; --uninstall --quiet is non-interactive.
Browser approval remains per profile. Automatic Core reclamation is locked.
