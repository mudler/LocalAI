# SPDX-License-Identifier: MIT
"""Turns a Playwright JSON report into the case lines the verify runner reads.

usage: ui_cases.py playwright REPORT.json

One line {"case": <spec title>, "status": passed|failed|skipped|flaky} per
test. A test is passed only when it passed on its first attempt: a retry that
passed is flaky, and a skipped, fixme or todo test is skipped. Any error the
runner reports outside a test is a failed case of its own, so an empty or
partial run can never look like success.
"""
import json
import sys


def walk(suite, out):
    for spec in suite.get('specs', []):
        for test in spec.get('tests', []):
            results = test.get('results', [])
            status = test.get('status')
            if status == 'expected' and len(results) == 1 and results[0].get('status') == 'passed':
                state = 'passed'
            elif status == 'skipped' or not results or all(r.get('status') == 'skipped' for r in results):
                state = 'skipped'
            elif status == 'flaky':
                state = 'flaky'
            else:
                state = 'failed'
            out.append({'case': spec['title'], 'status': state})
    for child in suite.get('suites', []):
        walk(child, out)


def main(argv):
    if len(argv) != 3 or argv[1] != 'playwright':
        print('usage: ui_cases.py playwright REPORT.json', file=sys.stderr)
        return 2
    try:
        with open(argv[2]) as f:
            report = json.load(f)
        assert isinstance(report, dict)
    except (OSError, ValueError, AssertionError):
        print(json.dumps({'case': 'playwright-report', 'status': 'failed'}))
        return 1
    cases = []
    for suite in report.get('suites', []):
        walk(suite, cases)
    for error in report.get('errors', []) or []:
        cases.append({'case': 'playwright-error: ' + str(error.get('message', ''))[:120], 'status': 'failed'})
    if not cases:
        print(json.dumps({'case': 'playwright-report-empty', 'status': 'failed'}))
        return 1
    for case in cases:
        print(json.dumps(case))
    return 0


sys.exit(main(sys.argv))
