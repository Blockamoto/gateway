#!/usr/bin/env python3
"""Offline fake-origin tests only. No network, protected key, or promotion."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
import zipfile

FILE = Path(__file__).with_name("prepare-initial-update-feed-079.py")
spec = importlib.util.spec_from_file_location("prepare079", FILE)
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


def sha(raw): return hashlib.sha256(raw).hexdigest()


def bundle():
    data = io.BytesIO()
    with zipfile.ZipFile(data, "w") as archive:
        archive.writestr("manifest.json", b"fake-app-manifest")
        archive.writestr("headers-manifest.json", b"fake-header-manifest")
    return data.getvalue()


class Scenario:
    def __init__(self, temp):
        self.temp = Path(temp)
        self.root = self.temp / "workspace"
        self.run = self.temp / "runner"
        self.run.mkdir(); (self.root / "assets/bootstrap").mkdir(parents=True)
        (self.root / "assets/bootstrap/update-public-key.json").write_text(json.dumps(prepare.KEY))
        (self.root / "assets/bootstrap/update-channels.json").write_text(json.dumps({"channels": [{"id": "stable", "publisher_url": prepare.PUBLISHER_URL, "trusted_key": prepare.KEY}]}))
        self.publisher = self.temp / "publisher"
        self.publisher.write_text("fake")
        self.out = self.run / "candidate"
        self.env = {"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": prepare.REPOSITORY, "GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_WORKSPACE": str(self.root), "RUNNER_TEMP": str(self.run), "GITHUB_SHA": "2" * 40, "GH_TOKEN": "synthetic-token", "GATEWAY_UPDATE_SIGNING_KEY_JSON": "synthetic-test-key"}
        self.raw = {name: ("fixture " + name).encode() for name in prepare.FILES}
        self.pins = {name: {"bytes": len(raw), "sha256": sha(raw)} for name,raw in self.raw.items()}
        self.manifest = {"version": prepare.VERSION, "source_revision": "1"*40, "artifacts": [{"file": name, **self.pins[name]} for name in sorted(self.raw)]}
        self.manifest_raw = json.dumps(self.manifest).encode()
        self.previous_raw = bundle()
        self.assets = [{"id": i + 10, "name": name, "size": len(raw), "digest": "sha256:" + sha(raw)} for i,(name,raw) in enumerate(self.raw.items())]
        self.manifest_asset = {"id": 90, "name": "artifact-manifest.json", "size": len(self.manifest_raw), "digest": "sha256:" + sha(self.manifest_raw)}
        self.previous_asset = {"id": 91, "name": "gateway-update-feed-v0.7.8.zip", "size": len(self.previous_raw), "digest": "sha256:" + sha(self.previous_raw)}
        self.headers = {"sequence": 8, "network": "mainnet", "count": 5, "tip_hash": "abc", "chunks": [{"sha256": "old"}], "issued_at": "2026-01-01T00:00:00Z", "expires_at": "2026-01-31T00:00:00Z", "source_release": "https://github.com/Blockamoto/gateway/releases/tag/v0.7.9"}
        self.application = {"version": "0.7.9", "source_revision": "1"*40, "sequence": 11, "release_url": self.headers["source_release"], "artifacts": [{"file": f"gateway-client-v0.7.9-{platform}.zip", "platform": platform, **self.pins[f"gateway-client-v0.7.9-{platform}.zip"]} for platform in ("windows-amd64", "linux-amd64")], "issued_at": self.headers["issued_at"], "expires_at": self.headers["expires_at"]}
        self.old = {"application": {"source_revision": prepare.PREVIOUS_REVISION, "sequence": 10}, "headers": {**self.headers, "sequence": 7}}
        self.live = {"/manifest.json": b"fake-app-manifest", "/headers/manifest.json": b"fake-header-manifest"}
        self.fail_sign = False; self.fail_restore = False; self.fail_candidate_restore = False; self.changed_live = False; self.changed_origin = False
        self.overrides = {}
        self.release_name = prepare.TITLE; self.release_prerelease = True; self.release_draft = False
        self.release_reads = 0; self.live_reads = 0; self.operations = []; self.private_path = None; self.git_envs = []

    def release(self, version):
        if version == prepare.PREVIOUS_VERSION: return {"assets": [self.previous_asset]}
        self.release_reads += 1
        rows = copy.deepcopy(self.assets + [self.manifest_asset])
        if self.changed_origin and self.release_reads > 1: rows[0]["id"] += 1000
        return {"name": self.release_name, "prerelease": self.release_prerelease, "draft": self.release_draft, "assets": rows}

    def tag_revision(self, version): return prepare.PREVIOUS_REVISION if version == prepare.PREVIOUS_VERSION else "1"*40
    def download(self, asset):
        if asset["id"] == 90: return self.manifest_raw
        if asset["id"] == 91: return self.previous_raw
        return self.raw[asset["name"]]
    def request(self, url, **kwargs):
        self.live_reads += 1
        value = self.live[url[len(prepare.PUBLISHER_URL):]]
        return b"changed" if self.changed_live and self.live_reads > 2 else value
    def tool(self, publisher, op, *args, **kwargs):
        self.operations.append(op)
        args = list(args)
        def arg(name): return args[args.index(name)+1]
        if op == "restore":
            if self.fail_restore: raise prepare.CandidateError("synthetic restore failure")
            if self.fail_candidate_restore and Path(arg("-bundle")).name != "previous.zip": raise prepare.CandidateError("synthetic candidate restore failure")
            return self.old if Path(arg("-bundle")).name == "previous.zip" else {"application": self.application, "headers": self.headers}
        if op in ("publish", "publish-headers"):
            self.private_path = Path(arg("-private"))
            if self.private_path.read_text() != "synthetic-test-key": raise AssertionError("wrong test key")
            if self.fail_sign: raise prepare.CandidateError("synthetic signing failure")
            return self.application if op == "publish" else self.headers
        if op == "check": return self.application
        if op == "export": Path(arg("-out")).write_bytes(bundle()); return None
        raise AssertionError(op)

    def run_execute(self):
        helper = SimpleNamespace(GitHub=lambda token: self, request=self.request)
        spec = SimpleNamespace(loader=SimpleNamespace(exec_module=lambda module: None))
        def git(command, **kwargs):
            self.git_envs.append(dict(kwargs["env"]))
            return self.env["GITHUB_SHA"] + "\n" if "rev-parse" in command else ""
        # Windows does not implement POSIX chmod semantics. On Linux retain the
        # real mode check, including in the protected workflow's integration.
        mode_check = SimpleNamespace(S_IMODE=lambda mode: 0o600) if os.name == "nt" else prepare.stat
        pins = dict(REVISION="1"*40, EXPECTED_SEQUENCES=(11, 8), EXPECTED_MANIFEST_SHA256=sha(self.manifest_raw), EXPECTED_ARTIFACTS=self.pins, HEADER_POLICY="unchanged", EXPECTED_HEADER_SNAPSHOT_SHA256=prepare.header_identity(self.headers), stat=mode_check)
        pins.update(self.overrides)
        with mock.patch.multiple(prepare, **pins), mock.patch.object(prepare.sys, "platform", "linux"), mock.patch.dict(os.environ, self.env, clear=True), mock.patch.object(prepare.subprocess, "check_output", side_effect=git), mock.patch.object(prepare.importlib.util, "spec_from_file_location", return_value=spec), mock.patch.object(prepare.importlib.util, "module_from_spec", return_value=helper), mock.patch.object(prepare, "run_tool", side_effect=self.tool):
            receipt = {}
            prepare.execute(SimpleNamespace(out=self.out, publisher=self.publisher), receipt)
            return receipt


class PreparationTests(unittest.TestCase):
    def test_protected_values_never_reach_git_or_unrelated_operator(self):
        sensitive = ("GH_TOKEN", "GITHUB_TOKEN", "GATEWAY_UPDATE_SIGNING_KEY_JSON", "GATEWAY_PUBLISHER_GITHUB_TOKEN", "GATEWAY_RENDER_DEPLOY_HOOK", "ACTIONS_RUNTIME_TOKEN")
        with tempfile.TemporaryDirectory() as tmp:
            s=Scenario(tmp);s.env.update({"GITHUB_TOKEN":"fake", "GATEWAY_RENDER_DEPLOY_HOOK":"fake", "ACTIONS_RUNTIME_TOKEN":"fake"})
            s.run_execute()
            self.assertEqual(len(s.git_envs), 2)
            for child in s.git_envs:
                self.assertTrue(all(name not in child for name in sensitive))
        environment={"PATH":"safe", **{name:"synthetic-only" for name in sensitive}}
        result=SimpleNamespace(returncode=0,stdout=b"{}")
        with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(prepare.subprocess,"run",return_value=result) as child:
            prepare.run_tool("publisher", "check")
            self.assertEqual(child.call_args.kwargs["env"], {"PATH":"safe"})
            prepare.run_tool("publisher", "publish-headers", origin_token="synthetic-token")
            self.assertEqual(child.call_args.kwargs["env"], {"PATH":"safe", "GATEWAY_PUBLISHER_GITHUB_TOKEN":"synthetic-token"})

    def test_synthetic_operator_refuses_protected_credential_variables(self):
        path = Path(__file__).with_name("test-prepare-real-079.py")
        real_spec = importlib.util.spec_from_file_location("real_fixture_safety", path)
        real_fixture = importlib.util.module_from_spec(real_spec)
        real_spec.loader.exec_module(real_fixture)
        for name in real_fixture.SENSITIVE_NAMES:
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                real_fixture.clean_test_environment({name: "synthetic-only"})
        self.assertEqual(real_fixture.clean_test_environment({"PATH": "test"}), {"PATH": "test"})

    def test_unfrozen_defaults_fail_before_environment_or_network(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(prepare.CandidateError): prepare.execute(SimpleNamespace(), {})

    def test_every_release_input_is_required(self):
        complete = {"REVISION": "1"*40, "EXPECTED_MANIFEST_SHA256": "2"*64,
                    "EXPECTED_SEQUENCES": (11, 8), "HEADER_POLICY": "unchanged",
                    "EXPECTED_HEADER_SNAPSHOT_SHA256": "3"*64,
                    "EXPECTED_ARTIFACTS": {name: {"bytes": 1, "sha256": "4"*64} for name in prepare.FILES}}
        with mock.patch.multiple(prepare, **complete): prepare.frozen_inputs()
        for key, value in (("REVISION", ""), ("EXPECTED_MANIFEST_SHA256", ""),
                           ("EXPECTED_SEQUENCES", None), ("HEADER_POLICY", ""),
                           ("EXPECTED_HEADER_SNAPSHOT_SHA256", ""), ("EXPECTED_ARTIFACTS", {})):
            with self.subTest(key=key), mock.patch.multiple(prepare, **{**complete, key: value}):
                with self.assertRaises(prepare.CandidateError): prepare.frozen_inputs()

    def test_success_only_creates_sanitized_local_candidate(self):
        with tempfile.TemporaryDirectory() as tmp:
            s=Scenario(tmp); receipt=s.run_execute()
            self.assertEqual(receipt["status"], "PREPARED_NOT_PROMOTED")
            self.assertTrue(receipt["private_temporary_file_removed"])
            self.assertFalse(any(receipt[k] for k in ("github_release_mutated", "main_mutated", "render_mutated", "live_feed_mutated")))
            self.assertNotIn("synthetic-test-key", json.dumps(receipt))
            self.assertEqual(sorted(p.name for p in s.out.iterdir()), ["gateway-update-feed-v0.7.9.zip"])
            self.assertFalse(s.private_path.exists())

    def test_context_and_output_guards(self):
        for key,value in (("GITHUB_REPOSITORY", "attacker/repo"), ("GITHUB_REF", "refs/heads/branch"), ("GITHUB_EVENT_NAME", "push")):
            with self.subTest(key=key), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp);s.env[key]=value
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.operations);self.assertFalse(s.out.exists())
        with tempfile.TemporaryDirectory() as tmp:
            s=Scenario(tmp);s.out.mkdir();(s.out/"sentinel").write_text("keep")
            with self.assertRaises(prepare.CandidateError): s.run_execute()
            self.assertEqual((s.out/"sentinel").read_text(),"keep")

    def test_origin_integrity_failures(self):
        for mutation in ("tag", "missing", "duplicate", "changed-bytes", "trust-key", "previous-tag", "advanced-sequence"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp)
                if mutation=="tag": s.tag_revision=lambda v: "9"*40
                elif mutation=="previous-tag": s.tag_revision=lambda v: "1"*40 if v==prepare.VERSION else "9"*40
                elif mutation=="missing": s.assets.pop()
                elif mutation=="duplicate": s.assets.append(copy.deepcopy(s.assets[0]))
                elif mutation=="changed-bytes": s.raw[next(iter(s.raw))] = b"tampered"
                elif mutation=="trust-key": (s.root/"assets/bootstrap/update-public-key.json").write_text("{}")
                elif mutation=="advanced-sequence": s.old["headers"]["sequence"] += 1
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.out.exists())

    def test_release_title_and_publication_state_are_required_before_signing(self):
        for field, value in (("release_name", "wrong"), ("release_prerelease", False), ("release_draft", True)):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp); setattr(s, field, value)
                with self.assertRaisesRegex(prepare.CandidateError, "release title or state"): s.run_execute()
                self.assertFalse(s.operations)
                self.assertFalse(s.out.exists())

    def test_concurrent_origin_and_live_changes_abort(self):
        for field in ("changed_live", "changed_origin"):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp);setattr(s,field,True)
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.out.exists())
                self.assertFalse(s.private_path.exists())

    def test_failed_sign_and_restore_leave_no_key_or_candidate(self):
        for field in ("fail_sign", "fail_restore", "fail_candidate_restore"):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp);setattr(s,field,True)
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.out.exists())
                if s.private_path: self.assertFalse(s.private_path.exists())

    @unittest.skipUnless(os.name == "posix", "POSIX mode enforcement requires a POSIX host")
    def test_private_mode_rejects_real_non_owner_permissions(self):
        with tempfile.TemporaryDirectory() as tmp:
            s=Scenario(tmp); real_open=os.open; key_paths=[]
            def unsafe_open(path, flags, mode=0o777, *args, **kwargs):
                fd=real_open(path, flags, mode, *args, **kwargs)
                if Path(path).name == "private.json":
                    os.fchmod(fd, 0o644)
                    key_paths.append(Path(path))
                return fd
            with mock.patch.object(prepare.os, "open", side_effect=unsafe_open):
                with self.assertRaisesRegex(prepare.CandidateError, "owner-only"): s.run_execute()
            self.assertEqual(len(key_paths), 1)
            self.assertFalse(key_paths[0].exists())
            self.assertFalse(s.out.exists())

    def test_manifest_mismatch_is_rejected_even_with_a_matching_manifest_hash(self):
        for change in ("wrong-revision", "wrong-version", "missing", "extra", "changed-hash"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp)
                if change == "wrong-revision": s.manifest["source_revision"] = "9"*40
                elif change == "wrong-version": s.manifest["version"] = "0.7.10"
                elif change == "missing": s.manifest["artifacts"].pop()
                elif change == "extra": s.manifest["artifacts"].append({"file": "unexpected", "bytes": 1, "sha256": "a"*64})
                else: s.manifest["artifacts"][0]["sha256"] = "a"*64
                s.manifest_raw = json.dumps(s.manifest).encode()
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.out.exists())

    def test_signed_sequences_validity_and_header_policy(self):
        for change in ("app-sequence", "header-sequence", "validity", "unreviewed-headers", "wrong-header-pin", "duplicate-platform", "wrong-platform-file"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                s=Scenario(tmp)
                if change == "app-sequence": s.application["sequence"] -= 1
                elif change == "header-sequence": s.headers["sequence"] -= 1
                elif change == "validity": s.application["expires_at"] = "2026-01-02T00:00:00Z"
                elif change == "unreviewed-headers": s.headers["chunks"] = [{"sha256": "new"}]
                elif change == "wrong-header-pin": s.overrides["EXPECTED_HEADER_SNAPSHOT_SHA256"] = "0"*64
                elif change == "duplicate-platform": s.application["artifacts"][1] = copy.deepcopy(s.application["artifacts"][0])
                else: s.application["artifacts"][0]["platform"] = "linux-amd64"
                with self.assertRaises(prepare.CandidateError): s.run_execute()
                self.assertFalse(s.out.exists())
        with tempfile.TemporaryDirectory() as tmp:
            s=Scenario(tmp);s.headers["chunks"]=[{"sha256": "reviewed-new"}];s.overrides["HEADER_POLICY"]="reviewed-new"
            receipt=s.run_execute()
            self.assertEqual(receipt["header_policy"], "reviewed-new")
            self.assertFalse(receipt["headers_unchanged"])

if __name__ == "__main__": unittest.main()
