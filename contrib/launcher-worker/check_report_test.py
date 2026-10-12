#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Exercise report completeness with normal and optimized Python."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


PARSER = Path(__file__).with_name("check-report.py")


def suite(count=1):
    return {
        "SuiteSucceeded": True,
        "PreRunStats": {"TotalSpecs": count, "SpecsThatWillRun": count},
        "SpecReports": [{"State": "passed", "LeafNodeType": "It"}] * count,
    }


class ReportTests(unittest.TestCase):
    def check_report(self, report, status):
        with tempfile.TemporaryDirectory(dir=os.environ["LAUNCHER_VERIFY_WORKDIR"]) as tmp:
            path = Path(tmp) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            for flags in ([], ["-O"]):
                with self.subTest(flags=flags):
                    result = subprocess.run(
                        [sys.executable, *flags, str(PARSER), str(path)],
                        capture_output=True, text=True, check=False,
                    )
                    self.assertEqual(result.returncode, status, result.stdout + result.stderr)
                    if status == 0:
                        count = sum(s["PreRunStats"]["TotalSpecs"] for s in report)
                        self.assertIn(f"{count} passing specs; no skips", result.stdout)

    def test_truncated_launcher(self):
        report = suite(41)
        report["SpecReports"] = report["SpecReports"][:1]
        self.check_report([report], 1)

    def test_missing_or_malformed_stats(self):
        for stats in (None, [], "stats", 1, True, {}):
            with self.subTest(stats=stats):
                report = suite()
                report["PreRunStats"] = stats
                self.check_report([report], 1)
        report = suite()
        del report["PreRunStats"]
        self.check_report([report], 1)

    def test_missing_or_malformed_counts(self):
        for field in ("TotalSpecs", "SpecsThatWillRun"):
            for value in (None, True, False, "1", 1.0, [], {}, -1, 0):
                with self.subTest(field=field, value=value):
                    report = suite()
                    report["PreRunStats"][field] = value
                    self.check_report([report], 1)
            report = suite()
            del report["PreRunStats"][field]
            self.check_report([report], 1)

    def test_inconsistent_counts(self):
        for total, scheduled, actual in ((2, 1, 1), (1, 2, 1), (1, 1, 2), (2, 2, 1)):
            with self.subTest(total=total, scheduled=scheduled, actual=actual):
                report = suite(actual)
                report["PreRunStats"] = {"TotalSpecs": total, "SpecsThatWillRun": scheduled}
                self.check_report([report], 1)

    def test_counts_reconciled_per_suite(self):
        first, second = suite(1), suite(2)
        first["PreRunStats"], second["PreRunStats"] = second["PreRunStats"], first["PreRunStats"]
        self.check_report([first, second], 1)

    def test_complete_suites(self):
        reports = [suite(count) for count in (41, 9, 235, 782)]
        for report in reports:
            self.check_report([report], 0)
        self.check_report(reports, 0)

    def test_hooks_are_not_it_specs(self):
        report = suite()
        report["SpecReports"] += [
            {"State": "passed", "LeafNodeType": hook}
            for hook in ("BeforeSuite", "AfterSuite", "SynchronizedBeforeSuite",
                         "SynchronizedAfterSuite", "ReportBeforeSuite", "ReportAfterSuite")
        ]
        self.check_report([report], 0)
        incomplete = copy.deepcopy(report)
        incomplete["PreRunStats"] = {"TotalSpecs": 7, "SpecsThatWillRun": 7}
        self.check_report([incomplete], 1)
        report["SpecReports"][-1]["State"] = "failed"
        self.check_report([report], 1)


if __name__ == "__main__":
    unittest.main()
