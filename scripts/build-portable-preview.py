#!/usr/bin/env python3
"""Build a local Windows portable preview, without installers or update delivery."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import zipfile


ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go', help='existing Go executable')
    parser.add_argument('--out', type=Path, default=ROOT / 'build' / 'portable-v0.7.8')
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    version = (ROOT / 'VERSION.txt').read_text().strip()
    subprocess.run([sys.executable, str(ROOT / 'scripts' / 'build-icon-resources.py'),
                    '--out', str(ROOT / 'gateway_windows_amd64.syso')], cwd=ROOT, check=True,
                   stdout=subprocess.DEVNULL)
    env = dict(os.environ, GO111MODULE='off', CGO_ENABLED='0', GOOS='windows', GOARCH='amd64')
    subprocess.run([args.go, 'build', '-trimpath', '-ldflags',
                    '-s -w -X main.portablePreviewBuild=' + version,
                    '-o', str(out / 'GatewayClient.exe'), '.'], cwd=ROOT, env=env, check=True)
    for name in ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt', 'COMPATIBILITY.json'):
        shutil.copy2(ROOT / name, out / name)
    shutil.copy2(ROOT / 'docs' / 'TESTING-0.7.8.md', out / 'TESTING-0.7.8.md')
    shutil.copy2(ROOT / 'docs' / 'BITMAP-CHAIN-DEV-V1.md', out / 'BITMAP-CHAIN-DEV-V1.md')
    shutil.copy2(ROOT / 'docs' / 'VERIFICATION-0.7.8.md', out / 'VERIFICATION-0.7.8.md')
    shutil.copy2(ROOT / 'scripts' / 'portable-preview.ps1', out / 'Preview.ps1')
    (out / 'Start-Preview.cmd').write_text('@echo off\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Preview.ps1"\n', encoding='ascii')
    (out / 'Stop-Preview.cmd').write_text('@echo off\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Preview.ps1" -Stop\n', encoding='ascii')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT, text=True))
    names = ['GatewayClient.exe', 'LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt',
             'COMPATIBILITY.json', 'TESTING-0.7.8.md', 'BITMAP-CHAIN-DEV-V1.md', 'VERIFICATION-0.7.8.md', 'Preview.ps1', 'Start-Preview.cmd', 'Stop-Preview.cmd']
    manifest = dict(version=version, kind='local-portable-development-preview', source_revision=revision,
                    source_dirty=dirty, updates='disabled at build time', installers=False,
                    files={name: hashlib.sha256((out / name).read_bytes()).hexdigest() for name in names})
    (out / 'artifact-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    archive = out.parent / ('Gateway-v' + version + '-portable-preview-windows-amd64.zip')
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as package:
        for name in names + ['artifact-manifest.json']:
            package.write(out / name, arcname=name)
    print(json.dumps(dict(directory=str(out), archive=str(archive),
                         sha256=hashlib.sha256(archive.read_bytes()).hexdigest(), **manifest), indent=2))


if __name__ == '__main__':
    main()
