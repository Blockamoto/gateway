# Approved-release feed renewal

The weekly `Renew approved Gateway update feeds` workflow renews the application
and header manifests for the release in `approved-release.json`. It never follows
GitHub's latest release and accepts no version input. Changing the approved
version is a separate release review. The exact `vVERSION` tag must resolve to
the source revision already signed into the selected bundle.

Both feeds receive 30 days of validity and advance their independent sequences.
Application archives and compressed header chunks remain byte-for-byte unchanged.
An idle client can fetch the current renewed feed when it returns; expiry still
fails closed when hosting or renewal is unavailable.

## One-time setup

1. Publish the approved version with its initial signed application/header
   bundle named `gateway-update-feed-vVERSION.zip`. Both feeds must name that
   same release. Commit its version in `approved-release.json` and confirm the
   channel URL/key in `assets/bootstrap/update-channels.json` match the tracked
   `assets/bootstrap/update-public-key.json`.
2. Create the GitHub environment **gateway-update-renewal**. Restrict deployment
   branches to **main before adding secrets**. Scheduled renewal cannot require
   a person to approve each environment deployment.
3. Provision `GATEWAY_UPDATE_SIGNING_KEY_JSON` with the approved publisher's
   private JSON key document and `GATEWAY_RENDER_DEPLOY_HOOK` with the dedicated
   updater service's deployment-hook URL. These are environment secrets. Keep
   the offline recovery copy of the signing identity. Rotate a disclosed hook.
4. Permit this workflow's short-lived `GITHUB_TOKEN` to write release assets and
   `docs/UPDATE-FEED-STATUS.json` on main. A main-branch rule blocking that receipt
   write makes the run fail and needs an explicit repository-policy decision;
   the workflow never force-pushes or weakens protection.
5. Configure the separate Render service with the public repository, approved
   release version, and tracked **public** key. Render never receives a signing
   key. Public GitHub downloads require no token. While the repository remains
   private, anonymous live delivery waits for its owner to make it public; the
   workflow itself can still access private assets using `GITHUB_TOKEN`.
   Anonymous startup downloads the exact approved `releases/download/vVERSION/`
   bundle directly, avoiding shared-IP REST API rate limits. It still verifies
   pinned signatures, expiry, release identity, and every app/header byte before
   serving. An explicitly supplied private-origin token retains the REST path.
6. Once live delivery is available, run the workflow manually on main and check
   that its receipt says `verified` and both hosted sequences/expiries match.
   The committed workflow then runs weekly. No Codex or workstation task is
   required.

The workflow uses pinned actions, no pull-request trigger, a default-branch
guard, and one concurrency group. Keep manual publisher operations outside an
active renewal run. The signing document exists briefly in an owner-only runner
temporary file outside the serving directory and is deleted after signing.
Only the sanitized receipt is uploaded as a workflow artifact.

## Recovery and failure reporting

The operator `restore -for-renewal` authenticates an expired bundle at its
original signed issue time. It does not relax signature, repository, tag,
archive, digest, or header checks. Expired restored feeds cannot be served or
exported until renewed. Ordinary `serve-release` never uses this exception.

Renewal uploads and hash-checks a candidate before replacing the fixed-name
asset. The previous asset remains available under a backup name. Interrupted
renames and lost success responses are recovered without replacing a successful
higher sequence with an older one. Candidates are named by base asset and both
sequences, authenticated again on retry, and checked to contain identical
approved content. Expired candidates or those with eight days or less remaining
are renewed at higher sequences before promotion. Empty GitHub upload
placeholders can be safely retried. Conflicting signed content fails for review.

After promotion the job triggers Render, then compares both live manifest byte
sequences to the freshly verified bundle. It records the result and keeps one
previous bundle for investigation. Do not manually roll back metadata sequences:
repair the approved content and publish a newly signed, higher-sequence bundle.

Failure uses the normal GitHub Actions failed-run status and account notification
settings; it does not post issues or send separate messages. If hosting fails,
the receipt identifies `published-awaiting-host`; rerun after resolving the
service problem. Monitor failed or disabled workflows before the 30-day expiry.

GitHub can disable scheduled workflows in public repositories after 60 days of
repository inactivity. Successful renewals commit the meaningful public
`docs/UPDATE-FEED-STATUS.json` receipt, providing an audit record and continuing
repository activity. This cannot recover a manually disabled workflow, revoked
permissions, prolonged failed runs, or a deleted environment. Restore those
settings and run the workflow manually; expired authentic bundles remain
recoverable. See [GitHub's schedule lifecycle documentation](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule).

## Local operator commands

Use an isolated private working directory and the independently provisioned
public key. These commands only prepare files; no network promotion is implied:

```text
update-publisher restore -for-renewal -bundle approved.zip -out work/feed -repository Blockamoto/gateway -version 0.7.0 -public assets/bootstrap/update-public-key.json
update-publisher renew-all -out work/feed -approve-release 0.7.0 -validity 720h -private /private/offline-key.json
update-publisher check -dir work/feed -public assets/bootstrap/update-public-key.json
update-publisher export -dir work/feed -out renewed.zip -public assets/bootstrap/update-public-key.json
```

Treat `renew-all`'s work directory as private staging, never as a concurrently
served distribution. Both inputs are verified before mutation; export only
after complete success. A failed operation cannot change the existing GitHub
bundle or hosted service.
