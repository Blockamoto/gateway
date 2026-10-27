#!/usr/bin/env python3
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('promote078', Path(__file__).with_name('promote-initial-update-feed-078.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class Rejected(Exception):
    pass


def require(condition, reason):
    if not condition:
        raise Rejected(reason)


class PromotionTests(unittest.TestCase):
    def test_unfrozen_draft_rejects_before_network(self):
        p = SimpleNamespace(frozen_inputs=Mock(), require=require)
        helper = SimpleNamespace(GitHub=Mock())
        with self.assertRaisesRegex(Rejected, 'Signing run'):
            m.execute(SimpleNamespace(), p, helper)
        helper.GitHub.assert_not_called()

    def test_all_promotion_pins_required(self):
        p = SimpleNamespace(frozen_inputs=Mock(), require=require)
        with patch.object(m, 'SIGNING_RUN_ID', 42):
            with self.assertRaisesRegex(Rejected, 'Promotion identity'):
                m.frozen(p)

    def test_signed_source_and_sequences_required(self):
        p = SimpleNamespace(require=require, VERSION='0.7.8', REVISION='a'*40,
            REPOSITORY='Blockamoto/gateway', EXPECTED_SEQUENCES=(17, 16),
            header_identity=lambda value: 'approved', EXPECTED_HEADER_SNAPSHOT_SHA256='approved')
        url = 'https://github.com/Blockamoto/gateway/releases/tag/v0.7.8'
        value = {'application': {'version': '0.7.8', 'source_revision': 'a'*40,
                 'release_url': url, 'sequence': 17}, 'headers': {'source_release': url, 'sequence': 16}}
        m.candidate_identity(p, value)
        value['application']['sequence'] = 16
        with self.assertRaisesRegex(Rejected, 'candidate identity'):
            m.candidate_identity(p, value)

    def test_wrong_header_identity_rejected(self):
        p = SimpleNamespace(require=require, VERSION='0.7.8', REVISION='a'*40,
            REPOSITORY='Blockamoto/gateway', EXPECTED_SEQUENCES=(17,16),
            header_identity=lambda value: 'wrong', EXPECTED_HEADER_SNAPSHOT_SHA256='approved')
        url = 'https://github.com/Blockamoto/gateway/releases/tag/v0.7.8'
        value = {'application': {'version':'0.7.8', 'source_revision':'a'*40,
                 'release_url':url, 'sequence':17}, 'headers':{'source_release':url, 'sequence':16}}
        with self.assertRaisesRegex(Rejected, 'header identity'):
            m.candidate_identity(p, value)


if __name__ == '__main__':
    unittest.main()
