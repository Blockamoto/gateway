#!/usr/bin/env python3
"""Require the actual POSIX mode rejection test to run, not skip."""
import importlib.util
import os
from pathlib import Path
import sys
import unittest

if os.name != 'posix':
    raise SystemExit('This isolated check requires a POSIX runner')
for name in ('GATEWAY_UPDATE_SIGNING_KEY_JSON', 'GATEWAY_PUBLISHER_GITHUB_TOKEN', 'GATEWAY_RENDER_DEPLOY_HOOK', 'GH_TOKEN'):
    if name in os.environ:
        raise SystemExit('Unexpected credential variable in offline test environment: ' + name)
spec = importlib.util.spec_from_file_location('offline_signing_tests', Path(__file__).with_name('test-prepare-initial-update-feed-078.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
suite = unittest.defaultTestLoader.loadTestsFromName('PreparationTests.test_private_mode_rejects_real_non_owner_permissions', module)
result = unittest.TextTestRunner(verbosity=2).run(suite)
if result.testsRun != 1 or result.skipped or not result.wasSuccessful():
    raise SystemExit('Required POSIX permission rejection did not pass without skips')
print('Required POSIX permission rejection passed without skips')
