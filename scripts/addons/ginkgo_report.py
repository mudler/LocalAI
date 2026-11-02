# SPDX-License-Identifier: MIT
"""Exact required-case comparison shared by the harness and direct evidence checks."""
import json
import re


def ginkgo_report(path, expected, focus=None):
    try:
        reports = json.loads(path.read_text())
        assert isinstance(reports, list) and reports
        cases = []
        for report in reports:
            assert report['SuiteSucceeded'] is True
            specs = report['SpecReports']
            assert specs
            for spec in specs:
                # Suite hooks are execution evidence, not focused It cases. Ginkgo
                # gives them a null hierarchy; still require every hook to pass.
                if spec['LeafNodeType'] != 'It':
                    assert spec['State'] == 'passed'
                    continue
                # Ginkgo reports filtered-out specs as skipped. Only an explicit
                # report focus may exclude them; a required skipped case still fails.
                text = ' '.join((spec.get('ContainerHierarchyTexts') or []) + [spec['LeafNodeText']])
                if focus and not re.search(focus, text):
                    assert spec['State'] == 'skipped'
                    continue
                assert spec['State'] == 'passed'
                if spec['LeafNodeType'] == 'It':
                    cases.append(spec['LeafNodeText'])
        assert cases and sorted(cases) == sorted(expected)
    except (OSError, ValueError, KeyError, TypeError, AssertionError):
        raise SystemExit('Ginkgo report missing, empty, skipped, failed or inventory mismatch: ' + str(path))
    return cases
