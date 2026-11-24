# SPDX-License-Identifier: MIT
"""Regression checks for the exact Ginkgo evidence gate (no Go toolchain needed)."""
import json
import pathlib
import tempfile
import unittest

from ginkgo_report import ginkgo_report


def spec(text='required', state='passed', kind='It', containers=None):
    return dict(LeafNodeText=text, State=state, LeafNodeType=kind,
                ContainerHierarchyTexts=containers)


class ReportTests(unittest.TestCase):
    def setUp(self):
        scratch_root = pathlib.Path(__file__).resolve().parents[2] / '.cache/addons/tmp'
        scratch_root.mkdir(parents=True, exist_ok=True)
        self.scratch = tempfile.TemporaryDirectory(dir=scratch_root)
        self.addCleanup(self.scratch.cleanup)
        self.path = pathlib.Path(self.scratch.name) / 'report.json'

    def check(self, specs, expected=None, focus=None, succeeded=True):
        self.path.write_text(json.dumps([dict(SuiteSucceeded=succeeded, SpecReports=specs)]))
        return ginkgo_report(self.path, ['required'] if expected is None else expected, focus)

    def test_suite_hooks_with_null_hierarchy(self):
        self.assertEqual(self.check([spec('', kind='BeforeSuite'), spec(containers=['group']),
                                     spec('', kind='DeferCleanup (Suite)')]), ['required'])

    def test_focused_suite_hooks_are_not_filtered_specs(self):
        self.assertEqual(self.check([spec('', kind='BeforeSuite'), spec(containers=['selected']),
                                     spec('other', 'skipped', containers=['elsewhere']),
                                     spec('', kind='DeferCleanup (Suite)')], focus='selected'), ['required'])

    def test_top_level_it_with_null_hierarchy(self):
        self.assertEqual(self.check([spec()]), ['required'])

    def test_failed_hook_rejected_even_outside_focus(self):
        with self.assertRaises(SystemExit):
            self.check([spec('', 'failed', 'BeforeSuite'), spec(containers=['selected'])], focus='selected')

    def test_skipped_hook_rejected(self):
        with self.assertRaises(SystemExit):
            self.check([spec('', 'skipped', 'BeforeSuite'), spec()])

    def test_failed_suite_rejected(self):
        with self.assertRaises(SystemExit):
            self.check([spec()], succeeded=False)

    def test_failed_skipped_pending_required_rejected(self):
        for state in ['failed', 'skipped', 'pending', 'panicked', 'interrupted']:
            with self.subTest(state=state), self.assertRaises(SystemExit):
                self.check([spec(state=state, containers=[])])

    def test_missing_extra_and_duplicate_cases_rejected(self):
        for specs in [[spec('other')], [spec(), spec('extra')], [spec(), spec()]]:
            with self.subTest(specs=specs), self.assertRaises(SystemExit):
                self.check(specs)

    def test_required_case_cannot_be_excluded_by_focus(self):
        with self.assertRaises(SystemExit):
            self.check([spec(containers=['selected']), spec('other', 'skipped', containers=[])],
                       expected=['required', 'other'], focus='selected')

    def test_excluded_passed_spec_rejected(self):
        with self.assertRaises(SystemExit):
            self.check([spec(containers=['selected']), spec('other', containers=[])], focus='selected')

    def test_hooks_only_are_not_tests(self):
        with self.assertRaises(SystemExit):
            self.check([spec('', kind='BeforeSuite')])

    def test_missing_empty_and_malformed_artifacts_rejected(self):
        with self.assertRaises(SystemExit):
            ginkgo_report(self.path, ['required'])
        for text in ['', '[]', '{}', 'null', '[', '[{"SuiteSucceeded":true,"SpecReports":[]}]']:
            self.path.write_text(text)
            with self.subTest(text=text), self.assertRaises(SystemExit):
                ginkgo_report(self.path, ['required'])


if __name__ == '__main__':
    unittest.main()
