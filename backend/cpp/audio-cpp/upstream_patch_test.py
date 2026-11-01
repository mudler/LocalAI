#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Offline fixture tests for the strict patch delivery protocol."""
import argparse
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('delivery', Path(__file__).with_name('apply-upstream-patches.py'))
delivery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(delivery)


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'source'
        self.source.mkdir()
        delivery.git(self.source, 'init', '-q')
        for name in ('one', 'two', 'other'):
            (self.source / name).write_text(name + '\n')
        delivery.git(self.source, 'add', '.')
        delivery.git(self.source, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'base')
        self.pin = delivery.git(self.source, 'rev-parse', 'HEAD').decode().strip()
        self.files = {}
        for name in ('one', 'two'):
            path = self.source / name
            base = delivery.digest(path.read_bytes())
            path.write_text(name + ' patched\n')
            self.files[name] = {'base_sha256': base, 'final_sha256': delivery.digest(path.read_bytes())}
        self.patch = self.root / 'change.patch'
        self.patch.write_bytes(delivery.git(self.source, 'diff', '--binary', 'HEAD', '--'))
        for name in self.files:
            (self.source / name).write_text(name + '\n')
        self.manifest = self.root / 'manifest.json'
        self.manifest.write_text(json.dumps({'base_commit': self.pin, 'patch_sha256': delivery.digest(self.patch.read_bytes()), 'files': self.files}))
        self.digest = delivery.digest(self.manifest.read_bytes())

    def run_prepare(self, verify=False):
        return delivery.prepare(self.source, self.manifest, self.patch, self.digest, self.pin, self.files, verify)

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched\n')

    def test_verified_second_apply_unchanged(self):
        self.run_prepare()
        before = [(self.source / name).stat().st_mtime_ns for name in self.files]
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, [(self.source / name).stat().st_mtime_ns for name in self.files])

    def test_changed_touched_file(self):
        (self.source / 'one').write_text('drift\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_unrelated_tracked_dirt(self):
        (self.source / 'other').write_text('drift\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_partial_apply(self):
        (self.source / 'one').write_text('one patched\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_wrong_head(self):
        delivery.git(self.source, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '--allow-empty', '-qm', 'different')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_changed_patch(self):
        self.patch.write_bytes(self.patch.read_bytes() + b'\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_changed_manifest(self):
        self.manifest.write_text('{}')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_untracked_collision(self):
        (self.source / 'one.rej').write_text('collision')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_verify_only_refuses_base(self):
        with self.assertRaises(ValueError): self.run_prepare(True)
        self.assertEqual((self.source / 'one').read_text(), 'one\n')

    def test_staged_change(self):
        (self.source / 'one').write_text('one patched\n')
        delivery.git(self.source, 'add', 'one')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_hidden_tracked_dirt(self):
        delivery.git(self.source, 'update-index', '--assume-unchanged', 'other')
        (self.source / 'other').write_text('hidden drift\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_skip_worktree(self):
        delivery.git(self.source, 'update-index', '--skip-worktree', 'other')
        (self.source / 'other').write_text('hidden drift\n')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_sparse_missing_unapproved(self):
        delivery.git(self.source, 'update-index', '--skip-worktree', 'other')
        (self.source / 'other').unlink()
        with self.assertRaises(ValueError): self.run_prepare()

    def test_unrelated_mode_drift(self):
        (self.source / 'other').chmod(0o755)
        with self.assertRaises(ValueError): self.run_prepare()

    def test_final_mode_drift(self):
        self.run_prepare()
        (self.source / 'one').chmod(0o755)
        with self.assertRaises(ValueError): self.run_prepare()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--require-min-tests', type=int, default=7)
    args = parser.parse_args()
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(DeliveryTests)
    if suite.countTestCases() < args.require_min_tests:
        raise SystemExit('insufficient discovered delivery tests')
    raise SystemExit(not unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful())
