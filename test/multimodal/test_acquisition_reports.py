# SPDX-License-Identifier: MIT
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest

from check_acquisition_reports import check, GROUP, SECURITY_CASES, LIMITER_CASES


class AcquisitionReportTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ.get('MM_ACQUISITION_TEST_SCRATCH'))
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / 'acquisition.json'
        specs = [{'ContainerHierarchyTexts': ['Multimodal contracts'], 'LeafNodeText': 'x', 'State': 'passed'}]
        specs += [{'ContainerHierarchyTexts': [GROUP, 'area'], 'LeafNodeText': n, 'State': 'passed'}
                  for n in sorted(SECURITY_CASES | LIMITER_CASES)]
        self.report = [{'SuiteSucceeded': True, 'SpecReports': specs}]
        self.save()

    def save(self):
        self.path.write_text(json.dumps(self.report))

    def test_complete(self):
        counts = check(self.path)
        self.assertEqual(counts['failed'] + counts['skipped'] + counts['pending'], 0)
        self.assertEqual(counts['acquisition_cases'], len(SECURITY_CASES | LIMITER_CASES))

    def test_every_state_but_passed_is_rejected(self):
        original = copy.deepcopy(self.report)
        for state in ('skipped', 'pending', 'failed'):
            for index in (0, 1, len(original[0]['SpecReports']) - 1):
                self.report = copy.deepcopy(original)
                self.report[0]['SpecReports'][index]['State'] = state
                self.save()
                with self.assertRaises(ValueError):
                    check(self.path)

    def test_missing_security_case(self):
        self.report[0]['SpecReports'] = [s for s in self.report[0]['SpecReports']
                                         if s['LeafNodeText'] != sorted(SECURITY_CASES)[0]]
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)

    def test_missing_limiter_case(self):
        self.report[0]['SpecReports'] = [s for s in self.report[0]['SpecReports']
                                         if s['LeafNodeText'] != sorted(LIMITER_CASES)[0]]
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)

    def test_case_outside_named_group(self):
        for spec in self.report[0]['SpecReports']:
            if spec['LeafNodeText'] == sorted(SECURITY_CASES)[0]:
                spec['ContainerHierarchyTexts'] = ['Other group']
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)

    def test_duplicate_case(self):
        self.report[0]['SpecReports'].append(copy.deepcopy(self.report[0]['SpecReports'][1]))
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)

    def test_empty_and_failed_suite(self):
        self.report[0]['SpecReports'] = []
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)
        self.report[0]['SuiteSucceeded'] = False
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)

    def test_multiple_suites(self):
        self.report.append(copy.deepcopy(self.report[0]))
        self.save()
        with self.assertRaises(ValueError):
            check(self.path)


if __name__ == '__main__':
    unittest.main()
