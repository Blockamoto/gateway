#!/usr/bin/env python3
"""Build the same Gateway Client payload for portable and installer releases.
Requires Python 3 and Go 1.23+; no third-party Go packages or network fetches.
"""
from __future__ import annotations
import argparse, base64, datetime, hashlib, json, os, shutil, subprocess, sys, tempfile
from pathlib import Path
from urllib.parse import urlsplit

def public_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('Duplicate provisioning field')
        value[key] = item
    return value

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--headers-baseline', type=Path, help='explicit mainnet headers.bin input; validated by the built runtime before delivery')
    parser.add_argument('--without-header-baseline', action='store_true', help='explicit development build without bundled headers')
    parser.add_argument('--update-channels', type=Path, help='independently approved public channel descriptor; contains no secrets')
    args = parser.parse_args()
    if bool(args.headers_baseline) == args.without_header_baseline:
        parser.error('Choose --headers-baseline PATH for releases or --without-header-baseline for development.')
    root = Path(__file__).resolve().parents[1]
    version = (root/'VERSION.txt').read_text().strip()
    out = args.out.resolve(); out.mkdir(parents=True, exist_ok=True)
    bootstrap = out/'bootstrap'; bootstrap.mkdir(exist_ok=True)
    replacements = {}
    header_metadata = {'schema':1,'network':'mainnet','count':0}
    if args.headers_baseline:
        source = args.headers_baseline.resolve()
        if Path(str(source)+'.append-pending').exists():
            raise SystemExit('Header source has an unfinished append; use a completed snapshot.')
        with source.open('rb') as stream:
            length = os.fstat(stream.fileno()).st_size
            if length < 80 or length % 80 or length > 2_000_000*80:
                raise SystemExit('Header baseline must contain 1–2,000,000 complete mainnet headers.')
            raw = stream.read(length)
        if len(raw) != length or Path(str(source)+'.append-pending').exists():
            raise SystemExit('Header source changed during capture; retry from a completed snapshot.')
        copied = bootstrap/'headers-mainnet.bin'; copied.write_bytes(raw)
        tip = hashlib.sha256(hashlib.sha256(raw[-80:]).digest()).digest()[::-1].hex()
        captured = datetime.datetime.fromtimestamp(int(os.environ['SOURCE_DATE_EPOCH']),datetime.timezone.utc) if 'SOURCE_DATE_EPOCH' in os.environ else datetime.datetime.now(datetime.timezone.utc)
        header_metadata = {'schema':1,'network':'mainnet','count':length//80,'sha256':hashlib.sha256(raw).hexdigest(),'tip_hash':tip,'created_at':captured.isoformat(timespec='seconds').replace('+00:00','Z')}
        metadata = bootstrap/'headers-mainnet.json'; metadata.write_text(json.dumps(header_metadata)+'\n')
    else:
        (bootstrap/'headers-mainnet.bin').write_bytes(b'')
        (bootstrap/'headers-mainnet.json').write_text(json.dumps(header_metadata)+'\n')
    if args.update_channels:
        channels = args.update_channels.read_bytes()
        if len(channels) > 64*1024:
            raise SystemExit('Public channel descriptor exceeds 64 KiB.')
        # Reject unknown/secret-bearing fields before bytes enter any executable.
        # The runtime repeats complete URL/key/channel validation after building.
        try:
            document = json.loads(channels,object_pairs_hook=public_object)
            if set(document) != {'schema','channels'} or document['schema'] != 1:
                raise ValueError('Invalid descriptor schema')
            if not isinstance(document['channels'],list) or len(document['channels']) > 2:
                raise ValueError('Invalid channels')
            for channel in document['channels']:
                if set(channel) != {'id','label','publisher_url','trusted_key'}:
                    raise ValueError('Only public channel fields permitted')
                url = urlsplit(channel['publisher_url'])
                if url.scheme not in ('http','https') or not url.hostname or url.username is not None or url.password is not None or url.query or url.fragment:
                    raise ValueError('Publisher URL must not contain credentials')
                key = channel['trusted_key']
                if set(key) != {'key_id','public_key'}:
                    raise ValueError('Only public key fields permitted')
                public = base64.b64decode(key['public_key'],validate=True)
                if len(public) != 32 or hashlib.sha256(public).hexdigest()[:32] != key['key_id']:
                    raise ValueError('Invalid public key identity')
        except (ValueError,TypeError,KeyError):
            raise SystemExit('Invalid public-only channel descriptor; no secrets may be embedded.')
        target = bootstrap/'update-channels.json'; target.write_bytes(channels)
        replacements[str(root/'assets/bootstrap/update-channels.json')] = str(target)
    overlay = bootstrap/'overlay.json'; overlay.write_text(json.dumps({'Replace':replacements}))
    subprocess.run([sys.executable,str(root/'scripts/build-icon-resources.py')],check=True)
    base = dict(os.environ, GO111MODULE='off', CGO_ENABLED='0', GOARCH='amd64')
    def build(cwd: Path, target: str, name: str, gui: bool=False) -> None:
        env = dict(base, GOOS=target)
        overlay_args = ['-overlay', str(overlay)] if cwd == root else []
        subprocess.run(['go', 'build', *overlay_args, '-trimpath', '-ldflags', '-s -w' + (' -H windowsgui' if gui else ''), '-o', str(out/name), '.'], cwd=cwd, env=env, check=True)
    build(root, 'linux', 'gateway-client')
    build(root/'packaging/update-helper', 'linux', 'gateway-update-helper')
    build(root, 'windows', 'GatewayClient.exe')
    native = out/('GatewayClient.exe' if sys.platform == 'win32' else 'gateway-client')
    verification = subprocess.check_output([str(native), '-verify-release-bootstrap'], text=True)
    print(verification.strip())
    (out/'release-bootstrap.json').write_text(json.dumps({'headers':header_metadata,'update_channels_supplied':bool(args.update_channels),'validation':verification.strip()},indent=2)+'\n')
    build(root/'packaging/update-helper', 'windows', 'GatewayUpdateHelper.exe', True)
    build(root/'packaging/gateway-launcher', 'windows', 'GatewayOnDemand.exe', True)
    build(root/'packaging/native-host', 'windows', 'GatewayNativeHost.exe')
    with tempfile.TemporaryDirectory(prefix='gateway-installer-') as work:
        temp = Path(work); payload = temp/'payload'; payload.mkdir()
        for name in ('GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe', 'GatewayUpdateHelper.exe'):
            shutil.copy2(out/name, payload/name)
        shutil.copy2(root/'COMPATIBILITY.json', payload/'COMPATIBILITY.json')
        for notice in ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt'):
            shutil.copy2(root/notice, payload/notice)
        shutil.copy2(root/'docs'/f'TESTING-{version}.md', payload/f'TESTING-{version}.md')
        shutil.copytree(bootstrap, payload/'bootstrap', ignore=shutil.ignore_patterns('overlay.json', 'update-channels.json'))
        shutil.copytree(root/'browser-companion', payload/'browser-companion')
        (temp/'main.go').write_text((root/'packaging/windows-installer/main.go.txt').read_text(encoding='utf-8').replace('__GATEWAY_VERSION__', version), encoding='utf-8')
        shutil.copytree(root/'internal', temp/'internal')
        shutil.copy2(root/'packaging/windows-installer/gateway_windows_amd64.syso',temp/'gateway_windows_amd64.syso')
        build(temp, 'windows', f'gateway-client-v{version}-installer.exe', True)
    print('Built Linux and Windows clients/update helpers, native launcher and embedded-payload installer:', out)

if __name__ == '__main__':
    main()
