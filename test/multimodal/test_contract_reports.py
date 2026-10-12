# SPDX-License-Identifier: MIT
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest

from check_contract_reports import check


class ContractReportTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ.get('MM_CONTRACT_TEST_SCRATCH'))
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.reports = {}
        for package, groups in {
            'core-schema': {'Multimodal contracts': 39, 'Legacy embedding parsing': 7},
            'pkg-embeddingmedia': {'Multimodal contracts': 20},
            'core-http-middleware': {'Multimodal contracts': 17, 'Legacy embedding parsing': 1},
        }.items():
            specs = []
            for group, count in groups.items():
                specs.extend({'ContainerHierarchyTexts': [group] + (
                    ['budget boundaries'] if package == 'pkg-embeddingmedia' and i < 12 else []),
                    'State': 'passed'} for i in range(count))
            self.reports[package] = [{'SuiteSucceeded': True, 'SpecReports': specs}]
        self.save()

    def save(self):
        for name, report in self.reports.items():
            (self.root / (name + '.json')).write_text(json.dumps(report))

    def test_complete(self):
        self.assertEqual(check(self.root)['core-schema']['passed'], 46)

    def test_missing_file(self):
        (self.root / 'core-schema.json').unlink()
        with self.assertRaises(FileNotFoundError):
            check(self.root)

    def test_required_states(self):
        original = copy.deepcopy(self.reports)
        for state in ('skipped', 'pending', 'failed'):
            self.reports = copy.deepcopy(original)
            self.reports['core-schema'][0]['SpecReports'][0]['State'] = state
            self.save()
            with self.assertRaises(ValueError):
                check(self.root)

    def test_empty_group(self):
        self.reports['core-schema'][0]['SpecReports'] = []
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_failed_suite(self):
        self.reports['core-schema'][0]['SuiteSucceeded'] = False
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_missing_boundaries(self):
        for spec in self.reports['pkg-embeddingmedia'][0]['SpecReports']:
            spec['ContainerHierarchyTexts'] = ['Multimodal contracts']
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)
