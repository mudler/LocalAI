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
import subprocess
import re
import fnmatch

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ['test.yml', 'build-test.yaml', 'tests-ui-e2e.yml', 'tests-e2e.yml',
             'tests-e2e-distributed.yml', 'addons-tests.yml']

def load(name):
    # BaseLoader keeps the GitHub `on` key a string (YAML 1.2 semantics).
    return yaml.load((ROOT / '.github/workflows' / name).read_text(), Loader=yaml.BaseLoader)

ALL_WORKFLOWS = WORKFLOWS + ['addons-package.yml']

def as_list(value):
    return value if isinstance(value, list) else [value]

def base_workflow(name):
    return yaml.load(subprocess.check_output(['git', 'show', '16acc8926:.github/workflows/'+name], cwd=ROOT), Loader=yaml.BaseLoader)

def triggered(workflow, paths, event):
    if event not in workflow['on']: return False
    rule = workflow['on'][event] or {}
    if rule.get('paths') and not any(fnmatch.fnmatchcase(p, pat) for p in paths for pat in rule['paths']): return False
    if rule.get('paths-ignore') and all(any(fnmatch.fnmatchcase(p, pat) for pat in rule['paths-ignore']) for p in paths): return False
    return True

def selection(paths, event='pull_request'):
    script = """import { selectEvent } from './scripts/ci/select.mjs';
const paths = JSON.parse(process.argv[1]), name = process.argv[2];
const a = 'a'.repeat(40), b = 'b'.repeat(40);
const event = name === 'push' ? {before:a, after:b} : {pull_request:{base:{sha:a},head:{sha:b,repo:{fork:true}}}};
const git = args => args[0] === 'diff' ? paths.join('\\0')+'\\0' : args[0] === 'merge-base' ? a : '';
console.log(JSON.stringify(selectEvent(name, event, name === 'push' ? 'refs/heads/master' : 'refs/pull/1/merge', git)));"""
    return json.loads(subprocess.check_output(['node', '--input-type=module', '-e', script, json.dumps(paths), event], cwd=ROOT))

def condition(expression, needs):
    # Evaluate the actual supported job expressions. Refuse unfamiliar syntax
    # rather than silently modeling a different guard from the YAML.
    s = expression.replace('${{', '').replace('}}', '').strip()
    s = s.replace('always()', 'True').replace('&&', ' and ').replace('||', ' or ')
    s = re.sub(r'needs\.([\w-]+)\.outputs\.([\w-]+)', lambda m: repr(needs[m[1]]['outputs'][m[2]]), s)
    s = re.sub(r'needs\.([\w-]+)\.result', lambda m: repr(needs[m[1]]['result']), s)
    if re.search(r'[^a-zA-Z0-9_ \'"()=!-]', s): raise AssertionError('unsupported condition '+s)
    return eval(s, {'__builtins__': {}}, {})

def eligible_jobs(result):
    outputs = {k: str(v).lower() for k,v in result['selected'].items()}
    outputs.update({'groups-json': json.dumps({'include':result['groups']}), 'validation-json': json.dumps(result['addons'] or ['rules'])})
    rows = []
    for addon in result['addons']:
        cfg = yaml.load((ROOT/'addons'/addon/'package/platforms.yaml').read_text(), Loader=yaml.BaseLoader)
        rows.extend(dict(row, addon=addon) for row in cfg['platforms'] if row['enabled'] == 'true')
    outputs.update({'sandbox': str('sandbox' in result['addons']).lower()})
    needs = {'classify': {'result':'success', 'outputs':outputs}, 'prepare': {'result':'success'}, 'plan': {'result':'success', 'outputs':{'sandbox': outputs['sandbox'], 'has_platforms': str(bool(rows)).lower(), 'matrix': json.dumps({'include':rows})}}, 'validate': {'result':'success'}, 'sandbox-image': {'result':'success' if outputs['sandbox'] == 'true' else 'skipped'}}
    actual = []
    for name in ALL_WORKFLOWS:
        for id, job in load(name)['jobs'].items():
            if id in ['classify', 'result']: continue
            if name == 'addons-package.yml' and id not in ['plan', 'validate', 'sandbox-image', 'package']: continue
            if not condition(job['if'], needs): continue
            matrix = job.get('strategy', {}).get('matrix')
            legs = [None]
            if isinstance(matrix, str):
                match = re.fullmatch(r'\$\{\{ fromJSON\(needs.(classify|plan).outputs.([\w-]+)\) \}\}', matrix)
                if not match: raise AssertionError(matrix)
                legs = [g.get('group') or g['addon']+'@'+g['platform'] for g in json.loads(needs[match[1]]['outputs'][match[2]])['include']]
            elif matrix and 'addon' in matrix:
                self_expression = matrix['addon']
                assert self_expression == '${{ fromJSON(needs.classify.outputs.validation-json) }}'
                legs = json.loads(outputs['validation-json'])
            for leg in legs: actual.append(name+':'+id+('['+leg+']' if leg else ''))
    return actual

class Wiring(unittest.TestCase):
    def test_all_yaml_parses(self):
        for path in (ROOT / '.github').rglob('*.y*ml'):
            if path.suffix in ('.yml', '.yaml'):
                yaml.load(path.read_text(), Loader=yaml.BaseLoader)

    def test_preallocation_and_fail_closed_results(self):
        for name in WORKFLOWS:
            workflow = load(name); jobs = workflow['jobs']
            self.assertIn('workflow_dispatch', workflow['on'])
            old = base_workflow(name)
            self.assertEqual(workflow['on'].get('schedule'), old['on'].get('schedule'))
            self.assertEqual(jobs['classify']['runs-on'], 'ubuntu-latest')
            self.assertEqual(len(jobs['classify']['steps']), 2)
            self.assertEqual(jobs['classify']['steps'][0]['with']['persist-credentials'], 'false')
            self.assertEqual(jobs['result']['if'], 'always()')
            self.assertEqual(set(jobs['result']['needs']), set(jobs) - {'result'})
            mapping = json.loads(jobs['result']['steps'][-1]['env']['JOB_DOMAINS'])
            self.assertEqual(set(mapping), set(jobs)-{'result', 'classify'})
            for id, domain in mapping.items():
                job = jobs[id]
                self.assertIn('classify', as_list(job['needs']))
                self.assertIn(f"needs.classify.outputs.{domain} == 'true'", job['if'])
                self.assertNotIn('steps.affected', str(job['steps']))
                self.assertNotIn('check-steps.mjs', str(job))
                self.assertNotIn('continue-on-error', job)
            if name == 'addons-tests.yml':
                self.assertEqual(jobs['suites']['strategy']['matrix'], '${{ fromJSON(needs.classify.outputs.groups-json) }}')
                self.assertIn("needs.prepare.result == 'success'", jobs['suites']['if'])

    def test_publish_locks_and_manual_guest_safety(self):
        workflow = load('addons-package.yml'); jobs = workflow['jobs']
        self.assertEqual(workflow['on']['push']['branches'], ['master'])
        self.assertNotIn('schedule', workflow['on'])
        self.assertEqual(workflow['concurrency']['cancel-in-progress'], "${{ github.event_name == 'pull_request' }}")
        for id in ['publish', 'promote']:
            self.assertEqual(jobs[id]['concurrency']['cancel-in-progress'], 'false')
        self.assertEqual(jobs['sandbox-image']['with']['validate-only'], "${{ github.event_name == 'workflow_dispatch' || github.event_name == 'pull_request' }}")
        self.assertIn('!inputs.validate-only', load('sandbox-images.yml')['jobs']['build']['env']['PUSH'])
        plan = next(s for s in jobs['plan']['steps'] if s.get('id') == 'plan')
        self.assertEqual(plan['env']['EVENT'], "${{ github.event_name == 'workflow_dispatch' && 'pull_request' || github.event_name }}")
        self.assertIn('for id in $SELECTED_ADDONS;', plan['run'])
        self.assertEqual(set(jobs['result']['needs']), set(jobs)-{'result'})
        for id in ['validate', 'plan']:
            self.assertEqual(jobs[id]['needs'], 'classify')
            self.assertEqual(jobs[id]['if'], "needs.classify.outputs.packages == 'true'")
        self.assertEqual(jobs['validate']['strategy']['matrix']['addon'], '${{ fromJSON(needs.classify.outputs.validation-json) }}')
        for step in jobs['validate']['steps']:
            if step.get('name') in ['Run the packaging checks', 'Check that the committed catalog is the generated one']:
                self.assertEqual(step['if'], 'strategy.job-index == 0')
        # PR-executable code has no registry credentials; the guest reusable
        # workflow explicitly blocks login/write on its validate-only path.
        for id in ['classify', 'plan', 'validate', 'package']:
            self.assertNotIn('secrets.', str(jobs[id]))
        self.assertNotIn('pull_request_target', workflow['on'])

    def test_documentation_contract_job_does_not_run_browser_suites(self):
        case = selection(['docs/content/features/addons.md'])
        self.assertEqual(eligible_jobs(case), ['test.yml:contracts'])
        self.assertEqual(len(case['contracts']), 5)
        job = load('test.yml')['jobs']['contracts']
        self.assertNotIn('make react-ui', str(job))
        self.assertNotIn('playwright', str(job))
        runner = (ROOT/'scripts/ci/run-contracts.mjs').read_text()
        self.assertIn('-ginkgo.fail-on-empty', runner)
        self.assertIn('assert.equal(test.focus', runner)

    def test_shared_fixture_wiring(self):
        jobs = load('addons-tests.yml')['jobs']; producer, consumer = jobs['prepare'], jobs['suites']
        self.assertEqual(producer['runs-on'], consumer['runs-on'])
        self.assertIn('prepare', consumer['needs'])
        self.assertIn('PREPARE_RESULT', consumer['steps'][0]['run'])
        self.assertNotIn('install-deps', str(producer))
        self.assertIn('--prepare-build', str(producer))
        self.assertIn('fixtures.py pack', str(producer))
        self.assertIn('fixtures.py unpack', str(consumer))
        self.assertNotIn('--integration', str(consumer))
        self.assertIn('--check', str(consumer))

    def test_expensive_job_eligibility(self):
        fixtures = json.loads((ROOT/'scripts/ci/eligibility-cases.json').read_text())
        for case in fixtures:
            result = selection(case['paths'], case.get('event', 'pull_request'))
            actual = eligible_jobs(result)
            self.assertEqual(actual, case['jobs'], case['name'])
            # Read the original workflow trigger rules, not a hand-maintained
            # list of which workflows used to start for each fixture.
            old = [n for n in ALL_WORKFLOWS if triggered(base_workflow(n), case['paths'], case.get('event', 'pull_request'))]
            self.assertEqual(old, case['base_workflows'], case['name'])

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
    if '--table' in sys.argv:
        for case in json.loads((ROOT/'scripts/ci/eligibility-cases.json').read_text()):
            print(case['name'], '|', ', '.join(case['base_workflows']), '|', ', '.join(eligible_jobs(selection(case['paths'], case.get('event', 'pull_request')))))
        sys.argv.remove('--table')
    unittest.main()
