#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Require every named acquisition security and limiter case to have executed.

A zero exit status is not enough: the Ginkgo report must show a single
successful suite in which each named case ran exactly once and passed, and in
which nothing was skipped, pending or failed.
"""
import json
import sys
from pathlib import Path

GROUP = 'Multimodal acquisition security'

SECURITY_CASES = {
    'refuses redirects without following them or forwarding credentials',
    'rechecks resolution on every dial and rejects rebinding to a denied address',
    'rejects a name resolving to any denied address',
    'denies every reserved IPv4 and IPv6 range',
    'denies IPv4-mapped IPv6 destinations',
    'denies IP literal and internal-name URLs without dialing',
    'pins the dial to the validated address, never the hostname',
    'keeps the original hostname for TLS server name verification',
    'rejects URL credentials before any resolution or dial',
    'redacts credentials, query and fragments from every error',
    'ignores proxy environment variables',
    'sends no forwarded authentication or cookie headers',
    'classifies unsuccessful HTTP status as fetch failure',
    'rejects encoded response bodies it did not request',
    'rejects declared length above the budget before reading the body',
    'rejects unknown length bodies that exceed the budget at limit plus one',
    'accepts exactly the remaining media budget and charges actual bytes',
    'rejects a body shorter than its declared length as fetch failure',
    'maps a stalled response to 504 at the fetch deadline',
    'returns cancellation promptly when the caller cancels a stalled read',
    'bounds base64 output before allocation',
    'decodes inline data into an owned exact-size buffer and charges its length',
    'rejects malformed base64 data and data URIs',
    'validates data URI media types against the source kind',
    'accepts supported signatures per kind and rejects others with 415',
    'rejects a format hint that contradicts the detected signature',
    'rejects unsupported schemes, file paths and shell-like sources',
    'leaves the budget unchanged on every failure',
}

LIMITER_CASES = {
    'admits exactly capacity holders and returns 429 beyond it',
    'releases idempotently and frees exactly one slot',
    'never admits more than capacity under concurrent holders',
    'has a fixed production capacity of two per frontend and independent state',
    'frees the slot after a cancelled acquisition releases it',
}


def check(report_path):
    suites = json.loads(Path(report_path).read_text())
    if len(suites) != 1 or not suites[0].get('SuiteSucceeded'):
        raise ValueError('missing or failed suite')
    specs = suites[0].get('SpecReports') or []
    if not specs or not any(s.get('State') == 'passed' for s in specs):
        raise ValueError('empty suite')
    for spec in specs:
        if spec.get('State') != 'passed':
            raise ValueError('skipped, pending, failed or unexecuted test: ' + str(spec.get('LeafNodeText')))
    grouped = [s for s in specs if GROUP in (s.get('ContainerHierarchyTexts') or [])]
    for name in sorted(SECURITY_CASES | LIMITER_CASES):
        matched = [s for s in grouped if s.get('LeafNodeText') == name]
        if len(matched) != 1 or matched[0]['State'] != 'passed':
            raise ValueError('missing or unexecuted acquisition case: ' + name)
    return {state: sum(s.get('State') == state for s in specs)
            for state in ('passed', 'failed', 'pending', 'skipped')} | {
        'acquisition_cases': len(grouped)}


if __name__ == '__main__':
    report_path, revision, command = sys.argv[1:4]
    counts = check(report_path)
    out = {'revision': revision, 'exit_status': 0, 'command': command,
           'counts': counts, 'security_cases': sorted(SECURITY_CASES),
           'limiter_cases': sorted(LIMITER_CASES), 'evidence': report_path}
    Path(report_path).with_name('acquisition-summary.json').write_text(json.dumps(out, indent=2) + '\n')
    print(json.dumps(out, indent=2))
