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


class ChainTests(DeliveryTests):
    """Run all 29 legacy security checks against a genuine overlapping chain too."""
    def setUp(self):
        super().setUp()
        first = {'patch': self.patch.name,
                 'patch_sha256': delivery.digest(self.patch.read_bytes()),
                 'files': {n: h.copy() for n, h in self.files.items()}}
        for name in self.files:
            (self.source / name).write_text(name + ' patched\n')
        delivery.git(self.source, 'add', '.')
        # Step two overlaps 'one', leaving step one's 'two' result intact.
        (self.source / 'one').write_text('one patched twice\n')
        self.second = self.root / 'second.patch'
        self.second.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain = [first, {'patch': self.second.name,
                             'patch_sha256': delivery.digest(self.second.read_bytes()),
                             'files': {'one': {'base_sha256': first['files']['one']['final_sha256'],
                                               'final_sha256': self.files['one']['final_sha256']}}}]
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files:
            (self.source / name).write_text(name + '\n')
        self.save_chain()

    def save_chain(self):
        self.manifest.write_text(json.dumps({'base_commit': self.pin, 'files': self.files,
                                            'patches': self.chain}))
        self.digest = delivery.digest(self.manifest.read_bytes())

    def prefix(self):
        delivery.git(self.source, 'apply', str(self.patch))

    def snapshot(self):
        return {n: ((self.source / n).read_bytes(), (self.source / n).stat().st_mtime_ns)
                for n in ('one', 'two', 'other')} | {'index': (self.source / '.git/index').read_bytes()}

    def assert_rejected_without_writes(self, verify=False):
        before = self.snapshot()
        with self.assertRaises(ValueError):
            self.run_prepare(verify)
        self.assertEqual(before, self.snapshot())

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched twice\n')
        self.assertEqual(delivery.git(self.source, 'write-tree').strip(),
                         delivery.git(self.source, 'rev-parse', self.pin + '^{tree}').strip())

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched twice\n')

    def test_prefix_verify_only(self):
        self.prefix()
        self.assert_rejected_without_writes(True)

    def test_prefix_unrelated_hidden_dirt(self):
        self.prefix()
        delivery.git(self.source, 'update-index', '--assume-unchanged', 'other')
        (self.source / 'other').write_text('dirt\n')
        self.assert_rejected_without_writes()

    def test_mixed_final_base(self):
        (self.source / 'one').write_text('one patched twice\n')
        self.assert_rejected_without_writes()

    def test_ordering(self):
        self.chain.reverse()
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_step_two_tamper(self):
        self.second.write_bytes(self.second.read_bytes() + b'\n')
        self.assert_rejected_without_writes()

    def test_step_two_invalid_apply(self):
        self.second.write_bytes(self.second.read_bytes().replace(b'-one patched', b'-wrong context'))
        self.chain[1]['patch_sha256'] = delivery.digest(self.second.read_bytes())
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_step_two_wrong_base_digest(self):
        self.chain[1]['files']['one']['base_sha256'] = self.files['one']['base_sha256']
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_step_two_wrong_final_digest(self):
        self.chain[1]['files']['one']['final_sha256'] = '0' * 64
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_step_two_extra_path(self):
        extra = (b'diff --git a/extra b/extra\nnew file mode 100644\n'
                 b'--- /dev/null\n+++ b/extra\n@@ -0,0 +1 @@\n+unreviewed\n')
        self.second.write_bytes(self.second.read_bytes() + extra)
        self.chain[1]['patch_sha256'] = delivery.digest(self.second.read_bytes())
        self.save_chain()
        self.assert_rejected_without_writes()
        self.assertFalse((self.source / 'extra').exists())

    def test_prefix_dirty_index(self):
        self.prefix()
        delivery.git(self.source, 'add', 'one')
        self.assert_rejected_without_writes()

    def test_prefix_untracked_collision(self):
        self.prefix()
        (self.source / 'one.rej').write_text('preserve me')
        self.assert_rejected_without_writes()
        self.assertEqual((self.source / 'one.rej').read_text(), 'preserve me')

    def test_apply_failure_no_success(self):
        original = delivery.git
        def fail(source, *args, **kwargs):
            if Path(source) == self.source and args[:1] == ('apply',) and '--check' not in args:
                raise ValueError('fixture delivery failure')
            return original(source, *args, **kwargs)
        with patch.object(delivery, 'git', side_effect=fail):
            self.assert_rejected_without_writes()

    def test_final_readonly(self):
        self.run_prepare()
        before = self.snapshot()
        for verify in (False, True):
            self.assertEqual(self.run_prepare(verify), 'verified')
            self.assertEqual(before, self.snapshot())

    def test_path_union(self):
        del self.chain[0]['files']['two']
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_bounded_chain(self):
        self.chain = self.chain * 17
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_cmake_rejects_prefix_without_mutation(self):
        # Execute the actual CMake verification stanza, with only its source
        # directory redirected to a disposable authenticated fixture helper.
        self.prefix()
        helper = self.root / 'helper'
        helper.mkdir()
        script = Path(delivery.__file__).read_text()
        start = script.index('PIN = ')
        end = script.index('\n\ndef digest', start)
        script = script[:start] + ('PIN = ' + repr(self.pin) + '\nMANIFEST_SHA256 = ' + repr(self.digest) +
                                   '\nPATHS = ' + repr(frozenset(self.files))) + script[end:]
        (helper / 'apply-upstream-patches.py').write_text(script)
        (helper / 'patches').mkdir()
        for path in (self.root / step['patch'] for step in self.chain):
            (helper / 'patches' / path.name).write_bytes(path.read_bytes())
        (helper / 'patches/core-run-cancellation.manifest.json').write_bytes(self.manifest.read_bytes())
        cmake = Path(delivery.__file__).with_name('CMakeLists.txt').read_text()
        stanza = cmake[cmake.index('find_package(Python3'):cmake.index('\nif(APPLE)')]
        fixture = self.root / 'verify.cmake'
        fixture.write_text('set(AUDIO_CPP_DIR "' + self.source.as_posix() + '")\n' +
                           stanza.replace('${CMAKE_CURRENT_SOURCE_DIR}', helper.as_posix()))
        before = self.snapshot()
        result = subprocess.run(['cmake', '-P', str(fixture)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('requires verified preparation', result.stderr)
        self.assertNotIn(str(self.source), result.stderr)
        self.assertEqual(before, self.snapshot())


class ThreePatchTests(ChainTests):
    """The complete security/overlap suite also runs on three ordered steps."""
    def setUp(self):
        super().setUp()
        delivery.git(self.source, 'apply', str(self.patch))
        delivery.git(self.source, 'apply', str(self.second))
        delivery.git(self.source, 'add', '.')
        old = self.files['two']['final_sha256']
        (self.source / 'two').write_text('two patched three\n')
        self.third = self.root / 'third.patch'
        self.third.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['two']['final_sha256'] = delivery.digest((self.source / 'two').read_bytes())
        self.chain.append({'patch': self.third.name,
                           'patch_sha256': delivery.digest(self.third.read_bytes()),
                           'files': {'two': {'base_sha256': old,
                                             'final_sha256': self.files['two']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_second_prefix_upgrade(self):
        self.prefix()
        delivery.git(self.source, 'apply', str(self.second))
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'two').read_text(), 'two patched three\n')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_second_prefix_verify_only(self):
        self.prefix()
        delivery.git(self.source, 'apply', str(self.second))
        self.assert_rejected_without_writes(True)

    def test_third_tamper(self):
        self.third.write_bytes(self.third.read_bytes() + b'bad')
        self.assert_rejected_without_writes()

    def test_third_wrong_boundary(self):
        self.chain[2]['files']['two']['base_sha256'] = '0' * 64
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_third_wrong_final(self):
        self.chain[2]['files']['two']['final_sha256'] = '0' * 64
        self.save_chain()
        self.assert_rejected_without_writes()

    def test_cmake_rejects_prefix_without_mutation(self):
        # Parent fixture copies every patch referenced by its authenticated manifest.
        super().test_cmake_rejects_prefix_without_mutation()


class FourPatchTests(ThreePatchTests):
    """Run inherited security checks with four authenticated boundaries."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched four\n')
        self.fourth = self.root / 'fourth.patch'
        self.fourth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.fourth.name,
                           'patch_sha256': delivery.digest(self.fourth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched four\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched four\n')

    def test_third_prefix_upgrade(self):
        for step in self.chain[:3]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_fourth(self):
        for step in self.chain[:3]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial fourth\n')
        self.assert_rejected_without_writes()

    def test_fourth_tamper(self):
        self.fourth.write_bytes(self.fourth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class FivePatchTests(FourPatchTests):
    """The fifth boundary also extends the authenticated path union."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['two']['final_sha256']
        (self.source / 'two').write_text('two patched five\n')
        self.fifth = self.root / 'fifth.patch'
        self.fifth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['two']['final_sha256'] = delivery.digest((self.source / 'two').read_bytes())
        self.chain.append({'patch': self.fifth.name,
                           'patch_sha256': delivery.digest(self.fifth.read_bytes()),
                           'files': {'two': {'base_sha256': old,
                                            'final_sha256': self.files['two']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_second_prefix_upgrade(self):
        for step in self.chain[:2]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'two').read_text(), 'two patched five\n')

    def test_fourth_prefix_upgrade(self):
        for step in self.chain[:4]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'two').read_text(), 'two patched five\n')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_fifth(self):
        for step in self.chain[:4]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'two').write_text('partial fifth\n')
        self.assert_rejected_without_writes()

    def test_fifth_tamper(self):
        self.fifth.write_bytes(self.fifth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class SixPatchTests(FivePatchTests):
    """Sixth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched six\n')
        self.sixth = self.root / 'sixth.patch'
        self.sixth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.sixth.name,
                           'patch_sha256': delivery.digest(self.sixth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched six\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched six\n')

    def test_fifth_prefix_upgrade(self):
        for step in self.chain[:5]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_sixth(self):
        for step in self.chain[:5]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial sixth\n')
        self.assert_rejected_without_writes()

    def test_sixth_tamper(self):
        self.sixth.write_bytes(self.sixth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class SevenPatchTests(SixPatchTests):
    """Seventh step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched seven\n')
        self.seventh = self.root / 'seventh.patch'
        self.seventh.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.seventh.name,
                           'patch_sha256': delivery.digest(self.seventh.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched seven\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched seven\n')

    def test_sixth_prefix_upgrade(self):
        for step in self.chain[:6]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_seventh(self):
        for step in self.chain[:6]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial seventh\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_seventh_tamper(self):
        self.seventh.write_bytes(self.seventh.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class EightPatchTests(SevenPatchTests):
    """Eighth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched eight\n')
        self.eighth = self.root / 'eighth.patch'
        self.eighth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.eighth.name,
                           'patch_sha256': delivery.digest(self.eighth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched eight\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched eight\n')

    def test_seventh_prefix_upgrade(self):
        for step in self.chain[:7]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_eighth(self):
        for step in self.chain[:7]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial eighth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_eighth_tamper(self):
        self.eighth.write_bytes(self.eighth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class NinePatchTests(EightPatchTests):
    """Ninth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched nine\n')
        self.ninth = self.root / 'ninth.patch'
        self.ninth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.ninth.name,
                           'patch_sha256': delivery.digest(self.ninth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched nine\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched nine\n')

    def test_eighth_prefix_upgrade(self):
        for step in self.chain[:8]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_ninth(self):
        for step in self.chain[:8]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial ninth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_ninth_tamper(self):
        self.ninth.write_bytes(self.ninth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class TenPatchTests(NinePatchTests):
    """Tenth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched ten\n')
        self.tenth = self.root / 'tenth.patch'
        self.tenth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.tenth.name,
                           'patch_sha256': delivery.digest(self.tenth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched ten\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched ten\n')

    def test_ninth_prefix_upgrade(self):
        for step in self.chain[:9]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_tenth(self):
        for step in self.chain[:9]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial tenth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_actual_token_host_callsite(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0010-sheetsage-token-host-cancellation.patch').read_text()
        self.assertIn('window.generation_stop, assets_->config.max_position_embeddings', text)
        self.assertIn('execution_context().current_run_stop()', text)
        self.assertIn('auto host=token_prefix(', text)
        self.assertIn('if(token_transition(', text)
        self.assertIn('return token_publish(', text)

    def test_tenth_tamper(self):
        self.tenth.write_bytes(self.tenth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class ElevenPatchTests(TenPatchTests):
    """Eleventh step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched eleven\n')
        self.eleventh = self.root / 'eleventh.patch'
        self.eleventh.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.eleventh.name,
                           'patch_sha256': delivery.digest(self.eleventh.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched eleven\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched eleven\n')

    def test_tenth_prefix_upgrade(self):
        for step in self.chain[:10]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_eleventh(self):
        for step in self.chain[:10]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial eleventh\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_actual_token_host_callsite(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0010-sheetsage-token-host-cancellation.patch').read_text()
        self.assertIn('window.generation_stop, assets_->config.max_position_embeddings', text)
        self.assertIn('execution_context().current_run_stop()', text)
        self.assertIn('auto host=token_prefix(', text)
        self.assertIn('if(token_transition(', text)
        self.assertIn('return token_publish(', text)

    def test_actual_output_domain_and_finish_wiring(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0011-sheetsage-output-host-cancellation.patch').read_text()
        self.assertIn('output_owner->build_overlap_prefix_events', text)
        self.assertIn('output_owner->encode_events', text)
        self.assertIn('output_owner->decode_events', text)
        self.assertIn('output_owner->stitched_window_events', text)
        self.assertIn('return finish_sheetsage_output', text)
        self.assertIn('{max_tokens,static_cast<int64_t>(windows.size())},execution_context().current_run_stop()', text)

    def test_eleventh_tamper(self):
        self.eleventh.write_bytes(self.eleventh.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class TwelvePatchTests(ElevenPatchTests):
    """Twelfth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched twelve\n')
        self.twelfth = self.root / 'twelfth.patch'
        self.twelfth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.twelfth.name,
                           'patch_sha256': delivery.digest(self.twelfth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched twelve\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched twelve\n')

    def test_eleventh_prefix_upgrade(self):
        for step in self.chain[:11]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_twelfth(self):
        for step in self.chain[:11]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial twelfth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_actual_token_host_callsite(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0010-sheetsage-token-host-cancellation.patch').read_text()
        self.assertIn('window.generation_stop, assets_->config.max_position_embeddings', text)
        self.assertIn('execution_context().current_run_stop()', text)
        self.assertIn('auto host=token_prefix(', text)
        self.assertIn('if(token_transition(', text)
        self.assertIn('return token_publish(', text)

    def test_actual_output_domain_and_finish_wiring(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0011-sheetsage-output-host-cancellation.patch').read_text()
        self.assertIn('output_owner->build_overlap_prefix_events', text)
        self.assertIn('output_owner->encode_events', text)
        self.assertIn('output_owner->decode_events', text)
        self.assertIn('output_owner->stitched_window_events', text)
        self.assertIn('return finish_sheetsage_output', text)
        self.assertIn('{max_tokens,static_cast<int64_t>(windows.size())},execution_context().current_run_stop()', text)

    def test_twelfth_tamper(self):
        self.twelfth.write_bytes(self.twelfth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class ThirteenPatchTests(TwelvePatchTests):
    """Thirteenth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched thirteen\n')
        self.thirteenth = self.root / 'thirteenth.patch'
        self.thirteenth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.thirteenth.name,
                           'patch_sha256': delivery.digest(self.thirteenth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched thirteen\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched thirteen\n')

    def test_twelfth_prefix_upgrade(self):
        for step in self.chain[:12]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_thirteenth(self):
        for step in self.chain[:12]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial thirteenth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_actual_token_host_callsite(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0010-sheetsage-token-host-cancellation.patch').read_text()
        self.assertIn('window.generation_stop, assets_->config.max_position_embeddings', text)
        self.assertIn('execution_context().current_run_stop()', text)
        self.assertIn('auto host=token_prefix(', text)
        self.assertIn('if(token_transition(', text)
        self.assertIn('return token_publish(', text)

    def test_actual_output_domain_and_finish_wiring(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0011-sheetsage-output-host-cancellation.patch').read_text()
        self.assertIn('output_owner->build_overlap_prefix_events', text)
        self.assertIn('output_owner->encode_events', text)
        self.assertIn('output_owner->decode_events', text)
        self.assertIn('output_owner->stitched_window_events', text)
        self.assertIn('return finish_sheetsage_output', text)
        self.assertIn('{max_tokens,static_cast<int64_t>(windows.size())},execution_context().current_run_stop()', text)

    def test_thirteenth_tamper(self):
        self.thirteenth.write_bytes(self.thirteenth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


class FourteenPatchTests(ThirteenPatchTests):
    """Fourteenth step changes existing union paths; every exact prefix upgrades."""
    def setUp(self):
        super().setUp()
        for step in self.chain:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        delivery.git(self.source, 'add', '.')
        old = self.files['one']['final_sha256']
        (self.source / 'one').write_text('one patched fourteen\n')
        self.fourteenth = self.root / 'fourteenth.patch'
        self.fourteenth.write_bytes(delivery.git(self.source, 'diff', '--binary', '--'))
        self.files['one']['final_sha256'] = delivery.digest((self.source / 'one').read_bytes())
        self.chain.append({'patch': self.fourteenth.name,
                           'patch_sha256': delivery.digest(self.fourteenth.read_bytes()),
                           'files': {'one': {'base_sha256': old,
                                             'final_sha256': self.files['one']['final_sha256']}}})
        delivery.git(self.source, 'read-tree', self.pin)
        for name in self.files: (self.source / name).write_text(name + '\n')
        self.save_chain()

    def test_clean_apply(self):
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched fourteen\n')

    def test_prefix_upgrade(self):
        self.prefix()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual((self.source / 'one').read_text(), 'one patched fourteen\n')

    def test_thirteenth_prefix_upgrade(self):
        for step in self.chain[:13]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        self.assert_rejected_without_writes(True)
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(self.run_prepare(True), 'verified')
        before = self.snapshot()
        self.assertEqual(self.run_prepare(), 'verified')
        self.assertEqual(before, self.snapshot())

    def test_partial_fourteenth(self):
        for step in self.chain[:13]:
            delivery.git(self.source, 'apply', str(self.root / step['patch']))
        (self.source / 'one').write_text('partial fourteenth\n')
        self.assert_rejected_without_writes()

    def test_actual_window_callsite_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0007-sheetsage-window-cancellation.patch').read_text()
        self.assertIn('const auto windows = owned_window_plan(', text)
        self.assertIn('const auto window_audio = acquire_window_before_mel(', text)
        self.assertIn('execution_context().current_run_stop()', text)

    def test_actual_encoder_host_forwarding(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0008-sheetsage-encoder-host-cancellation.patch').read_text()
        self.assertIn('stage_encoder_inputs(config_, batch_, mel_frames_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('encoder_host_shape(impl_->config, batch, mel_frames)', text)
        self.assertIn('return read_encoder_output(mixed_, stop)', text)

    def test_decoder_kv_prefill_source_order(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0009-sheetsage-decoder-host-staging.patch').read_text()
        self.assertIn('head_dim_(decoder_host_shape(config, memory_steps, cache_steps).head_dim)', text)
        self.assertIn('stage_decoder_step(config_', text)
        self.assertIn('execution_.current_run_stop()', text)
        self.assertIn('decoder_prefix_validate(config_', text)
        self.assertIn('run_step(tokens[static_cast<size_t>(i)], false)', text)
        self.assertIn('run_step(tokens.back(), true)', text)
        self.assertIn('step_cache_.import_state(empty)', text)
        self.assertEqual(text.count(' step_cache_.advance_after_direct_append(1);'), 1)

    def test_actual_token_host_callsite(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0010-sheetsage-token-host-cancellation.patch').read_text()
        self.assertIn('window.generation_stop, assets_->config.max_position_embeddings', text)
        self.assertIn('execution_context().current_run_stop()', text)
        self.assertIn('auto host=token_prefix(', text)
        self.assertIn('if(token_transition(', text)
        self.assertIn('return token_publish(', text)

    def test_actual_output_domain_and_finish_wiring(self):
        text = (Path(__file__).resolve().parent / 'patches' / '0011-sheetsage-output-host-cancellation.patch').read_text()
        self.assertIn('output_owner->build_overlap_prefix_events', text)
        self.assertIn('output_owner->encode_events', text)
        self.assertIn('output_owner->decode_events', text)
        self.assertIn('output_owner->stitched_window_events', text)
        self.assertIn('return finish_sheetsage_output', text)
        self.assertIn('{max_tokens,static_cast<int64_t>(windows.size())},execution_context().current_run_stop()', text)

    def test_fourteenth_tamper(self):
        self.fourteenth.write_bytes(self.fourteenth.read_bytes() + b'bad')
        self.assert_rejected_without_writes()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--require-min-tests', type=int, default=7)
    args = parser.parse_args()
    suite = unittest.TestSuite(unittest.defaultTestLoader.loadTestsFromTestCase(case)
                               for case in (DeliveryTests, ChainTests, ThreePatchTests, FourPatchTests, FivePatchTests, SixPatchTests, SevenPatchTests, EightPatchTests, NinePatchTests, TenPatchTests, ElevenPatchTests, TwelvePatchTests, ThirteenPatchTests, FourteenPatchTests))
    if suite.countTestCases() < args.require_min_tests:
        raise SystemExit('insufficient discovered delivery tests')
    raise SystemExit(not unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful())
