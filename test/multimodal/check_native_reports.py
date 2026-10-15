#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Validate a native multimodal proof report against the frozen contract.

Outcomes: PASS, FAIL (a valid, honestly recorded measurement that misses the
contract) and BLOCKED (the probe could not run). PENDING is not an outcome.
Cosines, margins and ranks are recomputed from the raw vectors in the report;
claimed values are never trusted. An inconsistent or forged report raises
ValueError. Exit codes: 0 PASS, 3 recorded FAIL or BLOCKED, 1 invalid.
"""
import base64
import hashlib
import json
import math
from pathlib import Path
import re
import struct
import sys

BASE = Path(__file__).resolve().parent
PIN = 'f2cc7282ce0042641df55e0e563b75e824e2b647'
FORMS = ['plain-text', 'structured-text', 'image', 'audio', 'silent-video', 'audiovisual-video', 'mixed-item']
SPACE_CASE = 'single-space'
EXTRA = ['media-change', 'soundtrack-change', 'mixed-no-dropped-parts', SPACE_CASE]
TIMESTAMP = re.compile(r'Timestamp: (0|[1-9][0-9]*) ms\n')
FPS = 4
STEP_MS = 1000 // FPS
TOKEN_SLACK = 2


def require(condition, message):
    if not condition:
        raise ValueError(message)


def is_sha(value, length=64):
    return isinstance(value, str) and re.fullmatch('[0-9a-f]{%d}' % length, value) is not None


def required_cases(contract):
    return FORMS + EXTRA + [q['id'] for q in contract['queries']]


def decode_vector(item, dimensions, key):
    entry = item.get('vector')
    require(isinstance(entry, dict) and entry.get('dtype') == 'float32-le', 'missing vector: ' + key)
    require(entry.get('dimensions') == dimensions, 'inconsistent dimensions: ' + key)
    try:
        raw = base64.b64decode(entry['base64'], validate=True)
    except (KeyError, ValueError, TypeError):
        raise ValueError('undecodable vector: ' + key)
    require(len(raw) == 4 * dimensions, 'vector length differs from declared dimensions: ' + key)
    require(hashlib.sha256(raw).hexdigest() == entry.get('sha256'), 'vector hash mismatch: ' + key)
    values = struct.unpack('<%df' % dimensions, raw)
    require(all(math.isfinite(v) for v in values), 'non-finite vector: ' + key)
    require(any(v != 0.0 for v in values), 'zero vector: ' + key)
    return [float(v) for v in values]


def cosine(a, b):
    dot = sum(x * y for x, y in zip(a, b))
    return dot / (math.sqrt(sum(x * x for x in a)) * math.sqrt(sum(y * y for y in b)))


def parse_video_parts(parts, key):
    """Return (selected frame ms list, audio part or None) for one expansion."""
    require(parts and parts[0].get('kind') == 'text' and parts[0].get('text') == 'Video:\n', 'video must start with Video: marker: ' + key)
    frames = []
    i = 1
    while i + 1 < len(parts) and parts[i].get('kind') == 'text' and TIMESTAMP.fullmatch(parts[i].get('text', '')):
        match = TIMESTAMP.fullmatch(parts[i]['text'])
        frame = parts[i + 1]
        require(frame.get('kind') == 'image' and frame.get('frame_ms') == int(match[1]), 'timestamp marker must precede its frame: ' + key)
        frames.append(int(match[1]))
        i += 2
    require(frames, 'video without frames: ' + key)
    audio = None
    if i < len(parts):
        require(i + 2 == len(parts) and parts[i].get('kind') == 'text' and parts[i].get('text') == 'Audio track:\n'
                and parts[i + 1].get('kind') == 'audio', 'malformed video tail: ' + key)
        audio = parts[i + 1]
        i += 2
    return frames, audio, i


def expected_selection(source, duration_ms):
    require(isinstance(source, list) and source and all(type(v) is int for v in source), 'invalid source timestamps')
    require(source[0] == 0 and all(b > a for a, b in zip(source, source[1:])), 'source timestamps must start at zero and increase')
    require(type(duration_ms) is int and duration_ms > source[-1], 'invalid duration')
    times, selected, t = [], [], 0
    while t < duration_ms:
        times.append(t)
        frame = next((v for v in source if v >= t), None)
        if frame is not None and (not selected or selected[-1] != frame):
            selected.append(frame)
        t += STEP_MS
    return times, selected


def check_video(item, parts, key, manifest_sha, want_audio):
    """Validate the expansion at the start of parts; return (frames, audio_part, parts_consumed, status_detail)."""
    video = item.get('video')
    require(isinstance(video, dict), 'missing video evidence: ' + key)
    frames, audio, used = parse_video_parts(parts, key)
    times, selected = expected_selection(video.get('source_frames_ms'), video.get('duration_ms'))
    require(video.get('sample_times_ms') == times, 'sample times violate 4 fps policy: ' + key)
    require(video.get('selected_frames_ms') == selected == frames, 'frame selection violates sampling policy: ' + key)
    require(video.get('source_sha256') == manifest_sha.get(video.get('fixture')), 'video fixture hash mismatch: ' + key)
    images = [p for p in parts[:used] if p['kind'] == 'image']
    require([p.get('sha256') for p in images] == video.get('frame_sha256'), 'frame hashes differ from sent frames: ' + key)
    if audio is None:
        require(video.get('audio_pcm_sha256') is None, 'audio evidence without audio part: ' + key)
    else:
        require(video.get('audio_pcm_sha256') == audio.get('sha256') and is_sha(audio.get('sha256')), 'audio hash mismatch: ' + key)
        require(type(video.get('audio_samples')) is int and video['audio_samples'] > 0, 'empty audio: ' + key)
    return frames, audio, used


def media_ok(part, kind, fixture, manifest_sha, key):
    require(part.get('kind') == kind and part.get('fixture') == fixture, 'wrong part for %s: %s' % (fixture, key))
    require(part.get('source_sha256') == manifest_sha[fixture] and is_sha(part.get('sha256')), 'media hash mismatch: ' + key)


def text_ok(part, text, key):
    require(part.get('kind') == 'text' and part.get('text') == text, 'wrong text part: ' + key)
    require(part.get('sha256') == hashlib.sha256(text.encode()).hexdigest(), 'text hash mismatch: ' + key)


def check_fixture_parts(item, fixture_id, key, manifest_sha, texts, want_audio=None):
    """Parts of an item that should contain exactly one fixture's expansion. Returns status or raises."""
    parts = item['parts']
    modality = fixture_id.split('-')[0]
    if modality == 'text':
        require(len(parts) == 1, 'unexpected parts: ' + key)
        text_ok(parts[0], texts[fixture_id], key)
        return 'PASS', ''
    if modality in ('image', 'audio'):
        require(len(parts) == 1, 'unexpected parts: ' + key)
        media_ok(parts[0], modality, fixture_id, manifest_sha, key)
        return 'PASS', ''
    frames, audio, used = check_video(item, parts, key, manifest_sha, want_audio)
    require(used == len(parts) and item['video']['fixture'] == fixture_id, 'unexpected parts: ' + key)
    expected_audio = fixture_id != 'video-bell-silent'
    if expected_audio and audio is None:
        return 'FAIL', 'soundtrack absent from the sent sequence'
    require(expected_audio or audio is None, 'silent video carries audio: ' + key)
    return 'PASS', ''


def check_space(report, contract):
    ident = report['identity']
    dims = ident.get('dimensions')
    require(type(dims) is int and dims > 0, 'invalid dimensions')
    for field in ('model_sha256', 'projector_sha256'):
        require(is_sha(ident.get(field)), 'invalid identity: ' + field)
    for field in ('model_identity', 'projector_identity', 'pooling'):
        require(isinstance(ident.get(field), str) and ident[field].strip(), 'missing identity field: ' + field)
    require(isinstance(ident.get('license'), dict) and ident['license'], 'missing license')
    require(ident.get('preprocessing_version') == contract['preprocessing']['version'], 'preprocessing version differs from contract')
    require(type(ident.get('special_tokens_per_item')) is int and ident['special_tokens_per_item'] >= 0, 'missing special token count')
    for key, item in report['items'].items():
        require(item.get('model_sha256') == ident['model_sha256'], 'unrelated model identity: ' + key)
        require(item.get('projector_sha256') == ident['projector_sha256'], 'unrelated projector identity: ' + key)
        require(item.get('pooling') == ident['pooling'], 'inconsistent pooling: ' + key)
        require(item.get('preprocessing_version') == ident['preprocessing_version'], 'inconsistent preprocessing: ' + key)
        require(type(item.get('tokens')) is int and item['tokens'] > 0, 'missing token count: ' + key)
    return dims


def evaluate(report, contract, manifest=None):
    """Recompute every case from raw vectors. Returns cases, similarities and margins."""
    thresholds = contract['thresholds']
    require(isinstance(report.get('items'), dict) and report['items'], 'missing items')
    dims = check_space(report, contract)
    items = report['items']
    if manifest is None:
        manifest = json.loads((BASE / 'fixtures/manifest.json').read_text())
    manifest_sha = {f['id']: f['sha256'] for f in manifest['fixtures']}
    texts = {}
    vectors = {}

    def get(key):
        require(key in items, 'missing item: ' + key)
        if key not in vectors:
            vectors[key] = decode_vector(items[key], dims, key)
        return vectors[key]

    for key in items:
        get(key)
    # A text fixture's bytes are its manifest semantic, which the contract queries reuse verbatim.
    for fixture in manifest['fixtures']:
        if fixture['id'].startswith('text-'):
            texts[fixture['id']] = fixture['semantic']
    cases = {}

    def record(case, status, detail):
        cases[case] = {'status': status, 'detail': detail}

    def distance(a, b):
        return 1.0 - cosine(get(a), get(b))

    # Seven input forms.
    forms = contract['input_forms']
    item = items['plain-text']
    require(item.get('input_form') == 'plain' and len(item['parts']) == 1, 'plain text must be a bare string')
    text_ok(item['parts'][0], forms['plain-text'], 'plain-text')
    record('plain-text', 'PASS', 'tokens=%d' % item['tokens'])
    item = items['structured-text']
    require(item.get('input_form') == 'content' and len(item['parts']) == 1, 'structured text must be a content object')
    text_ok(item['parts'][0], forms['structured-text'][0]['text'], 'structured-text')
    record('structured-text', 'PASS', 'tokens=%d' % item['tokens'])
    for form in ('image', 'audio', 'silent-video', 'audiovisual-video'):
        fixture = forms[form][0]
        status, detail = check_fixture_parts(items[form], fixture, form, manifest_sha, texts)
        if form == 'silent-video':
            require(items[form]['video']['audio_pcm_sha256'] is None, 'silent video has audio')
        if status == 'PASS' and form in ('image', 'audio', 'silent-video', 'audiovisual-video'):
            modalities = report.get('server', {}).get('modalities', {})
            need = {'image': 'vision', 'audio': 'audio', 'silent-video': 'video', 'audiovisual-video': 'audio'}[form]
            require(modalities.get(need) is True, 'capability flags contradict a vector produced for ' + form)
        record(form, status, detail or 'tokens=%d' % items[form]['tokens'])

    # Mixed ordered item: text, image, audio, then the video expansion, in contract order.
    mixed = items['mixed-item']
    status, detail = 'PASS', ''
    parts = mixed['parts']
    cursor = 0
    ranges = {}
    for fid in forms['mixed-item']:
        modality = fid.split('-')[0]
        start = cursor
        if modality in ('text', 'image', 'audio'):
            require(cursor < len(parts), 'mixed item lost parts')
            if modality == 'text':
                text_ok(parts[cursor], texts[fid], 'mixed-item')
            else:
                media_ok(parts[cursor], modality, fid, manifest_sha, 'mixed-item')
            cursor += 1
        else:
            _, audio, used = check_video({'video': mixed.get('video')}, parts[cursor:], 'mixed-item', manifest_sha, True)
            require(mixed['video']['fixture'] == fid, 'mixed item video fixture differs')
            if audio is None:
                status, detail = 'FAIL', 'soundtrack absent from mixed item'
            cursor += used
        ranges[modality] = (start, cursor)
    require(cursor == len(parts), 'mixed item has extra parts')
    record('mixed-item', status, detail or 'tokens=%d' % mixed['tokens'])

    # Dropped parts: token accounting plus leave-one-out vector distance.
    drop_status, drop_detail = 'PASS', []
    require(all(f in items for f in forms['mixed-item']), 'missing constituent fixture items')
    constituents = [items[fid] for fid in forms['mixed-item']]
    special = report['identity']['special_tokens_per_item']
    expected = sum(c['tokens'] for c in constituents) - (len(constituents) - 1) * special
    if abs(mixed['tokens'] - expected) > TOKEN_SLACK:
        drop_status = 'FAIL'
        drop_detail.append('tokens %d differ from constituent sum %d' % (mixed['tokens'], expected))
    for name in ('image', 'audio', 'video'):
        key = 'mixed-without-' + name
        require(key in items, 'missing leave-one-out item: ' + key)
        start, end = ranges[name]
        require(items[key]['parts'] == parts[:start] + parts[end:], 'leave-one-out must remove exactly the %s part: %s' % (name, key))
        d = distance('mixed-item', key)
        if d < thresholds['media_change_min_cosine_distance']:
            drop_status = 'FAIL'
            drop_detail.append('%s ablation distance %.6f' % (name, d))
    record('mixed-no-dropped-parts', drop_status, '; '.join(drop_detail) or 'tokens and ablations consistent')

    # Media change and soundtrack change pairs.
    pairs = contract['pairs']
    surrounding = pairs['media-change']['surrounding_text']
    left, right = items['media-change-left'], items['media-change-right']
    for side, fid in ((left, pairs['media-change']['left']), (right, pairs['media-change']['right'])):
        require(len(side['parts']) == 2, 'media change item shape')
        text_ok(side['parts'][0], surrounding, 'media-change')
        media_ok(side['parts'][1], fid.split('-')[0], fid, manifest_sha, 'media-change')
    d = distance('media-change-left', 'media-change-right')
    ok = d >= thresholds['media_change_min_cosine_distance']
    record('media-change', 'PASS' if ok else 'FAIL', 'distance=%.6f' % d)

    surrounding = pairs['soundtrack-change']['surrounding_text']
    sides = []
    for key, fid in (('soundtrack-change-left', pairs['soundtrack-change']['left']), ('soundtrack-change-right', pairs['soundtrack-change']['right'])):
        side = items[key]
        require(side['parts'] and side['video'], 'soundtrack item shape')
        text_ok(side['parts'][0], surrounding, key)
        rest = {'video': side['video'], 'parts': side['parts'][1:]}
        frames, audio, used = check_video(rest, rest['parts'], key, manifest_sha, True)
        require(used == len(rest['parts']) and side['video']['fixture'] == fid, 'soundtrack item shape')
        sides.append((side['video'], audio))
    (lv, la), (rv, ra) = sides
    detail = []
    status = 'PASS'
    if lv['frame_sha256'] != rv['frame_sha256'] or lv['selected_frames_ms'] != rv['selected_frames_ms']:
        status = 'FAIL'
        detail.append('frames are not identical')
    if la is None or ra is None:
        status = 'FAIL'
        detail.append('soundtrack absent')
    elif la['sha256'] == ra['sha256']:
        status = 'FAIL'
        detail.append('audio streams are identical')
    d = distance('soundtrack-change-left', 'soundtrack-change-right')
    if d < thresholds['soundtrack_change_min_cosine_distance']:
        status = 'FAIL'
        detail.append('distance below threshold')
    record('soundtrack-change', status, '; '.join(detail) + (' ' if detail else '') + 'distance=%.6f' % d)

    # Fixed retrieval directions.
    similarities, margins = {}, {}
    for q in contract['queries']:
        qkey = 'query:' + q['id']
        qitem = items.get(qkey)
        require(qitem is not None, 'missing query item: ' + qkey)
        if q['query_text'] is not None:
            require(len(qitem['parts']) == 1 and qitem.get('input_form') == 'plain', 'query shape: ' + qkey)
            text_ok(qitem['parts'][0], q['query_text'], qkey)
        else:
            check_fixture_parts(qitem, q['query_fixture'], qkey, manifest_sha, texts)
        sims = {}
        for cand in [q['target']] + q['distractors']:
            citem = items.get(cand)
            require(citem is not None, 'missing candidate item: ' + cand)
            check_fixture_parts(citem, cand, cand, manifest_sha, texts)
            sims[cand] = cosine(get(qkey), get(cand))
        best_other = max(sims[c] for c in q['distractors'])
        margin = sims[q['target']] - best_other
        similarities[q['id']] = sims
        margins[q['id']] = margin
        ok = margin >= thresholds['retrieval_min_cosine_margin'] and sims[q['target']] > best_other
        record(q['id'], 'PASS' if ok else 'FAIL', 'margin=%.6f' % margin)
    record(SPACE_CASE, 'PASS', 'dimensions=%d one model, projector, pooling and preprocessing' % dims)
    return {'cases': cases, 'similarities': similarities, 'margins': margins}


def validate_native_report(report, contract, manifest, dossier=None):
    """Return PASS, FAIL or BLOCKED for a consistent report; raise ValueError otherwise."""
    require(isinstance(report, dict) and report.get('schema_version') == 1, 'invalid report')
    require(report.get('kind') == 'multimodal-native-proof-report', 'wrong report kind')
    require(report.get('contract_version') == contract['contract_version'], 'contract version mismatch')
    status = report.get('status')
    require(status in ('PASS', 'FAIL', 'BLOCKED'), 'native proof outcome must be PASS, FAIL or BLOCKED')
    require(isinstance(report.get('command'), str) and report['command'].strip(), 'missing command')
    require(type(report.get('exit_status')) is int, 'missing exit status')
    require(isinstance(report.get('blockers'), list), 'invalid blockers')
    if status == 'BLOCKED':
        require(report['blockers'] and all(isinstance(b, str) and b.strip() for b in report['blockers']), 'BLOCKED needs blockers')
        require(report['exit_status'] != 0, 'BLOCKED needs a nonzero exit status')
        return 'BLOCKED'
    require(report['exit_status'] == 0 and not report['blockers'], 'probe run failed or has blockers')
    for field in ('identity', 'source', 'decoder', 'hardware', 'approval', 'server', 'fixtures', 'items'):
        require(isinstance(report.get(field), dict) and report[field], 'missing report field: ' + field)
    source = report['source']
    require(source.get('native_revision') == PIN == contract['provenance']['native_revision'], 'native revision is not the pin')
    require(source.get('upstream_head') == PIN, 'upstream checkout is not the pin')
    require(is_sha(source.get('backend_binary_sha256')) and source.get('backend_version'), 'missing backend binary identity')
    require(report['decoder'].get('versions') and report['decoder'].get('config'), 'missing decoder evidence')
    hw = report['hardware']
    require(hw.get('cpu_model') and type(hw.get('ram_bytes')) is int and hw['ram_bytes'] > 0, 'missing hardware evidence')
    expected_fixtures = {f['id']: f['sha256'] for f in manifest['fixtures']}
    require(all(is_sha(v) for v in expected_fixtures.values()), 'manifest fixtures are not materialized')
    require(report['fixtures'] == expected_fixtures, 'fixture hashes differ from manifest')
    ident = report['identity']
    if dossier is not None:
        for field in ('model_sha256', 'projector_sha256', 'preprocessing_version'):
            require(dossier.get(field) == ident.get(field), 'dossier %s differs from report' % field)
    result = evaluate(report, contract, manifest)
    required = required_cases(contract)
    claimed = report.get('cases')
    require(isinstance(claimed, list) and [c.get('id') for c in claimed].count(None) == 0, 'missing cases')
    ids = [c['id'] for c in claimed]
    require(len(ids) == len(set(ids)) and set(ids) == set(required), 'required cases mismatch')
    for case in claimed:
        require(case.get('status') == result['cases'][case['id']]['status'], 'claimed case status differs from vectors: ' + case['id'])
    for qid, margin in result['margins'].items():
        require(abs(report['margins'].get(qid, math.inf) - margin) < 1e-6, 'claimed margin differs from vectors: ' + qid)
        for cand, sim in result['similarities'][qid].items():
            require(abs(report['similarities'].get(qid, {}).get(cand, math.inf) - sim) < 1e-6, 'claimed similarity differs from vectors: ' + qid)
    failed = sorted(c for c, v in result['cases'].items() if v['status'] != 'PASS')
    computed = 'FAIL' if failed else 'PASS'
    require(status == computed, 'claimed %s but vectors give %s' % (status, computed))
    require(sorted(report.get('failures', [])) == failed, 'failure list differs from vectors')
    counts = report.get('counts', {})
    require(counts == {'expected': len(required), 'executed': len(required), 'passed': len(required) - len(failed), 'failed': len(failed)},
            'counts differ from cases')
    return computed


def main(argv):
    if len(argv) < 2:
        print('usage: check_native_reports.py REPORT [CONTRACT MANIFEST DOSSIER_JSON]', file=sys.stderr)
        return 1
    try:
        report = json.loads(Path(argv[1]).read_text())
        contract = json.loads((BASE / 'acceptance-contract.json').read_text())
        manifest = json.loads((BASE / 'fixtures/manifest.json').read_text())
        dossier = None
        text = (BASE.parents[1] / 'docs/design/multimodal-embeddings-feasibility.md').read_text()
        blocks = re.findall(r'```json\n(.*?)\n```', text, re.S)
        if len(blocks) == 1:
            dossier = json.loads(blocks[0])
        outcome = validate_native_report(report, contract, manifest, dossier)
        if dossier is not None and '--no-dossier-outcome' not in argv:
            require(dossier.get('native_proof') == outcome, 'dossier records %s but the report is %s' % (dossier.get('native_proof'), outcome))
    except (ValueError, KeyError, TypeError, OSError) as exc:
        print('INVALID: %s' % exc, file=sys.stderr)
        return 1
    print('native proof outcome: %s' % outcome)
    if outcome in ('PASS', 'FAIL'):
        for case in report['cases']:
            print('%-4s %-28s %s' % (case['status'], case['id'], case.get('detail', '')))
    else:
        for blocker in report['blockers']:
            print('blocker: ' + blocker)
    return 0 if outcome == 'PASS' else 3


if __name__ == '__main__':
    sys.exit(main(sys.argv))
