#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Recompute and validate a LocalAI adapter proof against a frozen contract.

Inputs: the frozen contract, the raw vectors written by the e2e scorer, and the
fixture directory. Every cosine, margin and rank is recomputed from the raw
float32 bytes. A reported number is never trusted: a forged hash, a vector of
the wrong size, a missing vector, a contract that differs from the one scored,
or a committed report that disagrees with the recomputation is INVALID.

Outcomes: PASS, FAIL (a valid measurement that misses the contract). Nothing is
retuned: thresholds come from the contract, and a tie fails.

  check_adapter_proof.py CONTRACT VECTORS FIXTURES [--write REPORT | --report REPORT]

--write stores the recomputed report. --report requires the stored report to
equal the recomputation. Exit codes: 0 PASS, 3 recorded FAIL, 1 invalid.
"""
import base64
import hashlib
import json
import math
from pathlib import Path
import struct
import sys

TOLERANCE = 1e-9


class Invalid(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise Invalid(message)


def sha256_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def decode(entry, key, dims):
    require(isinstance(entry, dict), 'missing vector: ' + key)
    try:
        raw = base64.b64decode(entry['base64'], validate=True)
    except (KeyError, ValueError, TypeError):
        raise Invalid('undecodable vector: ' + key)
    require(entry.get('dimensions') == len(raw) // 4 and len(raw) % 4 == 0, 'inconsistent vector length: ' + key)
    require(hashlib.sha256(raw).hexdigest() == entry.get('sha256'), 'vector hash mismatch: ' + key)
    values = struct.unpack('<%df' % (len(raw) // 4), raw)
    require(dims is None or len(values) == dims, 'wrong dimensions: ' + key)
    require(all(math.isfinite(v) for v in values), 'non-finite vector: ' + key)
    require(any(v != 0.0 for v in values), 'zero vector: ' + key)
    return [float(v) for v in values]


def cosine(a, b):
    dot = sum(x * y for x, y in zip(a, b))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(y * y for y in b))
    return dot / (na * nb)


def fixture_hashes(contract, fixtures_dir):
    """Expected SHA-256 per fixture id: from the contract, else the fixture manifest."""
    declared = contract.get('fixtures')
    if isinstance(declared, dict):
        return {k: (v['path'], v['sha256']) for k, v in declared.items()}
    manifest = json.loads((Path(fixtures_dir) / 'manifest.json').read_text())
    return {f['id']: (f['path'], f['sha256']) for f in manifest['fixtures']}


def build_report(contract_path, vectors_path, fixtures_dir):
    contract_bytes = Path(contract_path).read_bytes()
    contract = json.loads(contract_bytes)
    doc = json.loads(Path(vectors_path).read_text())
    require(doc.get('kind') == 'multimodal-adapter-vectors', 'not an adapter vectors file')
    require(doc.get('contract_version') == contract['contract_version'], 'contract version differs from the one scored')
    require(doc.get('contract_sha256') == hashlib.sha256(contract_bytes).hexdigest(), 'contract bytes differ from the one scored')
    identity = doc.get('identity') or {}
    for key in ('model_sha256', 'projector_sha256', 'backend_sha256'):
        value = identity.get(key)
        require(isinstance(value, str) and len(value) == 64 and all(c in '0123456789abcdef' for c in value), 'identity field missing: ' + key)
    for fixture_id, (path, digest) in fixture_hashes(contract, fixtures_dir).items():
        file = Path(fixtures_dir) / path
        if file.exists():
            require(sha256_file(file) == digest, 'fixture bytes differ from the contract: ' + fixture_id)

    thresholds = contract['thresholds']
    margin_min = thresholds['retrieval_min_cosine_margin']
    distance_min = thresholds.get('media_change_min_cosine_distance')
    dims = thresholds.get('expected_dimensions')
    skipped = set(doc.get('skipped') or [])
    vectors = {}
    for key, entry in (doc.get('vectors') or {}).items():
        vectors[key] = decode(entry, key, dims)
    sizes = {len(v) for v in vectors.values()}
    require(len(sizes) == 1, 'vectors of different dimensions')

    cases = []
    for q in contract['queries']:
        ids = [q['target']] + q['distractors']
        query_key = ('query:' + q['id']) if q.get('query_text') is not None else ('fixture:' + q['query_fixture'])
        involved = ([q['query_fixture']] if q.get('query_fixture') else []) + ids
        if any(i in skipped for i in involved):
            cases.append({'id': q['id'], 'status': 'NOT_APPLICABLE', 'reason': 'a fixture of an unpublished modality'})
            continue
        require(query_key in vectors, 'missing query vector: ' + q['id'])
        for i in ids:
            require('fixture:' + i in vectors, 'missing fixture vector: ' + i)
        cos = {i: cosine(vectors[query_key], vectors['fixture:' + i]) for i in ids}
        best_id = max(q['distractors'], key=lambda i: cos[i])
        margin = cos[q['target']] - cos[best_id]
        # A tie is not a pass; the target must be strictly first and clear the margin.
        ok = cos[q['target']] > cos[best_id] and margin >= margin_min
        cases.append({
            'id': q['id'], 'direction': q['direction'], 'status': 'PASS' if ok else 'FAIL',
            'target': q['target'], 'target_cosine': round(cos[q['target']], 9),
            'best_distractor': best_id, 'best_distractor_cosine': round(cos[best_id], 9),
            'margin': round(margin, 9), 'required_margin': margin_min,
            'rank_one': cos[q['target']] > cos[best_id],
            'cosines': {i: round(cos[i], 9) for i in ids},
        })
    for name, pair in sorted((contract.get('pairs') or {}).items()):
        left, right = 'pair:%s:left' % name, 'pair:%s:right' % name
        if 'pair:' + name in skipped:
            cases.append({'id': name, 'status': 'NOT_APPLICABLE', 'reason': 'a fixture of an unpublished modality'})
            continue
        require(left in vectors and right in vectors, 'missing pair vectors: ' + name)
        distance = 1 - cosine(vectors[left], vectors[right])
        cases.append({'id': name, 'direction': 'media-change', 'status': 'PASS' if distance >= distance_min else 'FAIL',
                      'distance': round(distance, 9), 'required_distance': distance_min})
    applicable = [c for c in cases if c['status'] != 'NOT_APPLICABLE']
    require(applicable, 'nothing was scored')
    failures = [c['id'] for c in applicable if c['status'] == 'FAIL']
    counts = {
        'declared': len(cases), 'scored': len(applicable),
        'passed': sum(c['status'] == 'PASS' for c in applicable), 'failed': len(failures),
        'not_applicable': len(cases) - len(applicable),
    }
    return {
        'schema_version': 1,
        'kind': 'multimodal-adapter-proof-report',
        'contract_version': contract['contract_version'],
        'contract_sha256': doc['contract_sha256'],
        'vectors_sha256': sha256_file(vectors_path),
        'status': 'FAIL' if failures else 'PASS',
        'partial': counts['not_applicable'] > 0,
        'failures': failures,
        'counts': counts,
        'dimensions': sizes.pop(),
        'identity': identity,
        'cases': cases,
    }


def same(a, b, path='report'):
    if isinstance(a, float) or isinstance(b, float):
        require(isinstance(a, (int, float)) and isinstance(b, (int, float)) and abs(a - b) <= TOLERANCE, 'report differs at ' + path)
    elif isinstance(a, dict):
        require(isinstance(b, dict) and a.keys() == b.keys(), 'report differs at ' + path)
        for key in a:
            same(a[key], b[key], path + '.' + key)
    elif isinstance(a, list):
        require(isinstance(b, list) and len(a) == len(b), 'report differs at ' + path)
        for i, (x, y) in enumerate(zip(a, b)):
            same(x, y, '%s[%d]' % (path, i))
    else:
        require(a == b, 'report differs at ' + path)


def main(argv):
    args = argv[1:]
    mode, target = None, None
    for flag in ('--write', '--report'):
        if flag in args:
            i = args.index(flag)
            mode, target = flag, args[i + 1]
            del args[i:i + 2]
    if len(args) != 3:
        print(__doc__, file=sys.stderr)
        return 1
    try:
        report = build_report(*args)
        if mode == '--report':
            same(json.loads(Path(target).read_text()), report)
        elif mode == '--write':
            Path(target).write_text(json.dumps(report, indent=2) + '\n')
    except (Invalid, OSError, KeyError, ValueError) as error:
        print('INVALID: %s' % error, file=sys.stderr)
        return 1
    c = report['counts']
    print('%s: %d of %d scored cases passed, %d not applicable; failures: %s' % (
        report['status'], c['passed'], c['scored'], c['not_applicable'], ', '.join(report['failures']) or 'none'))
    return 0 if report['status'] == 'PASS' else 3


if __name__ == '__main__':
    sys.exit(main(sys.argv))
