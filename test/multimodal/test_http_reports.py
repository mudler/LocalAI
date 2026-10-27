# SPDX-License-Identifier: MIT
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest

import check_http_reports as c


def spec(group, name, state='passed', extra=()):
    return {'LeafNodeType': 'It', 'ContainerHierarchyTexts': [group, *extra], 'LeafNodeText': name, 'State': state}


def suite(specs):
    return [{'SuiteSucceeded': True, 'SuiteHasProgrammaticFocus': False,
             'SuiteConfig': {'FailOnEmpty': True, 'FailOnPending': True, 'DryRun': False,
                             'FocusStrings': [], 'SkipStrings': []},
             'SpecReports': specs}]


class HttpReportTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory(dir=os.environ.get('MM_HTTP_TEST_SCRATCH'))
        self.addCleanup(tmp.cleanup)
        self.dir = Path(tmp.name)
        endpoint = [spec(c.HTTP_GROUP, n) for n in sorted(c.ENDPOINT_CASES | c.STATUS_CASES)]
        endpoint += [spec(c.LEGACY_GROUP, n) for n in sorted(c.LEGACY_CASES)]
        self.suites = {
            'core-http-endpoints-openai': endpoint,
            'core-http-endpoints-jina': [spec(c.RERANK_GROUP, n) for n in sorted(c.RERANK_CASES)],
            'core-http-middleware': [spec('Multimodal contracts', 'x')],
            'core-http-auth': [spec('Auth', 'y')],
            'core-backend': [spec(c.HTTP_GROUP, n) for n in sorted(c.BACKEND_CASES)],
            'core-services-routing-piiadapter': [spec('OpenAICompletion adapter', str(i)) for i in range(c.PIIADAPTER_MIN_PASSED)],
        }
        self.save()

    def save(self, reports=None):
        for name, specs in self.suites.items():
            (self.dir / (name + '.json')).write_text(json.dumps(suite(specs)))

    def test_complete(self):
        out = c.check(self.dir, 'rev')
        self.assertEqual(out['counts'][c.LEGACY_GROUP], len(c.LEGACY_CASES))
        self.assertTrue((self.dir / 'http-report.json').exists())

    def test_every_status_is_required(self):
        self.assertEqual({int(n.split()[0]) for n in c.STATUS_CASES}, c.REQUIRED_STATUSES)
        for status in c.REQUIRED_STATUSES:
            self.setUp()
            self.suites['core-http-endpoints-openai'] = [
                s for s in self.suites['core-http-endpoints-openai']
                if not (s['LeafNodeText'].startswith(str(status) + ' ') and s['ContainerHierarchyTexts'][0] == c.HTTP_GROUP)]
            self.save()
            with self.assertRaises(ValueError):
                c.check(self.dir, 'rev')

    def test_nonpassing_states_are_rejected(self):
        for state in ('skipped', 'pending', 'failed'):
            self.setUp()
            self.suites['core-http-endpoints-openai'][3]['State'] = state
            self.save()
            with self.assertRaises(ValueError):
                c.check(self.dir, 'rev')

    def test_missing_named_group_cases(self):
        for group in (c.HTTP_GROUP, c.LEGACY_GROUP):
            self.setUp()
            self.suites['core-http-endpoints-openai'] = [
                s for s in self.suites['core-http-endpoints-openai'] if s['ContainerHierarchyTexts'][0] != group]
            self.save()
            with self.assertRaises(ValueError):
                c.check(self.dir, 'rev')

    def test_missing_or_repeated_rerank_cases(self):
        self.suites['core-http-endpoints-jina'] = self.suites['core-http-endpoints-jina'][1:]
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')
        self.setUp()
        self.suites['core-http-endpoints-jina'].append(copy.deepcopy(self.suites['core-http-endpoints-jina'][0]))
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')

    def test_missing_backend_result_cases(self):
        self.suites['core-backend'] = self.suites['core-backend'][1:]
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')

    def test_empty_failed_or_filtered_suite(self):
        for package in c.PACKAGES:
            self.setUp()
            self.suites[package] = []
            self.save()
            with self.assertRaises(ValueError):
                c.check(self.dir, 'rev')
        self.setUp()
        path = self.dir / 'core-http-auth.json'
        report = json.loads(path.read_text())
        for key, value in (('SuiteSucceeded', False), ('SuiteHasProgrammaticFocus', True)):
            broken = copy.deepcopy(report)
            broken[0][key] = value
            path.write_text(json.dumps(broken))
            with self.assertRaises(ValueError):
                c.check(self.dir, 'rev')
        broken = copy.deepcopy(report)
        broken[0]['SuiteConfig']['FocusStrings'] = ['x']
        path.write_text(json.dumps(broken))
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')

    def test_piiadapter_suite_is_pinned_to_its_total(self):
        self.assertEqual(c.check(self.dir, 'rev')['counts']['core-services-routing-piiadapter'], c.PIIADAPTER_MIN_PASSED)
        self.setUp()
        self.suites['core-services-routing-piiadapter'] = self.suites['core-services-routing-piiadapter'][1:]
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')
        self.setUp()
        self.suites['core-services-routing-piiadapter'][0]['State'] = 'skipped'
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')

    def test_duplicate_case(self):
        self.suites['core-http-endpoints-openai'].append(copy.deepcopy(self.suites['core-http-endpoints-openai'][0]))
        self.save()
        with self.assertRaises(ValueError):
            c.check(self.dir, 'rev')


if __name__ == '__main__':
    unittest.main()
