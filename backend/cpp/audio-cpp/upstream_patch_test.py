#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Offline fixture tests for the strict patch delivery protocol."""
import argparse
import importlib.util
import json
from pathlib import Path
import tempfile
import os
import subprocess
from unittest.mock import patch
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

    def test_info_attributes_filter_never_executes(self):
        # Payload exists only inside this disposable fixture.
        marker = self.root / 'MARKER'
        (self.source / '.git/info/attributes').write_text('other filter=reviewprobe\n')
        subprocess.run(['git', '-C', str(self.source), 'config', 'filter.reviewprobe.clean',
                        'touch ' + str(marker) + '; cat'], check=True)
        for verify in (False, True):
            with self.subTest(verify=verify):
                try: self.run_prepare(verify)
                except ValueError: pass
                self.assertFalse(marker.exists(), 'verification executed an untrusted filter')

    def security_fixture(self, location, verify):
        if verify: self.run_prepare()
        marker = self.root / 'MARKER'
        payload = 'touch ' + str(marker) + '; cat'
        def config(key, value):
            subprocess.run(['git', '-C', str(self.source), 'config', key, value], check=True)
        environment = {}
        if location == 'tracked':
            (self.source / '.gitattributes').write_text('other filter=reviewprobe diff=reviewprobe\n')
            # Trusted fixture setup must not call the payload while staging.
            delivery.git(self.source, 'add', '.gitattributes')
        else:
            (self.source / '.git/info/attributes').write_text('* filter=reviewprobe diff=reviewprobe\n')
        config('filter.reviewprobe.clean', payload)
        config('filter.reviewprobe.smudge', payload)
        config('diff.reviewprobe.command', payload)
        config('diff.reviewprobe.textconv', payload)
        config('core.fsmonitor', payload)
        config('diff.external', payload)
        if location == 'environment':
            environment = {'GIT_CONFIG_COUNT': '2', 'GIT_CONFIG_KEY_0': 'core.fsmonitor',
                           'GIT_CONFIG_VALUE_0': payload, 'GIT_CONFIG_KEY_1': 'diff.external',
                           'GIT_CONFIG_VALUE_1': payload, 'GIT_EXTERNAL_DIFF': payload,
                           'GIT_CONFIG_GLOBAL': str(self.source / '.git/config'),
                           'GIT_CONFIG_SYSTEM': str(self.source / '.git/config')}
        if location == 'worktree':
            config('extensions.worktreeConfig', 'true')
            (self.source / '.git/config.worktree').write_text('[core]\n fsmonitor = ' + payload + '\n')
        with patch.dict(os.environ, environment):
            if location == 'tracked':
                with self.assertRaises(ValueError): self.run_prepare(verify)
            else:
                self.assertEqual(self.run_prepare(verify), 'verified')
        self.assertFalse(marker.exists(), 'untrusted Git configuration executed')

    def test_tracked_attributes_apply(self): self.security_fixture('tracked', False)
    def test_tracked_attributes_verify(self): self.security_fixture('tracked', True)
    def test_local_drivers_apply(self): self.security_fixture('local', False)
    def test_local_drivers_verify(self): self.security_fixture('local', True)
    def test_environment_drivers_apply(self): self.security_fixture('environment', False)
    def test_environment_drivers_verify(self): self.security_fixture('environment', True)
    def test_worktree_drivers_apply(self): self.security_fixture('worktree', False)
    def test_worktree_drivers_verify(self): self.security_fixture('worktree', True)

    def pinned_attributes_fixture(self, verify):
        marker = self.root / 'MARKER'
        (self.source / '.gitattributes').write_text('other filter=reviewprobe diff=reviewprobe\n')
        delivery.git(self.source, 'add', '.gitattributes')
        delivery.git(self.source, '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                     'commit', '-qm', 'pinned attributes')
        self.pin = delivery.git(self.source, 'rev-parse', 'HEAD').decode().strip()
        manifest = json.loads(self.manifest.read_bytes())
        manifest['base_commit'] = self.pin
        self.manifest.write_text(json.dumps(manifest))
        self.digest = delivery.digest(self.manifest.read_bytes())
        if verify: self.run_prepare()
        payload = 'touch ' + str(marker) + '; cat'
        for key in ('filter.reviewprobe.clean', 'diff.reviewprobe.command', 'diff.reviewprobe.textconv'):
            subprocess.run(['git', '-C', str(self.source), 'config', key, payload], check=True)
        self.assertEqual(self.run_prepare(verify), 'verified')
        self.assertFalse(marker.exists())

    def test_pinned_attributes_apply(self): self.pinned_attributes_fixture(False)
    def test_pinned_attributes_verify(self): self.pinned_attributes_fixture(True)

    def test_unrelated_symlink_drift(self):
        (self.source / 'other').unlink()
        (self.source / 'other').symlink_to('one')
        with self.assertRaises(ValueError): self.run_prepare()

    def test_final_symlink_drift(self):
        self.run_prepare()
        (self.source / 'one').unlink()
        (self.source / 'one').symlink_to('two')
        with self.assertRaises(ValueError): self.run_prepare(True)

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
