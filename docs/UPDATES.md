# Gateway updates

Gateway supports signed application updates and separately signed header delivery. The bundled Gateway channel provisions `https://gateway-updates.onrender.com` and its public verification key on first launch. Ordinary users need no publisher account, access token or private key. The configured address and mode are shown in **Settings → Updates**. The public service is live and hosted update acceptance passed, as recorded in [status](STATUS.md).

## Choose when updates happen

The Windows installer's dedicated **Updates** page offers:

| Mode | Behavior |
| --- | --- |
| Check automatically; let me install | Default for a fresh profile. Startup and periodic checks can offer updates; download and installation require user action. |
| Download and install automatically | Explicit opt-in. A newer approved signed package can download, install and restart Gateway automatically. |
| Manual | Application checks, downloads and installation require user action. |
| Keep existing update preferences | Preserves a configured profile's choice on a later installation. |

Periodic application checks run approximately every six hours. A manual check remains available in every mode. Preferences can be changed later in Settings. Automatic installation preserves the profile; it does not mean every future package is installed without signature, platform and version checks.

Outbound-network disable cancels checks and downloads. A previously verified staged update may still be installed offline while its signed authorization remains valid. Withdrawing automatic-install permission before the commit prevents an unattended restart; after commit, the authenticated apply/restart must finish. A failed automatic attempt does not create an endless retry/restart loop.

## Advanced source settings

The installer's expandable source section supports keeping the current source, selecting the bundled Gateway hosted service, or entering a custom publisher. The first choice preserves an existing publisher and uses the reviewed bundled default for an unconfigured profile.

A custom source requires an HTTPS **base URL**, an Ed25519 public key and explicit confirmation that the key was verified independently. Do not enter a private signing key. Editing the URL/key clears confirmation. Invalid or conflicting fields block installation before files are written. Explicit loopback IP HTTP addresses are reserved for local test publishers; ordinary remote publishers require HTTPS.

Back/Next retains entered values, and the review page shows the selected mode and source. Existing settings are not silently replaced merely by opening the installer. Explicit selections are persisted as one installer request and applied once when the profile is safely opened.

Changing publisher authority clears an existing restricted-distribution credential rather than forwarding it to another server. Source fields in an installer request contain only public trust information, never tokens or publisher secrets.

### Preference durability

Installer schema 2 binds mode, public source choice and a unique request ID. The runtime saves source, mode and the consumed ID together in one configuration write. Later launches cannot reapply a consumed request over Settings changes. Strict schema 1 mode-only requests remain supported for existing installations.

A conflicting source change waits while a verified stage or apply recovery exists. Unattended actions pause rather than applying a package under a different authority. Completing/cancelling the existing stage preserves its original trust context. An explicit valid Settings save can supersede the pending installer request. Per-key replay history survives authority changes.

## What a successful check means

The client verifies signed metadata, its independent public key, validity window, sequence, platform, package identity and digest. A metadata response cannot introduce its own trusted key. A successful check distinguishes the installed version, signed publisher version and whether a newer package exists for the current platform. An older publisher version must not downgrade the app.

A timeout or unavailable publisher is a failed check, not evidence that the app is current. The hosted service may take time to respond after inactivity. Bounded retry is limited to transient failures; signature and trust failures do not become successful retries.

Application update ZIPs omit the full header snapshot. Settings → Check now also checks the separate header feed in the background. Existing matching headers are extended; ahead or divergent history is preserved. See [header delivery](HEADER-BASELINE.md).

## Publisher operations

The publisher reads approved GitHub release artifacts, checks source/tag and artifact identities, and signs separate app/header manifests. Serving uses public verification material and any narrowly scoped download credential required by the release source. Private signing keys are never shipped to clients.

Signed metadata has an issue and expiry time, with a maximum validity of 31 days. Expiry stops acceptance of stale delivery information; it does not expire the installed application or the signing key. Renewal must preserve the approved release identity and advance valid metadata without approving arbitrary new code.

The GitHub workflow runs weekly on Mondays at 06:17 UTC. Its `gateway-update-renewal` environment is restricted to `main`, with signing and deployment-hook secrets provisioned. It renews only the committed approved release, extends each feed to 30 days and advances its independent sequence without changing app/header content. It does not choose a new application release automatically.

The public service obtains the exact approved release bundle directly from GitHub's release-download URL, avoiding anonymous REST rate limits. It still verifies the pinned signatures, expiry, release identity and every app/header byte before serving. Render receives public verification material, not the private signing key. The repository and approved release are public, so downloads need no GitHub credential.

Local publisher/renewal checks and hosted application acceptance passed. An isolated synthetic older client automatically installed the exact public 0.7.0 release and restarted with its profile preserved; a fresh 0.7.0 client checked both feeds without manual setup. Independent HTTPS verification downloaded both application archives and all 97 header chunks, both on initial publication and again after renewal at application sequence 3 and header sequence 2. These checks used a warmed service, so idle cold-start latency remains unmeasured.

The [manual commissioning run](https://github.com/Blockamoto/gateway/actions/runs/37816973175) of that weekly workflow passed, including promotion, Render deployment and comparison of both hosted manifests against the verified bundle. Application sequence **3** and header sequence **2** both expire at **2026-11-07 17:30:26 UTC**.

The successful manual run validates the workflow, but does not guarantee future scheduled execution. Revoked permissions, disabled workflows or hosting failures still require attention before expiry. No Codex or workstation schedule is required. Follow the [renewal operator guide](../packaging/update-publisher/RENEWAL.md), check the workflow-maintained [feed status](UPDATE-FEED-STATUS.json), and see [release acceptance](RELEASE-ACCEPTANCE-0.7.0.json) for the tested scope.
