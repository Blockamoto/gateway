#!/usr/bin/env python3
"""Read-only published Linux package audit; no builds, installs or releases."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import zipfile


INPUTS = {
    'version': ('EXPECTED_VERSION', r'[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}'),
    'source_revision': ('EXPECTED_SOURCE_REVISION', r'[a-f0-9]{40}'),
    'fresh_sha256': ('EXPECTED_FRESH_SHA256', r'[a-f0-9]{64}'),
    'update_sha256': ('EXPECTED_UPDATE_SHA256', r'[a-f0-9]{64}'),
    'client_sha256': ('EXPECTED_CLIENT_SHA256', r'[a-f0-9]{64}'),
    'helper_sha256': ('EXPECTED_HELPER_SHA256', r'[a-f0-9]{64}'),
}
MAX_ARCHIVE = 256 << 20
MAX_PAYLOAD = 512 << 20


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def strict_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'Duplicate JSON field: ' + key)
        result[key] = value
    return result


def load_json(path):
    require(path.stat().st_size <= 2 << 20, 'JSON metadata exceeds budget')
    return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=strict_object)


def inputs():
    result = {}
    for name, (environment, pattern) in INPUTS.items():
        value = os.environ.get(environment, '')
        require(re.fullmatch(pattern, value) is not None, 'Invalid explicit input: ' + name)
        result[name] = value
    return result


def expected_members(version, fresh):
    names = {'gateway-client', 'gateway-update-helper', 'BINARY-AUDIT.json',
             'COMPATIBILITY.json', 'SOURCE-REVISION.txt', 'SHA256SUMS.txt', 'README.txt',
             'LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt',
             'RELEASE-NOTES-v' + version + '.md', 'TESTING-' + version + '.md',
             'UPDATES.md', 'GATEWAY-ROADMAP.md', 'INDEXING.md', 'STATUS.md',
             'RELEASE-ACCEPTANCE.json'}
    if fresh:
        names.update({'bootstrap/headers-mainnet.bin', 'bootstrap/headers-mainnet.json'})
    return names


def extract_verified(archive, target, version, fresh, expected_hash):
    require(archive.stat().st_size <= MAX_ARCHIVE, 'Archive exceeds budget')
    require(sha(archive) == expected_hash, 'Published archive SHA256 mismatch')
    package = 'gateway-client-v' + version + '-linux-amd64'
    wanted = {package + '/' + name for name in expected_members(version, fresh)}
    target.mkdir(parents=True, exist_ok=False)
    with zipfile.ZipFile(archive) as source:
        infos = source.infolist()
        names = [entry.filename for entry in infos]
        require(len(names) == len(set(names)), 'Duplicate ZIP entry')
        total = 0
        for entry in infos:
            name = entry.filename
            path = PurePosixPath(name)
            require(entry.orig_filename == name and '\\' not in name and '\0' not in name,
                    'Ambiguous ZIP filename')
            require(not path.is_absolute() and '..' not in path.parts and '.' not in path.parts
                    and path.as_posix() == name and ':' not in name, 'Unsafe ZIP path')
            require(not entry.is_dir() and name in wanted, 'Unexpected ZIP member: ' + name)
            kind = stat.S_IFMT(entry.external_attr >> 16)
            require(kind in (0, stat.S_IFREG), 'Nonregular ZIP member')
            require(not entry.flag_bits & 1 and entry.compress_type in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED),
                    'Unsupported ZIP encoding')
            require(0 <= entry.file_size <= MAX_ARCHIVE, 'ZIP member exceeds budget')
            total += entry.file_size
        require(set(names) == wanted and total <= MAX_PAYLOAD, 'Exact ZIP member set/budget mismatch')
        require(source.getinfo(package + '/SHA256SUMS.txt').file_size <= 64 << 10,
                'Internal checksum manifest exceeds budget')
        sums = source.read(package + '/SHA256SUMS.txt')
        checksums = {}
        for line in sums.decode('utf-8').splitlines():
            match = re.fullmatch(r'([a-f0-9]{64})  (.+)', line)
            require(match is not None, 'Invalid internal checksum line')
            digest, name = match.groups()
            require(name in expected_members(version, fresh) - {'SHA256SUMS.txt'}
                    and name not in checksums, 'Unexpected/duplicate internal checksum')
            checksums[name] = digest
        require(set(checksums) == expected_members(version, fresh) - {'SHA256SUMS.txt'},
                'Incomplete internal checksum manifest')
        for entry in infos:
            destination = target.joinpath(*PurePosixPath(entry.filename).parts)
            require(destination.resolve().is_relative_to(target.resolve()), 'Extraction escapes target')
            destination.parent.mkdir(parents=True, exist_ok=True)
            with source.open(entry) as stream, destination.open('xb') as output:
                copied = 0
                while chunk := stream.read(1 << 20):
                    copied += len(chunk)
                    require(copied <= entry.file_size, 'ZIP payload exceeds declared size')
                    output.write(chunk)
            require(copied == entry.file_size, 'Incomplete ZIP payload')
            destination.chmod(0o600)
            relative = destination.relative_to(target / package).as_posix()
            if relative != 'SHA256SUMS.txt':
                require(sha(destination) == checksums[relative], 'Internal payload SHA256 mismatch: ' + relative)
    return target / package, checksums


def audit_package(package, data, source_compatibility, fresh):
    record = (package / 'SOURCE-REVISION.txt').read_text(encoding='utf-8')
    for label, expected in [('Repository', 'https://github.com/' + os.environ['GITHUB_REPOSITORY']),
                            ('Commit', data['source_revision']), ('Version', data['version'])]:
        require(re.findall(r'^' + label + r': (.+)$', record, re.MULTILINE) == [expected],
                'Package source identity mismatch: ' + label)
    compatibility = load_json(package / 'COMPATIBILITY.json')
    require(compatibility == source_compatibility and compatibility['app_version'] == data['version'],
            'Exact source compatibility mismatch')
    require(load_json(package / 'RELEASE-ACCEPTANCE.json')['version'] == data['version'],
            'Acceptance record version mismatch')
    audit = load_json(package / 'BINARY-AUDIT.json')
    require(audit['status'] == 'PASS', 'Binary audit is not passed')
    for name, field, expected in [('gateway-client', 'linux', data['client_sha256']),
                                   ('gateway-update-helper', 'linux_helper', data['helper_sha256'])]:
        binary = package / name
        require(sha(binary) == expected, 'Explicit executable SHA256 mismatch: ' + name)
        with binary.open('rb') as stream:
            header = stream.read(20)
        require(len(header) == 20 and header[:6] == b'\x7fELF\x02\x01'
                and int.from_bytes(header[18:20], 'little') == 62, 'Expected Linux amd64 ELF: ' + name)
        require(audit[field]['file'] == name and audit[field]['format'] == 'ELF'
                and audit[field]['sha256'] == expected and audit[field]['bytes'] == binary.stat().st_size,
                'Embedded binary audit mismatch: ' + name)
    if fresh:
        baseline = package / 'bootstrap/headers-mainnet.bin'
        meta = load_json(package / 'bootstrap/headers-mainnet.json')
        require(meta['schema'] == 1 and meta['network'] == 'mainnet'
                and type(meta['count']) is int and 1 < meta['count'] <= 2_000_000
                and baseline.stat().st_size == 80 * meta['count'] and sha(baseline) == meta['sha256'],
                'Fresh header sidecar metadata/hash mismatch')
        with baseline.open('rb') as stream:
            first = stream.read(80)
            stream.seek(-80, 2)
            last = stream.read(80)
        display = lambda raw: hashlib.sha256(hashlib.sha256(raw).digest()).digest()[::-1].hex()
        require(display(first) == '000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f'
                and display(last) == meta['tip_hash'], 'Header endpoint identity mismatch')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['validate', 'audit'])
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--source', type=Path)
    parser.add_argument('--work', type=Path)
    args = parser.parse_args()
    args.evidence.mkdir(parents=True, exist_ok=True)
    data = inputs()
    (args.evidence / 'inputs.json').write_text(json.dumps(data, indent=2) + '\n')
    if args.mode == 'validate':
        print('Explicit version, full source revision and four SHA256 inputs validated.')
        return
    require(args.source and args.work, 'Audit requires source and work directories')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=args.source, text=True).strip()
    require(revision == data['source_revision'] and (args.source / 'VERSION.txt').read_text().strip() == data['version'],
            'Checked-out source identity mismatch')
    compatibility = load_json(args.source / 'COMPATIBILITY.json')
    repository = os.environ['GITHUB_REPOSITORY']
    require(re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository) is not None, 'Invalid repository')
    tag = 'v' + data['version']
    release = json.loads(subprocess.check_output(['gh', 'release', 'view', tag, '--repo', repository,
                       '--json', 'tagName,isDraft,isPrerelease,assets,url'], text=True))
    require(release['tagName'] == tag and not release['isDraft'], 'Release must be hosted and published')
    (args.evidence / 'release.json').write_text(json.dumps(release, indent=2) + '\n')
    downloads = args.work / 'downloads'
    downloads.mkdir(parents=True, exist_ok=False)
    reports, packages = {}, {}
    for kind in ['fresh', 'update']:
        filename = 'gateway-client-v' + data['version'] + '-linux-amd64' + ('-fresh' if kind == 'fresh' else '') + '.zip'
        assets = [asset for asset in release['assets'] if asset['name'] == filename]
        require(len(assets) == 1 and 0 < assets[0]['size'] <= MAX_ARCHIVE, 'Missing/oversized exact release asset')
        subprocess.run(['gh', 'release', 'download', tag, '--repo', repository, '--pattern', filename,
                        '--dir', str(downloads)], check=True, timeout=180)
        archive = downloads / filename
        require(archive.stat().st_size == assets[0]['size'], 'Published asset byte count mismatch')
        package, checksums = extract_verified(archive, args.work / kind, data['version'], kind == 'fresh', data[kind + '_sha256'])
        audit_package(package, data, compatibility, kind == 'fresh')
        reports[kind] = {'asset': filename, 'bytes': archive.stat().st_size, 'sha256': sha(archive),
                         'package': str(package), 'payload_sha256s': checksums}
        packages[kind] = package
    for name, expected in reports['update']['payload_sha256s'].items():
        require(reports['fresh']['payload_sha256s'][name] == expected, 'Fresh/update payload differs: ' + name)
    for package in packages.values():
        for name in ['gateway-client', 'gateway-update-helper']:
            (package / name).chmod(0o755)
    (args.evidence / 'package-audit.json').write_text(json.dumps({'status': 'PASS', **data, 'archives': reports}, indent=2) + '\n')
    with open(os.environ['GITHUB_ENV'], 'a', encoding='utf-8') as environment:
        environment.write('PUBLISHED_FRESH_DIR=' + str(packages['fresh']) + '\n')
        environment.write('PUBLISHED_UPDATE_DIR=' + str(packages['update']) + '\n')
    print('Both published archives passed external/internal hashes, exact safe extraction, source/version/compatibility and binary identity checks.')


if __name__ == '__main__':
    main()
