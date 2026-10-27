#!/usr/bin/env python3
"""Validate generated packages with the actual released 0.7.7 archive validator.

Uses only temporary synthetic packages and locally available Git source/Go.
The negative control reintroduces preview metadata and must be rejected.
"""
import argparse
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

PREVIOUS_REVISION = '0f60051a2560c43b5e78f1b3dc80e14fda64f6a4'
ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go')
    parser.add_argument('--packages', type=Path, help='Also validate rebuilt compact Windows/Linux ZIPs')
    parser.add_argument('--version', default='0.7.8', help='Version for --packages')
    args = parser.parse_args()
    spec = importlib.util.spec_from_file_location('packaging_fixture', ROOT/'scripts/test-update-packaging.py')
    fixture = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fixture)
    case = fixture.PackagingTests('test_packages_helpers_metadata_and_no_profiles')
    case.setUp()
    try:
        case.test_packages_helpers_metadata_and_no_profiles()
        with tempfile.TemporaryDirectory(prefix='gateway-released-validator-') as temporary:
            oldroot = Path(temporary)
            source = subprocess.check_output(['git', 'archive', '--format=zip', PREVIOUS_REVISION, 'internal'], cwd=ROOT)
            with zipfile.ZipFile(io.BytesIO(source)) as archive:
                archive.extractall(oldroot)
            (oldroot/'check').mkdir()
            mainfile = oldroot/'check/main.go'
            mainfile.write_text('''package main
import("fmt"; "os"; "../internal/updateapply")
func main(){ if err:=updateapply.ValidateArchive(os.Args[1], os.Args[2], os.Args[3]); err!=nil {fmt.Fprintln(os.Stderr,err);os.Exit(1)} }
''')
            environment = {key:value for key,value in os.environ.items() if key in
                           ('PATH','SYSTEMROOT','WINDIR','USERPROFILE','HOME','TEMP','TMP','GOCACHE','GOPATH')}
            environment['GO111MODULE'] = 'off'
            environment['CGO_ENABLED'] = '0'
            environment['GOCACHE'] = str(oldroot/'go-cache')
            binary = oldroot / ('validator.exe' if os.name == 'nt' else 'validator')
            subprocess.run([args.go, 'build', '-trimpath', '-o', str(binary), str(mainfile)],
                           cwd=oldroot, env=environment, check=True)
            for platform in ('windows-amd64','linux-amd64'):
                package = case.root/'packages'/f'gateway-client-v0.6.4-{platform}.zip'
                subprocess.run([str(binary), str(package), '0.6.4', platform], check=True, env=environment)
                if args.packages:
                    released = args.packages.resolve()/f'gateway-client-v{args.version}-{platform}.zip'
                    subprocess.run([str(binary), str(released), args.version, platform], check=True, env=environment)
            good = case.root/'packages/gateway-client-v0.6.4-windows-amd64.zip'
            bad = oldroot/'with-preview.zip'
            bad.write_bytes(good.read_bytes())
            with zipfile.ZipFile(bad, 'a') as archive:
                archive.writestr('gateway-client-v0.6.4-windows-amd64/browser-companion/PREVIEW-IDENTITY.json', '{}')
            rejected = subprocess.run([str(binary),str(bad),'0.6.4','windows-amd64'],capture_output=True,text=True,env=environment)
            if rejected.returncode == 0 or 'unexpected update payload' not in rejected.stderr:
                raise AssertionError('Released validator failed to reject preview metadata negative control')
            print('PASS: generated Windows/Linux packages accepted by exact released 0.7.7 validator; preview metadata rejected')
    finally:
        case.tearDown()


if __name__ == '__main__':
    main()
