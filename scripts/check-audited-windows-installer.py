#!/usr/bin/env python3
"""Fail-closed hosted reconstruction and disposable install/uninstall check.

Only the independently audited 6c33011 installer is eligible for execution.
This script runs only in the dedicated hosted workflow context.
"""
import argparse
import csv
import hashlib
import json
import os
from pathlib import Path
import runpy
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import urllib.request
import zipfile

REVISION = '6c33011a9cb3878d6246031f23938ca11ec5ba81'
SOURCE = Path(r'C:\GatewayReleaseBuild\0.7.8\source-6c33011')
STAGE = SOURCE / 'build' / 'release-work' / 'gateway-installer-o5z77qy_'
INSTALLER_SHA = '0634e5c3c5c3456e2ab22220b8d5042b95988543d31cd6b991b73f9262d4336c'
CLIENT_SHA = 'a7d44aee1475b82e940d536c18a288d0b746e29e0730f67d81b3c013d631f520'
HEADER_SHA = '64889ec9d2d520cb7e8ffef7c4cd30e07bbeb5f53ead05404fac70e66498d467'
HEADER_META_SHA = '7e61b63a39064be8633338cd3ee6794e8ee2cc845561905cfc4ae40793912679'
CHANNEL_SHA = 'cdb721f84a91b5397e003d54cadbc4cfbb6599d1f8bcd068aa6c54537d30ee0f'
ARCHIVE_SHA = 'e9464cad710d0b8cf5cfa94660b8238bc4728c04d16baecb8a56b48d03d48d2f'
ARCHIVE_SIZE = 65906545
HEADER_SIZE = 969480 * 80
HEADER_TIP = '00000000000000000000dcddaef250e1665ecc5dadbcbdb08769509c0a433253'
URL = 'https://github.com/Blockamoto/gateway/releases/download/v0.7.7/gateway-headers-v0.7.7.zip'
EXPECTED = {
    'GatewayClient.exe': CLIENT_SHA,
    'GatewayUpdateHelper.exe': 'c347d1b8bb299dcbc3acc008e8f0407364cbb62e7d09244cefcb739dfc651419',
    'GatewayOnDemand.exe': '904bbd88e09c5c269530ff763154ca636e6916be42b3457bc3b5f7fb7fd45570',
    'GatewayNativeHost.exe': 'f3ebe50b17e95b9178cfb83a7562eadf4d17ce5b5f4b88e3f4adf0678a4a1cd2',
}


def digest(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def require_hash(path, expected):
    path = Path(path)
    if path.is_symlink() or not path.is_file() or digest(path) != expected:
        raise RuntimeError('Required audited SHA-256 mismatch; execution refused: ' + path.name)


def hosted_only():
    if (sys.platform != 'win32' or os.environ.get('GITHUB_ACTIONS') != 'true'
            or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted'
            or os.environ.get('GITHUB_REPOSITORY') != 'Blockamoto/gateway'
            or os.environ.get('GITHUB_REF') != 'refs/heads/codex/windows-installer-check'
            or os.environ.get('GITHUB_EVENT_NAME') != 'push'):
        raise RuntimeError('This operation requires the dedicated GitHub-hosted Windows job')


def reject_links(path):
    path = Path(os.path.abspath(path))
    for part in reversed((path, *path.parents)):
        info = os.lstat(part)
        if part.is_symlink() or getattr(info, 'st_file_attributes', 0) & 0x400:
            raise RuntimeError('Link or reparse point refused: ' + str(part))
    return path


def new_workspace():
    base = reject_links(os.environ['RUNNER_TEMP'])
    out = base / 'gateway-audited-installer-check'
    if out.exists() or out.is_symlink():
        raise RuntimeError('Disposable output already exists')
    out.mkdir(mode=0o700)
    return out


def fetch_baseline(out):
    archive = out / 'headers-077.zip'
    h = hashlib.sha256()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(URL, timeout=120) as response, archive.open('xb') as dst:
        if not response.geturl().startswith('https://'):
            raise RuntimeError('Header download redirected away from HTTPS')
        total = 0
        while chunk := response.read(1024 * 1024):
            total += len(chunk)
            if total > ARCHIVE_SIZE:
                raise RuntimeError('Header archive exceeds approved size')
            h.update(chunk)
            dst.write(chunk)
    if total != ARCHIVE_SIZE or h.hexdigest() != ARCHIVE_SHA:
        raise RuntimeError('Public header archive does not match approved bytes')
    with zipfile.ZipFile(archive) as z:
        if z.testzip() is not None or sorted(z.namelist()) != ['headers-mainnet.bin', 'headers-mainnet.json']:
            raise RuntimeError('Header archive contents or CRC differ')
        if z.getinfo('headers-mainnet.bin').file_size != HEADER_SIZE or z.getinfo('headers-mainnet.json').file_size > 4096:
            raise RuntimeError('Header archive member sizes differ')
        if hashlib.sha256(z.read('headers-mainnet.json')).hexdigest() != HEADER_META_SHA:
            raise RuntimeError('Public header metadata differs from audited metadata')
        header = out / 'headers-mainnet.bin'
        with z.open('headers-mainnet.bin') as src, header.open('xb') as dst:
            shutil.copyfileobj(src, dst, 1024 * 1024)
    require_hash(header, HEADER_SHA)
    with header.open('rb') as f:
        f.seek(-80, os.SEEK_END)
        last = f.read(80)
    if hashlib.sha256(hashlib.sha256(last).digest()).digest()[::-1].hex() != HEADER_TIP:
        raise RuntimeError('Header tip differs')
    return header


def rebuild(out, header):
    reject_links(SOURCE)
    if os.path.normcase(str(SOURCE.resolve())) != os.path.normcase(str(SOURCE)):
        raise RuntimeError('Candidate source path differs from audited source path')
    if subprocess.check_output(['go', 'version'], text=True).strip() != 'go version go1.27.1 windows/amd64':
        raise RuntimeError('Unexpected Go toolchain')
    require_hash(SOURCE / 'assets' / 'bootstrap' / 'update-channels.json', CHANNEL_SHA)
    if STAGE.exists() or STAGE.is_symlink():
        raise RuntimeError('Original fixed installer staging path is already in use')
    build = out / 'build'
    if build.exists():
        raise RuntimeError('Build output exists')
    client = build / 'GatewayClient.exe'
    verify_root = out / 'bootstrap-verification-user'
    verify_root.mkdir()
    for name in ('temp', 'local', 'roaming', 'home'):
        (verify_root / name).mkdir()
    allowed = ('SYSTEMROOT', 'WINDIR', 'SYSTEMDRIVE', 'PATH', 'PATHEXT', 'COMSPEC')
    verify_env = {name: os.environ[name] for name in allowed if name in os.environ}
    verify_env.update(TEMP=str(verify_root/'temp'), TMP=str(verify_root/'temp'),
                      LOCALAPPDATA=str(verify_root/'local'), APPDATA=str(verify_root/'roaming'),
                      USERPROFILE=str(verify_root/'home'), HOME=str(verify_root/'home'))
    original_temporary, original_check_output, original_argv = tempfile.TemporaryDirectory, subprocess.check_output, sys.argv

    class FixedOriginalStage:
        def __init__(self, prefix=None, dir=None):
            if prefix != 'gateway-installer-' or Path(dir).resolve() != STAGE.parent.resolve():
                raise RuntimeError('Unexpected installer stage request')
        def __enter__(self):
            for name, expected in EXPECTED.items():
                require_hash(build / name, expected)
            require_hash(build / 'bootstrap' / 'headers-mainnet.bin', HEADER_SHA)
            require_hash(build / 'bootstrap' / 'headers-mainnet.json', HEADER_META_SHA)
            STAGE.mkdir()
            return str(STAGE)
        def __exit__(self, *args):
            # The entire ephemeral runner is discarded; retain this owned stage for diagnostics.
            return False

    def guarded_check_output(command, *args, **kwargs):
        if command and os.path.normcase(str(command[0])) == os.path.normcase(str(client)):
            if list(command[1:]) != ['-verify-release-bootstrap']:
                raise RuntimeError('Unexpected candidate client command')
            require_hash(client, CLIENT_SHA)
            kwargs['env'] = verify_env
        return original_check_output(command, *args, **kwargs)

    os.environ.update(GO111MODULE='off', CGO_ENABLED='0', GOARCH='amd64', GOAMD64='v1',
                      GOFLAGS='', GOEXPERIMENT='', SOURCE_DATE_EPOCH='1792010749')
    try:
        tempfile.TemporaryDirectory = FixedOriginalStage
        subprocess.check_output = guarded_check_output
        sys.argv = [str(SOURCE / 'scripts' / 'build-release.py'), '--out', str(build),
                    '--headers-baseline', str(header), '--update-channels',
                    str(SOURCE / 'assets' / 'bootstrap' / 'update-channels.json')]
        runpy.run_path(sys.argv[0], run_name='__main__')
    finally:
        tempfile.TemporaryDirectory, subprocess.check_output, sys.argv = original_temporary, original_check_output, original_argv
    for name, expected in EXPECTED.items():
        require_hash(build / name, expected)
    require_hash(build / 'bootstrap' / 'headers-mainnet.bin', HEADER_SHA)
    require_hash(build / 'bootstrap' / 'headers-mainnet.json', HEADER_META_SHA)
    installer = build / 'gateway-client-v0.7.8-installer.exe'
    require_hash(installer, INSTALLER_SHA)
    return build, installer


def integration_clear():
    import winreg
    keys = [r'Software\Microsoft\Windows\CurrentVersion\Uninstall\GatewayClient',
            r'Software\Classes\god', r'Software\Classes\ond']
    keys += ['Software\\' + vendor + r'\NativeMessagingHosts\com.gateway.client'
             for vendor in (r'Google\Chrome', r'Microsoft\Edge', 'Chromium', r'BraveSoftware\Brave-Browser')]
    for key in keys:
        try:
            with winreg.OpenKey(winreg.HKEY_CURRENT_USER, key):
                raise RuntimeError('Shared integration already exists: ' + key)
        except FileNotFoundError:
            pass
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, r'Software\Microsoft\Windows\CurrentVersion\Run') as key:
            for name in ('GatewayClient', 'BlocksOnDemand'):
                try:
                    winreg.QueryValueEx(key, name)
                except FileNotFoundError:
                    continue
                raise RuntimeError('Shared startup entry already exists: ' + name)
    except FileNotFoundError:
        pass


def shortcut_target(shortcut):
    # Read only the bounded local LinkInfo target. This does not resolve an IDList.
    with shortcut.open('rb') as f:
        data = f.read(1024*1024+1)
    if len(data) > 1024*1024 or len(data) < 76:
        raise RuntimeError('Invalid shortcut size')
    if data[:20] != bytes.fromhex('4c0000000114020000000000c000000000000046'):
        raise RuntimeError('Invalid shortcut header')
    flags = struct.unpack_from('<I', data, 20)[0]
    if not flags & 2 or flags & (0x20 | 0x200):
        raise RuntimeError('Unsupported shortcut target form')
    pos = 76
    if flags & 1:
        if pos+2 > len(data):
            raise RuntimeError('Truncated shortcut IDList')
        pos += 2+struct.unpack_from('<H', data, pos)[0]
    if pos+28 > len(data):
        raise RuntimeError('Truncated shortcut LinkInfo')
    size, header, infoflags, _, local, _, suffix = struct.unpack_from('<7I', data, pos)
    if size < 28 or header < 28 or header > size or pos+size > len(data) or infoflags != 1:
        raise RuntimeError('Unsupported shortcut LinkInfo')
    info = data[pos:pos+size]
    unicode_paths = header >= 36
    if unicode_paths:
        local, suffix = struct.unpack_from('<II', info, 28)

    def read_string(offset):
        if offset < header or offset >= size:
            raise RuntimeError('Invalid shortcut path offset')
        if unicode_paths:
            end = offset
            while end+1 < size and info[end:end+2] != b'\0\0':
                end += 2
            if end+1 >= size:
                raise RuntimeError('Unterminated shortcut path')
            return info[offset:end].decode('utf-16-le', errors='strict')
        end = info.find(b'\0', offset)
        if end < 0:
            raise RuntimeError('Unterminated shortcut path')
        # The owned runner paths are ASCII; do not guess a code page.
        return info[offset:end].decode('ascii', errors='strict')

    base, tail = read_string(local), read_string(suffix)
    target = base + tail
    if not Path(target).is_absolute():
        raise RuntimeError('Shortcut target is not absolute')
    return target


def runner_preflight():
    integration_clear()
    real_shortcut = Path(os.environ['APPDATA']) / 'Microsoft/Windows/Start Menu/Programs/Gateway Client.lnk'
    if real_shortcut.exists() or real_shortcut.is_symlink():
        raise RuntimeError('Runner already has a Gateway shortcut')
    names = ('GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe', 'GatewayUpdateHelper.exe')
    for name in names:
        result = subprocess.run(['tasklist.exe', '/FI', 'IMAGENAME eq '+name, '/FO', 'CSV', '/NH'],
                                capture_output=True, text=True, timeout=30, check=True)
        if any(row and row[0].lower() == name.lower() for row in csv.reader(result.stdout.splitlines())):
            raise RuntimeError('Runner already has a Gateway process: ' + name)
    return real_shortcut


def run_audited(binary, expected, arguments, env):
    # Guard immediately before *each* installer/uninstaller invocation.
    require_hash(binary, expected)
    return subprocess.run([str(binary), *arguments], env=env, cwd=env['TEMP'],
                          capture_output=True, timeout=180, check=True)


def exercise(out, build, installer):
    import winreg
    real_shortcut = runner_preflight()
    root = out / 'isolated-user'
    root.mkdir()
    for name in ('temp', 'local', 'roaming', 'home'):
        (root / name).mkdir()
    app = root / 'application'
    app.mkdir()
    sentinel = app / 'unrelated-user-file.txt'
    sentinel.write_text('preserve me\n', encoding='utf-8')
    profile = root / 'local/Gateway/data'
    profile.mkdir(parents=True)
    marker = profile / '.gateway-owned-profile.json'
    marker.write_text(json.dumps({'application':'Gateway Client', 'path':str(profile), 'kind':'profile'}), encoding='utf-8')
    profile_sentinel = profile / 'synthetic-profile-sentinel.txt'
    profile_sentinel.write_text('preserve profile\n', encoding='utf-8')
    profile_hashes = {p.name:digest(p) for p in (marker, profile_sentinel)}
    allow = ('SYSTEMROOT', 'WINDIR', 'SYSTEMDRIVE', 'PATH', 'PATHEXT', 'COMSPEC')
    env = {name: os.environ[name] for name in allow if name in os.environ}
    env.update(TEMP=str(root/'temp'), TMP=str(root/'temp'), LOCALAPPDATA=str(root/'local'),
               APPDATA=str(root/'roaming'), USERPROFILE=str(root/'home'), HOME=str(root/'home'))
    shortcut = root / 'roaming/Microsoft/Windows/Start Menu/Programs/Gateway Client.lnk'
    args = ['-cli', '-quiet', '-no-startup', '-no-god', '-no-launch', '-no-browser-companion-setup',
            '-update-mode', 'manual', '-dir', str(app)]
    run_audited(installer, INSTALLER_SHA, args, env)
    for name, expected in EXPECTED.items():
        require_hash(app/name, expected)
    require_hash(app/'bootstrap/headers-mainnet.bin', HEADER_SHA)
    require_hash(app/'bootstrap/headers-mainnet.json', HEADER_META_SHA)
    uninstaller = app / 'Uninstall Gateway Client.exe'
    require_hash(uninstaller, INSTALLER_SHA)
    if sentinel.read_text(encoding='utf-8') != 'preserve me\n':
        raise RuntimeError('Install modified unrelated file')
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, r'Software\Microsoft\Windows\CurrentVersion\Uninstall\GatewayClient') as key:
        location, _ = winreg.QueryValueEx(key, 'InstallLocation')
        version, _ = winreg.QueryValueEx(key, 'DisplayVersion')
    if os.path.normcase(location) != os.path.normcase(str(app)) or version != '0.7.8':
        raise RuntimeError('Uninstall registration target differs')
    if not shortcut.is_file() or os.path.normcase(shortcut_target(shortcut)) != os.path.normcase(str(app/'GatewayOnDemand.exe')):
        raise RuntimeError('Disposable shortcut target differs')
    # Uninstall invokes the installed client for narrowly scoped integration removal.
    require_hash(app/'GatewayClient.exe', CLIENT_SHA)
    run_audited(uninstaller, INSTALLER_SHA, ['-uninstall', '-quiet'], env)
    wait_started = time.monotonic()
    deadline = wait_started+30
    while uninstaller.exists() and time.monotonic() < deadline:
        time.sleep(.2)
    integration_clear()
    remaining = [path.name for path in (shortcut, uninstaller, *(app/name for name in EXPECTED)) if path.exists()]
    if remaining:
        raise RuntimeError('Disposable uninstall left managed files or shortcut after '
                           f'{time.monotonic()-wait_started:.2f}s: {remaining}')
    if sentinel.read_text(encoding='utf-8') != 'preserve me\n':
        raise RuntimeError('Uninstall modified unrelated file')
    if {p.name:digest(p) for p in (marker, profile_sentinel)} != profile_hashes:
        raise RuntimeError('Uninstall modified the disposable profile')
    if real_shortcut.exists():
        raise RuntimeError('Unexpected real runner shortcut was created')
    return {'status':'PASS', 'source_revision':REVISION, 'installer_sha256':INSTALLER_SHA,
            'client_sha256':CLIENT_SHA, 'header_sha256':HEADER_SHA,
            'scope':'exact audited installer; isolated hosted HKCU and app-data; quiet install and uninstall; no purge, startup, protocol, browser or launch',
            'registration_and_shortcut_created_then_removed':True, 'unrelated_sentinel_preserved':True,
            'synthetic_owned_profile_preserved':True,
            'limitations':['No existing user state, upgrade, GUI, browser approval, NRPT, network or antivirus acceptance tested.']}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', required=True, type=Path)
    args = parser.parse_args()
    hosted_only()
    if os.path.normcase(str(args.source.resolve(strict=True))) != os.path.normcase(str(SOURCE)):
        raise RuntimeError('Unexpected source directory')
    out = new_workspace()
    runner_preflight()
    header = fetch_baseline(out)
    build, installer = rebuild(out, header)
    result = exercise(out, build, installer)
    with (out/'result.json').open('x', encoding='utf-8') as f:
        json.dump(result, f, indent=2)
        f.write('\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
