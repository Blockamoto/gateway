#!/usr/bin/env python3
"""Non-installing guard tests. Uses only synthetic bytes and mocked processes."""
import hashlib
import importlib.util
import io
import os
import struct
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import zipfile

spec = importlib.util.spec_from_file_location('audited_installer', Path(__file__).with_name('check-audited-windows-installer.py'))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class Response(io.BytesIO):
    def geturl(self):
        return 'https://fixtures.example/headers.zip'


class GuardTests(unittest.TestCase):
    def test_bounded_shortcut_local_target_and_invalid_forms(self):
        target = r'C:\owned\GatewayOnDemand.exe'
        shell_header = bytearray(bytes.fromhex('4c0000000114020000000000c000000000000046') + bytes(56))
        struct.pack_into('<I', shell_header, 20, 2)
        for unicode_paths in (False, True):
            header_size = 36 if unicode_paths else 28
            base = (target.encode('utf-16-le')+b'\0\0') if unicode_paths else target.encode('ascii')+b'\0'
            tail = b'\0\0' if unicode_paths else b'\0'
            info = bytearray(header_size)
            struct.pack_into('<7I', info, 0, header_size+len(base)+len(tail), header_size, 1, 0,
                             header_size, 0, header_size+len(base))
            if unicode_paths:
                struct.pack_into('<II', info, 28, header_size, header_size+len(base))
            payload = bytes(shell_header+info+base+tail)
            with tempfile.TemporaryDirectory() as tmp:
                link = Path(tmp)/'fixture.lnk'
                link.write_bytes(payload)
                self.assertEqual(tool.shortcut_target(link), target)
                self.assertNotEqual(tool.shortcut_target(link), r'C:\owned\other.exe')
                link.write_bytes(payload[:-1])
                with self.assertRaises(RuntimeError):
                    tool.shortcut_target(link)
                bad = bytearray(payload)
                struct.pack_into('<I', bad, 20, 2 | 0x200)
                link.write_bytes(bad)
                with self.assertRaisesRegex(RuntimeError, 'Unsupported'):
                    tool.shortcut_target(link)

    def test_hosted_context_is_required(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(RuntimeError, 'GitHub-hosted'):
                tool.hosted_only()

    def test_hash_mismatch_never_starts_process(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = Path(tmp) / 'fake.exe'
            binary.write_bytes(b'not the installer')
            with mock.patch.object(tool.subprocess, 'run') as run:
                with self.assertRaisesRegex(RuntimeError, 'execution refused'):
                    tool.run_audited(binary, '0'*64, ['-cli'], {'TEMP':tmp})
                run.assert_not_called()

    def test_matching_hash_reaches_only_mocked_process(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = Path(tmp) / 'fake.exe'
            binary.write_bytes(b'synthetic executable')
            expected = hashlib.sha256(binary.read_bytes()).hexdigest()
            with mock.patch.object(tool.subprocess, 'run') as run:
                tool.run_audited(binary, expected, ['-preview'], {'TEMP':tmp})
                self.assertEqual(run.call_args.args[0], [str(binary), '-preview'])
                self.assertTrue(run.call_args.kwargs['check'])

    def test_existing_registration_aborts_before_process_check(self):
        with mock.patch.object(tool, 'integration_clear', side_effect=RuntimeError('existing registration')), \
             mock.patch.object(tool.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'existing registration'):
                tool.runner_preflight()
            run.assert_not_called()

    def test_existing_gateway_process_aborts_preflight(self):
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(os.environ, {'APPDATA':tmp}), \
             mock.patch.object(tool, 'integration_clear'), \
             mock.patch.object(tool.subprocess, 'run') as run:
            run.return_value.stdout = '"GatewayClient.exe","1234","Console","1","10 K"\n'
            with self.assertRaisesRegex(RuntimeError, 'already has a Gateway process'):
                tool.runner_preflight()

    def test_baseline_is_verified_before_extraction(self):
        headers = bytes(80)
        meta = b'synthetic metadata\n'
        memory = io.BytesIO()
        with zipfile.ZipFile(memory, 'w') as z:
            z.writestr('headers-mainnet.bin', headers)
            z.writestr('headers-mainnet.json', meta)
        archive = memory.getvalue()
        fake_opener = mock.Mock()
        fake_opener.open.side_effect = lambda *args, **kwargs: Response(archive)
        pins = dict(ARCHIVE_SIZE=len(archive), ARCHIVE_SHA=hashlib.sha256(archive).hexdigest(),
                    HEADER_SIZE=len(headers), HEADER_SHA=hashlib.sha256(headers).hexdigest(),
                    HEADER_META_SHA=hashlib.sha256(meta).hexdigest(),
                    HEADER_TIP=hashlib.sha256(hashlib.sha256(headers).digest()).digest()[::-1].hex())
        with tempfile.TemporaryDirectory() as tmp, mock.patch.multiple(tool, **pins), \
             mock.patch.object(tool.urllib.request, 'build_opener', return_value=fake_opener):
            self.assertEqual(tool.fetch_baseline(Path(tmp)).read_bytes(), headers)
        with tempfile.TemporaryDirectory() as tmp, mock.patch.multiple(tool, **{**pins, 'ARCHIVE_SHA':'0'*64}), \
             mock.patch.object(tool.urllib.request, 'build_opener', return_value=fake_opener):
            with self.assertRaisesRegex(RuntimeError, 'approved bytes'):
                tool.fetch_baseline(Path(tmp))
            self.assertFalse((Path(tmp)/'headers-mainnet.bin').exists())


if __name__ == '__main__':
    unittest.main()
