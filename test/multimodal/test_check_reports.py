# SPDX-License-Identifier: MIT
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import check_reports as checker


class ReportTests(unittest.TestCase):
    def setUp(self):
        # Own the entire evidence root; no developer logs or prior runs required.
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.evidence = root / '.sdd-work/evidence.log'
        self.evidence.parent.mkdir()
        self.evidence.write_text('Owned inventory report validation fixture\n')
        root_patch = patch.object(checker, 'ROOT', root)
        root_patch.start()
        self.addCleanup(root_patch.stop)
        self.report = {
            'status': 'PENDING', 'blockers': ['native approval absent'],
            'command': 'inventory', 'revision': '8' * 40,
            'counts': {'expected': 1, 'executed': 1, 'passed': 1, 'skipped': 0, 'pending': 0},
            'cases': [{'id': 'inventory', 'status': 'PASS'}], 'exit_status': 0,
            'evidence_paths': ['.sdd-work/evidence.log'],
        }

    def validate(self):
        checker.validate_report(self.report, {'inventory'}, 'inventory')

    def test_pending_inventory_allowed(self):
        self.validate()

    def test_missing_evidence_file(self):
        self.evidence.unlink()
        with self.assertRaises(ValueError):
            self.validate()

    def test_unsafe_evidence_path(self):
        self.report['evidence_paths'] = ['../evidence.log']
        with self.assertRaises(ValueError):
            self.validate()

    def test_malformed_report_fields(self):
        for field, value in [('command', ['inventory']), ('command', ' '),
                             ('blockers', 'absent'), ('blockers', [' ']),
                             ('exit_status', False), ('evidence_paths', [None]),
                             ('cases', [{'id': None, 'status': 'PASS'}])]:
            with self.subTest(field=field, value=value):
                original = self.report[field]
                self.report[field] = value
                with self.assertRaises(ValueError):
                    self.validate()
                self.report[field] = original

    def test_duplicate_cases(self):
        self.report['cases'] *= 2
        with self.assertRaises(ValueError):
            self.validate()

    def test_boolean_count(self):
        self.report['counts']['passed'] = True
        with self.assertRaises(ValueError):
            self.validate()

    def test_pending_count(self):
        self.report['counts']['pending'] = 1
        with self.assertRaises(ValueError):
            self.validate()

    def test_fake_pass(self):
        self.report['status'] = 'PASS'
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_cases(self):
        self.report['cases'] = []
        with self.assertRaises(ValueError):
            self.validate()

    def test_zero_counts(self):
        self.report['counts']['executed'] = 0
        with self.assertRaises(ValueError):
            self.validate()

    def test_skipped(self):
        self.report['counts']['skipped'] = 1
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_count(self):
        del self.report['counts']['passed']
        with self.assertRaises(ValueError):
            self.validate()

    def test_invalid_provenance(self):
        self.report['revision'] = 'latest'
        with self.assertRaises(ValueError):
            self.validate()

    def test_pending_native(self):
        with self.assertRaises(ValueError):
            checker.validate_report(self.report, {'inventory'}, 'native')

    def test_pending_without_blocker(self):
        self.report['blockers'] = []
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_evidence(self):
        self.report['evidence_paths'] = []
        with self.assertRaises(ValueError):
            self.validate()


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.contract, self.manifest, self.dossier = checker.load_inventory()

    def validate(self):
        checker.validate_inventory(self.contract, self.manifest, self.dossier)

    def test_complete_inventory(self):
        self.validate()

    def test_missing_required_case(self):
        self.contract['required_case_ids'].remove('soundtrack-change')
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_direction(self):
        self.contract['queries'] = [q for q in self.contract['queries'] if q['direction'] != 'text-to-audio']
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_distractor(self):
        self.contract['queries'][0]['distractors'].pop()
        with self.assertRaises(ValueError):
            self.validate()

    def test_fake_fixture_hash(self):
        self.manifest['fixtures'][0]['sha256'] = 'a' * 64
        with self.assertRaises(ValueError):
            self.validate()

    def test_materialized_missing_bytes(self):
        f = self.manifest['fixtures'][0]
        f.update(status='materialized', sha256='a' * 64)
        with self.assertRaises(ValueError):
            self.validate()

    def test_fake_native_pass(self):
        self.contract['native_proof']['status'] = 'PASS'
        with self.assertRaises(ValueError):
            self.validate()

    def test_unknown_native_revision(self):
        self.contract['provenance']['native_revision'] = 'latest'
        with self.assertRaises(ValueError):
            self.validate()


class RecordedOutcomeTests(unittest.TestCase):
    """A dossier may leave PENDING only with a valid native report that agrees."""

    def setUp(self):
        import test_native_reports as native
        self.native = native
        self.contract, self.manifest, self.dossier = checker.load_inventory()
        self.builder = native.Builder()
        self.dossier.update(model_sha256=native.MODEL, projector_sha256=native.PROJECTOR)

    def validate(self, report=None):
        checker.validate_inventory(self.contract, self.manifest, self.dossier, report)

    def failing_report(self):
        self.builder.items['audio-bell']['vector'] = self.native.encode(self.native.vec('typing', 0.3))
        return self.builder.report()

    def test_failure_outcome_without_report(self):
        self.dossier['native_proof'] = 'FAIL'
        with self.assertRaises(ValueError):
            self.validate(None)

    def test_modality_claim_while_pending(self):
        self.dossier['modality_evidence']['audio'] = 'PASS'
        with self.assertRaises(ValueError):
            self.validate(None)

    def test_pass_claim_against_failing_report(self):
        self.dossier['native_proof'] = 'PASS'
        with self.assertRaises(ValueError):
            self.validate(self.failing_report())

    def test_pass_claim_without_report(self):
        self.dossier['native_proof'] = 'PASS'
        with self.assertRaises(ValueError):
            self.validate(None)

    def test_pending_claim_with_report(self):
        with self.assertRaises(ValueError):
            self.validate(self.builder.report())

    def test_recorded_failure_accepted(self):
        report = self.failing_report()
        self.dossier['native_proof'] = 'FAIL'
        self.dossier['modality_evidence'] = {k: 'PASS' for k in self.dossier['modality_evidence']}
        self.dossier['blockers'] = ['recorded failure']
        self.validate(report)

    def test_recorded_pass_accepted(self):
        self.dossier['native_proof'] = 'PASS'
        self.dossier['modality_evidence'] = {k: 'PASS' for k in self.dossier['modality_evidence']}
        self.dossier['blockers'] = ['none outstanding for the candidate']
        self.validate(self.builder.report())

    def test_dossier_hash_must_match_report(self):
        report = self.failing_report()
        self.dossier['native_proof'] = 'FAIL'
        self.dossier['modality_evidence'] = {k: 'PASS' for k in self.dossier['modality_evidence']}
        self.dossier['model_sha256'] = 'e' * 64
        with self.assertRaises(ValueError):
            self.validate(report)


if __name__ == '__main__':
    unittest.main()
