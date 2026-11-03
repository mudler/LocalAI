#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Static native-filter examples, not a GitHub expression evaluator. Needs PyYAML."""
import fnmatch
import subprocess
import unittest
from pathlib import Path
import yaml

ROOT = Path(__file__).resolve().parents[2]
NAMES = ['test.yml', 'build-test.yaml', 'tests-ui-e2e.yml', 'tests-e2e.yml',
         'tests-e2e-distributed.yml', 'addons-tests.yml', 'addons-package.yml']

def load(name, base=False):
    path = '.github/workflows/' + name
    text = subprocess.check_output(['git', 'show', '16acc8926:' + path], cwd=ROOT) if base else (ROOT/path).read_text()
    return yaml.load(text, Loader=yaml.BaseLoader)

def matches(name, path, event):
    return any(fnmatch.fnmatchcase(path, pattern) for pattern in load(name)['on'][event]['paths'])

class NativePaths(unittest.TestCase):
    def test_shared_inputs_and_original_jobs(self):
        for path in (ROOT/'.github/workflows').iterdir():
            if path.suffix in ['.yml', '.yaml']:
                yaml.load(path.read_text(), Loader=yaml.BaseLoader)
        for name in NAMES:
            workflow = load(name); base = load(name, True)
            self.assertEqual(workflow['on']['push']['paths'], workflow['on']['pull_request']['paths'])
            self.assertEqual(workflow['on'].get('schedule'), base['on'].get('schedule'))
            self.assertEqual(set(workflow['jobs']), set(base['jobs']) | ({'prepare'} if name == 'addons-tests.yml' else set()))
            for event in ['pull_request', 'push']:
                for path in ['go.mod', 'go.sum', 'Makefile', 'scripts/build.sh', '.github/workflows/' + name]:
                    self.assertTrue(matches(name, path, event), (name, path, event))
                self.assertFalse(matches(name, 'docs/content/news.md', event), name)
                self.assertFalse(matches(name, 'website/content/blog/news.md', event), name)
            if name != 'addons-tests.yml':
                self.assertEqual(workflow['jobs'], base['jobs'])
            self.assertNotIn('classify', str(workflow))
            self.assertNotIn('job-result', str(workflow))
            self.assertEqual(workflow['concurrency']['cancel-in-progress'], "${{ github.event_name == 'pull_request' }}")

    def test_representative_consumers(self):
        cases = {
            'core/services/chats/store.go': NAMES[:-1],
            'pkg/model/loader.go': NAMES[:-1],
            'tests/e2e/mock-backend/main.go': ['test.yml', 'tests-e2e.yml', 'tests-ui-e2e.yml', 'addons-tests.yml'],
            'backend/go/cloud-proxy/main.go': ['test.yml', 'tests-e2e.yml', 'tests-ui-e2e.yml', 'addons-tests.yml'],
            'gallery/models/example.yaml': ['test.yml'],
            'backend/index.yaml': ['test.yml'],
            'coverage-baseline.txt': ['test.yml'],
            'Entitlements.plist': ['build-test.yaml'],
            'docs/content/features/image-generation.md': ['test.yml'],
            'docs/content/features/addons.md': ['test.yml', 'addons-tests.yml', 'addons-package.yml'],
            'docs/content/features/tables.md': ['test.yml', 'addons-tests.yml'],
            'docs/content/features/diagnostics.md': ['test.yml'],
            'docs/content/reference/cli-reference.md': ['test.yml'],
            'docs/content/getting-started/troubleshooting.md': ['test.yml'],
        }
        for path, names in cases.items():
            for name in names:
                for event in ['pull_request', 'push']:
                    self.assertTrue(matches(name, path, event), (path, name, event))
        for event in ['pull_request', 'push']:
            self.assertFalse(matches('addons-package.yml', 'core/http/react-ui/src/index.css', event))
            self.assertFalse(matches('test.yml', 'future-area/unknown.input', event))

    def test_suite_and_publish_preservation(self):
        old = load('addons-tests.yml', True)['jobs']['suites']
        new = load('addons-tests.yml')['jobs']['suites']
        self.assertEqual(new['strategy'], old['strategy'])
        self.assertEqual(new['needs'], 'prepare')
        self.assertNotIn('if', new)
        for path in ['scripts/addons/suites.json', 'pkg/addons/packaging/policy.go', 'pkg/addons/packaging/packaging_test.go']:
            self.assertEqual((ROOT/path).read_bytes(), subprocess.check_output(['git', 'show', '16acc8926:' + path], cwd=ROOT))
        package = load('addons-package.yml'); base = load('addons-package.yml', True)
        self.assertEqual(package['jobs'], base['jobs'])
        self.assertEqual(package['on']['push']['branches'], ['master'])
        self.assertEqual(package['on']['push']['tags'], ['v*'])
        self.assertEqual(package['on']['release'], base['on']['release'])
        self.assertNotIn('workflow_dispatch', package['on'])
        self.assertEqual(load('sandbox-images.yml'), load('sandbox-images.yml', True))
        for name in NAMES:
            self.assertEqual(load(name)['on']['push'].get('tags'), load(name, True)['on']['push'].get('tags'))
        for name in ['lint.yml', 'tests-ui-unit.yml', 'yaml-check.yml']:
            self.assertEqual(load(name), load(name, True))
        self.assertFalse((ROOT/'scripts/lib/ci-select.mjs').exists())
        self.assertFalse((ROOT/'scripts/ci/select.mjs').exists())

if __name__ == '__main__':
    unittest.main()
