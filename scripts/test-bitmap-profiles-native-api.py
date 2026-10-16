#!/usr/bin/env python3
"""Exercise compiled Bitmap management routes in an owned disposable profile."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    binary, output = args.binary.resolve(), args.out.resolve()
    output.mkdir(parents=True, exist_ok=True)
    profile = Path(tempfile.mkdtemp(prefix='native-bitmap-api-', dir=output))
    settings = dict(network_disabled=True, headers_paused=True, core_disabled=True,
                    core_mount_disabled=True, serve_data=False, share_cache=False,
                    onboarded=True, lan_discovery=False, graph_index=False,
                    cache_blocks=False, privacy_mode=True)
    (profile / 'settings.json').write_text(json.dumps(settings), encoding='utf-8')
    process = None
    passed = False
    checks = []
    log = (output / 'runtime.log').open('wb')
    try:
        process = subprocess.Popen([str(binary), '-data', str(profile), '-background',
                                    '-no-tray', '-http', '127.0.0.1:0'],
                                   cwd=binary.parent, stdout=log, stderr=log,
                                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        endpoint = None
        for _ in range(600):
            if process.poll() is not None:
                raise AssertionError('Disposable runtime exited before readiness')
            try:
                runtime = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                assert runtime['pid'] == process.pid
                candidate = runtime['url']
                with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                    endpoint = candidate
                break
            except (OSError, ValueError, urllib.error.URLError):
                time.sleep(0.1)
        assert endpoint, 'Compiled runtime did not become ready'

        def call(path, body=None, expected=200):
            request = urllib.request.Request(endpoint + path,
                data=None if body is None else json.dumps(body).encode(),
                headers={'Content-Type': 'application/json', 'Origin': endpoint})
            try:
                response = urllib.request.urlopen(request, timeout=30)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw, status, kind = response.read(), response.status, response.headers.get('Content-Type', '')
            assert status == expected, (path, status, raw[:400])
            return json.loads(raw) if 'application/json' in kind else raw

        initial = call('/api/v1/bitmap/profiles')
        old_schema = 'gateway-bitmap-named-semantics-v2'
        new_schema = 'gateway-bitmap-named-semantics-v3'
        assert initial['profile_schema'] == new_schema
        assert initial['reference_revision'] == 'ccf7271a61a2cead4781d4a2bb9c86da761f1cd6'
        assert len(initial['registry']['district']) == 14 and len(initial['registry']['parcel']) == 15
        assert len(initial['registries'][old_schema]['district']) == 12
        assert len(initial['registries'][old_schema]['parcel']) == 13
        assert len(initial['instances']) == 3
        builtins = {item['id']: item for item in initial['instances']}
        assert builtins['bitmap-districts']['label'] == 'Bitmap Districts Standard'
        assert builtins['bitmap-parcels']['label'] == 'Bitmap Parcels Standard'
        assert builtins['bitmap-districts']['profile_hash'] == '3e957003ceefbeebd84a6cff74e483956d51b456229d556c6cbdcc7d414c99da'
        assert builtins['bitmap-parcels']['profile_hash'] == '19422536eee0541785a3bb8f15aad6f16ec4c09145b7f622aeb891f179df5af6'
        old_builtin_hashes = {identity: item['profile_hash'] for identity, item in builtins.items()}
        for identity, kind in (('bitmap-districts', 'district'), ('bitmap-parcels', 'parcel')):
            projection = builtins[identity]['legacy_rule_answers']
            order = [rule['id'] for rule in initial['registries'][old_schema][kind]]
            assert projection['schema'] == 'gateway-legacy-policy-projection-v1'
            assert projection['rule_schema'] == old_schema and projection['rule_ids'] == order
            assert projection['answers'] == initial['references'][old_schema][kind]
            assert projection['bits'] == ''.join(str(projection['answers'][rule]) for rule in order)
            assert 'delegate_exclusion' not in projection['answers'] and 'raw_utf8_body' not in projection['answers']
        for kind in ('district', 'parcel'):
            registry = {item['id']: item for item in initial['registry'][kind]}
            reference = initial['reference'][kind]
            for rule in ('delegate_exclusion', 'raw_utf8_body'):
                assert reference[rule] == registry[rule]['reference_value'] == 0
                assert registry[rule]['supported_values'] == [0, 1] and not registry[rule]['core']
            context = dict(kind=kind, label='Optional rule validation', profile_schema=new_schema)
            if kind == 'parcel':
                context['district_instance'] = 'bitmap-districts'
            delegate = call('/api/v1/bitmap/profiles/validate', dict(context, rules=dict(reference, delegate_exclusion=1)))
            assert delegate['state'] == 'ready' and delegate['profile']['answers']['delegate_exclusion'] == 1
            utf8 = call('/api/v1/bitmap/profiles/validate', dict(context, rules=dict(reference, text=0, raw_utf8_body=1)))
            assert utf8['state'] == 'ready' and utf8['profile']['decoder_policy'] == 'own-body-utf8-ignore-media-v1'
            for invalid in (dict(reference, raw_utf8_body=1), dict(reference, text=0, raw_utf8_body=1, brotli=1)):
                conflict = call('/api/v1/bitmap/profiles/validate', dict(context, rules=invalid))
                assert conflict['state'] == 'invalid' and conflict['issues']
            malformed = dict(reference)
            malformed.pop('delegate_exclusion')
            assert call('/api/v1/bitmap/profiles/validate', dict(context, rules=malformed))['state'] == 'invalid'
        checks.append('versioned District and Parcel registries preserve old questions; new options default Off and reject contradictory or incomplete profiles')
        for asset in ('bitmap-workspace.js', 'bitmap-workspace.css'):
            assert len(call('/shell/' + asset)) > 500
        checks.append('compiled profile routes and both embedded workspace assets')
        definitions = {item['id']: item for item in call('/api/v1/index/rules')}
        assert definitions['bitmap-districts']['name'] == 'Bitmap Districts Standard'
        assert definitions['bitmap-parcels']['name'] == 'Bitmap Parcels Standard'
        before = call('/api/v1/index/status')
        assert not before['jobs'] and before['run'].get('run') is None

        made = call('/api/v1/bitmap/profiles', dict(action='create', kind='district',
                    label='Native reference', rules=initial['reference']['district'], linked_parcel=True))
        track, parcel, chain = made['instance'], made['linked_parcel'], made['chain']
        assert track['profile']['fingerprint_schema'] == new_schema
        assert parcel['profile']['fingerprint_schema'] == new_schema
        assert parcel['label'] == 'Bitmap Parcels Custom 1' and parcel['auto_label']
        assert parcel['district_instance'] == track['id'] == chain['district_instance']
        assert parcel['district_profile_hash'] == track['profile_hash']
        assert len(call('/api/v1/bitmap/profiles')['instances']) == 6
        checks.append('atomic linked District, Chain and Parcel creation')
        status = call('/api/v1/index/status')
        definitions = {item['id']: item for item in status['definitions']}
        for item in (track, parcel, chain):
            definition = definitions[item['id']]
            assert definition['name'] == item['label'] and definition['buildable']
        assert track['id'] in definitions[parcel['id']]['dependencies']
        assert definitions[chain['id']]['dependencies'] == [track['id']]
        assert not status['jobs'] and status['run'].get('run') is None
        checks.append('real status exposes all unbuilt custom tracks and their exact dependencies without work')
        for item in (track, parcel, chain):
            view = call('/api/v1/bitmap/view?kind=' + item['kind'] + '&instance=' + item['id'] + '&expected_profile=' + item['profile_hash'])
            assert view['state'] == 'not_built'
        checks.append('instance/profile-bound unknown reads without jobs')
        renamed = call('/api/v1/bitmap/profiles', dict(action='rename', id=track['id'], label='Renamed reference'))
        assert renamed['instance']['profile_hash'] == track['profile_hash']
        assert renamed['instance']['configuration_revision'] == track['configuration_revision']
        checks.append('rename preserves executable rules identity')
        track = renamed['instance']
        assert track['classification'] == 'standard' and track['rules_editable']
        assert not any(item.get('rules_editable') for item in initial['instances'])
        delete_before_edit = call('/api/v1/index/delete?index=' + track['id'])
        original_hash = track['profile_hash']
        original_revision = track['configuration_revision']
        update = dict(action='update', id=track['id'], kind='district',
                      label=track['label'], rules=dict(initial['reference']['district'], text=0),
                      expected_profile=original_hash,
                      expected_configuration_revision=original_revision)
        edited = call('/api/v1/bitmap/profiles', update)
        track = edited['instance']
        assert track['id'] == parcel['district_instance']
        assert track['profile_hash'] != original_hash
        assert track['configuration_revision'] > original_revision
        assert track['classification'] == 'non_standard' and track['rules_editable']
        current = {item['id']: item for item in call('/api/v1/bitmap/profiles')['instances']}
        parcel, chain = current[parcel['id']], current[chain['id']]
        assert parcel['district_profile_hash'] == chain['district_profile_hash'] == track['profile_hash']
        assert parcel['profile']['district_profile_hash'] == track['profile_hash']
        call('/api/v1/bitmap/profiles', update, 409)
        call('/api/v1/bitmap/view?kind=district&instance=' + track['id'] + '&expected_profile=' + original_hash, expected=400)
        call('/api/v1/index/delete', dict(index=track['id'], review_id=delete_before_edit['review_id']), 409)
        checks.append('unindexed rules edit preserves IDs, atomically rebinds children and refuses stale edits, views and deletion reviews')
        restored_rules = call('/api/v1/bitmap/profiles', dict(action='update', id=track['id'],
                    kind='district', label=track['label'], rules=initial['reference']['district'],
                    expected_profile=track['profile_hash'],
                    expected_configuration_revision=track['configuration_revision']))
        track = restored_rules['instance']
        assert track['classification'] == 'standard' and track['profile_hash'] == original_hash
        assert track['configuration_revision'] > original_revision
        assert track['label'] == 'Renamed reference'
        current = {item['id']: item for item in call('/api/v1/bitmap/profiles')['instances']}
        parcel, chain = current[parcel['id']], current[chain['id']]
        checks.append('Standard classification follows rule values independently of the custom display name')
        rules = dict(initial['reference']['district'], text=0)
        variant = call('/api/v1/bitmap/profiles', dict(action='create', kind='district',
                    label='', auto_label=True, rules=rules))['instance']
        assert variant['profile_hash'] != track['profile_hash']
        assert variant['auto_label'] and variant['label'] == 'Bitmap Districts Custom 1'
        assert variant['classification'] == 'non_standard'
        result = call('/api/v1/bitmap/compare?baseline=' + track['id'] + '&variant=' + variant['id'])
        assert result['state'] == 'unavailable' and result['compared_blocks'] == 0
        checks.append('distinct alternative profile and honest unavailable comparison with Chain Off')
        variant = call('/api/v1/bitmap/profiles', dict(action='update', id=variant['id'],
                    kind='district', label=variant['label'], auto_label=True,
                    rules=initial['reference']['district'], expected_profile=variant['profile_hash'],
                    expected_configuration_revision=variant['configuration_revision']))['instance']
        assert variant['classification'] == 'standard' and variant['label'] == 'Bitmap Districts Custom 1'
        variant = call('/api/v1/bitmap/profiles', dict(action='rename', id=variant['id'],
                    label=variant['label']))['instance']
        assert not variant.get('auto_label')
        variant = call('/api/v1/bitmap/profiles', dict(action='update', id=variant['id'],
                    kind='district', label=variant['label'], rules=rules,
                    expected_profile=variant['profile_hash'],
                    expected_configuration_revision=variant['configuration_revision']))['instance']
        assert variant['classification'] == 'non_standard' and variant['label'] == 'Bitmap Districts Custom 1'
        checks.append('numbered Custom labels stay stable across Standard/Non-standard rule changes and remain explicitly renameable')

        optional_rules = dict(rules, delegate_exclusion=1, raw_utf8_body=1)
        previous_hash, previous_revision = variant['profile_hash'], variant['configuration_revision']
        variant = call('/api/v1/bitmap/profiles', dict(action='update', id=variant['id'],
                    kind='district', label=variant['label'], rules=optional_rules,
                    expected_profile=previous_hash, expected_configuration_revision=previous_revision))['instance']
        assert variant['profile_hash'] != previous_hash and variant['configuration_revision'] > previous_revision
        assert variant['rules']['delegate_exclusion'] == variant['rules']['raw_utf8_body'] == 1
        assert variant['profile']['decoder_policy'] == 'own-body-utf8-ignore-media-v1'
        assert variant['label'] == 'Bitmap Districts Custom 1'
        call('/api/v1/bitmap/view?kind=district&instance=' + variant['id'] + '&expected_profile=' + previous_hash, expected=400)
        second = call('/api/v1/bitmap/profiles', dict(action='create', kind='district',
                    label='', auto_label=True, rules=initial['reference']['district']))['instance']
        assert second['label'] == 'Bitmap Districts Custom 2' and second['classification'] == 'standard'
        assert second['id'] != variant['id']
        extra_parcel = call('/api/v1/bitmap/profiles', dict(action='create', kind='parcel',
                    district_instance=second['id'], label='', auto_label=True,
                    rules=initial['reference']['parcel']))['instance']
        assert extra_parcel['label'] == 'Bitmap Parcels Custom 2'
        assert extra_parcel['district_profile_hash'] == second['profile_hash']
        checks.append('compiled rule edits select the UTF-8/delegate interpretation and invalidate stale bindings; numbered District and Parcel allocation is independent')

        legacy = call('/api/v1/bitmap/profiles', dict(action='create', kind='district',
                    label='Preserved version 2', profile_schema=old_schema,
                    rules=initial['references'][old_schema]['district']))['instance']
        assert legacy['profile']['fingerprint_schema'] == old_schema
        assert legacy['profile']['reference_revision'] == '1751e0d37073ab920cbd8b5548c61b74c37716d1'
        assert legacy['profile_hash'] == '3468ea11488f21eece241bd7d31af1d94026f9cfb5be8a4fc85e25e29b0c7e83'
        assert len(legacy['rules']) == 12 and 'delegate_exclusion' not in legacy['rules']
        assert legacy['classification'] == 'standard' and legacy['rules_editable']
        assert legacy['profile_hash'] != original_hash
        checks.append('legacy named profiles retain their original reference, exact answer map and separate identity beside new profiles')

        owned = profile / 'indexes' / 'bitmap-development-v1' / track['id']
        owned.mkdir(parents=True)
        (owned / 'fixture.txt').write_text('Disposable owned deletion fixture', encoding='utf-8')
        blocked = call('/api/v1/index/delete?index=' + track['id'])
        assert not blocked['ready'] and blocked['exists']
        assert {parcel['id'], chain['id']} <= {item['index'] for item in blocked['dependencies']}
        deletion_ids = [track['id'], parcel['id'], chain['id']]
        deletion_query = '/api/v1/index/delete?' + '&'.join('index=' + identity for identity in deletion_ids)
        review = call(deletion_query)
        assert review['ready'] and review['exists']
        (owned / 'head.json').write_text('{}', encoding='utf-8')
        call('/api/v1/index/delete', dict(indices=deletion_ids, review_id=review['review_id']), 409)
        review = call(deletion_query)
        result = call('/api/v1/index/delete', dict(indices=deletion_ids, review_id=review['review_id']))
        assert result['deleted'] and not result.get('cleanup_pending') and not owned.exists()
        assert set(result['removed_tracks']) == set(deletion_ids)
        remaining = {item['id'] for item in call('/api/v1/bitmap/profiles')['instances']}
        assert not remaining.intersection(deletion_ids)
        assert variant['id'] in remaining
        checks.append('stale batch deletion refused with every explicitly selected dependency preserved')
        checks.append('confirmed whole custom deletion removes only the selected District/Parcel/Chain definitions and owned data')
        after = call('/api/v1/index/status')
        assert not after['jobs'] and after['run'].get('run') is None
        checks.append('management and inspection never launched an index job')
        layout = {'root': ['group:bitmap', 'group:txio', 'group:blocks'],
                  'group:txio': ['txio-sat-ranges', 'txio-links'],
                  'district:bitmap-districts': ['bitmap-parcels']}
        saved = call('/api/v1/index/layout', dict(order=layout))
        assert saved['order'] == layout and saved['revision'] > 0
        expected_instances = {item['id']: (item['label'], item['profile_hash'], item['configuration_revision'])
                              for item in call('/api/v1/bitmap/profiles')['instances']}
        process.terminate()
        process.wait(timeout=10)
        previous_endpoint = endpoint
        process = subprocess.Popen([str(binary), '-data', str(profile), '-background',
                                    '-no-tray', '-http', '127.0.0.1:0'],
                                   cwd=binary.parent, stdout=log, stderr=log,
                                   creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        endpoint = None
        for _ in range(600):
            if process.poll() is not None:
                raise AssertionError('Disposable runtime exited during restart')
            try:
                runtime = json.loads((profile / 'runtime.json').read_text(encoding='utf-8-sig'))
                if runtime['pid'] != process.pid:
                    raise ValueError('Waiting for restarted runtime')
                candidate = runtime['url']
                with urllib.request.urlopen(candidate + '/api/v1/runtime/ping', timeout=1):
                    endpoint = candidate
                break
            except (OSError, ValueError, urllib.error.URLError):
                time.sleep(0.1)
        assert endpoint and endpoint != previous_endpoint, 'Restart must exercise a new browser origin'
        restored = call('/api/v1/index/layout')
        assert restored == saved
        restarted_profiles = call('/api/v1/bitmap/profiles')['instances']
        assert {item['id']: (item['label'], item['profile_hash'], item['configuration_revision'])
                for item in restarted_profiles} == expected_instances
        restored_builtins = {item['id']: item['profile_hash'] for item in restarted_profiles if item['id'] in old_builtin_hashes}
        assert restored_builtins == old_builtin_hashes
        assert call('/api/v1/index/status')['run'].get('run') is None
        checks.append('compiled restart preserves profiles and parent-bound layout across local ports without work')
        receipt = dict(result='PASS', binary=str(binary), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                       checks=checks, limitations=['No evaluated claim or network-source fixture in this management-route suite.'])
        (output / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8')
        passed = True
        print('Compiled Bitmap management API PASS: creation, identities, unknown reads, assets and reviewed deletion.')
    except Exception as error:
        (output / 'failure.json').write_text(json.dumps(dict(error=str(error), checks=checks), indent=2) + '\n', encoding='utf-8')
        raise
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
        log.close()
        if passed:
            assert profile.resolve().parent == output and profile.name.startswith('native-bitmap-api-') and not profile.is_symlink()
            shutil.rmtree(profile)


if __name__ == '__main__':
    main()
