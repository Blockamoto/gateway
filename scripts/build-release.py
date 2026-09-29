#!/usr/bin/env python3
"""Build the same Gateway Client payload for portable and installer releases.
Requires Python 3 and Go 1.23+; no third-party Go packages or network fetches.
"""
from __future__ import annotations
import argparse, os, shutil, subprocess, sys, tempfile
from pathlib import Path

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    version = (root/'VERSION.txt').read_text().strip()
    out = args.out.resolve(); out.mkdir(parents=True, exist_ok=True)
    subprocess.run([sys.executable,str(root/'scripts/build-icon-resources.py')],check=True)
    base = dict(os.environ, GO111MODULE='off', CGO_ENABLED='0', GOARCH='amd64')
    def build(cwd: Path, target: str, name: str, gui: bool=False) -> None:
        env = dict(base, GOOS=target)
        subprocess.run(['go', 'build', '-trimpath', '-ldflags', '-s -w' + (' -H windowsgui' if gui else ''), '-o', str(out/name), '.'], cwd=cwd, env=env, check=True)
    build(root, 'linux', 'gateway-client')
    build(root, 'windows', 'GatewayClient.exe')
    build(root/'packaging/gateway-launcher', 'windows', 'GatewayOnDemand.exe', True)
    build(root/'packaging/native-host', 'windows', 'GatewayNativeHost.exe')
    with tempfile.TemporaryDirectory(prefix='gateway-installer-') as work:
        temp = Path(work); payload = temp/'payload'; payload.mkdir()
        for name in ('GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe'):
            shutil.copy2(out/name, payload/name)
        shutil.copytree(root/'browser-companion', payload/'browser-companion')
        (temp/'main.go').write_text((root/'packaging/windows-installer/main.go.txt').read_text(encoding='utf-8').replace('__GATEWAY_VERSION__', version), encoding='utf-8')
        shutil.copytree(root/'internal', temp/'internal')
        shutil.copy2(root/'gateway_windows_amd64.syso',temp/'gateway_windows_amd64.syso')
        build(temp, 'windows', f'gateway-client-v{version}-installer.exe', True)
    print('Built Linux, Windows, native launcher and embedded-payload installer:', out)

if __name__ == '__main__':
    main()
