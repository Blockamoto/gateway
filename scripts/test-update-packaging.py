#!/usr/bin/env python3
"""Exercise build/package boundaries with isolated synthetic payloads; no release.

The full binary audit still belongs to audit-release.py against compiled outputs.
"""
import contextlib
import hashlib
import io
import json
from pathlib import Path
import runpy
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
WINDOWS = ('GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe', 'GatewayUpdateHelper.exe')
NOTICES = ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt')
DOCS = ('RELEASE-NOTES-v0.6.4.md', 'UPDATES.md', 'UPDATE-REVIEW-0.6.4.md', 'INTEGRATION-0.6.4-INDEX-EXPLORER.md', 'GATEWAY-ROADMAP.md', 'INDEXING.md', 'INDEX-PEER.md', 'GATEWAY-BOOTSTRAP.md', 'STATUS.md', 'BITMAP-RESEARCH.md', 'RELEASE-ACCEPTANCE.json', 'ZERO-STATE-INDEX-ACCEPTANCE.json')


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='gateway-update-packaging-')
        self.root = Path(self.temp.name).resolve()
        for folder in ('scripts', 'docs', 'browser-companion', 'internal', 'packaging/windows-installer', 'packaging/update-helper'):
            (self.root / folder).mkdir(parents=True, exist_ok=True)
        (self.root / 'VERSION.txt').write_text('0.6.4\n')
        self.compatibility = b'{"app_version":"0.6.4","storage_schema":2}\n'
        (self.root / 'COMPATIBILITY.json').write_bytes(self.compatibility)
        for notice in NOTICES:
            (self.root / notice).write_text('Required distribution notice: ' + notice + '\n')
        (self.root / 'gateway_windows_amd64.syso').write_bytes(b'fixture-icon-object')
        (self.root / 'packaging/windows-installer/gateway_windows_amd64.syso').write_bytes(b'fixture-installer-resource-object')
        (self.root / 'browser-companion/manifest.json').write_text('{"name":"fixture companion"}')
        for doc in DOCS:
            (self.root / 'docs' / doc).write_text('fixture documentation\n')
        (self.root / 'docs/RELEASE-ACCEPTANCE.json').write_text('{"version":"0.6.4","fixture":true}')
        (self.root / 'docs/TESTING-0.6.4.md').write_bytes(b'QA fixture: install, explore, browser address entry.\n')
        for name in ('TestStandalone.cmd', 'TestFreshStandalone.cmd'):
            (self.root / name).write_text('fixture launcher\n')
        shutil.copy2(ROOT / 'packaging/windows-installer/main.go.txt', self.root / 'packaging/windows-installer/main.go.txt')
        for script in ('build-release.py', 'package-release.py'):
            shutil.copy2(ROOT / 'scripts' / script, self.root / 'scripts' / script)

    def tearDown(self):
        self.temp.cleanup()

    def namespace(self, script):
        return runpy.run_path(str(self.root / 'scripts' / script))

    def test_release_build_info_rejects_personal_package_identities(self):
        validate = self.namespace('build-release.py')['validate_release_build_info']
        for value in ('_/C_/Users/example/source', '_/home/example/source', '_/root/source', 'C:\\Users\\example\\source'):
            with self.subTest(value=value), self.assertRaisesRegex(SystemExit, 'personal Go package'):
                validate('app: go1.27.1\n\tpath\t' + value + '\n\tbuild\t-trimpath=true\n')
        validate('app: go1.27.1\n\tpath\t_/C_/GatewayReleaseBuild/source\n\tbuild\t-trimpath=true\n')
        validate('app: go1.27.1\n\tpath\t_/opt/gateway-release/source\n')
        with self.assertRaisesRegex(SystemExit, 'missing or personal'):
            validate('app: no Go build information\n')

    def test_release_workspace_bounds(self):
        validate = self.namespace('build-release.py')['validate_release_workspace']
        neutral = Path(self.root.anchor) / 'GatewayReleaseBuild' / 'fixture' / 'source'
        home = neutral.parent / 'operator-home'
        with patch.object(Path, 'home', return_value=home):
            validate(neutral, neutral/'build/work', str(neutral.parent/'gopath'))
            with self.assertRaisesRegex(SystemExit, 'personal home'):
                validate(home/'source', home/'source/work', '')
            with self.assertRaisesRegex(SystemExit, 'outside GOPATH'):
                validate(neutral, neutral/'build/work', str(neutral.parent))
            with self.assertRaisesRegex(SystemExit, 'inside the neutral'):
                validate(neutral, neutral.parent/'outside-work', '')

    def test_builder_installer_payload_and_helper_targets(self):
        calls = []
        out = self.root / 'build'

        def run(command, **options):
            if command[:2] != ['go', 'build']:
                return
            target = Path(command[command.index('-o') + 1])
            calls.append((target.name, options['env']['GOOS'], command[command.index('-ldflags') + 1]))
            if target.name.endswith('-installer.exe'):
                payload = Path(options['cwd']) / 'payload'
                self.assertTrue(Path(options['cwd']).resolve().is_relative_to(self.root/'build/release-work'))
                self.assertEqual((Path(options['cwd'])/'gateway_windows_amd64.syso').read_bytes(),b'fixture-installer-resource-object')
                self.assertEqual((payload / 'COMPATIBILITY.json').read_bytes(), self.compatibility)
                self.assertEqual((payload / 'TESTING-0.6.4.md').read_bytes(), (self.root / 'docs/TESTING-0.6.4.md').read_bytes())
                for notice in NOTICES:
                    self.assertEqual((payload / notice).read_bytes(), (self.root / notice).read_bytes())
                    self.assertIn('payload/' + notice, (Path(options['cwd']) / 'main.go').read_text())
                for name in WINDOWS:
                    self.assertEqual((payload / name).read_bytes(), (out / name).read_bytes())
                self.assertIn('payload/GatewayUpdateHelper.exe', (Path(options['cwd']) / 'main.go').read_text())
                self.assertIn('payload/COMPATIBILITY.json', (Path(options['cwd']) / 'main.go').read_text())
                self.assertTrue((payload / 'bootstrap/headers-mainnet.json').is_file())
            target.write_bytes(('synthetic:' + target.name).encode())

        module = self.namespace('build-release.py')
        with patch.object(sys, 'argv', ['build-release.py', '--out', str(out), '--without-header-baseline']), patch('subprocess.run', side_effect=run), patch('subprocess.check_output', return_value='No release header baseline bundled.\n') as verify, contextlib.redirect_stdout(io.StringIO()):
            module['main']()
        self.assertEqual(verify.call_args.args[0][-1], '-verify-release-bootstrap')
        self.assertEqual(json.loads((out/'release-bootstrap.json').read_text())['headers']['count'], 0)
        self.assertIn(('gateway-update-helper', 'linux', '-s -w'), calls)
        self.assertIn(('GatewayUpdateHelper.exe', 'windows', '-s -w -H windowsgui'), calls)
        self.assertEqual(len(calls), 7)
        replacements = json.loads((out/'bootstrap/overlay.json').read_text())['Replace']
        self.assertFalse(any('headers-mainnet' in name for name in replacements))

    def test_baseline_capture_rejects_incomplete_append(self):
        source = self.root/'headers.bin'
        source.write_bytes(b'x'*160)
        Path(str(source)+'.append-pending').write_text('unfinished')
        module = self.namespace('build-release.py')
        with patch.object(sys,'argv',['build-release.py','--out',str(self.root/'build'),'--headers-baseline',str(source)]), patch('subprocess.run') as build:
            with self.assertRaisesRegex(SystemExit, 'unfinished append'):
                module['main']()
            build.assert_not_called()

    def test_builder_rejects_secret_bearing_provisioning_before_compilation(self):
        source = self.root/'channels.json'
        source.write_text(json.dumps({'schema':1,'channels':[],'private_key':'not-for-clients'}))
        module = self.namespace('build-release.py')
        with patch.object(sys,'argv',['build-release.py','--out',str(self.root/'build'),'--without-header-baseline','--update-channels',str(source)]), patch('subprocess.run') as build:
            with self.assertRaisesRegex(SystemExit,'public-only'):
                module['main']()
            build.assert_not_called()

    def test_packages_helpers_metadata_and_no_profiles(self):
        (self.root / 'docs/RELEASE-ACCEPTANCE-0.6.4.json').write_text('{"version":"0.6.4","fixture":"current"}')
        (self.root / 'docs/RELEASE-ACCEPTANCE.json').write_text('{"version":"0.6.3","fixture":"historical"}')
        (self.root / 'docs/HEADER-BASELINE.md').write_text('Repository-only baseline details.\n')
        source_status = '[Current](RELEASE-ACCEPTANCE-0.6.4.json) [Historical](RELEASE-ACCEPTANCE.json) [Baseline](HEADER-BASELINE.md#validation)\n'
        (self.root / 'docs/STATUS.md').write_text(source_status)
        build = self.root / 'build'
        build.mkdir()
        for name in WINDOWS + ('gateway-client', 'gateway-update-helper', 'gateway-client-v0.6.4-installer.exe'):
            path = build / name
            path.write_bytes(('synthetic:' + name).encode())
            path.chmod(0o755)
        (build / 'binary-audit.json').write_text('{"fixture":true}')
        (build / 'bootstrap').mkdir()
        header_payload = b'HEADER-SNAPSHOT-MUST-NOT-ENTER-APP-UPDATES' * 100
        (build / 'bootstrap/headers-mainnet.bin').write_bytes(header_payload)
        (build / 'bootstrap/headers-mainnet.json').write_text('{"schema":1,"network":"mainnet","count":0}')
        (self.root / 'data').mkdir()
        (self.root / 'data/private-signing-key').write_text('DO-NOT-PACKAGE-THIS-SECRET')
        out = self.root / 'packages'
        module = self.namespace('package-release.py')
        def git(command, **options):
            return b'' if 'status' in command else 'a' * 40 + '\n'
        with patch.object(sys, 'argv', ['package-release.py', '--build', str(build), '--out', str(out)]), patch('subprocess.check_output', side_effect=git), contextlib.redirect_stdout(io.StringIO()):
            module['main']()
        for platform, helper in [('windows-amd64', 'GatewayUpdateHelper.exe'), ('linux-amd64', 'gateway-update-helper')]:
            package_root = 'gateway-client-v0.6.4-' + platform
            with zipfile.ZipFile(out / (package_root + '.zip')) as archive:
                self.assertEqual(archive.read(package_root + '/COMPATIBILITY.json'), self.compatibility)
                self.assertEqual(archive.read(package_root + '/TESTING-0.6.4.md'), (self.root / 'docs/TESTING-0.6.4.md').read_bytes())
                self.assertEqual(json.loads(archive.read(package_root + '/RELEASE-ACCEPTANCE.json'))['fixture'], 'current')
                packaged_status = archive.read(package_root + '/STATUS.md').decode('utf-8')
                pinned_docs = 'https://github.com/Blockamoto/gateway/blob/' + 'a' * 40 + '/docs/'
                self.assertEqual(packaged_status, '[Current](RELEASE-ACCEPTANCE.json) [Historical](' + pinned_docs + 'RELEASE-ACCEPTANCE.json) [Baseline](' + pinned_docs + 'HEADER-BASELINE.md#validation)\n')
                self.assertNotIn(package_root + '/HEADER-BASELINE.md', archive.namelist())
                self.assertEqual(archive.read(package_root + '/' + helper), (build / helper).read_bytes())
                for notice in NOTICES:
                    self.assertEqual(archive.read(package_root + '/' + notice), (self.root / notice).read_bytes())
                source_record = archive.read(package_root + '/SOURCE-REVISION.txt').decode()
                self.assertEqual(source_record.splitlines()[0], 'Repository: https://github.com/Blockamoto/gateway')
                self.assertNotIn('gateway-dev', source_record)
                readme = archive.read(package_root + '/README.txt').decode()
                self.assertIn('public testing release', readme)
                self.assertNotIn('internal development', readme)
                self.assertIsNone(archive.testzip())
                for name in archive.namelist():
                    self.assertNotIn('/data/', name)
                    self.assertNotIn('/bootstrap/', name)
                    self.assertFalse(name.endswith('-installer.exe'))
                    self.assertNotIn(header_payload, archive.read(name))
                    self.assertNotIn('DO-NOT-PACKAGE-THIS-SECRET', archive.read(name).decode(errors='replace'))
                for line in archive.read(package_root + '/SHA256SUMS.txt').decode().splitlines():
                    digest, name = line.split('  ', 1)
                    self.assertEqual(hashlib.sha256(archive.read(package_root + '/' + name)).hexdigest(), digest)
            with zipfile.ZipFile(out / (package_root + '-fresh.zip')) as archive:
                self.assertEqual(archive.read(package_root + '/bootstrap/headers-mainnet.bin'), header_payload)
                for notice in NOTICES:
                    self.assertEqual(archive.read(package_root + '/' + notice), (self.root / notice).read_bytes())
        with zipfile.ZipFile(out / 'gateway-headers-v0.6.4.zip') as archive:
            self.assertEqual(set(archive.namelist()), {'headers-mainnet.bin', 'headers-mainnet.json'})
            self.assertEqual(archive.read('headers-mainnet.bin'), header_payload)
        manifest = json.loads((out / 'artifact-manifest.json').read_text())
        self.assertEqual((self.root / 'docs/STATUS.md').read_text(), source_status)
        self.assertEqual(len(manifest['artifacts']), 6)
        for artifact in manifest['artifacts']:
            self.assertEqual(hashlib.sha256((out / artifact['file']).read_bytes()).hexdigest(), artifact['sha256'])

    def test_document_links_pin_repository_sources_and_preserve_local_targets(self):
        module = self.namespace('package-release.py')
        docs = self.root / 'docs'
        current = docs / 'RELEASE-ACCEPTANCE-0.6.5.json'
        current.write_text('{"version":"0.6.5"}')
        omitted = ('SATS-AND-INSCRIPTIONS-0.6.5.md', 'HEADER-BASELINE.md', 'UPDATE-PRODUCTION-SETUP.md', 'gateway-0.6.5-brief.md')
        for name in omitted:
            (docs / name).write_text('Repository-only reference.\n')
        (docs / 'detail with spaces.md').write_text('Reference.\n')
        revision = 'b' * 40
        pinned = 'https://github.com/Blockamoto/gateway/blob/' + revision + '/docs/'
        links = ['[Reference](' + name + '#scope)' for name in omitted]
        links += ['[Current](./RELEASE-ACCEPTANCE-0.6.5.json)', '[Historical](RELEASE-ACCEPTANCE.json)', '[Bundled](./INDEXING.md#rules)', '[External](https://example.test/docs?a=1#x)', '[Anchor](#scope)', '[Mail](mailto:operator@example.test)', '[Missing](not-present.md)', '[Space](detail%20with%20spaces.md?plain=1#section)']
        source = '\n'.join(links)
        result = module['package_document_links'](source, docs / 'STATUS.md', self.root, revision, {current.resolve(): 'RELEASE-ACCEPTANCE.json', (docs / 'INDEXING.md').resolve(): 'INDEXING.md'})
        expected = ['[Reference](' + pinned + name + '#scope)' for name in omitted]
        expected += ['[Current](RELEASE-ACCEPTANCE.json)', '[Historical](' + pinned + 'RELEASE-ACCEPTANCE.json)', '[Bundled](INDEXING.md#rules)', *links[-5:-1], '[Space](' + pinned + 'detail%20with%20spaces.md?plain=1#section)']
        self.assertEqual(result, '\n'.join(expected))

    def test_dirty_source_still_refuses_packaging(self):
        module = self.namespace('package-release.py')
        out = self.root / 'refused-output'
        with patch.object(sys, 'argv', ['package-release.py', '--build', str(self.root / 'build'), '--out', str(out)]), patch('subprocess.check_output', return_value=b' M private.txt'), self.assertRaisesRegex(SystemExit, 'Commit source'):
            module['main']()
        self.assertFalse(out.exists())

    def test_historical_acceptance_cannot_be_relabelled(self):
        (self.root / 'docs/RELEASE-ACCEPTANCE.json').write_text('{"version":"0.6.3"}')
        module = self.namespace('package-release.py')
        out = self.root / 'refused-output'
        def git(command, **options):
            return b'' if 'status' in command else 'a' * 40 + '\n'
        with patch.object(sys, 'argv', ['package-release.py', '--build', str(self.root / 'build'), '--out', str(out)]), patch('subprocess.check_output', side_effect=git), self.assertRaisesRegex(SystemExit, 'Acceptance record'):
            module['main']()
        self.assertFalse(out.exists())


if __name__ == '__main__':
    unittest.main()
