"""Observe pinned-baseline defects using Go overlays; never edit production files.

These tests PASS when a defect is reproduced. They are not release acceptance tests.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--go', default='go', help='Go executable (default: PATH)')
args = parser.parse_args()
here = Path(__file__).resolve().parent
root = here.parents[2]
mapping = {
    str(root / 'zz_audit_network_floor_test.go'): str(here / 'network_floor_repro_test.go.txt'),
    str(root / 'zz_audit_index_apps_test.go'): str(here / 'index_apps_repro_test.go.txt'),
}
for path in mapping:
    if Path(path).exists():
        raise SystemExit(f'Refusing to overlay an existing file: {path}')
env = os.environ.copy()
env['GO111MODULE'] = 'off'
print('WARNING: green reproduction tests confirm baseline defects, not repairs.', flush=True)
with tempfile.TemporaryDirectory(prefix='gateway-audit-overlay-') as temporary:
    overlay = Path(temporary) / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': mapping}), encoding='utf-8')
    result = subprocess.run([
        args.go, 'test', '-vet=off', '-count=1', '-timeout=120s',
        '-overlay', str(overlay), '-run', '^TestAudit', '-v', '.',
    ], cwd=root, env=env)
raise SystemExit(result.returncode)
