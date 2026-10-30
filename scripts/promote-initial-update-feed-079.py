#!/usr/bin/env python3
"""Inactive draft: upload authenticated initial canonical bundle; no signer or Render access."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile
import zipfile

SIGNING_RUN_ID = 40977955358
SIGNING_WORKFLOW_REVISION = "14a34c27bd8873d5c690eebdeaecdf51f0b11ab0"
EXPECTED_BUNDLE_SHA256 = "35a1db9ca3e478e8dca8aabc8d60190ad1f5b3e6c88507ce193bd37516180a1b"
EXPECTED_PREVIOUS_BUNDLE_SHA256 = "0570ecbb7d618ec192748c97ca118b9a3e52a935fd29727035d0777b44024a15"
TITLE = "0.7.9 Parcels"


def load(path):
    spec = importlib.util.spec_from_file_location(path.stem.replace('-', '_'), path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def frozen(p):
    p.frozen_inputs()
    p.require(type(SIGNING_RUN_ID) is int and SIGNING_RUN_ID > 0, "Signing run is not frozen")
    for value, length in [(SIGNING_WORKFLOW_REVISION, 40), (EXPECTED_BUNDLE_SHA256, 64),
                          (EXPECTED_PREVIOUS_BUNDLE_SHA256, 64)]:
        p.require(re.fullmatch('[0-9a-f]{' + str(length) + '}', value) is not None,
                  "Promotion identity is not frozen")


def candidate_identity(p, result):
    app, headers = result['application'], result['headers']
    url = f'https://github.com/{p.REPOSITORY}/releases/tag/v{p.VERSION}'
    p.require(app['version'] == p.VERSION and app['source_revision'] == p.REVISION and
              app['release_url'] == headers['source_release'] == url and
              (app['sequence'], headers['sequence']) == p.EXPECTED_SEQUENCES,
              "Signed candidate identity differs")
    p.require(p.header_identity(headers) == p.EXPECTED_HEADER_SNAPSHOT_SHA256,
              "Signed header identity differs")


def execute(args, p, helper):
    frozen(p)  # Before credentials or network.
    p.require(os.environ.get('GITHUB_ACTIONS') == 'true' and
              os.environ.get('GITHUB_REPOSITORY') == p.REPOSITORY and
              os.environ.get('GITHUB_REF') == 'refs/heads/main' and
              os.environ.get('GITHUB_EVENT_NAME') == 'workflow_dispatch', "Trusted main workflow required")
    p.require(not any(os.environ.get(k) for k in
              ['GATEWAY_UPDATE_SIGNING_KEY_JSON', 'GATEWAY_RENDER_DEPLOY_HOOK']),
              "Promotion must not receive signer or deploy secrets")
    token = os.environ.pop('GH_TOKEN', '')
    p.require(bool(token), "Workflow token is missing")
    github = helper.GitHub(token)
    root = Path(__file__).resolve().parents[1]
    public = root / 'assets/bootstrap/update-public-key.json'
    p.require(json.loads(public.read_text()) == p.KEY, "Pinned public key differs")
    raw = args.bundle.read_bytes()
    p.require(p.digest(raw) == EXPECTED_BUNDLE_SHA256, "Candidate bundle hash differs")
    run = github.api(f'/actions/runs/{SIGNING_RUN_ID}')
    p.require(run['conclusion'] == 'success' and run['event'] == 'workflow_dispatch' and
              run['head_branch'] == 'main' and run['head_sha'] == SIGNING_WORKFLOW_REVISION and
              run['path'] == '.github/workflows/prepare-initial-update-feed-079.yml',
              "Signing run provenance differs")
    with tempfile.TemporaryDirectory(prefix='gateway-promote-079-') as temporary:
        work = Path(temporary)
        candidate = p.run_tool(args.publisher, 'restore', '-bundle', args.bundle, '-out', work/'candidate',
                               '-version', p.VERSION, '-repository', p.REPOSITORY, '-public', public)
        candidate_identity(p, candidate)

        def origins():
            previous = github.release(p.PREVIOUS_VERSION)
            oldraw = github.download(p.one_asset(previous, f'gateway-update-feed-v{p.PREVIOUS_VERSION}.zip'))
            p.require(p.digest(oldraw) == EXPECTED_PREVIOUS_BUNDLE_SHA256 and
                      github.tag_revision(p.PREVIOUS_VERSION) == p.PREVIOUS_REVISION,
                      "Previous canonical origin changed")
            return oldraw

        previous_raw = origins()
        previous_file = work/'previous.zip'
        previous_file.write_bytes(previous_raw)
        old = p.run_tool(args.publisher, 'restore', '-bundle', previous_file, '-out', work/'previous',
                         '-version', p.PREVIOUS_VERSION, '-repository', p.REPOSITORY, '-public', public)
        p.require(old['application']['source_revision'] == p.PREVIOUS_REVISION and
                  (old['application']['sequence']+1, old['headers']['sequence']+1) == p.EXPECTED_SEQUENCES,
                  "Previous signed source or sequences differ")
        with zipfile.ZipFile(previous_file) as archive:
            live = {'/manifest.json': archive.read('manifest.json'),
                    '/headers/manifest.json': archive.read('headers-manifest.json')}
        release = github.release(p.VERSION)
        p.require(release['name'] == TITLE and release.get('prerelease') is True and not release.get('draft') and
                  github.tag_revision(p.VERSION) == p.REVISION, "Release title, state or source differs")
        manifest = github.download(p.one_asset(release, 'artifact-manifest.json'))
        p.require(p.digest(manifest) == p.EXPECTED_MANIFEST_SHA256, "Published manifest differs")
        for name, identity in p.EXPECTED_ARTIFACTS.items():
            asset = p.one_asset(release, name)
            data = github.download(asset)
            p.require(len(data) == identity['bytes'] and p.digest(data) == identity['sha256'],
                      "Published artifact differs")
        canonical = f'gateway-update-feed-v{p.VERSION}.zip'
        # The workflow holds renewal's concurrency lock throughout these checks/upload.
        # External operators must obey the same lock; GitHub upload rejects name collisions.
        p.require(origins() == previous_raw, "Previous origin changed before upload")
        p.live_matches(helper, live)
        current = github.release(p.VERSION)
        p.require(current['name'] == TITLE and current.get('prerelease') is True and not current.get('draft') and
                  github.tag_revision(p.VERSION) == p.REVISION, "Target changed before upload")
        p.require(p.digest(github.download(p.one_asset(current, 'artifact-manifest.json'))) ==
                  p.EXPECTED_MANIFEST_SHA256, "Target manifest changed before upload")
        for name, identity in p.EXPECTED_ARTIFACTS.items():
            observed = p.one_asset(current, name)
            p.require(observed['size'] == identity['bytes'] and
                      observed.get('digest') == 'sha256:' + identity['sha256'],
                      "Target artifact metadata changed before upload")
        found = [a for a in current['assets'] if a['name'] == canonical]
        p.require(len(found) <= 1, "Ambiguous canonical bundle")
        if found:
            p.require(github.download(found[0]) == raw, "Conflicting canonical bundle; refusing replacement")
            asset = found[0]
        else:
            asset = github.upload(current['id'], canonical, raw)
        p.require(github.download(p.one_asset(github.release(p.VERSION), canonical)) == raw,
                  "Uploaded canonical bytes differ")
        args.receipt.write_text(json.dumps({'status': 'CANONICAL_UPLOADED_NOT_HOSTED',
            'version': p.VERSION, 'title': TITLE, 'source_revision': p.REVISION,
            'bundle_sha256': p.digest(raw), 'bundle_asset_id': asset['id'],
            'signing_run_id': SIGNING_RUN_ID, 'render_mutated': False}, indent=2)+'\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path)
    parser.add_argument('--publisher', type=Path)
    parser.add_argument('--receipt', type=Path)
    parser.add_argument('--check-frozen', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    p = load(root/'scripts/prepare-initial-update-feed-079.py')
    try:
        frozen(p)
        if args.check_frozen:
            return 0
        p.require(args.bundle and args.publisher and args.receipt, "Bundle, publisher and receipt required")
        execute(args, p, load(root/'scripts/renew-update-feed.py'))
        return 0
    except Exception as error:
        print('Promotion rejected: ' + (str(error) if isinstance(error, p.CandidateError) else type(error).__name__))
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
