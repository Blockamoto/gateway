"""Small safety checks only: no benchmark process and no user profiles."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
import uuid

from bitmap_pilot_paths import BUILD, init, inspect_tree, workspace, io_path, stop_owned

ROOT = Path(__file__).resolve().parents[1]

class PilotGuards(unittest.TestCase):
    def setUp(self):
        self.out = BUILD / ("bitmap-pilot-guard-" + uuid.uuid4().hex)
        init(self.out)

    def tearDown(self):
        shutil.rmtree(io_path(self.out))

    def runner(self, name, *args):
        return subprocess.run([sys.executable, str(ROOT / "scripts" / name), "--out", str(self.out), *args], cwd=ROOT, capture_output=True, text=True)

    def fake_seed(self):
        seed = self.out / "seed"
        store = seed / "indexes/bitmap-development-v1/bitmap-districts-standard-v1"
        store.mkdir(parents=True)
        page = b"{}"
        digest = hashlib.sha256(page).hexdigest()
        page_path = store / "pages" / digest[:2] / (digest + ".json")
        io_path(page_path.parent).mkdir(parents=True)
        io_path(page_path).write_bytes(page)
        (store / "head.json").write_text(json.dumps({"commitment": "test"}))
        (seed / "pilot-summary.json").write_text(json.dumps({"commitment": "test", **{k:digest for k in ("winner_root", "candidate_root", "target_root", "block_root")}}))
        return seed

    def test_init_and_seed_refuse_reuse(self):
        self.assertEqual((self.out / ".bitmap-populated-pilot-owned").read_bytes(), b"owned bitmap pilot v1\n")
        with self.assertRaises(FileExistsError): init(self.out)
        receipt = self.out / "seed-process.json"
        receipt.write_text("keep")
        result = self.runner("benchmark-bitmap-populated-seed.py")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(receipt.read_text(), "keep")
        self.assertFalse((self.out / "seed-test.log").exists())

    def test_pair_rejects_existing_profile_and_receipts(self):
        self.fake_seed()
        for name in ("01-baseline", "02-current", "01-baseline.log", "pairs-first-partial-01.json", "pairs-first-result.json"):
            item = self.out / name
            item.write_text("keep")
            result = self.runner("benchmark-bitmap-populated-pairs.py")
            self.assertNotEqual(result.returncode, 0, name)
            self.assertEqual(item.read_text(), "keep")
            self.assertFalse((self.out / "01-baseline.log").exists() and name != "01-baseline.log")
            item.unlink()

    def test_junction_in_seed_and_alias_are_rejected(self):
        if os.name != "nt": self.skipTest("Windows junction test")
        seed = self.fake_seed()
        outside = self.out / "outside"
        outside.mkdir()
        sentinel = outside / "sentinel"
        sentinel.write_text("keep")
        junction = seed / "linked"
        created = subprocess.run(["cmd", "/c", "mklink", "/J", str(junction), str(outside)], capture_output=True, text=True)
        if created.returncode: self.skipTest("junction creation unavailable: " + created.stderr)
        try:
            with self.assertRaises(ValueError): inspect_tree(seed)
            result = self.runner("benchmark-bitmap-populated-pairs.py")
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(sentinel.read_text(), "keep")
            self.assertFalse((self.out / "01-baseline").exists())
        finally:
            os.rmdir(junction)
        alias = BUILD / ("bitmap-pilot-alias-" + uuid.uuid4().hex)
        created = subprocess.run(["cmd", "/c", "mklink", "/J", str(alias), str(self.out)], capture_output=True, text=True)
        self.assertEqual(created.returncode, 0, created.stderr)
        try:
            with self.assertRaises(ValueError): workspace(alias)
        finally:
            os.rmdir(alias)

    def test_hardlinked_seed_file_rejected(self):
        seed = self.fake_seed()
        original = self.out / "outside-file"
        original.write_text("keep")
        try: os.link(original, seed / "hardlink")
        except OSError as exc: self.skipTest("hardlinks unavailable: " + str(exc))
        with self.assertRaises(ValueError): inspect_tree(seed)
        self.assertEqual(original.read_text(), "keep")

    def test_owned_child_cleanup(self):
        process = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
        try:
            stop_owned(process)
            self.assertIsNotNone(process.poll())
        finally:
            stop_owned(process)

if __name__ == "__main__":
    unittest.main()
