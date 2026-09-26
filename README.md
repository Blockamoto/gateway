# Gateway Client v0.5.4-repair.1
## Discovery and session repair candidate

This candidate repairs the known discovery and session defects found in the v0.5.4 audit: premature scout completion, failed-announcement cooldowns, stale automatic public-address evidence, request-timeout disconnects, inbound session asymmetry and blocked peer recovery. It also repairs Windows header rollback and public-data boundary defects. See [repair notes](docs/discovery-repair-candidate.md) for scope, data compatibility and validation. No Gateway bootstrap service or bundled Gateway endpoint list is added.

This is an unsigned test candidate. Native Windows installation, stock-Core address propagation and unrestricted public Gateway discovery remain separate acceptance gates. The [repair test record](docs/audits/2026-09-26/discovery-repair-tests.json) supersedes the historical v0.5.4 verification for these repairs.

## Start on Windows

Extract `gateway-client-v0.5.4-repair.1-windows-amd64.zip`. On the Core-backed publishing laptop, run `GatewayOnDemand.exe` and configure Core and deliberate serving. For an isolated receiving-client test, run `TestStandalone.cmd`. It uses `%LOCALAPPDATA%\GatewayClient-054-QA`, without borrowing your ordinary Gateway profile. Its initial settings disable Core RPC, mounted Core files, inbound serving, public sharing and Satline publication. No manual peer or Gateway endpoint is preloaded. Ordinary Bitcoin bootstrap remains enabled.

Running the same launcher again resumes that profile and its discovery ledger. It does not reset your checks. Existing settings in that QA profile are preserved, so inspect them before claiming a clean standalone test.

`TestFreshStandalone.cmd` creates an additional uniquely named QA profile without deleting or renaming any previous one. It prints the new directory and writes a `ResumeTest.cmd` there. That resume launcher uses this extracted executable's full path, so keep the portable directory in place. PowerShell is used only to generate the fresh session identifier. Neither launcher installs persistent Windows/browser integrations by itself. Actions explicitly selected in guided setup remain your choice.

For ordinary use, `GatewayOnDemand.exe` opens the client. The ZIP also includes `gateway-client-v0.5.4-repair.1-installer.exe`. Close the old Gateway process and keep a separate test profile when upgrading. Keep all three Windows executables and the Browser Companion folder together. Legacy graph files are preserved; public graph schema 2 requires rebuilding from public blocks.

## What to watch

Open **Discovery**. The leading counters show Gateway hints seen, hints attempted, direct checks and historical compatibility. The stage strip also shows current negotiated connections and useful providers. The lower Session coverage section retains ordinary endpoint and repeated-observation totals. Recorded endpoints may exceed the 4,096-entry working-queue limit. The full record is the discovery ledger, not that queue.

**Session coverage** distinguishes observations, repeats, imports, attempts, suppliers and actual Bitcoin handshakes. Its mutually exclusive current-work categories describe connected, checking, queued, deferred, cooling down and excluded records. An endpoint can be retained without being eligible to dial.

**Gateway candidate checks** is immediately below the primary progress view, above storage statistics. Use its filters and Next/Previous pages to reach every retained candidate, not just the first 80. Details show exact first/latest claim masks and suppliers where known, evidence quality, direct outcomes, transport retry and independent Gateway recheck time. Imported attempts lacking direct-check evidence remain explicitly unknown. A failed socket or repeated rumour does not erase the 30-minute direct-negative recheck gate.

**Inspect endpoint** checks only the local ledger. Entering your serving Gateway's expected endpoint there neither adds it as a peer nor opens a connection. This lets you distinguish "never learned" from "learned but deferred", "unreachable", "incompatible" and "service unavailable" without contaminating the discovery test.

**Retry eligible peers** retains outcomes, cooldowns and rate limits. Pause/Resume controls scouting independently of existing useful Bitcoin transport. A normal restart retains the session identifier and committed history. An interrupted attempt is not counted as a successful connection.

**Export diagnostics** downloads server-produced JSON without JavaScript rounding of 64-bit service masks. It remains a compact snapshot with a labelled 80-candidate detail sample; **Export full ledger** includes all recorded endpoint rows as newline-delimited JSON. Exports may reveal addresses, introductions and local probe history. Review before sharing. They are deliberate local downloads, not peer publication.

## Two-machine acceptance

On a Core-backed serving machine, use its existing provider and opt-in serving configuration. Discovery must show an honest advertisement and a reachable Gateway endpoint. Core's RPC listener and Core's Bitcoin listener are not Gateway's listener. Do not expose RPC port 8332.

On the receiving machine, use a genuinely fresh isolated profile. Do not add the serving Gateway manually. Its endpoint must be learned through ordinary Bitcoin address exchange. Observe the recorded introduction, completed Gateway negotiation, declared BOD capability, and a useful request. Mere bit-25 hints are not success.

After substantial crawling, export diagnostics, restart the receiver using the same launcher, and compare its session ID, recorded endpoints, attempt history and next retries. Live connections naturally change; cumulative unique records must not disappear because the working queue changes.

A failure still needs diagnosis: no introduction is an advertisement/propagation issue; retained-but-unattempted is a scheduling or policy issue; a failed connection is a reachability issue; protocol and service failures have their own records. Fixing the ledger cannot guarantee that a serving endpoint is publicly reachable.

## Resource policy

Defaults are **250,000 endpoint records** and a **512 MiB journal budget**, independent of the working queue and connection caps. The ledger is exact and memory-resident with a durable local journal; it is not a disk-only database. Record sizes and runtime overhead vary. This release adds claim provenance and cooldown fields, so use the new measurements in VERIFICATION-v0.5.4.md rather than previous-version memory estimates.

History budget controls allow up to 500,000 records and 1,024 MiB. Reaching a limit pauses new discovery intake rather than silently evicting history. Already arriving records that cannot be retained are counted explicitly, and completeness is no longer claimed. Known records can still receive outcome updates. A safety budget is not an unlimited-memory mode.

The journal uses checksummed committed transactions. Compaction needs temporary room for one additional checkpoint, potentially near the journal budget. In-flight updates can add bounded overhead around a threshold. A failed write stops new dials; inspect the error and export any pending in-memory records before correcting storage and restarting. Restart restores committed transactions, not a promise that uncommitted RAM survived a power cut. Do not run concurrent independent processes against the same data directory.

## Existing data and scope

v0.5.2's retained `bitcoin-peers.json` imports once when no new ledger exists. Its known attempts and cooldowns are retained as far as that old format supports them. Earlier evicted endpoints cannot be reconstructed, and imported attempt counts may be lower bounds. The old file remains untouched.

The retained local journal is `discovery-ledger-v1.wal`; v0.5.3 resumes without reimporting or clearing it. Normal upgrades do not replace Bitcoin block storage, rebuild global Ord/sat indexes, modify Core databases, alter sharing choices or delete cached blockchain data. Explicit uninstall removal is a separate destructive choice. Existing logo, guided setup and browser work are retained, not expanded into another project.

## Stable advertising and Gateway connections

Automatic public-IP selection uses fresh observations from separate Bitcoin peer network groups. A supported choice stays stable across polls; expired evidence is not treated as live. Conflicting observations remain visible. A replacement must stabilize, and each recipient is rate-limited even when the selected endpoint changes or its session reconnects. Ordinary short-lived scouts do not repeatedly broadcast advertisements. An outgoing IP observation is NOT proof that the incoming Gateway port is reachable.

A small bounded set of negotiated Gateway links stays connected without requiring a useful non-genesis BOD sample. The view says "Gateway connected. Seeking data providers." while discovery continues for missing capabilities. Useful block proof remains a separate milestone with the existing integrity checks. A leading genesis-only range no longer hides later non-genesis ranges during sample selection.

Specific bit-25 advertisements get priority over all-bits/unknown-aggregate hints, but weak hints retain a bounded share of candidate opportunities. All addresses remain in the exact ledger. The global dial policy is deliberately unchanged: one new dial token per five seconds, burst at most three, with bounded live connections. This release removes wasted work; it does not claim an unmeasured speed increase.

## Remove Gateway and start fresh

The Windows uninstaller now asks whether to KEEP data (default) or DELETE Gateway data and settings. Deletion requires a final path/file-count/size preview and confirmation. Review it carefully: saved jobs, credentials and non-reconstructible work inside owned stores are included. Export anything you want to keep first.

Known installed and legacy stores are reviewed. Recognized separate QA/custom profiles and configured separate archives are offered independently, with NO as the default. Archives are limited to matching Gateway block/receipt pairs. Unknown directories are not erased by name. Ambiguous existing state at a normal installed location blocks a declared fresh purge rather than being silently skipped. Older registered Windows uninstallers lack the new choice; first install v0.5.4 in place, then use its uninstaller to request the clean removal.

Core/Ord source databases, chainstate, wallets, mounted files, links/junction targets and recognized user export paths are excluded. Gateway-owned indexes over those source files can be removed. The cleanup engine checks unchanged previewed files, holds an exclusive profile lock and stops the matching runtime through its authenticated loopback API. Locked files or changed paths stop or report partial removal; they are not called a successful clean start. Close ALL older Gateway copies first, since older binaries do not honor the new profile lock.

Normal silent uninstall keeps data. Destructive silent mode requires BOTH `--purge-data --confirm-purge` alongside `--uninstall --quiet`; separately selected recognized profiles may be supplied with repeated `--purge-profile "absolute path"`. Use the graphical preview for the first cleanup test. The removal report is saved locally and can contain paths; review before sharing.

The shared cleanup engine and real Linux runtime reset are tested. Native Windows installation/uninstall and reparse/locked-file behavior still need Windows acceptance. Do not equate cross-compilation with that test.

## Linux and source

The Linux archive contains `gateway-client`. Run `./gateway-client -data /your/chosen/gateway-data -no-open` for a local API process, or omit `-no-open` for the normal browser launch. A data path is local application storage, never a Core wallet path. The source archive and `BUILDING.md` provide reproducible build commands. Current release evidence is versioned separately from older historical notes.
