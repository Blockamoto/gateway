#!/usr/bin/env python3
"""Offline unit checks for guard decisions; no populated seed or process."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

runner=Path(__file__).with_name("hosted-bitmap-run.py")
if not runner.exists(): runner=Path(__file__).with_name("run-hosted.py")
spec=importlib.util.spec_from_file_location("hosted",runner)
hosted=importlib.util.module_from_spec(spec);spec.loader.exec_module(hosted)
builder_path=Path(__file__).with_name("hosted-bitmap-build.py")
if not builder_path.exists(): builder_path=Path(__file__).with_name("build-hosted.py")
build_spec=importlib.util.spec_from_file_location("hosted_build",builder_path)
builder=importlib.util.module_from_spec(build_spec);build_spec.loader.exec_module(builder)

class Guards(unittest.TestCase):
    def test_projection_requires_valid_checkpoint_and_reserve(self):
        row={"winners":80000,"page_files":10000,"page_bytes":50000000}
        projected,reason=hosted.project_checkpoint(row,12*hosted.GiB,3*hosted.GiB)
        self.assertIsNone(reason)
        self.assertEqual(projected["rounding_upper_bound_bytes"],50000000+4095*10000)
        _,reason=hosted.project_checkpoint(row,3*hosted.GiB,3*hosted.GiB)
        self.assertIn("free disk",reason)
        with self.assertRaises(ValueError):hosted.project_checkpoint({**row,"winners":79999},12*hosted.GiB,3*hosted.GiB)
        _,reason=hosted.project_checkpoint({**row,"page_files":60000},100*hosted.GiB,3*hosted.GiB)
        self.assertIn("page-file",reason)

    def test_sampling_failure_stops_owned_child_and_saves_receipt(self):
        with tempfile.TemporaryDirectory() as temp:
            proc=mock.Mock();proc.poll.return_value=None;proc.returncode=None
            out=Path(temp)
            with mock.patch.object(hosted,"system_times",return_value=None), mock.patch.object(hosted.subprocess,"Popen",return_value=proc), mock.patch.object(hosted.metrics,"process_metrics",side_effect=RuntimeError("sample failure")), mock.patch.object(hosted,"stop_owned") as stop:
                with self.assertRaisesRegex(RuntimeError,"sample failure"):
                    hosted.monitor(out,Path("dummy.exe"),"Dummy",{},"sample",False,"smoke")
            self.assertTrue(stop.called)
            receipt=json.loads((out/"sample-process.json").read_text())
            self.assertEqual(receipt["guard_reason"],"monitor exception: RuntimeError")

    def test_build_stop_targets_only_owned_tree(self):
        proc=mock.Mock();proc.pid=123456;proc.poll.side_effect=[None,0]
        with mock.patch.object(builder.subprocess,"run") as command:
            builder.stop_owned_build_tree(proc)
        self.assertEqual(command.call_args.args[0],["taskkill.exe","/PID","123456","/T","/F"])
        proc.wait.assert_called_once_with(timeout=10)

if __name__=="__main__":unittest.main()
