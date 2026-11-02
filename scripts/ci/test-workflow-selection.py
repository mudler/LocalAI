#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Dependency wiring invariants. Requires PyYAML; runs without product builds."""
import importlib.util
import io
import json
from pathlib import Path
import sys
sys.dont_write_bytecode = True
import tarfile
import tempfile
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ['test.yml', 'build-test.yaml', 'tests-ui-e2e.yml', 'tests-e2e.yml',
             'tests-e2e-distributed.yml', 'addons-tests.yml']

def load(name):
    # BaseLoader keeps the GitHub `on` key a string (YAML 1.2 semantics).
    return yaml.load((ROOT / '.github/workflows' / name).read_text(), Loader=yaml.BaseLoader)

class Wiring(unittest.TestCase):
    def test_all_yaml_parses(self):
        for path in (ROOT / '.github').rglob('*.y*ml'):
            if path.suffix in ('.yml', '.yaml'):
                yaml.load(path.read_text(), Loader=yaml.BaseLoader)

    def test_stable_names_routes_and_mandatory_steps(self):
        for name in WORKFLOWS:
            workflow = load(name)
            self.assertIn('workflow_dispatch', workflow['on'], name)
            self.assertIn('schedule', workflow['on'], name)
            for event in ('pull_request', 'push'):
                trigger = workflow['on'][event] or {}
                self.assertNotIn('paths', trigger, name)
                self.assertNotIn('paths-ignore', trigger, name)
            old = json.loads((ROOT / 'scripts/ci/validation-checks.json').read_text())[name]
            for job_id, oldjob in old.items():
                job = workflow['jobs'][job_id]
                self.assertEqual(job.get('name'), oldjob.get('name'), name + job_id)
                self.assertIn(job.get('if', ''), ('', 'always()'))
                if 'strategy' in oldjob:
                    self.assertEqual(job['strategy'], oldjob['strategy'])
            for job in workflow['jobs'].values():
                steps = job.get('steps', [])
                selector = next(s for s in steps if s.get('id') == 'affected')
                self.assertEqual(selector['uses'], './.github/actions/ci-select')
                checkout = next(s for s in steps if 'actions/checkout@' in s.get('uses', ''))
                self.assertEqual(checkout['with']['fetch-depth'], '0')
                gate = steps[-1]
                self.assertEqual(gate['run'], 'node scripts/ci/check-steps.mjs')
                self.assertEqual(gate['if'], 'always()')
                required = set(gate['env']['REQUIRED'].split())
                actual = {s['id'] for s in steps if "== 'true'" in s.get('if', '') and 'steps.affected.outputs.' in s.get('if', '') and s.get('id')}
                self.assertEqual(required, actual, name)
                self.assertTrue(required)

    def test_publish_locks_and_manual_guest_safety(self):
        package = load('addons-package.yml')
        self.assertEqual(package['on']['push']['branches'], ['master'])
        self.assertEqual(package['on']['push']['tags'], ['v*'])
        self.assertIn('workflow_dispatch', package['on'])
        self.assertEqual(package['concurrency']['cancel-in-progress'], "${{ github.event_name == 'pull_request' }}")
        self.assertIn('github.run_id', package['concurrency']['group'])
        self.assertNotIn('schedule', package['on'])
        jobs = package['jobs']
        for id in ['publish', 'promote']:
            self.assertEqual(jobs[id]['concurrency']['cancel-in-progress'], 'false')
        guest = jobs['sandbox-image']
        self.assertEqual(guest['with']['validate-only'], "${{ github.event_name == 'workflow_dispatch' || github.event_name == 'pull_request' }}")
        push = load('sandbox-images.yml')['jobs']['build']['env']['PUSH']
        self.assertIn('!inputs.validate-only', push)
        plan = next(s for s in jobs['plan']['steps'] if s.get('id') == 'plan')
        self.assertEqual(plan['env']['EVENT'], "${{ github.event_name == 'workflow_dispatch' && 'pull_request' || github.event_name }}")
        self.assertIn('for id in $SELECTED_ADDONS;', plan['run'])
        self.assertIn('always()', jobs['package']['if'])
        self.assertEqual(jobs['result']['if'], 'always()')
        self.assertEqual(set(jobs['result']['needs']), set(jobs) - {'result'})
        validate = jobs['validate']['steps']
        for needle in ['Run the packaging checks', 'Check that the committed catalog is the generated one']:
            step = next(s for s in validate if s.get('name') == needle)
            self.assertEqual(step['if'], "matrix.addon == 'tables'")

    def test_shared_fixture_wiring(self):
        jobs = load('addons-tests.yml')['jobs']
        producer, consumer = jobs['prepare'], jobs['suites']
        self.assertEqual(producer['runs-on'], consumer['runs-on'])
        self.assertEqual(consumer['needs'], 'prepare')
        self.assertEqual(consumer['if'], 'always()')
        self.assertIn('PREPARE_RESULT', consumer['steps'][0]['run'])
        ptext = str(producer)
        ctext = str(consumer)
        self.assertNotIn('install-deps', ptext)
        self.assertIn('--prepare-build', ptext)
        self.assertNotIn('--prepare-build', ctext)
        self.assertIn('fixtures.py pack', ptext)
        self.assertIn('fixtures.py unpack', ctext)
        self.assertNotIn('--integration', ctext)
        self.assertIn('--check', ctext)
        self.assertIn('retention-days', ptext)

spec = importlib.util.spec_from_file_location('fixtures', ROOT / 'scripts/ci/fixtures.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)

class Fixtures(unittest.TestCase):
    def make(self, root):
        for name in ['core/http/react-ui/dist/index.html', 'pkg/grpc/proto/backend.pb.go',
                     'pkg/grpc/proto/backend_grpc.pb.go', 'pkg/motion/proto/motion.pb.go'] + fixtures.BINARIES:
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('fixture')
            if name in fixtures.BINARIES:
                path.chmod(0o755)

    def test_roundtrip_modes_paths_sha_and_checksums(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); src = root/'src'; dst = root/'dst'
            src.mkdir(); dst.mkdir(); self.make(src)
            archive = root/'fixtures.tgz'
            fixtures.transfer('pack', src, archive, 'abc', {'go': '1.26'})
            fixtures.transfer('unpack', dst, archive, 'abc', {'go': '1.26'})
            self.assertEqual(fixtures.metadata(src), fixtures.metadata(dst))
            with self.assertRaisesRegex(AssertionError, 'sha mismatch'):
                fixtures.transfer('unpack', dst, archive, 'def', {'go': '1.26'})
            with self.assertRaisesRegex(AssertionError, 'tools mismatch'):
                fixtures.transfer('unpack', dst, archive, 'abc', {'go': '1.27'})
            with tarfile.open(archive, 'w:gz') as tar:
                payload = b'escape'; member = tarfile.TarInfo('../escape'); member.size = len(payload)
                tar.addfile(member, io.BytesIO(payload))
            with self.assertRaises(AssertionError):
                fixtures.transfer('unpack', dst, archive, 'abc', {'go': '1.26'})

    def test_missing_executable_and_tampered_payload(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); self.make(root)
            binary = root/fixtures.BINARIES[0]; binary.chmod(0o644)
            with self.assertRaisesRegex(AssertionError, 'not executable'):
                fixtures.transfer('pack', root, root/'fixtures.tgz', 'abc')
            binary.chmod(0o755)
            fixtures.transfer('pack', root, root/'fixtures.tgz', 'abc')
            binary.write_text('tampered')
            # Pack files with the old manifest, emulating damaged artifact contents.
            with tarfile.open(root/'bad.tgz', 'w:gz') as tar:
                for name in fixtures.PATHS + ['addon-fixtures.json']:
                    tar.add(root/name, arcname=name)
            with self.assertRaisesRegex(AssertionError, 'checksum/mode mismatch'):
                fixtures.transfer('unpack', root, root/'bad.tgz', 'abc')

if __name__ == '__main__':
    unittest.main()
