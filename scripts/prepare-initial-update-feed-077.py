#!/usr/bin/env python3
"""Prepare only the explicitly approved 0.7.7 signed candidate in trusted CI.

No release-asset writes, main commits, Render calls, or live feed changes.
The existing protected signer is used only inside a temporary directory.
"""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import zipfile

VERSION = "0.7.7"
REVISION = "0f60051a2560c43b5e78f1b3dc80e14fda64f6a4"
PREVIOUS_VERSION = "0.7.6"
PREVIOUS_REVISION = "b666c11c1f248769b3984a1168e9808930a8dc00"
REPOSITORY = "Blockamoto/gateway"
PUBLISHER_URL = "https://gateway-updates.onrender.com"
KEY = {"key_id": "c5f20c46d5daf2aa7f21c1602efd3a0b",
       "public_key": "KDki4gPkMMcXH7epA7cuyvta0iPue+Vxqzl+YC1U3Rw="}
EXPECTED_SEQUENCES = (13, 12)
FILES = {f"gateway-client-v{VERSION}-{platform}{suffix}.zip"
         for platform in ["windows-amd64", "linux-amd64"] for suffix in ["", "-fresh"]}
FILES |= {f"gateway-client-v{VERSION}-installer.exe", f"gateway-headers-v{VERSION}.zip"}


class CandidateError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise CandidateError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def one_asset(release, name):
    rows = [a for a in release["assets"] if a["name"] == name]
    require(len(rows) == 1, "Required published release asset is missing or ambiguous")
    return rows[0]


def run_tool(publisher, *args, origin_token=""):
    environment = dict(os.environ)
    if origin_token:
        environment["GATEWAY_PUBLISHER_GITHUB_TOKEN"] = origin_token
    result = subprocess.run([str(publisher), *map(str, args)], capture_output=True, env=environment)
    # Never print subprocess output: diagnostics can include private paths.
    require(result.returncode == 0, "Publisher operation failed: " + str(args[0]))
    if not result.stdout.strip():
        return None
    if args[0] == "publish":
        output = result.stdout.decode("utf-8").lstrip()
        value, end = json.JSONDecoder().raw_decode(output)
        require(output[end:].strip() ==
                "Prepared signed local distribution snapshot. No GitHub release or public service was created.",
                "Unexpected publisher status output")
        return value
    return json.loads(result.stdout)


def live_matches(helper, expected):
    for route, raw in expected.items():
        actual = helper.request(PUBLISHER_URL + route, binary=True, limit=256 << 10)
        require(actual == raw, "Live manifests changed or differ from the authenticated previous bundle")


def execute(args, receipt):
    require(sys.platform.startswith("linux") and os.environ.get("GITHUB_ACTIONS") == "true" and
            os.environ.get("GITHUB_REPOSITORY") == REPOSITORY and
            os.environ.get("GITHUB_REF") == "refs/heads/main" and
            os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch",
            "Initial signing is restricted to the protected manual main workflow")
    root = Path(os.environ["GITHUB_WORKSPACE"]).resolve()
    runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve()
    out = args.out.resolve()
    require(out.is_relative_to(runner_temp) and out != runner_temp and not out.exists(),
            "Candidate output must be a new directory inside RUNNER_TEMP")
    require(args.publisher.is_file() and not args.publisher.is_symlink(), "Operator must be a regular built file")
    require(subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip() ==
            os.environ.get("GITHUB_SHA"), "Trusted workflow source identity differs from checkout")
    require(not subprocess.check_output(["git", "status", "--short", "--untracked-files=no"], cwd=root,
                                        text=True).strip(), "Trusted workflow checkout has tracked modifications")
    public = root / "assets/bootstrap/update-public-key.json"
    require(json.loads(public.read_text()) == KEY, "Pinned public signing authority changed")
    descriptor = json.loads((root / "assets/bootstrap/update-channels.json").read_text())
    channels = [c for c in descriptor["channels"] if c["id"] == "stable"]
    require(len(channels) == 1 and channels[0]["publisher_url"] == PUBLISHER_URL and
            channels[0]["trusted_key"] == KEY, "Bundled public channel authority differs")
    spec = importlib.util.spec_from_file_location("gateway_renewal_helpers", root / "scripts/renew-update-feed.py")
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    token = os.environ.pop("GH_TOKEN", "")
    signing_document = os.environ.pop("GATEWAY_UPDATE_SIGNING_KEY_JSON", "")
    os.environ.pop("GITHUB_TOKEN", None)
    os.environ.pop("GATEWAY_PUBLISHER_GITHUB_TOKEN", None)
    os.environ.pop("GATEWAY_RENDER_DEPLOY_HOOK", None)
    require(token and 0 < len(signing_document.encode()) <= 16 << 10,
            "Existing protected signing environment is not provisioned")
    github = helper.GitHub(token)
    receipt["stage"] = "authenticate-published-origins"
    release = github.release(VERSION)
    require(github.tag_revision(VERSION) == REVISION, "Published v0.7.7 tag differs from frozen runtime source")
    require(not any(a["name"] == f"gateway-update-feed-v{VERSION}.zip" for a in release["assets"]),
            "A canonical 0.7.7 bundle already exists; do not repeat initial signing")
    manifest_asset = one_asset(release, "artifact-manifest.json")
    require(0 < manifest_asset["size"] <= 2 << 20, "Origin artifact manifest exceeds its bound")
    manifest_raw = github.download(manifest_asset)
    manifest = json.loads(manifest_raw)
    require(manifest["version"] == VERSION and manifest["source_revision"] == REVISION and
            len(manifest["artifacts"]) == len(FILES) and
            {a["file"] for a in manifest["artifacts"]} == FILES, "Published artifact manifest has the wrong identity")
    artifacts = {a["file"]: a for a in manifest["artifacts"]}
    previous = github.release(PREVIOUS_VERSION)
    require(github.tag_revision(PREVIOUS_VERSION) == PREVIOUS_REVISION, "Previous release tag changed")
    previous_asset = one_asset(previous, f"gateway-update-feed-v{PREVIOUS_VERSION}.zip")
    previous_raw = github.download(previous_asset)
    with tempfile.TemporaryDirectory(prefix="gateway-initial-077-", dir=runner_temp) as temporary:
        work = Path(temporary)
        os.chmod(work, 0o700)
        bundle = work / "previous.zip"
        bundle.write_bytes(previous_raw)
        feed = work / "feed"
        old = run_tool(args.publisher, "restore", "-bundle", bundle, "-out", feed, "-version", PREVIOUS_VERSION,
                       "-repository", REPOSITORY, "-public", public)
        require(old["application"]["source_revision"] == PREVIOUS_REVISION,
                "Authenticated previous bundle has a different source")
        sequences = (old["application"]["sequence"] + 1, old["headers"]["sequence"] + 1)
        require(sequences == EXPECTED_SEQUENCES, "Previous sequences advanced; review new initial sequences")
        with zipfile.ZipFile(bundle) as archive:
            live = {"/manifest.json": archive.read("manifest.json"),
                    "/headers/manifest.json": archive.read("headers-manifest.json")}
        live_matches(helper, live)
        receipt.update(previous_bundle_sha256=digest(previous_raw), previous_asset_id=previous_asset["id"],
                       previous_application_sequence=sequences[0]-1, previous_header_sequence=sequences[1]-1,
                       application_sequence=sequences[0], header_sequence=sequences[1],
                       artifact_manifest_sha256=digest(manifest_raw), origin_artifacts=manifest["artifacts"])
        receipt["stage"] = "prepare-private-temporary-signer"
        private = work / "private.json"
        fd = os.open(private, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(signing_document)
                handle.flush()
                os.fsync(handle.fileno())
            signing_document = ""
            require(stat.S_IMODE(private.stat().st_mode) == 0o600, "Temporary signer permissions are not owner-only")
            receipt["stage"] = "sign-fixed-approved-app-and-headers"
            application = run_tool(args.publisher, "publish", "-repository", REPOSITORY, "-version", VERSION,
                                   "-approve-release", VERSION, "-sequence", sequences[0], "-validity", "720h",
                                   "-private", private, "-out", feed, origin_token=token)
            headers = run_tool(args.publisher, "publish-headers", "-repository", REPOSITORY, "-version", VERSION,
                               "-approve-release", VERSION, "-sequence", sequences[1], "-validity", "720h",
                               "-private", private, "-out", feed, origin_token=token)
        finally:
            signing_document = ""
            private.unlink(missing_ok=True)
        receipt["stage"] = "authenticate-candidate-and-unchanged-headers"
        checked = run_tool(args.publisher, "check", "-dir", feed, "-public", public)
        require(checked == application and application["version"] == VERSION and
                application["source_revision"] == REVISION and application["sequence"] == sequences[0] and
                headers["sequence"] == sequences[1] and
                application["release_url"] == headers["source_release"] ==
                f"https://github.com/{REPOSITORY}/releases/tag/v{VERSION}", "Signed candidate identity differs")
        require(len(application["artifacts"]) == 2, "Signed app platform set differs")
        for row in application["artifacts"]:
            expected = artifacts.get(row["file"])
            require(expected is not None and all(row[k] == expected[k] for k in ["file", "bytes", "sha256"]),
                    "Signed app differs from captured GitHub artifact manifest")
        for key in ["network", "count", "tip_hash", "chunks"]:
            require(headers[key] == old["headers"][key], "Header payload bytes changed")
        for value in [application, headers]:
            issued = dt.datetime.fromisoformat(value["issued_at"].replace("Z", "+00:00"))
            expires = dt.datetime.fromisoformat(value["expires_at"].replace("Z", "+00:00"))
            require(expires-issued == dt.timedelta(hours=720), "Signed validity differs from 720h")
        require(github.download(one_asset(github.release(VERSION), "artifact-manifest.json")) == manifest_raw and
                github.tag_revision(VERSION) == REVISION, "Published origin changed during preparation")
        live_matches(helper, live)
        candidate = work / f"gateway-update-feed-v{VERSION}.zip"
        run_tool(args.publisher, "export", "-dir", feed, "-public", public, "-out", candidate)
        verified = run_tool(args.publisher, "restore", "-bundle", candidate, "-out", work / "candidate-verified",
                            "-version", VERSION, "-repository", REPOSITORY, "-public", public)
        require(verified["application"] == application and verified["headers"] == headers,
                "Exported candidate differs from checked signed feeds")
        raw = candidate.read_bytes()
        out.mkdir()
        (out / candidate.name).write_bytes(raw)
        with zipfile.ZipFile(candidate) as archive:
            app_manifest = archive.read("manifest.json")
            header_manifest = archive.read("headers-manifest.json")
        receipt.update(status="PREPARED_NOT_PROMOTED", stage="complete", bundle_sha256=digest(raw),
                       bundle_bytes=len(raw), public_key_id=KEY["key_id"], headers_unchanged=True,
                       headers_count=headers["count"], header_chunks=len(headers["chunks"]),
                       application_manifest_sha256=digest(app_manifest),
                       headers_manifest_sha256=digest(header_manifest),
                       application_issued_at=application["issued_at"], application_expires_at=application["expires_at"],
                       headers_issued_at=headers["issued_at"], headers_expires_at=headers["expires_at"],
                       private_temporary_file_removed=not private.exists(), github_release_mutated=False,
                       main_mutated=False, render_mutated=False, live_feed_mutated=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publisher", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    output_was_absent = not args.out.exists()
    receipt = {"schema": 1, "status": "RUNNING", "stage": "trusted-workflow-guard", "version": VERSION,
               "source_revision": REVISION, "workflow_revision": os.environ.get("GITHUB_SHA", ""),
               "scope": "Fixed077 candidate signing only; no canonical asset upload or hosted promotion."}
    code = 0
    try:
        execute(args, receipt)
    except Exception as error:
        code = 1
        receipt.update(status="FAILED", error_type=type(error).__name__,
                       reason=str(error) if isinstance(error, CandidateError) else "Candidate preparation failed")
    receipt["recorded_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    # Only the fixed workflow output under RUNNER_TEMP may receive a receipt.
    runner_temp = os.environ.get("RUNNER_TEMP")
    if output_was_absent and runner_temp and args.out.resolve().is_relative_to(Path(runner_temp).resolve()) and args.out.resolve() != Path(runner_temp).resolve():
        args.out.mkdir(parents=True, exist_ok=True)
        (args.out / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps({k: receipt[k] for k in ["status", "stage", "source_revision"]}))
    return code


if __name__ == "__main__":
    sys.exit(main())
