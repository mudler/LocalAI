# SPDX-License-Identifier: MIT
"""Tiny native SONAME regression; no CUDA, engine, model, or GPU required."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

HELPER = Path(__file__).with_name('dispatch-diagnostics-test-loader.sh').resolve()

class LoaderTest(unittest.TestCase):
    def test_isolated_soname_lookup(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'stub.c').write_text('int fixture_value(void) { return 7; }\n')
            (root / 'main.c').write_text('extern int fixture_value(void); int main(void) { return fixture_value() != 7; }\n')
            stub = root / 'libcuda.so'
            binary = root / 'fixture'
            subprocess.run(['cc', '-shared', '-fPIC', '-Wl,-soname,libcuda.so.1', str(root / 'stub.c'), '-o', str(stub)], check=True)
            subprocess.run(['cc', str(root / 'main.c'), str(stub), '-o', str(binary)], check=True)
            env = dict(os.environ)
            env.pop('LD_LIBRARY_PATH', None)
            before = subprocess.run([str(binary)], env=env, capture_output=True)
            self.assertNotEqual(before.returncode, 0)
            self.assertIn(b'libcuda.so.1', before.stderr)
            subprocess.run(['bash', str(HELPER), str(stub), str(root / 'identity.txt'), str(binary)], env=env, check=True)
            self.assertNotEqual(subprocess.run([str(binary)], env=env, capture_output=True).returncode, 0)
            self.assertIn('libcuda.so.1', (root / 'identity.txt').read_text())
            for bad in [root / 'missing', binary]:
                self.assertNotEqual(subprocess.run(['bash', str(HELPER), str(bad), str(root / 'bad.txt'), 'true'], env=env, capture_output=True).returncode, 0)

    def test_ctest_result_gate(self):
        script = HELPER.with_name('dispatch-diagnostics-worker-ci.sh').read_text()
        gate = script.split("<<'PYTEST'\n", 1)[1].split('\nPYTEST', 1)[0]
        good = '<testsuite tests="2" failures="0"><testcase name="dispatch_diagnostics_test" status="run"/><testcase name="dispatch_correlation_fixture" status="run"/></testsuite>'
        variants = [good, good.replace('status="run"', 'status="notrun"', 1),
                    good.replace('/><testcase', '><skipped/></testcase><testcase', 1),
                    good.replace('dispatch_correlation_fixture', 'other'),
                    good.replace('failures="0"', 'failures="1"')]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'ctest.xml'
            for index, xml in enumerate(variants):
                path.write_text(xml)
                result = subprocess.run(['python3', '-c', gate, str(path)], capture_output=True)
                self.assertEqual(result.returncode == 0, index == 0)

if __name__ == '__main__':
    unittest.main()
