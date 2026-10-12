# SPDX-License-Identifier: MIT
"""Fail-closed Task 1 inventory gate; deliberately not a native proof validator."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
BASE = ROOT / 'test/multimodal'
PIN = 'f2cc7282ce0042641df55e0e563b75e824e2b647'
DIRECTIONS = {'text-to-image', 'text-to-audio', 'text-to-video', 'media-to-text'}
FORMS = {'plain-text', 'structured-text', 'image', 'audio', 'silent-video', 'audiovisual-video', 'mixed-item'}
FAILURES = {'missing-projector': 501, 'missing-decoder': 501, 'unsupported-model': 422,
            'old-backend': 501, 'malformed-structure': 400, 'malformed-media': 415,
            'overflow': 413, 'context-overflow': 413, 'capacity': 429,
            'fetch-failure': 502, 'corrupt-result': 502, 'deadline': 504,
            'cancellation': 'cancelled-no-partial-response', 'model-reload': 'identity-invalidated'}
REQUIRED = FORMS | set(FAILURES) | {'ordered-mixed-batch', 'media-change', 'soundtrack-change', 'mixed-no-dropped-parts'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def fields(value, names):
    require(isinstance(value, dict), 'expected object')
    require(all(name in value for name in names), 'missing fields: ' + ', '.join(names))


def sha(value, length=40):
    return isinstance(value, str) and re.fullmatch('[0-9a-f]{%d}' % length, value) is not None


def validate_report(report, required_cases, mode):
    fields(report, ['status', 'blockers', 'command', 'revision', 'counts', 'cases', 'exit_status', 'evidence_paths'])
    # Task 1 has neither an authorized runtime nor a runtime-evidence validator.
    # Do not accept arbitrary JSON declaring PASS as a substitute for either.
    require(mode == 'inventory', 'native evidence validation unavailable in Task 1')
    require(isinstance(report['blockers'], list) and report['blockers'] and
            all(isinstance(b, str) and b.strip() for b in report['blockers']), 'invalid blockers')
    require(report['status'] == 'PENDING', 'native proof must remain explicitly PENDING with blockers')
    require(sha(report['revision']) and isinstance(report['command'], str) and report['command'].strip(), 'invalid command/revision provenance')
    require(type(report['exit_status']) is int and report['exit_status'] == 0, 'command failed')
    counts = report['counts']
    fields(counts, ['expected', 'executed', 'passed', 'skipped', 'pending'])
    require(all(type(v) is int and v >= 0 for v in counts.values()), 'invalid counts')
    require(counts['expected'] == counts['executed'] == counts['passed'] > 0, 'missing/failed tests')
    require(counts['skipped'] == counts['pending'] == 0, 'skipped/pending required tests')
    cases = report['cases']
    require(isinstance(cases, list) and cases, 'missing cases')
    for case in cases:
        fields(case, ['id', 'status'])
        require(isinstance(case['id'], str) and case['id'].strip(), 'invalid case ID')
        require(case['status'] == 'PASS', 'required case not passed')
    ids = [case['id'] for case in cases]
    require(len(ids) == len(set(ids)) == counts['executed'], 'case/count mismatch')
    require(set(ids) == set(required_cases), 'required cases mismatch')
    require(isinstance(report['evidence_paths'], list) and report['evidence_paths'], 'missing evidence')
    for path in report['evidence_paths']:
        require(isinstance(path, str) and path.strip(), 'invalid evidence path')
        resolved = (ROOT / path).resolve()
        require(resolved.is_relative_to(ROOT / '.sdd-work') and resolved.is_file(), 'missing or unsafe evidence path')


def load_inventory():
    contract = json.loads((BASE / 'acceptance-contract.json').read_text())
    manifest = json.loads((BASE / 'fixtures/manifest.json').read_text())
    text = (ROOT / 'docs/design/multimodal-embeddings-feasibility.md').read_text()
    blocks = re.findall(r'```json\n(.*?)\n```', text, re.S)
    require(len(blocks) == 1, 'missing/ambiguous dossier register')
    return contract, manifest, json.loads(blocks[0])


def validate_inventory(c, m, d):
    fields(c, ['schema_version', 'contract_version', 'frozen', 'freeze_policy', 'native_proof', 'required_case_ids', 'directions', 'queries_per_direction', 'distractors_per_query', 'queries', 'space', 'thresholds', 'pairs', 'input_forms', 'ordered_mixed_batch', 'failure_matrix', 'provenance', 'preprocessing'])
    fields(m, ['schema_version', 'status', 'materialization_policy', 'fixtures'])
    require(c['schema_version'] == m['schema_version'] == 1 and c['frozen'] is True, 'contract version/freeze missing')
    require(c['native_proof'].get('status') == 'PENDING' and c['native_proof'].get('blockers'), 'native proof cannot PASS without runtime')
    require(set(c['directions']) == DIRECTIONS, 'missing direction')
    require(c['queries_per_direction'] >= 3 and c['distractors_per_query'] >= 3, 'insufficient declared counts')
    required = c['required_case_ids']
    require(len(required) == len(set(required)) and REQUIRED <= set(required), 'missing/duplicate required cases')
    require(set(c['input_forms']) == FORMS and all(c['input_forms'].values()), 'missing input forms')
    require(c['failure_matrix'] == FAILURES, 'failure matrix differs from specification')
    require(c['space'].get('same_model_projector_pooling_dimensions_preprocessing') is True and c['space'].get('no_caption_ocr_transcription') is True, 'single space required')
    require(c['provenance'].get('native_revision') == PIN, 'invalid native provenance')
    for key in ['localai_baseline', 'spec_baseline']:
        require(sha(c['provenance'].get(key)), 'invalid revision')
    runtime_fields = {'model_identity', 'model_sha256', 'projector_identity', 'projector_sha256', 'license', 'localai_revision', 'backend_revision', 'backend_binary_sha256', 'decoder_versions', 'hardware', 'dimensions', 'pooling', 'preprocessing_version', 'command', 'exit_status', 'counts', 'case_ids', 'raw_vectors', 'similarities', 'margins', 'cleanup_evidence'}
    require(runtime_fields <= set(c['provenance'].get('required_runtime_fields', [])), 'incomplete runtime provenance requirements')
    fixtures = {}
    for f in m['fixtures']:
        fields(f, ['id', 'path', 'sha256', 'status', 'license', 'ownership', 'media_format', 'expected_tracks', 'expected_duration_seconds', 'semantic', 'semantic_target', 'distractor_groups'])
        require(f['id'] not in fixtures, 'duplicate fixture')
        fixtures[f['id']] = f
        path = Path(f['path'])
        full = (BASE / 'fixtures' / path).resolve()
        require(not path.is_absolute() and '..' not in path.parts and full.is_relative_to(BASE / 'fixtures'), 'unsafe fixture path')
        require(all(f[k] for k in ['license', 'ownership', 'media_format', 'semantic', 'semantic_target']), 'incomplete fixture provenance')
        require(f['expected_tracks'].keys() == {'video', 'audio'} and all(type(v) is int and 0 <= v <= 1 for v in f['expected_tracks'].values()), 'invalid tracks')
        require(type(f['expected_duration_seconds']) in (int, float) and 0 <= f['expected_duration_seconds'] <= 30, 'invalid duration')
        if f['status'] == 'planned':
            require(f['sha256'] is None and not full.exists(), 'planned fixture cannot claim bytes/checksum')
        else:
            require(f['status'] == 'materialized' and sha(f['sha256'], 64) and full.is_file(), 'invalid materialized fixture')
            require(hashlib.sha256(full.read_bytes()).hexdigest() == f['sha256'], 'fixture checksum mismatch')
    require(fixtures and m['status'] in ('planned', 'materialized'), 'empty/invalid manifest')
    ids = set()
    for q in c['queries']:
        fields(q, ['id', 'direction', 'query_text', 'query_fixture', 'target', 'distractors', 'space'])
        require(q['id'] not in ids and q['id'] in required, 'missing/duplicate query case')
        ids.add(q['id'])
        require(q['direction'] in DIRECTIONS and q['space'] == c['space']['id'], 'invalid direction/space')
        candidates = [q['target']] + q['distractors']
        require(len(q['distractors']) >= c['distractors_per_query'] and len(set(candidates)) == len(candidates), 'insufficient distinct distractors')
        require(all(x in fixtures for x in candidates), 'missing target/distractor fixture')
        target = fixtures[q['target']]
        require(all(fixtures[x]['semantic_target'] != target['semantic_target'] for x in q['distractors']), 'related distractor')
        if q['direction'] == 'media-to-text':
            require(q['query_fixture'] in fixtures and q['query_text'] is None, 'missing media query')
            require(q['target'].startswith('text-'), 'wrong target modality')
            require(fixtures[q['query_fixture']]['semantic_target'] == target['semantic_target'], 'wrong semantic match')
        else:
            medium = q['direction'].removeprefix('text-to-')
            require(isinstance(q['query_text'], str) and q['query_text'] and q['query_fixture'] is None, 'missing fixed text query')
            require(all(x.startswith(medium + '-') for x in candidates), 'wrong retrieval modality')
    for direction in DIRECTIONS:
        require(sum(q['direction'] == direction for q in c['queries']) >= c['queries_per_direction'], 'missing queries')
    media_queries = [q['query_fixture'] for q in c['queries'] if q['direction'] == 'media-to-text']
    require(any(x.startswith('image-') for x in media_queries) and any(x.startswith('audio-') for x in media_queries), 'image/audio queries required')
    t = c['thresholds']
    require(t == {'retrieval_min_cosine_margin': 0.01, 'media_change_min_cosine_distance': 0.001, 'soundtrack_change_min_cosine_distance': 0.001, 'finite_nonzero_vectors': True, 'rank': 1, 'ties_pass': False, 'record_raw_vectors_similarities_margins': True}, 'threshold policy drift')
    for name in ['media-change', 'soundtrack-change']:
        pair = c['pairs'].get(name, {})
        require(pair.get('left') in fixtures and pair.get('right') in fixtures and pair['left'] != pair['right'] and pair.get('surrounding_text'), 'missing ablation pair')
    require(c['pairs']['soundtrack-change'].get('identical_frames') is True, 'soundtrack ablation needs identical frames')
    batch = c['ordered_mixed_batch']
    require(set(batch.get('items', [])) == FORMS and sorted(batch.get('permutation', [])) == list(range(7)) and batch['permutation'] != list(range(7)), 'missing reordered mixed batch')
    fields(d, ['schema_version', 'native_proof', 'blockers', 'candidate', 'model_identity', 'model_sha256', 'projector_identity', 'projector_sha256', 'license', 'native_revision', 'localai_revision', 'modality_evidence', 'dimensions', 'pooling', 'decoder_versions', 'hardware', 'preprocessing_version', 'commands_results', 'peak_storage', 'approval'])
    require(all(d.values()), 'incomplete dossier')
    require(d['native_proof'] == 'PENDING' and d['blockers'] and d['native_revision'] == PIN, 'inconsistent dossier/native status')
    require(d['localai_revision'] == c['provenance']['localai_baseline'], 'dossier revision mismatch')
    require(d['preprocessing_version'] == c['preprocessing']['version'], 'preprocessing mismatch')
    require(d['modality_evidence'] == dict.fromkeys(['text', 'image', 'audio', 'silent-video', 'audiovisual-video', 'mixed'], 'PENDING'), 'unproven modality claim')
    for key in ['model_sha256', 'projector_sha256']:
        require(d[key] == 'unknown' or sha(d[key], 64), 'invalid artifact checksum')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['inventory', 'native'])
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    try:
        require(args.mode == 'inventory', 'native proof PENDING: no approved runtime evidence validator yet')
        c, m, d = load_inventory()
        validate_inventory(c, m, d)
        if args.report:
            report = json.loads(args.report.read_text())
            validate_report(report, {'inventory'}, args.mode)
        print(f"Inventory complete: {len(c['required_case_ids'])} planned cases, {len(c['queries'])} queries, {len(m['fixtures'])} planned fixtures; native proof PENDING")
    except (ValueError, KeyError, TypeError, OSError) as exc:
        print(f'FAIL: {exc}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
