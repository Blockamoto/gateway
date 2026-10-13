import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

spec = importlib.util.spec_from_file_location('published', Path(__file__).with_name('verify-published-linux.py'))
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class PublishedLinuxAuditTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.version = '0.7.5'
        self.package = 'gateway-client-v0.7.5-linux-amd64/'
        self.payloads = {name: ('payload:' + name).encode() for name in audit.expected_members(self.version, False) - {'SHA256SUMS.txt'}}
        binary = b'\x7fELF\x02\x01' + bytes(12) + bytes([62, 0])
        digest = hashlib.sha256(binary).hexdigest()
        self.data = dict(version=self.version, source_revision='a' * 40, client_sha256=digest, helper_sha256=digest)
        self.compatibility = {'app_version': self.version, 'utxo_schema': 2}
        self.payloads.update({'gateway-client': binary, 'gateway-update-helper': binary,
                             'SOURCE-REVISION.txt': ('Repository: https://github.com/Blockamoto/gateway\nCommit: ' + 'a' * 40 + '\nVersion: 0.7.5\n').encode(),
                             'COMPATIBILITY.json': json.dumps(self.compatibility).encode(),
                             'RELEASE-ACCEPTANCE.json': b'{"version":"0.7.5"}'})
        self.payloads['BINARY-AUDIT.json'] = json.dumps({'status': 'PASS', 'linux': {'file': 'gateway-client', 'format': 'ELF', 'sha256': digest, 'bytes': len(binary)}, 'linux_helper': {'file': 'gateway-update-helper', 'format': 'ELF', 'sha256': digest, 'bytes': len(binary)}}).encode()
        self.environment = patch.dict(os.environ, GITHUB_REPOSITORY='Blockamoto/gateway')
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def archive(self, extra=None, wrong_sum=False, symlink=False):
        path = self.root / 'fixture.zip'
        manifest = ''.join(hashlib.sha256(value).hexdigest() + '  ' + name + '\n' for name, value in sorted(self.payloads.items()))
        if wrong_sum:
            manifest = '0' * 64 + manifest[64:]
        with warnings.catch_warnings():
            warnings.simplefilter('ignore', UserWarning)
            with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED) as target:
                for name, value in self.payloads.items():
                    info = zipfile.ZipInfo(self.package + name)
                    if symlink and name == 'gateway-client':
                        info.create_system = 3
                        info.external_attr = (stat.S_IFLNK | 0o777) << 16
                    target.writestr(info, value)
                target.writestr(self.package + 'SHA256SUMS.txt', manifest)
                if extra:
                    target.writestr(extra, b'bad')
        return path

    def extract(self, path, expected=None):
        return audit.extract_verified(path, self.root / 'extracted', self.version, False, expected or audit.sha(path))[0]

    def test_valid_exact_archive_and_identity(self):
        package = self.extract(self.archive())
        audit.audit_package(package, self.data, self.compatibility, False)
        self.assertEqual(len(list(package.iterdir())), 17)

    def test_wrong_external_hash_rejected_before_extraction(self):
        with self.assertRaisesRegex(ValueError, 'Published archive SHA256'):
            self.extract(self.archive(), '0' * 64)
        self.assertFalse((self.root / 'extracted').exists())

    def test_unsafe_duplicate_and_extra_members_rejected(self):
        for extra in [self.package + '../escape', self.package + 'gateway-client', self.package + 'unexpected', self.package + '..\\escape']:
            with self.subTest(extra=extra), self.assertRaises(ValueError):
                audit.extract_verified(self.archive(extra=extra), self.root / ('reject-' + str(len(list(self.root.iterdir())))), self.version, False, audit.sha(self.root / 'fixture.zip'))
        self.assertFalse((self.root / 'escape').exists())

    def test_symlink_member_rejected(self):
        with self.assertRaisesRegex(ValueError, 'Nonregular ZIP'):
            self.extract(self.archive(symlink=True))

    def test_wrong_internal_hash_rejected(self):
        with self.assertRaisesRegex(ValueError, 'Internal payload SHA256'):
            self.extract(self.archive(wrong_sum=True))

    def test_source_identity_mismatch_rejected(self):
        package = self.extract(self.archive())
        with self.assertRaisesRegex(ValueError, 'Package source identity mismatch'):
            audit.audit_package(package, dict(self.data, source_revision='b' * 40), self.compatibility, False)

    def test_duplicate_json_metadata_rejected(self):
        self.payloads['COMPATIBILITY.json'] = b'{"app_version":"0.7.5","app_version":"0.7.5"}'
        package = self.extract(self.archive())
        with self.assertRaisesRegex(ValueError, 'Duplicate JSON field'):
            audit.audit_package(package, self.data, self.compatibility, False)

    def test_inputs_cannot_inject_paths_or_commands(self):
        values = {environment: ('0.7.5' if name == 'version' else 'a' * (40 if name == 'source_revision' else 64)) for name, (environment, _) in audit.INPUTS.items()}
        with patch.dict(os.environ, values):
            self.assertEqual(audit.inputs()['version'], '0.7.5')
            for unsafe in ['../0.7.5', '0.7.5\nEVIL=x', '0.7.5;echo bad', '$(echo bad)']:
                with patch.dict(os.environ, EXPECTED_VERSION=unsafe), self.assertRaises(ValueError):
                    audit.inputs()


if __name__ == '__main__':
    unittest.main()
