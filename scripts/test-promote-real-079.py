#!/usr/bin/env python3
"""Offline full upload-path exercise using real synthetic-key signed bundles.

Called by test-prepare-real-079.py after generating its ephemeral fixtures.
Only GitHub and live HTTP origins are replaced; restore/signature checks are real.
"""
import copy
import json
import os
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


def exercise(prepare, scenario, candidate, verified):
    promote = prepare.importlib.util.spec_from_file_location('promotion_real',
        Path(__file__).with_name('promote-initial-update-feed-079.py'))
    module = prepare.importlib.util.module_from_spec(promote)
    promote.loader.exec_module(module)
    raw = candidate.read_bytes()
    canonical = 'gateway-update-feed-v0.7.9.zip'
    pins = dict(REVISION='1'*40, KEY=json.loads((scenario.root/'assets/bootstrap/update-public-key.json').read_text()),
        EXPECTED_SEQUENCES=(11,8), EXPECTED_MANIFEST_SHA256=prepare.digest(scenario.manifest_raw),
        EXPECTED_ARTIFACTS=scenario.pins, HEADER_POLICY='unchanged',
        EXPECTED_HEADER_SNAPSHOT_SHA256=prepare.header_identity(verified['headers']))

    class Origin:
        def __init__(self, case):
            self.case = case
            self.uploads = 0
            self.previous_reads = 0
            self.stored = raw if case == 'retry' else (b'conflict' if case == 'conflict' else None)

        def api(self, route):
            assert route == '/actions/runs/42'
            return {'conclusion':'success', 'event':'workflow_dispatch', 'head_branch':'main',
                    'head_sha':'2'*40, 'path':'.github/workflows/prepare-initial-update-feed-079.yml'}

        def release(self, version):
            if version == '0.7.8':
                self.previous_reads += 1
                return {'assets':[scenario.previous_asset]}
            assets = copy.deepcopy(scenario.assets + [scenario.manifest_asset])
            if self.stored is not None:
                assets.append({'id':999, 'name':canonical, 'size':len(self.stored),
                               'digest':'sha256:'+prepare.digest(self.stored)})
            return {'id':78, 'name':'0.7.9 Parcels', 'prerelease':True, 'assets':assets}

        def tag_revision(self, version):
            return scenario.tag_revision(version)

        def download(self, asset):
            if asset['name'] == canonical:
                return self.stored
            if self.case == 'previous-drift' and asset['id'] == 91 and self.previous_reads > 1:
                return b'changed-predecessor'
            if self.case == 'artifact-tamper' and asset['name'].endswith('installer.exe'):
                return b'changed-installer'
            return scenario.download(asset)

        def upload(self, release_id, name, data):
            assert release_id == 78 and name == canonical and self.stored is None
            assert data == raw
            self.uploads += 1
            self.stored = data
            if self.case == 'lost-response':
                raise RuntimeError('synthetic upload response lost after remote commit')
            return {'id':999, 'name':name}

        def request(self, url, **kwargs):
            if self.case == 'live-drift':
                return b'changed-live'
            return scenario.live[url[len(prepare.PUBLISHER_URL):]]

    for case in ('success', 'retry', 'lost-response', 'conflict', 'previous-drift', 'artifact-tamper', 'live-drift'):
        origin = Origin(case)
        helper = SimpleNamespace(GitHub=lambda token: origin, request=origin.request)
        receipt = scenario.root / ('promotion-'+case+'.json')
        environment = {k:v for k,v in scenario.env.items() if k != 'GATEWAY_UPDATE_SIGNING_KEY_JSON'}
        args = SimpleNamespace(bundle=candidate, publisher=scenario.publisher, receipt=receipt)
        with (mock.patch.multiple(prepare, **pins), mock.patch.multiple(module,
            SIGNING_RUN_ID=42, SIGNING_WORKFLOW_REVISION='2'*40,
            EXPECTED_BUNDLE_SHA256=prepare.digest(raw),
            EXPECTED_PREVIOUS_BUNDLE_SHA256=prepare.digest(scenario.previous_raw),
            __file__=str(scenario.root/'scripts/promote-initial-update-feed-079.py')),
            mock.patch.dict(os.environ, environment, clear=True)):
            if case == 'lost-response':
                try:
                    module.execute(args, prepare, helper)
                except RuntimeError as error:
                    assert 'response lost' in str(error)
                else:
                    raise AssertionError('lost response was not simulated')
                assert origin.uploads == 1 and origin.stored == raw and not receipt.exists()
                origin.case = 'retry'
                os.environ['GH_TOKEN'] = 'synthetic-token'
                module.execute(args, prepare, helper)
                assert origin.uploads == 1 and receipt.exists()
            elif case in ('success', 'retry'):
                module.execute(args, prepare, helper)
                result = json.loads(receipt.read_text())
                assert result['status'] == 'CANONICAL_UPLOADED_NOT_HOSTED'
                assert result['title'] == '0.7.9 Parcels' and not result['render_mutated']
                assert origin.uploads == (1 if case == 'success' else 0)
            else:
                try:
                    module.execute(args, prepare, helper)
                except prepare.CandidateError:
                    pass
                else:
                    raise AssertionError('unsafe promotion accepted: '+case)
                assert origin.uploads == 0 and not receipt.exists()
    print('PASS: real synthetic-key candidate upload, retry, lost-response recovery, conflict, predecessor drift, artifact tamper and live drift; fake origins only')
