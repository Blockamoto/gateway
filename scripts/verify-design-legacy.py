#!/usr/bin/env python3
"""Open an explicitly owned earlier test profile and verify frozen Bitmap reads.

No profile copy, identity migration, run control, block fetch or index build is
performed. Only runtime startup/shutdown and explicit read APIs are used.
"""
import argparse
import importlib.util
import json
from pathlib import Path


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--profile', type=Path, required=True)
    p.add_argument('--out', type=Path, required=True)
    args = p.parse_args()
    root = Path(__file__).resolve().parents[1]
    profile = args.profile.resolve(strict=True)
    assert profile.is_relative_to(root / 'build/qa-078'), 'Only a task-owned earlier QA profile is accepted'
    out = args.out.resolve()
    assert not out.exists()
    out.mkdir(parents=True)
    spec = importlib.util.spec_from_file_location('replay', Path(__file__).with_name('benchmark-index-replay.py'))
    h = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(h)
    binary = args.binary.resolve(strict=True)
    ids = ('bitmap-districts-canonical-v2', 'bitmap-parcels-canonical-v2')
    base = profile / 'indexes/bitmap-development-v1'
    files = [file for identity in ids for file in (base / identity).rglob('*') if file.is_file()]
    before = {str(file.relative_to(profile)): h.sha(file) for file in files}
    assert before and not any((base / identity).exists() for identity in ('bitmap-districts-standard-v1', 'bitmap-parcels-standard-v1'))
    original = {}
    for identity in ids:
        pointer = h.read_json(base / identity / 'head.json')
        commit = pointer['commitment']
        original[identity] = h.read_json(base / identity / 'commits' / commit[:2] / (commit+'.json'))
    settings = h.read_json(profile / 'settings.json')
    assert settings['network_disabled'] and settings['core_disabled'] and settings['core_mount_disabled']
    runtime = h.Runtime(binary, profile, out)
    result = dict(binary_sha256=h.sha(binary), profile=str(profile), original_heads=original)
    try:
        runtime.start()
        status = runtime.call('/api/v1/index/status')
        definitions = {d['id']: d for d in status['definitions']}
        for identity in ids:
            assert definitions[identity]['locked'] and not definitions[identity]['buildable']
        query = runtime.call('/api/v1/bitmap/view?instance='+ids[0]+'&district=0')
        assert query['instance'] == ids[0] and query['state'] == 'selected', query
        assert query['snapshot']['commitment'] == original[ids[0]]['commitment']
        assert query['profile_hash'] == original[ids[0]]['profile_hash']
        assert query['result']['winner_blocator'] == '953i0.792435'
        parcel = runtime.call('/api/v1/bitmap/view?instance='+ids[1]+'&district=0&transaction=0')
        assert parcel['instance'] == ids[1] and parcel['district_instance'] == ids[0]
        assert parcel['snapshot']['commitment'] == original[ids[1]]['commitment']
        default = runtime.call('/api/v1/bitmap/view?district=0')
        assert default['instance'] == 'bitmap-districts-standard-v1' and not default['snapshot']
        runtime.stop()
        after = {str(file.relative_to(profile)): h.sha(file) for identity in ids for file in (base / identity).rglob('*') if file.is_file()}
        assert after == before, 'Opening the new executable changed frozen saved evidence'
        assert not any((base / identity).exists() for identity in ('bitmap-districts-standard-v1', 'bitmap-parcels-standard-v1'))
        h.write(out / 'legacy-district.json', query)
        h.write(out / 'legacy-parcel.json', parcel)
        h.write(out / 'new-default.json', default)
        result.update(result='PASS', frozen_files=len(before), frozen_files_unchanged=True,
                      earlier_ids_locked=True, explicit_legacy_reads=True, standard_stores_not_created=True)
        print('Compiled legacy identity PASS: original canonical V2 hashes/heads/files readable and unchanged; locked execution; new defaults do not alias saved stores.')
    except BaseException as error:
        result.update(result='FAIL', error=str(error))
        raise
    finally:
        runtime.stop()
        h.write(out / 'result.json', result)
        h.write(out / 'api-calls.json', runtime.calls)


if __name__ == '__main__':
    main()
