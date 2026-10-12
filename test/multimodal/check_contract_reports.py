#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Require executed contract groups in every suite, not just successful exit."""
import json
import sys
from pathlib import Path


def check(scratch):
    counts = {}
    required = {
        'core-schema': {'Multimodal contracts': 39, 'Legacy embedding parsing': 7},
        'pkg-embeddingmedia': {'Multimodal contracts': 20},
        'core-http-middleware': {'Multimodal contracts': 17, 'Legacy embedding parsing': 1},
    }
    for package, groups in required.items():
        suites = json.loads((scratch / (package + '.json')).read_text())
        if len(suites) != 1 or not suites[0]['SuiteSucceeded']:
            raise ValueError('missing or failed suite: ' + package)
        specs = suites[0]['SpecReports']
        for group, minimum in groups.items():
            selected = [s for s in specs if group in s['ContainerHierarchyTexts']]
            if len(selected) < minimum or any(s['State'] != 'passed' for s in selected):
                raise ValueError('missing, skipped, pending or failed group: ' + group)
        if package == 'pkg-embeddingmedia':
            boundaries = [s for s in specs if 'budget boundaries' in s['ContainerHierarchyTexts']]
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
