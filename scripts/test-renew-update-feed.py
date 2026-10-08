#!/usr/bin/env python3
"""Offline failure-path checks; never calls GitHub, Render, or a real signer."""
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
import urllib.error
import zipfile

spec = importlib.util.spec_from_file_location("renewal", Path(__file__).with_name("renew-update-feed.py"))
renewal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renewal)

VERSION = "0.7.0"
CANONICAL = "gateway-update-feed-v0.7.0.zip"
OLD = {"id": 41, "name": CANONICAL, "size": 123, "digest": "sha256:" + "a" * 64}
NEW = {"id": 42, "name": "gateway-update-feed-v0.7.0-pending-41-2-2.zip", "size": 124, "digest": "sha256:" + "b" * 64}


class FakeGitHub:
    def __init__(self, recovering=False, failure=None):
        self.assets = {41: copy.deepcopy(OLD), 42: copy.deepcopy(NEW)}
        if recovering:
            self.assets[41]["name"] = "gateway-update-feed-v0.7.0-previous-41.zip"
        self.failure = failure
        self.renames = []

    def release(self, version):
        if version != VERSION:
            raise AssertionError("Selected another release")
        return {"assets": copy.deepcopy(list(self.assets.values()))}

    def rename(self, asset, name):
        self.renames.append((asset["id"], name))
        if any(a["id"] != asset["id"] and a["name"] == name for a in self.assets.values()):
            raise renewal.RenewalError("Duplicate name")
        if asset["id"] == 42 and self.failure == "before":
            raise renewal.RenewalError("Upload rename failed")
        self.assets[asset["id"]]["name"] = name
        if asset["id"] == 42 and self.failure == "after":
            raise renewal.RenewalError("Response lost after successful rename")
        return copy.deepcopy(self.assets[asset["id"]])


class PromotionTests(unittest.TestCase):
    def test_staged_bundle_promoted_with_previous_preserved(self):
        gh = FakeGitHub()
        result = renewal.promote(gh, VERSION, OLD, NEW, CANONICAL, False)
        self.assertEqual(result["id"], 42)
        self.assertEqual(gh.renames, [(41, "gateway-update-feed-v0.7.0-previous-41.zip"), (42, CANONICAL)])
        self.assertEqual(gh.assets[41]["digest"], OLD["digest"])

    def test_failed_rename_restores_original_canonical(self):
        gh = FakeGitHub(failure="before")
        with self.assertRaises(renewal.RenewalError):
            renewal.promote(gh, VERSION, OLD, NEW, CANONICAL, False)
        self.assertEqual(gh.assets[41]["name"], CANONICAL)
        self.assertEqual(gh.assets[42]["name"], NEW["name"])

    def test_lost_success_response_never_rolls_back_new_sequence(self):
        gh = FakeGitHub(failure="after")
        result = renewal.promote(gh, VERSION, OLD, NEW, CANONICAL, False)
        self.assertEqual(result["id"], 42)
        self.assertEqual(gh.assets[42]["name"], CANONICAL)
        self.assertEqual(len(gh.renames), 2)

    def test_interrupted_swap_can_resume_without_resigning(self):
        gh = FakeGitHub(recovering=True)
        name, original, recovering = renewal.selected_assets(gh.release(VERSION), VERSION)
        self.assertTrue(recovering)
        self.assertEqual(original["id"], 41)
        renewal.promote(gh, VERSION, original, NEW, name, recovering)
        self.assertEqual(gh.renames, [(42, CANONICAL)])

    def test_changed_base_aborts_without_mutation(self):
        gh = FakeGitHub()
        gh.assets[41]["digest"] = "sha256:" + "c" * 64
        with self.assertRaises(renewal.RenewalError):
            renewal.promote(gh, VERSION, OLD, NEW, CANONICAL, False)
        self.assertEqual(gh.renames, [])

    def test_missing_or_ambiguous_recovery_fails(self):
        for assets in ([], [dict(OLD, name="gateway-update-feed-v0.7.0-previous-99.zip"), NEW]):
            with self.assertRaises(renewal.RenewalError):
                renewal.selected_assets({"assets": assets}, VERSION)

    def test_recovery_ignores_lower_sequence_leftovers(self):
        gh = FakeGitHub(recovering=True)
        gh.assets[30] = dict(OLD, id=30, name="gateway-update-feed-v0.7.0-previous-30.zip")
        gh.assets[31] = dict(NEW, id=31, name="gateway-update-feed-v0.7.0-pending-30-1-1.zip")
        _, base, recovering = renewal.selected_assets(gh.release(VERSION), VERSION)
        self.assertTrue(recovering)
        self.assertEqual(base["id"], 41)


class ApprovalTests(unittest.TestCase):
    def old_and_new(self):
        old = {"application": {"sequence": 9, "issued_at": "old", "expires_at": "old", "version": VERSION, "artifacts": [{"sha256": "original"}]},
               "headers": {"sequence": 4, "issued_at": "old", "expires_at": "old", "chunks": [{"sha256": "original"}]}}
        new = copy.deepcopy(old)
        for field in new:
            new[field].update(sequence=new[field]["sequence"] + 1, issued_at="new", expires_at="new")
        return old, new

    def test_each_independent_sequence_advances(self):
        renewal.assert_renewal(*self.old_and_new())

    def test_expired_or_near_expiry_candidate_requires_fresh_higher_sequence(self):
        now = renewal.dt.datetime.now(renewal.dt.timezone.utc)
        old, new = self.old_and_new()
        for remaining_days in (-1, 1, 7, 8, 9, 30):
            for field in new:
                new[field]["issued_at"] = now.isoformat()
                new[field]["expires_at"] = (now + renewal.dt.timedelta(days=remaining_days)).isoformat()
            self.assertEqual(renewal.enough_validity(new, now), remaining_days > 8)
        # Re-renewing authenticated staged metadata can skip intermediate
        # unserved sequences, but both feeds must advance by the same amount.
        for field in new:
            new[field]["sequence"] += 1
        renewal.assert_renewal(old, new, steps=None)
        new["headers"]["sequence"] += 1
        with self.assertRaises(renewal.RenewalError):
            renewal.assert_renewal(old, new, steps=None)

    def test_replacement_content_or_reused_sequence_rejected(self):
        for kind in ("app", "header", "app-sequence", "header-sequence", "version"):
            old, new = self.old_and_new()
            if kind == "app":
                new["application"]["artifacts"][0]["sha256"] = "new"
            elif kind == "header":
                new["headers"]["chunks"][0]["sha256"] = "new"
            elif kind == "version":
                new["application"]["version"] = "0.7.1"
            else:
                new["application" if kind == "app-sequence" else "headers"]["sequence"] -= 1
            with self.assertRaises(renewal.RenewalError):
                renewal.assert_renewal(old, new)

    def test_committed_approval_and_channel_match(self):
        config, channel = renewal.load_approval(renewal.ROOT)
        self.assertEqual(config["repository"], "Blockamoto/gateway")
        self.assertTrue(channel["publisher_url"].startswith("https://"))

    def test_channel_key_mismatch_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in ("packaging/update-publisher/approved-release.json", "assets/bootstrap/update-channels.json", "assets/bootstrap/update-public-key.json"):
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((renewal.ROOT / name).read_bytes())
            (root / "assets/bootstrap/update-public-key.json").write_text('{}')
            with self.assertRaises(renewal.RenewalError):
                renewal.load_approval(root)


class NetworkTests(unittest.TestCase):
    def test_release_upload_sends_raw_zip_but_accepts_json_metadata(self):
        payload = b"PK\x03\x04fixture zip bytes"
        name = "gateway-update-feed-v0.7.0-pending-41-2-2.zip"
        expected = {"id": 42, "name": name, "size": len(payload), "digest": "sha256:" + renewal.digest(payload)}
        testcase = self

        class Opener:
            def open(self, req, timeout):
                # Match GitHub's upload contract: unlike asset GET downloads,
                # POST returns JSON metadata even though its body is a ZIP.
                if req.get_header("Accept") != "application/vnd.github+json":
                    raise urllib.error.HTTPError(req.full_url, 415, "Unsupported response media type", {}, None)
                testcase.assertEqual(req.get_method(), "POST")
                testcase.assertEqual(req.get_header("Content-type"), "application/zip")
                testcase.assertEqual(req.get_header("Authorization"), "Bearer ephemeral-test-token")
                testcase.assertEqual(req.data, payload)
                testcase.assertEqual(renewal.urllib.parse.urlsplit(req.full_url).hostname, "uploads.github.com")
                testcase.assertEqual(renewal.urllib.parse.parse_qs(renewal.urllib.parse.urlsplit(req.full_url).query), {"name": [name]})
                return io.BytesIO(json.dumps(expected).encode())

        with mock.patch.object(renewal.urllib.request, "build_opener", return_value=Opener()):
            self.assertEqual(renewal.GitHub("ephemeral-test-token").upload(100, name, payload), expected)

    def test_github_credential_does_not_follow_asset_redirect(self):
        calls = []

        class Opener:
            def open(self, req, timeout):
                calls.append(req)
                if len(calls) == 1:
                    raise urllib.error.HTTPError(req.full_url, 302, "", {"Location": "https://release-assets.githubusercontent.com/file"}, None)
                return io.BytesIO(b"verified public bundle")

        with mock.patch.object(renewal.urllib.request, "build_opener", return_value=Opener()):
            result = renewal.request(renewal.API + "/releases/assets/41", token="ephemeral-test-token", binary=True)
        self.assertEqual(result, b"verified public bundle")
        self.assertEqual(calls[0].get_header("Authorization"), "Bearer ephemeral-test-token")
        self.assertEqual(calls[0].get_header("Accept"), "application/octet-stream")
        self.assertIsNone(calls[1].get_header("Authorization"))

    def test_external_redirect_and_credential_destination_rejected(self):
        class Opener:
            def open(self, req, timeout):
                raise urllib.error.HTTPError(req.full_url, 302, "", {"Location": "https://evil.example/asset"}, None)

        with mock.patch.object(renewal.urllib.request, "build_opener", return_value=Opener()):
            with self.assertRaises(renewal.RenewalError):
                renewal.request(renewal.API, token="secret", binary=True)
        with self.assertRaises(renewal.RenewalError):
            renewal.request("https://evil.example/", token="secret")

    def test_hook_failure_does_not_echo_secret_url(self):
        hook = "https://api.render.com/deploy/fake?key=secret-test-key"

        class Opener:
            def open(self, req, timeout):
                raise urllib.error.HTTPError(req.full_url, 500, hook, {}, None)

        with mock.patch.object(renewal.urllib.request, "build_opener", return_value=Opener()):
            with self.assertRaises(renewal.RenewalError) as result:
                renewal.request(hook, method="POST")
        self.assertNotIn("secret-test-key", str(result.exception))
        self.assertNotIn("render.com", str(result.exception))

    def test_digest_mismatch_prevents_restore(self):
        gh = renewal.GitHub("fixture-token")
        with mock.patch.object(renewal, "request", return_value=b"tampered"):
            with self.assertRaises(renewal.RenewalError):
                gh.download(dict(OLD, size=8))


class ExecuteRecoveryTests(unittest.TestCase):
    def test_full_resumed_run_keeps_promoted_bundle_and_refreshes_expired_candidates(self):
        for remaining_days in (20, 1, -2):
            with self.subTest(remaining_days=remaining_days), tempfile.TemporaryDirectory() as temporary:
                now = renewal.dt.datetime.now(renewal.dt.timezone.utc)
                original = {"application": {"sequence": 1, "source_revision": "a" * 40, "issued_at": now.isoformat(), "expires_at": now.isoformat()},
                            "headers": {"sequence": 1, "issued_at": now.isoformat(), "expires_at": now.isoformat()}}
                staged = copy.deepcopy(original)
                for field in staged:
                    staged[field].update(sequence=2, issued_at=(now - renewal.dt.timedelta(days=30-remaining_days)).isoformat(),
                                         expires_at=(now + renewal.dt.timedelta(days=remaining_days)).isoformat())

                def bundle():
                    data = io.BytesIO()
                    with zipfile.ZipFile(data, "w") as archive:
                        archive.writestr("manifest.json", b"authenticated app fixture")
                        archive.writestr("headers-manifest.json", b"authenticated header fixture")
                    return data.getvalue()

                class FlowGitHub(FakeGitHub):
                    def __init__(self):
                        super().__init__(recovering=True)
                        self.deleted = []

                    def tag_revision(self, version):
                        return "a" * 40

                    def release(self, version):
                        return dict(super().release(version), id=100)

                    def download(self, asset):
                        return bundle()

                    def upload(self, release_id, name, raw):
                        self.assets[43] = {"id": 43, "name": name, "size": len(raw), "digest": "sha256:" + renewal.digest(raw)}
                        return copy.deepcopy(self.assets[43])

                    def api(self, path, method="GET", value=None):
                        if method == "DELETE":
                            ident = int(path.rsplit("/", 1)[1])
                            self.deleted.append(ident)
                            del self.assets[ident]
                        elif method == "GET":
                            return {"type": "file", "sha": "fixture-file-sha"}
                        return {}

                gh = FlowGitHub()
                operations = []

                def fake_tool(publisher, *args):
                    operations.append(args[0])
                    if args[0] == "restore":
                        path = Path(args[args.index("-bundle") + 1])
                        return copy.deepcopy(original if path.name == "approved.zip" else staged)
                    if args[0] == "renew-all":
                        renewed = copy.deepcopy(staged)
                        for field in renewed:
                            renewed[field].update(sequence=3, issued_at=now.isoformat(), expires_at=(now + renewal.dt.timedelta(days=30)).isoformat())
                        return renewed
                    if args[0] == "export":
                        Path(args[args.index("-out") + 1]).write_bytes(bundle())

                environment = {"GITHUB_REPOSITORY": renewal.REPOSITORY, "GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch",
                               "GH_TOKEN": "fake-token", "GATEWAY_UPDATE_SIGNING_KEY_JSON": "fake-private-fixture", "GATEWAY_RENDER_DEPLOY_HOOK": "https://api.render.com/deploy/fake?key=fake",
                               "RUNNER_TEMP": temporary}
                args = SimpleNamespace(publisher=Path("fixture-publisher"), receipt=Path(temporary) / "public" / "receipt.json")
                with mock.patch.dict(renewal.os.environ, environment), mock.patch.object(renewal, "GitHub", return_value=gh), \
                        mock.patch.object(renewal, "tool", side_effect=fake_tool), mock.patch.object(renewal, "request", return_value=b"{}"), \
                        mock.patch.object(renewal, "hosted_matches", return_value=True), mock.patch("builtins.print"):
                    renewal.execute(args)
                expected = 42 if remaining_days > 8 else 43
                self.assertEqual(gh.assets[expected]["name"], CANONICAL)
                self.assertNotIn(expected, gh.deleted, "Cleanup deleted the newly canonical signed bundle")
                self.assertIn(41, gh.assets, "Previous authenticated bundle was not retained")
                self.assertEqual("renew-all" in operations, remaining_days <= 8)
                receipt = json.loads(args.receipt.read_text())
                self.assertEqual(receipt["status"], "verified")
                self.assertEqual(receipt["application_sequence"], 2 if remaining_days > 8 else 3)
                self.assertFalse(list(Path(temporary).glob("gateway-renewal-*")), "Private staging survived the run")


if __name__ == "__main__":
    unittest.main()
