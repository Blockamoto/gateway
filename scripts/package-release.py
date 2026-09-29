#!/usr/bin/env python3
"""Package committed Gateway binaries without copying any runtime profile."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import zipfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--build', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=root).strip():
        raise SystemExit('Commit source and validation records before packaging.')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    version = (root / 'VERSION.txt').read_text().strip()
    build, out = args.build.resolve(), args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    installer = f'gateway-client-v{version}-installer.exe'
    artifacts = []
    for platform, binaries in [('windows-amd64', ['GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe', installer]), ('linux-amd64', ['gateway-client'])]:
        name = f'gateway-client-v{version}-{platform}'
        folder = out / name
        if folder.exists():
            raise SystemExit(f'Package directory already exists: {folder}')
        folder.mkdir()
        for binary in binaries:
            shutil.copy2(build / binary, folder / binary)
        if platform.startswith('windows'):
            for launcher in ['TestStandalone.cmd', 'TestFreshStandalone.cmd']:
                shutil.copy2(root / launcher, folder / launcher)
            shutil.copytree(root / 'browser-companion', folder / 'browser-companion')
            (folder / 'portable.marker').write_text('Gateway portable profile.\n')
        for document in ['INDEXING.md', 'INDEX-PEER.md', 'GATEWAY-BOOTSTRAP.md', 'STATUS.md', 'BITMAP-RESEARCH.md', 'RELEASE-ACCEPTANCE.json', 'ZERO-STATE-INDEX-ACCEPTANCE.json']:
            shutil.copy2(root / 'docs' / document, folder / document)
        shutil.copy2(build / 'binary-audit.json', folder / 'BINARY-AUDIT.json')
        (folder / 'SOURCE-REVISION.txt').write_text(f'Repository: https://github.com/Blockamoto/gateway\nCommit: {revision}\nVersion: {version}\nPackaged UTC: {datetime.datetime.now(datetime.timezone.utc).isoformat()}\n')
        executable = 'GatewayClient.exe' if platform.startswith('windows') else './gateway-client'
        (folder / 'README.txt').write_text(f'''Gateway Client {version}

Extract the whole archive. On Windows, GatewayOnDemand.exe is the portable
launcher; {installer} is the optional installer with the same client payload.
On Linux, chmod +x gateway-client, then run ./gateway-client.

Open Indexes in the app, review Bitmap rules and plan, then choose a bounded
range and source retention. Core and Ord are optional. Example terminal flow:
  {executable} index plan bitmap --to 792435 --retention ephemeral
  {executable} index build bitmap --to 792435 --retention ephemeral
  {executable} index lookup bitmap 0

New derived records are private. Publishing an index freezes an explicit
snapshot; global serving must also be enabled for peer access. Configure your
own reachable Gateway entrances in Discovery. No project server is bundled.

STATUS.md and RELEASE-ACCEPTANCE.json list actual verification and alpha limits.
This is an unsigned prerelease, not a claim of completed historical compatibility
or native installer/visual browser acceptance. SOURCE-REVISION.txt identifies
the exact source. No wallet, Core data, credentials or node profile is included.
''')
        files = sorted(p for p in folder.rglob('*') if p.is_file())
        (folder / 'SHA256SUMS.txt').write_text(''.join(f'{digest(p)}  {p.relative_to(folder).as_posix()}\n' for p in files))
        archive = out / (name + '.zip')
        with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for path in sorted(p for p in folder.rglob('*') if p.is_file()):
                z.write(path, path.relative_to(out).as_posix())
        with zipfile.ZipFile(archive) as z:
            assert z.testzip() is None
            for path in sorted(p for p in folder.rglob('*') if p.is_file()):
                assert hashlib.sha256(z.read(path.relative_to(out).as_posix())).hexdigest() == digest(path)
        artifacts.append(archive)
    # The installer is also a direct GitHub asset, avoiding source-ZIP confusion.
    shutil.copy2(build / installer, out / installer)
    artifacts.append(out / installer)
    manifest = {'version': version, 'source_revision': revision, 'verification': 'zip_crc_and_all_file_hashes_pass', 'artifacts': [{'file': p.name, 'bytes': p.stat().st_size, 'sha256': digest(p)} for p in artifacts]}
    (out / 'SHA256SUMS.txt').write_text(''.join(f"{r['sha256']}  {r['file']}\n" for r in manifest['artifacts']))
    (out / 'artifact-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
