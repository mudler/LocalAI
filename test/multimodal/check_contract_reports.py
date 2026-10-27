#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Require executed contract groups in every suite, not just successful exit."""
import json
import sys
from pathlib import Path


SECURITY_CASES = {
    'core-http': {
        'bounds reads before production auth and tracing for every alias and transfer length',
        'preserves nonadmin model permissions and quotas on every alias',
        'omits production validation and unsupported payloads from traces',
        'leaves nonembedding body consumption and global limits unchanged',
        'reads almost nothing from unauthenticated embedding requests',
    },
    'core-http-middleware': {
        'keeps legacy embedding traces and omits structured payloads including errors',
        'omits a top-level array body that the embedding binder rejects as structured',
    },
}

# The gate focuses these packages on one group. Ginkgo still lists the specs it
# excluded, as skipped with no failure message, so those are the only allowed
# non-group entries besides the suite lifecycle record.
FOCUSED_GROUPS = {'core-http': 'Embedding production security'}


def is_lifecycle(spec):
    return not spec.get('ContainerHierarchyTexts') and not spec.get('LeafNodeText')


def check(scratch):
    counts = {}
    required = {
        'core-schema': {'Multimodal contracts': 46, 'Legacy embedding parsing': 7},
        'pkg-embeddingmedia': {'Multimodal contracts': 20},
        'core-http-middleware': {'Multimodal contracts': 19, 'Legacy embedding parsing': 1},
        'core-http': {'Embedding production security': 5},
        'core-http-auth': {},
    }
    for package, groups in required.items():
        suites = json.loads((scratch / (package + '.json')).read_text())
        if len(suites) != 1 or not suites[0]['SuiteSucceeded']:
            raise ValueError('missing or failed suite: ' + package)
        specs = suites[0]['SpecReports']
        if not specs or not any(s['State'] == 'passed' for s in specs):
            raise ValueError('empty suite: ' + package)
        for name in SECURITY_CASES.get(package, set()):
            matched = [s for s in specs if s.get('LeafNodeText') == name]
            if len(matched) != 1 or matched[0]['State'] != 'passed':
                raise ValueError('missing or unexecuted security case: ' + name)
        focus = FOCUSED_GROUPS.get(package)
        for spec in specs:
            hierarchy = spec.get('ContainerHierarchyTexts') or []
            if focus and focus not in hierarchy and not is_lifecycle(spec):
                if spec['State'] == 'skipped' and not spec.get('Failure', {}).get('Message'):
                    continue
                raise ValueError('spec outside focus group ran or failed: ' + package)
            if spec['State'] == 'skipped':
                reason = spec.get('Failure', {}).get('Message', '')
                native_skip = (package == 'core-http-middleware'
                               and spec['ContainerHierarchyTexts'] == ['native decisions integration']
                               and spec.get('LeafNodeText') == 'routes overlapping labels through the central factory and native runner'
                               and reason == 'requires an existing decision GGUF and a dedicated llama-cpp gRPC server')
                if not native_skip:
                    raise ValueError('unexpected skipped test: ' + package)
            elif spec['State'] != 'passed':
                raise ValueError('unexecuted or failed test: ' + package)
        for group, minimum in groups.items():
            selected = [s for s in specs if group in (s.get('ContainerHierarchyTexts') or [])]
            if len(selected) < minimum or any(s['State'] != 'passed' for s in selected):
                raise ValueError('missing, skipped, pending or failed group: ' + group)
            if focus == group and len(selected) != minimum:
                raise ValueError('focused group count differs from pin: ' + group)
        if focus:
            passed = [s for s in specs if s['State'] == 'passed' and not is_lifecycle(s)]
            if len(passed) != groups[focus]:
                raise ValueError('passing specs outside focus group: ' + package)
        if package == 'pkg-embeddingmedia':
            boundaries = [s for s in specs if 'budget boundaries' in (s.get('ContainerHierarchyTexts') or [])]
            if len(boundaries) < 12 or any(s['State'] != 'passed' for s in boundaries):
                raise ValueError('missing budget boundaries')
        counts[package] = {state: sum(s['State'] == state for s in specs)
                           for state in ('passed', 'failed', 'pending', 'skipped')}
    return counts


if __name__ == '__main__':
    scratch = Path(sys.argv[1])
    counts = check(scratch)
    report = {'revision': sys.argv[2], 'exit_status': 0, 'counts': counts,
              'command': 'bash test/multimodal/verify.sh contracts',
              'evidence': str(scratch)}
    (scratch / 'contracts.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
