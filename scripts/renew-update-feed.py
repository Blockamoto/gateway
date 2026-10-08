#!/usr/bin/env python3
"""Renew the two signed feeds for one committed approved release.

Only the GitHub default-branch environment may provide signing/deploy secrets.
No latest lookup, arbitrary version option, persistent origin token, or signer
on Render. The Go operator tool authenticates inputs and every unchanged asset.
"""
import argparse
import base64
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "Blockamoto/gateway"
API = "https://api.github.com/repos/" + REPOSITORY
MAX_BUNDLE = 256 << 20
MAX_METADATA = 2 << 20


class RenewalError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise RenewalError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


def request(url, *, method="GET", token="", data=None, binary=False, limit=MAX_METADATA):
    """Bounded HTTPS only. Credentials never follow asset/CDN redirects."""
    opener = urllib.request.build_opener(NoRedirect)
    for attempt in range(6):
        parsed = urllib.parse.urlsplit(url)
        require(parsed.scheme == "https" and parsed.username is None and parsed.password is None,
                "Network request requires HTTPS without user information")
        headers = {"Accept": "application/octet-stream" if binary else "application/vnd.github+json",
                   "User-Agent": "Gateway-approved-renewal", "X-GitHub-Api-Version": "2022-11-28"}
        if token:
            require(parsed.hostname in {"api.github.com", "uploads.github.com"}, "Credential destination rejected")
            headers["Authorization"] = "Bearer " + token
        if data is not None:
            headers["Content-Type"] = "application/zip" if binary else "application/json"
        try:
            with opener.open(urllib.request.Request(url, data=data, headers=headers, method=method), timeout=120) as response:
                raw = response.read(limit + 1)
                require(len(raw) <= limit, "Network response exceeds its limit")
                return raw
        except urllib.error.HTTPError as error:
            if method == "GET" and binary and error.code in {301, 302, 303, 307, 308}:
                target = urllib.parse.urljoin(url, error.headers.get("Location", ""))
                host = urllib.parse.urlsplit(target).hostname
                require(host in {"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"},
                        "Unapproved release download redirect")
                url, token = target, ""
                continue
            # Never include URL/body: deployment hooks contain bearer secrets.
            raise RenewalError("Network request failed with HTTP " + str(error.code)) from None
        except (OSError, urllib.error.URLError):
            raise RenewalError("Network request failed or timed out") from None
    raise RenewalError("Too many release download redirects")


class GitHub:
    def __init__(self, token):
        self.token = token

    def api(self, path, method="GET", value=None):
        raw = request(API + path, method=method, token=self.token,
                      data=None if value is None else json.dumps(value).encode())
        return json.loads(raw) if raw else None

    def release(self, version):
        release = self.api("/releases/tags/v" + version)
        require(not release.get("draft") and release.get("tag_name") == "v" + version and
                release.get("html_url") == "https://github.com/" + REPOSITORY + "/releases/tag/v" + version,
                "Approved release is missing, draft, or mismatched")
        # List assets independently: release JSON can truncate a growing list.
        assets = []
        for page in range(1, 11):
            batch = self.api(f"/releases/{release['id']}/assets?per_page=100&page={page}")
            assets.extend(batch)
            if len(batch) < 100:
                break
        else:
            raise RenewalError("Too many release assets")
        names = [a["name"] for a in assets]
        require(len(names) == len(set(names)), "Duplicate release asset names")
        release["assets"] = assets
        return release

    def tag_revision(self, version):
        obj = self.api("/git/ref/tags/v" + version)["object"]
        for _ in range(5):
            require(re.fullmatch(r"[0-9a-f]{40}", obj.get("sha", "")), "Invalid tag object")
            if obj.get("type") == "commit":
                return obj["sha"]
            require(obj.get("type") == "tag", "Release tag does not name a commit")
            obj = self.api("/git/tags/" + obj["sha"])["object"]
        raise RenewalError("Release tag nesting exceeds limit")

    def download(self, asset):
        require(isinstance(asset.get("size"), int) and 0 < asset["size"] <= MAX_BUNDLE and asset.get("id", 0) > 0,
                "Invalid release bundle size or identity")
        raw = request(API + f"/releases/assets/{asset['id']}", token=self.token, binary=True, limit=MAX_BUNDLE)
        require(len(raw) == asset["size"] and asset.get("digest") == "sha256:" + digest(raw),
                "GitHub asset digest or size mismatch")
        return raw

    def upload(self, release_id, name, raw):
        url = f"https://uploads.github.com/repos/{REPOSITORY}/releases/{release_id}/assets?" + urllib.parse.urlencode({"name": name})
        asset = json.loads(request(url, method="POST", token=self.token, data=raw, binary=True))
        require(asset.get("name") == name and asset.get("size") == len(raw) and asset.get("digest") == "sha256:" + digest(raw),
                "Uploaded candidate failed GitHub digest verification")
        return asset

    def rename(self, asset, name):
        result = self.api(f"/releases/assets/{asset['id']}", "PATCH", {"name": name})
        require(result.get("id") == asset["id"] and result.get("name") == name and
                result.get("digest") == asset["digest"] and result.get("size") == asset["size"],
                "Release asset rename verification failed")
        return result


def load_approval(root):
    approved = json.loads((root / "packaging/update-publisher/approved-release.json").read_text())
    require(set(approved) == {"schema", "repository", "version", "channel"} and approved["schema"] == 1 and
            approved["repository"] == REPOSITORY and re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", approved["version"]),
            "Committed release approval is invalid")
    descriptor = json.loads((root / "assets/bootstrap/update-channels.json").read_text())
    channels = [c for c in descriptor["channels"] if c["id"] == approved["channel"]]
    require(len(channels) == 1 and descriptor.get("schema") == 1, "Approved channel is missing or ambiguous")
    channel = channels[0]
    key = json.loads((root / "assets/bootstrap/update-public-key.json").read_text())
    require(channel["trusted_key"] == key, "Bundled channel and pinned public key disagree")
    url = urllib.parse.urlsplit(channel["publisher_url"])
    require(url.scheme == "https" and url.hostname and not url.username and not url.password and
            not url.query and not url.fragment and "?" not in channel["publisher_url"], "Invalid approved publisher address")
    return approved, channel


def selected_assets(release, version):
    """Recover an interrupted rename without following any other version."""
    name = "gateway-update-feed-v" + version + ".zip"
    assets = {a["name"]: a for a in release["assets"]}
    if name in assets:
        return name, assets[name], False
    prefix = "gateway-update-feed-v" + version
    pairs = []
    for asset in release["assets"]:
        match = re.fullmatch(re.escape(prefix) + r"-previous-([1-9][0-9]*)\.zip", asset["name"])
        if match and asset["id"] == int(match[1]):
            for candidate in assets:
                pending = re.fullmatch(re.escape(prefix + "-pending-" + match[1]) + r"-([1-9][0-9]*)-([1-9][0-9]*)\.zip", candidate)
                if pending:
                    pairs.append((int(pending[1]), asset))
    require(pairs, "Canonical bundle missing; no interrupted renewal to recover")
    highest = max(sequence for sequence, _ in pairs)
    newest = {asset["id"]: asset for sequence, asset in pairs if sequence == highest}
    require(len(newest) == 1, "Ambiguous interrupted renewal")
    # Filenames only locate the candidate. The caller authenticates every
    # candidate sequence and exact approved content before it can be promoted.
    return name, next(iter(newest.values())), True


def assert_renewal(old, new, steps=1):
    observed_steps = None
    for field in ("application", "headers"):
        previous, renewed = dict(old[field]), dict(new[field])
        advance = renewed.pop("sequence") - previous.pop("sequence")
        require(advance > 0 and (steps is None or advance == steps) and
                (observed_steps is None or advance == observed_steps), "Renewal sequences did not advance together")
        observed_steps = advance
        for date in ("issued_at", "expires_at"):
            renewed.pop(date)
            previous.pop(date)
        require(renewed == previous, "Renewal changed approved release content")


def enough_validity(result, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    for field in ("application", "headers"):
        issued = dt.datetime.fromisoformat(result[field]["issued_at"].replace("Z", "+00:00"))
        expires = dt.datetime.fromisoformat(result[field]["expires_at"].replace("Z", "+00:00"))
        if issued > now + dt.timedelta(minutes=5) or expires <= now + dt.timedelta(days=8):
            return False
    return True


def promote(github, version, original, candidate, canonical_name, recovering):
    # Optimistic check supplements workflow concurrency for external operators.
    current = github.release(version)
    _, observed, observed_recovery = selected_assets(current, version)
    require(observed["id"] == original["id"] and observed.get("digest") == original.get("digest") and
            observed_recovery == recovering, "Approved base bundle changed during renewal; refusing stale promotion")
    previous_name = canonical_name[:-4] + f"-previous-{original['id']}.zip"
    if not recovering:
        original = github.rename(original, previous_name)
    try:
        published = github.rename(candidate, canonical_name)
    except RenewalError:
        # An ambiguous response might have completed remotely. Verify before
        # attempting a rollback which could overwrite an already live name.
        observed = {a["name"]: a for a in github.release(version)["assets"]}
        if observed.get(canonical_name, {}).get("id") == candidate["id"]:
            published = observed[canonical_name]
        else:
            if canonical_name not in observed:
                github.rename(original, canonical_name)
            raise
    require(published.get("digest") == candidate.get("digest"), "Promoted bundle digest mismatch")
    return published


def tool(publisher, *args):
    run = subprocess.run([str(publisher), *map(str, args)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    # Operator diagnostics can contain filesystem paths. Keep CI errors short
    # and never print signing-key environment or subprocess output on failure.
    require(run.returncode == 0, "Publisher operation failed: " + str(args[0]))
    return json.loads(run.stdout) if run.stdout.strip() else None


def hosted_matches(publisher_url, manifests):
    for route, raw in manifests.items():
        served = request(publisher_url.rstrip("/") + route, binary=True, limit=256 << 10)
        if served != raw:
            return False
    return True


def commit_receipt(github, receipt):
    path = "/contents/docs/UPDATE-FEED-STATUS.json"
    content = (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode()
    # Use the content API's expected file SHA: concurrent documentation edits
    # cannot be overwritten, and there is no force push of default main.
    for attempt in range(3):
        old = github.api(path + "?ref=main")
        require(old.get("type") == "file" and old.get("sha"), "Public feed-status receipt is missing")
        try:
            github.api(path, "PUT", {"message": "Record verified Gateway update feed renewal", "branch": "main",
                                     "sha": old["sha"], "content": base64.b64encode(content).decode()})
            return
        except RenewalError:
            if attempt == 2:
                raise


def execute(args):
    approved, channel = load_approval(ROOT)
    require(os.environ.get("GITHUB_REPOSITORY") == REPOSITORY and os.environ.get("GITHUB_REF") == "refs/heads/main" and
            os.environ.get("GITHUB_EVENT_NAME") in {"schedule", "workflow_dispatch"},
            "Renewal is restricted to the approved repository's default-branch workflow")
    token = os.environ.pop("GH_TOKEN", "")
    signing_document = os.environ.pop("GATEWAY_UPDATE_SIGNING_KEY_JSON", "")
    deploy_hook = os.environ.pop("GATEWAY_RENDER_DEPLOY_HOOK", "")
    require(token and signing_document and deploy_hook, "The protected renewal environment is not provisioned")
    hook = urllib.parse.urlsplit(deploy_hook)
    require(hook.scheme == "https" and hook.hostname == "api.render.com" and hook.path.startswith("/deploy/") and
            not hook.username and not hook.password and not hook.fragment, "Invalid protected Render deploy hook")
    github = GitHub(token)
    version = approved["version"]
    release = github.release(version)
    revision = github.tag_revision(version)
    canonical, original, recovering = selected_assets(release, version)
    public = ROOT / "assets/bootstrap/update-public-key.json"
    with tempfile.TemporaryDirectory(prefix="gateway-renewal-", dir=os.environ.get("RUNNER_TEMP")) as temporary:
        work = Path(temporary)
        bundle = work / "approved.zip"
        bundle.write_bytes(github.download(original))
        old = tool(args.publisher, "restore", "-for-renewal", "-bundle", bundle, "-out", work / "feed", "-version", version,
                   "-repository", REPOSITORY, "-public", public)
        require(old["application"]["source_revision"] == revision, "Signed release revision differs from the exact approved Git tag")
        pending_prefix = canonical[:-4] + f"-pending-{original['id']}"
        pending_pattern = re.compile(re.escape(pending_prefix) + r"-([1-9][0-9]*)-([1-9][0-9]*)\.zip")
        existing = [a for a in release["assets"] if pending_pattern.fullmatch(a["name"])]
        require(len(existing) <= 32, "Too many interrupted renewal candidates")
        output = work / "renewed.zip"
        candidates = []
        seen_sequences = set()
        for asset in existing:
            if asset.get("state") == "starter" and asset.get("size") == 0:
                # GitHub can leave an empty placeholder after a failed upload.
                # It contains no signed candidate; remove only this exact
                # release/base/sequence-named incomplete asset before retrying.
                github.api(f"/releases/assets/{asset['id']}", "DELETE")
                continue
            match = pending_pattern.fullmatch(asset["name"])
            candidate_zip = work / f"candidate-{asset['id']}.zip"
            candidate_zip.write_bytes(github.download(asset))
            candidate_dir = work / f"candidate-{asset['id']}"
            result = tool(args.publisher, "restore", "-for-renewal", "-bundle", candidate_zip, "-out", candidate_dir,
                          "-version", version, "-repository", REPOSITORY, "-public", public)
            assert_renewal(old, result, steps=None)
            sequence = result["application"]["sequence"]
            require((sequence, result["headers"]["sequence"]) == (int(match[1]), int(match[2])) and sequence not in seen_sequences,
                    "Staged renewal sequence conflicts with its identity")
            seen_sequences.add(sequence)
            candidates.append((sequence, asset, result, candidate_zip, candidate_dir))
        if candidates:
            _, candidate, new, candidate_zip, candidate_dir = max(candidates, key=lambda value: value[0])
            output.write_bytes(candidate_zip.read_bytes())
            base, feed = new, candidate_dir
        else:
            if recovering:
                # The only pending uploads were empty placeholders. Restore
                # the authenticated last canonical input before starting over.
                original = github.rename(original, canonical)
                recovering = False
            candidate, base, feed = None, old, work / "feed"
        if candidate is None or not enough_validity(new):
            private = work / "private.json"
            fd = os.open(private, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(signing_document)
            signing_document = ""
            try:
                new = tool(args.publisher, "renew-all", "-out", feed, "-private", private,
                           "-approve-release", version, "-validity", "720h")
            finally:
                private.unlink(missing_ok=True)
            tool(args.publisher, "check", "-dir", feed, "-public", public)
            output.unlink(missing_ok=True)
            tool(args.publisher, "export", "-dir", feed, "-out", output, "-public", public)
            assert_renewal(base, new)
            pending_name = pending_prefix + f"-{new['application']['sequence']}-{new['headers']['sequence']}.zip"
            candidate = github.upload(release["id"], pending_name, output.read_bytes())
        assert_renewal(old, new, steps=None)
        # Recheck tag just before publication, so changing the approved tag while
        # this job runs cannot silently change what source was authorized.
        require(github.tag_revision(version) == revision, "Approved tag changed during renewal")
        published = promote(github, version, original, candidate, canonical, recovering)
        with zipfile.ZipFile(output) as archive:
            manifests = {"/manifest.json": archive.read("manifest.json"), "/headers/manifest.json": archive.read("headers-manifest.json")}
        receipt = {"schema": 1, "repository": REPOSITORY, "version": version, "source_revision": revision,
                   "status": "published-awaiting-host", "bundle_sha256": digest(output.read_bytes()),
                   "bundle_asset_id": published["id"], "publisher_url": channel["publisher_url"],
                   "application_sequence": new["application"]["sequence"], "header_sequence": new["headers"]["sequence"],
                   "application_expires_at": new["application"]["expires_at"], "headers_expires_at": new["headers"]["expires_at"],
                   "application_manifest_sha256": digest(manifests["/manifest.json"]),
                   "headers_manifest_sha256": digest(manifests["/headers/manifest.json"])}
        args.receipt.parent.mkdir(parents=True, exist_ok=True)
        args.receipt.write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
        # No body/query echo and no redirect following for this bearer hook.
        request(deploy_hook, method="POST", data=b"{}")
        deadline = time.monotonic() + 18 * 60
        while time.monotonic() < deadline:
            try:
                if hosted_matches(channel["publisher_url"], manifests):
                    break
            except RenewalError:
                pass  # Expected during cold start/deploy; bounded by deadline.
            time.sleep(20)
        else:
            raise RenewalError("Renewed bundle is uploaded, but hosted feeds did not match before the deadline")
        receipt["status"] = "verified"
        receipt["verified_at"] = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
        args.receipt.write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
        commit_receipt(github, receipt)
        # Keep one authenticated previous bundle for rollback; remove only our
        # older backups after both live feeds and the public receipt succeed.
        backup_pattern = re.compile(re.escape(canonical[:-4]) + r"-previous-([1-9][0-9]*)\.zip")
        for asset in github.release(version)["assets"]:
            match = backup_pattern.fullmatch(asset["name"])
            if match and asset["id"] == int(match[1]) and asset["id"] != original["id"]:
                github.api(f"/releases/assets/{asset['id']}", "DELETE")
            elif asset["id"] != published["id"] and (asset["id"], asset["name"]) in {(item[1]["id"], item[1]["name"]) for item in candidates}:
                github.api(f"/releases/assets/{asset['id']}", "DELETE")
        print(f"Verified approved v{version}: app sequence {new['application']['sequence']}, header sequence {new['headers']['sequence']}.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publisher", type=Path, required=True)
    parser.add_argument("--receipt", type=Path, required=True)
    args = parser.parse_args()
    try:
        execute(args)
    except (RenewalError, OSError, ValueError, KeyError) as error:
        # Only our controlled messages are suitable for public logs; unexpected
        # parser/filesystem failures might contain content from secret documents.
        detail = str(error) if isinstance(error, RenewalError) else "Invalid input or local operation failed"
        print("Gateway renewal failed: " + detail, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
