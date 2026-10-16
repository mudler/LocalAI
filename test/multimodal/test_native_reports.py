# SPDX-License-Identifier: MIT
"""Negative-first tests for the native proof report checker.

The reports built here are synthetic. They exercise the checker only and are
never accepted as runtime evidence.
"""
import base64
import copy
import hashlib
import json
import math
from pathlib import Path
import struct
import unittest

import check_native_reports as checker

BASE = Path(__file__).resolve().parent
CONTRACT = json.loads((BASE / 'acceptance-contract.json').read_text())
MANIFEST = json.loads((BASE / 'fixtures/manifest.json').read_text())
FIXTURE_SHA = {f['id']: f['sha256'] for f in MANIFEST['fixtures']}
DIM = 8
MODEL = 'a' * 64
PROJECTOR = 'b' * 64
VERSION = CONTRACT['preprocessing']['version']
TOPICS = ['bell', 'drum', 'water', 'typing']
TEXTS = {'bell': 'A hand rings a brass bell.', 'drum': 'A hand strikes a small drum.',
         'water': 'Water pours from a jug into a glass.', 'typing': 'Hands type on a computer keyboard.'}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def unit(values):
    norm = math.sqrt(sum(v * v for v in values))
    return [v / norm for v in values]


def vec(topic, salt=0.0, extra=None):
    """A vector near the topic axis; salt moves it a little."""
    base = [0.0] * DIM
    base[TOPICS.index(topic)] = 1.0
    base[4 + (int(salt * 10) % 4)] += 0.2 + salt
    if extra:
        base[7] += extra
    return unit(base)


def encode(values):
    raw = struct.pack('<%df' % len(values), *values)
    return {'dimensions': len(values), 'dtype': 'float32-le', 'sha256': sha(raw),
            'base64': base64.b64encode(raw).decode()}


def text_part(text):
    return {'kind': 'text', 'text': text, 'sha256': sha(text.encode())}


def media_part(kind, fixture):
    return {'kind': kind, 'fixture': fixture, 'source_sha256': FIXTURE_SHA[fixture], 'sha256': sha(b'sent-' + fixture.encode())}


def video_parts(fixture, audio=True, frames=None):
    frames = frames or [0, 300, 500]
    parts = [text_part('Video:\n')]
    for ms in frames:
        parts.append(text_part('Timestamp: %d ms\n' % ms))
        parts.append({'kind': 'image', 'fixture': None, 'frame_ms': ms, 'sha256': sha(b'frame-%d' % ms)})
    if audio:
        parts.append(text_part('Audio track:\n'))
        parts.append({'kind': 'audio', 'fixture': fixture, 'source_sha256': FIXTURE_SHA[fixture],
                      'sha256': sha(b'pcm-' + fixture.encode())})
    return parts


def video_info(fixture, audio=True, frames=None, source=None):
    frames = frames or [0, 300, 500]
    source = source or list(range(0, 600, 100))
    return {'fixture': fixture, 'source_sha256': FIXTURE_SHA[fixture], 'duration_ms': 600, 'source_frames_ms': source, 'sample_times_ms': [0, 250, 500],
            'selected_frames_ms': frames, 'frame_sha256': [sha(b'frame-%d' % ms) for ms in frames],
            'audio_pcm_sha256': sha(b'pcm-' + fixture.encode()) if audio else None,
            'audio_samples': 32000 if audio else 0}


class Builder:
    def __init__(self):
        self.items = {}
        self.add('plain-text', [text_part(TEXTS['bell'])], vec('bell', 0.1), form='plain')
        self.add('structured-text', [text_part(TEXTS['bell'])], vec('bell', 0.1), form='content')
        for topic in TOPICS:
            self.add('text-' + topic, [text_part(TEXTS[topic])], vec(topic, 0.0), form='plain')
            self.add('image-' + topic, [media_part('image', 'image-' + topic)], vec(topic, 0.2), tokens=85)
            self.add('audio-' + topic, [media_part('audio', 'audio-' + topic)], vec(topic, 0.3), tokens=54)
            self.add('video-' + topic, video_parts('video-' + topic), vec(topic, 0.4), tokens=300,
                     video=video_info('video-' + topic))
        self.add('image', [media_part('image', 'image-bell')], vec('bell', 0.2), tokens=85)
        self.add('audio', [media_part('audio', 'audio-bell')], vec('bell', 0.3), tokens=54)
        self.add('silent-video', video_parts('video-bell-silent', audio=False), vec('bell', 0.5), tokens=270,
                 video=video_info('video-bell-silent', audio=False))
        self.add('audiovisual-video', video_parts('video-bell'), vec('bell', 0.4), tokens=300,
                 video=video_info('video-bell'))
        mixed = [text_part(TEXTS['bell']), media_part('image', 'image-bell'), media_part('audio', 'audio-bell')] + video_parts('video-bell')
        self.add('mixed-item', mixed, vec('bell', 0.6), tokens=9 + 85 + 54 + 300 - 3 * 2,
                 video=video_info('video-bell'))
        self.add('mixed-without-image', [mixed[0], mixed[2]] + mixed[3:], vec('bell', 0.7), tokens=300)
        self.add('mixed-without-audio', [mixed[0], mixed[1]] + mixed[3:], vec('bell', 0.8), tokens=300)
        self.add('mixed-without-video', mixed[:3], vec('bell', 0.9), tokens=150)
        self.add('media-change-left', [text_part('Find related evidence'), media_part('image', 'image-bell')], vec('bell', 0.2), tokens=100)
        self.add('media-change-right', [text_part('Find related evidence'), media_part('image', 'image-drum')], vec('drum', 0.2), tokens=100)
        self.add('soundtrack-change-left', [text_part('Find related evidence')] + video_parts('video-bell'), vec('bell', 0.4), tokens=300,
                 video=video_info('video-bell'))
        right = video_info('video-bell-drum-track')
        right['frame_sha256'] = video_info('video-bell')['frame_sha256']
        self.add('soundtrack-change-right', [text_part('Find related evidence')] + video_parts('video-bell-drum-track'), vec('drum', 0.4),
                 tokens=300, video=right)
        for q in CONTRACT['queries']:
            if q['query_text'] is not None:
                self.add('query:' + q['id'], [text_part(q['query_text'])], vec(q['target'].split('-')[1], 0.05), form='plain')
            else:
                fixture = q['query_fixture']
                self.items['query:' + q['id']] = copy.deepcopy(self.items[fixture])
                self.items['query:' + q['id']]['vector'] = encode(vec(fixture.split('-')[1], 0.1))

    def add(self, key, parts, vector, tokens=9, form='content', video=None):
        self.items[key] = {'parts': parts, 'tokens': tokens, 'input_form': form,
                           'model_sha256': MODEL, 'projector_sha256': PROJECTOR, 'pooling': 'mean',
                           'preprocessing_version': VERSION, 'vector': encode(vector)}
        if video:
            self.items[key]['video'] = video

    def report(self):
        report = {
            'schema_version': 1, 'kind': 'multimodal-native-proof-report', 'status': 'PASS',
            'contract_version': CONTRACT['contract_version'], 'blockers': [], 'failures': [],
            'command': 'python3 test/multimodal/native_probe/probe.py', 'exit_status': 0,
            'identity': {'model_identity': 'repo@rev:model.gguf', 'model_sha256': MODEL,
                         'projector_identity': 'repo@rev:mmproj.gguf', 'projector_sha256': PROJECTOR,
                         'license': {'declared': 'apache-2.0', 'source': 'model card'},
                         'pooling': 'mean', 'dimensions': DIM, 'preprocessing_version': VERSION,
                         'special_tokens_per_item': 2},
            'source': {'native_revision': CONTRACT['provenance']['native_revision'],
                       'upstream_head': CONTRACT['provenance']['native_revision'],
                       'backend_binary_sha256': 'c' * 64, 'backend_version': 'version: 1 (f2cc728)'},
            'decoder': {'versions': {'ffmpeg': '6.1.1', 'ffprobe': '6.1.1'}, 'config': {'audio_rate_hz': 16000}},
            'hardware': {'cpu_model': 'cpu', 'cores': 16, 'ram_bytes': 1 << 30, 'os': 'linux'},
            'approval': {'recorded_by': 'root', 'actions': ['native-build', 'model-download']},
            'server': {'modalities': {'vision': True, 'audio': True, 'video': True}, 'context': 16384, 'instance': 'one'},
            'fixtures': dict(FIXTURE_SHA),
            'items': copy.deepcopy(self.items),
            'counts': {}, 'cases': [], 'similarities': {}, 'margins': {},
        }
        return finalize(report)


def finalize(report):
    """Fill cases, similarities and margins the way an honest probe would."""
    result = checker.evaluate(report, CONTRACT)
    report['cases'] = [{'id': k, 'status': v['status'], 'detail': v['detail']} for k, v in result['cases'].items()]
    report['similarities'] = result['similarities']
    report['margins'] = result['margins']
    failed = [c['id'] for c in report['cases'] if c['status'] != 'PASS']
    report['status'] = 'FAIL' if failed else 'PASS'
    report['failures'] = failed
    report['counts'] = {'expected': len(report['cases']), 'executed': len(report['cases']),
                        'passed': len(report['cases']) - len(failed), 'failed': len(failed)}
    return report


class NativeReportTests(unittest.TestCase):
    def setUp(self):
        self.builder = Builder()
        self.dossier = {'model_sha256': MODEL, 'projector_sha256': PROJECTOR, 'preprocessing_version': VERSION}

    def check(self, report):
        return checker.validate_native_report(report, CONTRACT, MANIFEST, self.dossier)

    def refinalize(self, report):
        return finalize(report)

    # Rejections first.
    def test_absent_audio_in_audiovisual_video(self):
        item = self.builder.items['audiovisual-video']
        item['parts'] = [p for p in item['parts'] if p['kind'] != 'audio' and p.get('text') != 'Audio track:\n']
        item['video']['audio_pcm_sha256'] = None
        report = self.builder.report()
        self.assertEqual(report['status'], 'FAIL')
        self.assertEqual(self.check(report), 'FAIL')
        report['status'] = 'PASS'
        with self.assertRaises(ValueError):
            self.check(report)

    def test_identical_soundtrack_vectors(self):
        items = self.builder.items
        items['soundtrack-change-right']['vector'] = copy.deepcopy(items['soundtrack-change-left']['vector'])
        report = self.builder.report()
        self.assertEqual(report['status'], 'FAIL')
        report['status'] = 'PASS'
        for case in report['cases']:
            if case['id'] == 'soundtrack-change':
                case['status'] = 'PASS'
        with self.assertRaises(ValueError):
            self.check(report)

    def test_soundtrack_frames_must_be_identical(self):
        item = self.builder.items['soundtrack-change-right']
        item['video']['frame_sha256'][1] = sha(b'other')
        item['parts'][5]['sha256'] = sha(b'other')
        report = self.builder.report()
        self.assertEqual(self.check(report), 'FAIL')

    def test_inconsistent_dimensions(self):
        self.builder.items['audio-drum']['vector'] = encode([1.0, 0.0, 0.0, 0.0])
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_unrelated_model_identity(self):
        self.builder.items['audio-drum']['model_sha256'] = 'd' * 64
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_unrelated_projector_identity(self):
        self.builder.items['image-drum']['projector_sha256'] = 'd' * 64
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_report_identity_differs_from_dossier(self):
        report = self.builder.report()
        self.dossier['model_sha256'] = 'e' * 64
        with self.assertRaises(ValueError):
            self.check(report)

    def test_preprocessing_version_must_match_contract(self):
        report = self.builder.report()
        report['identity']['preprocessing_version'] = 'mm-v0'
        with self.assertRaises(ValueError):
            self.check(report)

    def test_forged_vector_hash(self):
        report = self.builder.report()
        report['items']['image']['vector']['sha256'] = '0' * 64
        with self.assertRaises(ValueError):
            self.check(report)

    def test_nonfinite_or_zero_vector(self):
        for bad in ([0.0] * DIM, [float('nan')] + [1.0] * (DIM - 1)):
            with self.subTest(bad=bad):
                self.builder.items['image']['vector'] = encode(bad)
                with self.assertRaises(ValueError):
                    self.check(self.builder.report())

    def test_retrieval_tie_fails(self):
        items = self.builder.items
        items['image-drum']['vector'] = copy.deepcopy(items['image-bell']['vector'])
        report = self.builder.report()
        self.assertEqual(report['status'], 'FAIL')
        self.assertIn('text-to-image-bell', report['failures'])
        report['status'] = 'PASS'
        with self.assertRaises(ValueError):
            self.check(report)

    def test_margin_below_threshold_fails(self):
        items = self.builder.items
        mix = [a * 0.9995 + b * 0.0005 for a, b in zip(vec('bell', 0.2), vec('drum', 0.2))]
        items['image-drum']['vector'] = encode(unit(mix))
        report = self.builder.report()
        self.assertIn('text-to-image-bell', report['failures'])

    def test_claimed_pass_with_failed_case(self):
        report = self.builder.report()
        report['cases'][0]['status'] = 'FAIL'
        with self.assertRaises(ValueError):
            self.check(report)

    def test_missing_required_case(self):
        report = self.builder.report()
        report['cases'] = [c for c in report['cases'] if c['id'] != 'image']
        with self.assertRaises(ValueError):
            self.check(report)

    def test_missing_item_vector(self):
        del self.builder.items['video-water']
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_fixture_hash_must_match_manifest(self):
        report = self.builder.report()
        report['fixtures']['image-bell'] = 'f' * 64
        with self.assertRaises(ValueError):
            self.check(report)

    def test_frame_sampling_policy_recomputed(self):
        video = self.builder.items['silent-video']['video']
        video['selected_frames_ms'] = [0, 100, 200]
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_timestamp_text_must_match_selected_frame(self):
        item = self.builder.items['audiovisual-video']
        item['parts'][1] = text_part('Timestamp: 7 ms\n')
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_marker_order(self):
        parts = self.builder.items['audiovisual-video']['parts']
        parts[0], parts[1] = parts[1], parts[0]
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_mixed_item_order(self):
        parts = self.builder.items['mixed-item']['parts']
        parts[1], parts[2] = parts[2], parts[1]
        with self.assertRaises(ValueError):
            self.check(self.builder.report())

    def test_mixed_dropped_part_fails(self):
        items = self.builder.items
        items['mixed-without-audio']['vector'] = copy.deepcopy(items['mixed-item']['vector'])
        report = self.builder.report()
        self.assertIn('mixed-no-dropped-parts', report['failures'])

    def test_mixed_token_accounting_fails_when_part_dropped(self):
        self.builder.items['mixed-item']['tokens'] = 100
        report = self.builder.report()
        self.assertIn('mixed-no-dropped-parts', report['failures'])

    def test_failed_exit_status(self):
        report = self.builder.report()
        report['exit_status'] = 1
        with self.assertRaises(ValueError):
            self.check(report)

    def test_wrong_native_revision(self):
        report = self.builder.report()
        report['source']['upstream_head'] = '1' * 40
        with self.assertRaises(ValueError):
            self.check(report)

    def test_missing_provenance_fields(self):
        for field in ('hardware', 'decoder', 'approval', 'source', 'server'):
            with self.subTest(field=field):
                report = self.builder.report()
                del report[field]
                with self.assertRaises(ValueError):
                    self.check(report)

    def test_modality_capability_flags_alone_are_not_enough(self):
        report = self.builder.report()
        report['server']['modalities']['audio'] = False
        with self.assertRaises(ValueError):
            self.check(report)

    def test_blocked_requires_blockers(self):
        report = {'schema_version': 1, 'kind': 'multimodal-native-proof-report', 'status': 'BLOCKED',
                  'contract_version': CONTRACT['contract_version'], 'blockers': [], 'command': 'probe', 'exit_status': 2}
        with self.assertRaises(ValueError):
            self.check(report)
        report['blockers'] = ['model artifact absent']
        self.assertEqual(self.check(report), 'BLOCKED')

    def test_pending_is_not_a_probe_outcome(self):
        report = self.builder.report()
        report['status'] = 'PENDING'
        with self.assertRaises(ValueError):
            self.check(report)

    # Accepted outcomes.
    def test_complete_report_passes(self):
        report = self.builder.report()
        self.assertEqual(report['failures'], [])
        self.assertEqual(self.check(report), 'PASS')

    def test_all_required_cases_present(self):
        report = self.builder.report()
        ids = {c['id'] for c in report['cases']}
        self.assertEqual(ids, set(checker.required_cases(CONTRACT)))

    def test_recorded_failure_is_valid_and_not_pass(self):
        items = self.builder.items
        items['audio-bell']['vector'] = encode(vec('typing', 0.3))
        report = self.builder.report()
        self.assertEqual(report['status'], 'FAIL')
        self.assertEqual(self.check(report), 'FAIL')
        self.assertTrue(report['failures'])

    def test_margins_are_recomputed_from_vectors(self):
        report = self.builder.report()
        report['margins']['text-to-image-bell'] = 0.9
        with self.assertRaises(ValueError):
            self.check(report)


if __name__ == '__main__':
    unittest.main()
