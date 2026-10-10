#!/usr/bin/env python3
"""Package committed Gateway binaries without copying any runtime profile."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
from urllib.parse import quote, unquote, urlsplit
import zipfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def package_document_links(text, source, root, revision, bundled_sources):
    """Keep existing simple inline links useful without widening update archives.

    Match by source identity: the historical generic acceptance document is not
    the version-specific document copied under that name in a newer package.
    """
    def replace(match):
        target = match.group(2)
        parsed = urlsplit(target)
        if parsed.scheme or parsed.netloc or not parsed.path or parsed.path.startswith('/'):
            return match.group(0)
        resolved = (source.parent / unquote(parsed.path)).resolve()
        try:
            relative = resolved.relative_to(root)
        except ValueError:
            return match.group(0)
        if not resolved.is_file():
            return match.group(0)
        destination = bundled_sources.get(resolved)
        if destination is None:
            destination = 'https://github.com/Blockamoto/gateway/blob/' + revision + '/' + quote(relative.as_posix(), safe='/')
        else:
            destination = quote(destination, safe='/')
        return match.group(1) + destination + target[len(parsed.path):] + match.group(3)

    # The repository's document links use this ordinary inline form. Leave
    # other Markdown constructs intact rather than approximating a full parser.
    return re.sub(r'(\]\()([^\s)]+)(\))', replace, text)


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
    acceptance = root / 'docs' / f'RELEASE-ACCEPTANCE-{version}.json'
    if not acceptance.exists():
        acceptance = root / 'docs' / 'RELEASE-ACCEPTANCE.json'
    if json.loads(acceptance.read_text())['version'] != version:
        raise SystemExit('Acceptance record must describe the version being packaged.')
    build, out = args.build.resolve(), args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    installer = f'gateway-client-v{version}-installer.exe'
    documents = [f'RELEASE-NOTES-v{version}.md', 'UPDATES.md', 'GATEWAY-ROADMAP.md', 'INDEXING.md', 'STATUS.md', 'RELEASE-ACCEPTANCE.json']
    if (root / 'docs' / f'TESTING-{version}.md').exists():
        documents.append(f'TESTING-{version}.md')
    document_sources = {document: acceptance if document == 'RELEASE-ACCEPTANCE.json' else root / 'docs' / document for document in documents}
    bundled_sources = {source.resolve(): document for document, source in document_sources.items()}
    artifacts = []
    for platform, binaries in [('windows-amd64', ['GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe', 'GatewayUpdateHelper.exe']), ('linux-amd64', ['gateway-client', 'gateway-update-helper'])]:
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
        for document, source in document_sources.items():
            if source.suffix == '.md':
                text = package_document_links(source.read_text(encoding='utf-8'), source, root, revision, bundled_sources)
                (folder / document).write_bytes(text.encode('utf-8'))
            else:
                shutil.copy2(source, folder / document)
        shutil.copy2(build / 'binary-audit.json', folder / 'BINARY-AUDIT.json')
        shutil.copy2(root / 'COMPATIBILITY.json', folder / 'COMPATIBILITY.json')
        for notice in ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt'):
            shutil.copy2(root / notice, folder / notice)
        (folder / 'SOURCE-REVISION.txt').write_text(f'Repository: https://github.com/Blockamoto/gateway\nCommit: {revision}\nVersion: {version}\nPackaged UTC: {datetime.datetime.now(datetime.timezone.utc).isoformat()}\n')
        (folder / 'README.txt').write_text(f'''Gateway Client {version}

Extract the whole archive. On Windows, GatewayOnDemand.exe is the portable
launcher. The separate fresh-install archive and installer include a validated
header snapshot; this application-only update archive preserves your existing
header store without downloading that snapshot again.
On Linux, chmod +x gateway-client gateway-update-helper, then run ./gateway-client.
Keep the bundled update helper beside the client executable. This release
configures the bundled Render publisher and public verification key on first
launch. Fresh profiles check automatically and let you choose when to install.
The Windows installer has a dedicated Updates page, then a final review. It
also offers automatic installation or manual updates; upgrades default to keeping
existing preferences. Expand Advanced: update source to keep the current source,
use Gateway's hosted service, or enter a custom publisher and independently
verified public key. Invalid source details block installation before writes.
The selection applies once; later Settings changes remain in effect. A conflicting
source change waits for staged-update or recovery work to retain its original
verification authority. Access credentials are configured only in Settings.
No publisher signing secrets or GitHub credentials belong in a client package.

This prerelease enables Headers, Bitcoin Blocks and Inscriptions. The shared
schema, block explorer and positional inscription viewport remain available.
Related inscription transaction locator construction, the complete Transaction
Index and Gateway-to-Gateway peerhood remain gated until separately promoted.
Ordinary Bitcoin peer header and block access remains available.
Read TESTING-{version}.md for the QA checklist, including browser registration
and typing .bitcoin resource addresses in your browser's address bar.

STATUS.md and RELEASE-ACCEPTANCE.json list completed checks and testing limits.
STATUS.md also records publication status and the currently hosted release.
Windows binaries are not Authenticode signed. Publisher-signed update metadata
is verified separately using the public key bundled with this release.
Only checks recorded for this release describe its acceptance. Review the known
limits before testing; results from other builds do not establish this build's
installation, browser integration or antivirus acceptance.
Windows tray actions run natively inside Gateway. Optional Windows DNS setup
still requires approval and changes only Gateway-owned routing rules.
SOURCE-REVISION.txt identifies
the exact source. No wallet, Core data, credentials or node profile is included.
LICENSE contains Gateway's MIT license. THIRD-PARTY-NOTICES.txt and GO-LICENSE.txt
contain the accompanying software notices.
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
        # Fresh installs receive a separate sidecar snapshot. Never put the
        # installer (which embeds it) or any baseline file in the update ZIP.
        fresh = out / (name + '-fresh.zip')
        with zipfile.ZipFile(fresh, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            fresh_hashes = []
            for path in sorted(p for p in folder.rglob('*') if p.is_file()):
                if path.name == 'SHA256SUMS.txt':
                    continue
                z.write(path, path.relative_to(out).as_posix())
                fresh_hashes.append(f'{digest(path)}  {path.relative_to(folder).as_posix()}\n')
            for item in ('headers-mainnet.bin', 'headers-mainnet.json'):
                z.write(build/'bootstrap'/item, name+'/bootstrap/'+item)
                fresh_hashes.append(f'{digest(build/"bootstrap"/item)}  bootstrap/{item}\n')
            z.writestr(name+'/SHA256SUMS.txt', ''.join(fresh_hashes))
        artifacts.append(fresh)
    # The installer is also a direct GitHub asset, avoiding source-ZIP confusion.
    shutil.copy2(build / installer, out / installer)
    artifacts.append(out / installer)
    headers = out / f'gateway-headers-v{version}.zip'
    with zipfile.ZipFile(headers, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for item in ('headers-mainnet.bin', 'headers-mainnet.json'):
            z.write(build/'bootstrap'/item, item)
    artifacts.append(headers)
    manifest = {'version': version, 'source_revision': revision, 'verification': 'zip_crc_and_all_file_hashes_pass', 'artifacts': [{'file': p.name, 'bytes': p.stat().st_size, 'sha256': digest(p)} for p in artifacts]}
    (out / 'SHA256SUMS.txt').write_text(''.join(f"{r['sha256']}  {r['file']}\n" for r in manifest['artifacts']))
    (out / 'artifact-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
