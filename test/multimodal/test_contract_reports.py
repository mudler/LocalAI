# SPDX-License-Identifier: MIT
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest

from check_contract_reports import check, SECURITY_CASES


class ContractReportTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ.get('MM_CONTRACT_TEST_SCRATCH'))
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.reports = {}
        for package, groups in {
            'core-schema': {'Multimodal contracts': 46, 'Legacy embedding parsing': 7},
            'pkg-embeddingmedia': {'Multimodal contracts': 20},
            'core-http-middleware': {'Multimodal contracts': 19, 'Legacy embedding parsing': 1},
            'core-http': {'Embedding production security': 5},
            'core-http-auth': {'Auth Middleware': 1},
        }.items():
            specs = []
            for group, count in groups.items():
                specs.extend({'ContainerHierarchyTexts': [group] + (
                    ['budget boundaries'] if package == 'pkg-embeddingmedia' and i < 12 else []),
                    'State': 'passed'} for i in range(count))
            self.reports[package] = [{'SuiteSucceeded': True, 'SpecReports': specs}]
        for package, names in SECURITY_CASES.items():
            specs = self.reports[package][0]['SpecReports']
            if package == 'core-http':
                for spec, name in zip(specs, sorted(names)):
                    spec['LeafNodeText'] = name
            else:
                specs.extend({'ContainerHierarchyTexts': ['Embedding trace security'],
                              'LeafNodeText': name, 'State': 'passed'} for name in names)
        # Ginkgo's focus excludes the other core/http specs and reports them as skipped.
        core_http = self.reports['core-http'][0]['SpecReports']
        core_http.extend({'ContainerHierarchyTexts': ['Route auth coverage'],
                          'LeafNodeText': 'excluded %d' % i, 'State': 'skipped'} for i in range(3))
        core_http.append({'ContainerHierarchyTexts': None, 'State': 'passed'})
        self.save()

    def test_suite_lifecycle_report(self):
        self.reports['core-http'][0]['SpecReports'].append(
            {'ContainerHierarchyTexts': None, 'State': 'passed'})
        self.save()
        check(self.root)

    def test_unexpected_skip(self):
        self.reports['core-http-auth'][0]['SpecReports'][0]['State'] = 'skipped'
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_missing_named_security_case(self):
        self.reports['core-http'][0]['SpecReports'][0]['LeafNodeText'] = 'unrelated'
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_skipped_security_case(self):
        self.reports['core-http'][0]['SpecReports'][0]['State'] = 'skipped'
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def save(self):
        for name, report in self.reports.items():
            (self.root / (name + '.json')).write_text(json.dumps(report))

    def test_complete(self):
        self.assertEqual(check(self.root)['core-schema']['passed'], 53)

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

    def test_core_http_unfocused_spec_passing(self):
        self.reports['core-http'][0]['SpecReports'].append(
            {'ContainerHierarchyTexts': ['Gallery'], 'LeafNodeText': 'applies models from a gallery',
             'State': 'passed'})
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_core_http_unfocused_spec_failing(self):
        self.reports['core-http'][0]['SpecReports'][-2]['State'] = 'failed'
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_core_http_group_missing(self):
        for spec in self.reports['core-http'][0]['SpecReports']:
            if spec.get('ContainerHierarchyTexts') == ['Embedding production security']:
                spec['ContainerHierarchyTexts'] = ['Route auth coverage']
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)

    def test_core_http_group_count_differs_from_pin(self):
        spec = dict(self.reports['core-http'][0]['SpecReports'][0])
        self.reports['core-http'][0]['SpecReports'].append(spec)
        self.save()
        with self.assertRaises(ValueError):
            check(self.root)
