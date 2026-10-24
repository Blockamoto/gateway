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
    parser.add_argument('--out', type=Path, default=ROOT / 'build' / 'portable-v0.7.8-reviewed')
    parser.add_argument('--archive', type=Path, help='separate ZIP destination; existing archives are preserved')
    args = parser.parse_args()
    out = args.out.resolve()
    version = (ROOT / 'VERSION.txt').read_text().strip()
    archive = (args.archive or out.parent / ('Gateway-v' + version + '-reviewed-portable-preview-windows-amd64.zip')).resolve()
    if archive.exists():
        parser.error('archive already exists; choose a new --archive path')
    out.mkdir(parents=True, exist_ok=True)
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
    shutil.copy2(ROOT / 'docs' / 'APPROVED-BRIEF-0.7.8.md', out / 'APPROVED-BRIEF-0.7.8.md')
    shutil.copy2(ROOT / 'docs' / 'SPARSE-INDEXING-0.7.8.md', out / 'SPARSE-INDEXING-0.7.8.md')
    shutil.copy2(ROOT / 'docs' / 'VERIFICATION-0.7.8.md', out / 'VERIFICATION-0.7.8.md')
    performance_docs = ('APPROVED-PERFORMANCE-BRIEF-0.7.8.md', 'INDEX-REPLAY-PERFORMANCE.md',
                        'TRANSACTION-LOCATOR-BATCHING.md', 'VERIFICATION-PERFORMANCE-0.7.8.md',
                        'APPROVED-DESIGN-BRIEF-0.7.8.md', 'VERIFICATION-DESIGN-0.7.8.md',
                        'COMBINED-TRACKS-0.7.8.md', 'PARENT-CATEGORIES-0.7.8.md',
                        'VISUAL-POLISH-0.7.8.md')
    for name in performance_docs:
        shutil.copy2(ROOT / 'docs' / name, out / name)
    shutil.copy2(ROOT / 'scripts' / 'portable-preview.ps1', out / 'Preview.ps1')
    (out / 'Start-Preview.cmd').write_text('@echo off\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Preview.ps1"\n', encoding='ascii')
    (out / 'Stop-Preview.cmd').write_text('@echo off\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Preview.ps1" -Stop\n', encoding='ascii')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT, text=True))
    names = ['GatewayClient.exe', 'LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt',
             'COMPATIBILITY.json', 'TESTING-0.7.8.md', 'APPROVED-BRIEF-0.7.8.md', 'SPARSE-INDEXING-0.7.8.md', 'VERIFICATION-0.7.8.md', 'Preview.ps1', 'Start-Preview.cmd', 'Stop-Preview.cmd']
    names.extend(performance_docs)
    manifest = dict(version=version, kind='local-portable-development-preview', source_revision=revision,
                    source_dirty=dirty, updates='disabled at build time', installers=False,
                    files={name: hashlib.sha256((out / name).read_bytes()).hexdigest() for name in names})
    (out / 'artifact-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as package:
        for name in names + ['artifact-manifest.json']:
            package.write(out / name, arcname=name)
    print(json.dumps(dict(directory=str(out), archive=str(archive),
                         sha256=hashlib.sha256(archive.read_bytes()).hexdigest(), **manifest), indent=2))


if __name__ == '__main__':
    main()
