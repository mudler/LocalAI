# SPDX-License-Identifier: MIT
import base64
import hashlib
import json
import math
from pathlib import Path
import struct
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_adapter_proof as checker  # noqa: E402

DIMS = 4


def vec(values):
    raw = struct.pack('<%df' % len(values), *values)
    return {'dimensions': len(values), 'base64': base64.b64encode(raw).decode(), 'sha256': hashlib.sha256(raw).hexdigest()}


def unit(angle):
    return [math.cos(angle), math.sin(angle), 0.0, 0.0]


class ProofTests(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        (self.tmp / 'fx').mkdir()
        self.contract = {
            'contract_version': 'test-v1',
            'thresholds': {'retrieval_min_cosine_margin': 0.01, 'media_change_min_cosine_distance': 0.001, 'expected_dimensions': DIMS},
            'queries': [{'id': 'q1', 'direction': 'text-to-image', 'query_text': 'x', 'query_fixture': None, 'target': 'image-a',
                         'distractors': ['image-b', 'image-c']},
                        {'id': 'q2', 'direction': 'image-to-text', 'query_text': None, 'query_fixture': 'image-a', 'target': 'text-a',
                         'distractors': ['text-b']}],
            'pairs': {'change': {'surrounding_text': 't', 'left': 'image-a', 'right': 'image-b'}},
            'fixtures': {},
        }
        self.vectors = {
            'query:q1': vec(unit(0.0)), 'fixture:image-a': vec(unit(0.1)), 'fixture:image-b': vec(unit(0.5)), 'fixture:image-c': vec(unit(0.9)),
            'fixture:text-a': vec(unit(0.1)), 'fixture:text-b': vec(unit(1.0)),
            'pair:change:left': vec(unit(0.0)), 'pair:change:right': vec(unit(0.3)),
        }

    def write(self, contract=None, vectors=None, identity=True, sha=None, skipped=()):
        contract = contract or self.contract
        cpath = self.tmp / 'contract.json'
        cpath.write_text(json.dumps(contract))
        doc = {
            'kind': 'multimodal-adapter-vectors', 'contract_version': contract['contract_version'],
            'contract_sha256': sha or hashlib.sha256(cpath.read_bytes()).hexdigest(),
            'identity': {k: 'a' * 64 for k in ('model_sha256', 'projector_sha256', 'backend_sha256')} if identity else {},
            'vectors': vectors if vectors is not None else self.vectors, 'skipped': list(skipped),
        }
        vpath = self.tmp / 'vectors.json'
        vpath.write_text(json.dumps(doc))
        return str(cpath), str(vpath), str(self.tmp / 'fx')

    def test_pass_is_recomputed_from_raw_vectors(self):
        report = checker.build_report(*self.write())
        self.assertEqual(report['status'], 'PASS')
        self.assertEqual(report['counts'], {'declared': 3, 'scored': 3, 'passed': 3, 'failed': 0, 'not_applicable': 0})
        self.assertAlmostEqual(report['cases'][0]['margin'], math.cos(0.1) - math.cos(0.5), places=6)

    def test_a_margin_miss_is_a_recorded_fail_and_is_never_retuned(self):
        self.vectors['fixture:image-b'] = vec(unit(0.1 + 0.001))
        report = checker.build_report(*self.write())
        self.assertEqual(report['status'], 'FAIL')
        self.assertEqual(report['failures'], ['q1'])
        self.assertTrue(report['cases'][0]['rank_one'])
        self.assertEqual(checker.main(['x'] + list(self.write())), 3)

    def test_a_tie_fails(self):
        self.vectors['fixture:image-b'] = vec(unit(0.1))
        self.assertEqual(checker.build_report(*self.write())['failures'], ['q1'])

    def test_media_change_below_the_distance_fails(self):
        self.vectors['pair:change:right'] = vec(unit(0.0))
        self.assertIn('change', checker.build_report(*self.write())['failures'])

    def test_skipped_fixtures_are_not_applicable_not_passed(self):
        report = checker.build_report(*self.write(skipped=['text-b']))
        self.assertEqual(report['counts']['not_applicable'], 1)
        self.assertTrue(report['partial'])

    def test_forged_inputs_are_invalid(self):
        bad = dict(self.vectors['fixture:image-a'], sha256='0' * 64)
        for kwargs in (
            {'vectors': dict(self.vectors, **{'fixture:image-a': bad})},
            {'vectors': {k: v for k, v in self.vectors.items() if k != 'fixture:image-c'}},
            {'identity': False},
            {'sha': '0' * 64},
            {'vectors': dict(self.vectors, **{'fixture:image-a': vec([1.0, 0.0, 0.0])})},
            {'vectors': dict(self.vectors, **{'fixture:image-a': vec([0.0] * DIMS)})},
            {'vectors': dict(self.vectors, **{'fixture:image-a': vec([float('nan'), 0, 0, 0])})},
        ):
            with self.assertRaises(checker.Invalid, msg=str(list(kwargs))):
                checker.build_report(*self.write(**kwargs))

    def test_fixture_bytes_must_match_the_contract(self):
        (self.tmp / 'fx' / 'a.png').write_bytes(b'changed')
        contract = dict(self.contract, fixtures={'image-a': {'path': 'a.png', 'sha256': hashlib.sha256(b'original').hexdigest()}})
        with self.assertRaises(checker.Invalid):
            checker.build_report(*self.write(contract=contract))

    def test_replay_rejects_a_report_that_disagrees(self):
        args = self.write()
        report = checker.build_report(*args)
        path = self.tmp / 'report.json'
        path.write_text(json.dumps(report))
        self.assertEqual(checker.main(['x', *args, '--report', str(path)]), 0)
        report['status'] = 'FAIL'
        path.write_text(json.dumps(report))
        self.assertEqual(checker.main(['x', *args, '--report', str(path)]), 1)
        report = checker.build_report(*args)
        report['cases'][0]['margin'] += 0.5
        path.write_text(json.dumps(report))
        self.assertEqual(checker.main(['x', *args, '--report', str(path)]), 1)


if __name__ == '__main__':
    unittest.main()
