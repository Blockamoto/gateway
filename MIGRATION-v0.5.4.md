# Upgrade and removal: v0.5.3 to v0.5.4

## Non-destructive upgrade

1. Export diagnostics and back up the existing Gateway data directory before changing binaries. Close Gateway, its tray/background copy, and other old copies using that profile.
2. Replace the complete portable directory or install the v0.5.4 installer in place. Keep GatewayClient.exe, GatewayOnDemand.exe, GatewayNativeHost.exe and browser-companion together. Portable and installer payloads are byte-identical.
3. Start normally to continue the same settings, privacy choices and discovery ledger. New fields are additive. Stored direct negative evidence from v0.5.3 still enforces the recheck interval, even before another new check.
4. A new ownership marker and OS-level profile lock are added. A second independent v0.5.4 writer is refused; supported UI activation still forwards to the existing instance. A warning about ambiguous external ownership is not permission to erase those files.

The existing `discovery-ledger-v1.wal` resumes. No index rescan, Core mutation or forced clean test starts on upgrade. `bitcoin-peers.json` is imported only where there is no ledger to resume. An imported lower-bound attempt history is not an observed Gateway check. Historical first claims cannot be reconstructed where only an accumulated service mask was saved.

Older versions do not understand every v0.5.4 field or lock. Back up before downgrade; do not assume older writers preserve new provenance and recheck semantics. A backup and a copy of the matching old binaries are the reliable rollback path.

## Clean QA without deleting anything

`TestStandalone.cmd` uses `%LOCALAPPDATA%\GatewayClient-054-QA` and preserves it on subsequent launches. `TestFreshStandalone.cmd` creates a separate GUID-suffixed profile every time, leaving earlier tests and normal installed data untouched. Both start with Core/mounts disabled and without manual Gateway peers. Windows/browser integrations require explicit setup choices.

For the Bitcoin-only discovery acceptance run, use a truly fresh profile and do not add the serving Gateway. Inspect endpoint is a local read-only lookup and does not contaminate discovery. A server's own advertised endpoint is distinct from handing that address to the receiver.

## Explicit complete removal

An old installed uninstaller cannot gain the new choice merely because a separate portable copy was downloaded. First upgrade the installed application to v0.5.4, then use its Windows Installed Apps uninstaller.

Choose whether to retain data or review destructive removal. Keep is the default. For deletion, review every selected path, bytes/file counts and excluded paths; confirm only after backing up non-reconstructible work. Ordinary Gateway headers, blocks/cache, graph indexes, mounted-file locators, Satline/Ord work, discovery ledger, settings and saved provider credentials inside owned stores are covered. Known separately configured archives, custom stores and QA profiles are individually optional and default to no.

Core/Ord source files and wallets must never be selected. Unknown/ambiguous default state blocks a claimed fresh purge, rather than being deleted blindly or silently ignored. Unknown portable copies outside known/registered locations are not scanned or erased. User exports may remain by design; a retained export is not a restored peer database.

Removal stops the matching authenticated runtime, locks its approved stores and checks previewed files again. Locked/changed files or other failures report incomplete removal. Metadata is retained on deletion failure so a later reviewed retry can still establish ownership. Close all older versions first: they do not use the new lock.

Quiet uninstall keeps data unless both destructive flags are present. Administrative namespace removal can require UAC; the quiet mode does not silently request elevation. Use the graphical workflow for the first full Windows removal test. Browser extension approval/profile state is managed separately by the browser, not by deleting browser databases.

After successful selected-data removal, reinstall should start new setup and a new ledger, not import old peer history. New initialization can create a genesis entry and new Bitcoin bootstrap introductions; those are not remnants of the deleted profile. The shared Linux runtime test uses explicit offline isolation on its fresh restart so it can distinguish these effects.

Native Windows installer, locked-file/junction behavior and complete removal/reinstall still require a real Windows acceptance run. The automated Linux safety and reset fixtures are not that test.
