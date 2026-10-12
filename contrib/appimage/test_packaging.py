# SPDX-License-Identifier: MIT
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PACK = ROOT / 'contrib/appimage'


class PackagingTests(unittest.TestCase):
    def test_apprun_preserves_context_and_arguments(self):
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            app = Path(tmp) / 'space AppDir'
            (app / 'usr/bin').mkdir(parents=True)
            (app / 'AppRun').write_bytes((PACK / 'AppRun').read_bytes())
            binary = app / 'usr/bin/local-ai-launcher'
            binary.write_text('#!/bin/sh\nprintf "%s\\n" "$PWD" "$HOME" "$LD_LIBRARY_PATH" "$@"\n')
            binary.chmod(0o755)
            env = dict(os.environ, HOME=tmp, LD_LIBRARY_PATH='/caller/libs')
            result = subprocess.check_output(['sh', str(app / 'AppRun'), 'two words', '*'],
                                             cwd=tmp, env=env, text=True)
            self.assertEqual(result.splitlines(), [tmp, tmp, '/caller/libs', 'two words', '*'])

    def test_unsupported_architecture_fails_before_build(self):
        result = subprocess.run(['bash', str(PACK / 'build.sh')],
                                env=dict(os.environ, APPIMAGE_ARCH='arm64'), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('only amd64', result.stderr)

    def test_release_contract(self):
        release = (ROOT / '.github/workflows/release.yaml').read_text()
        workflow = (ROOT / '.github/workflows/launcher-appimage.yaml').read_text()
        self.assertIn('needs: goreleaser', release)
        self.assertIn('publish: true', release)
        self.assertIn('local-ai-launcher-linux.tar.xz', release)
        self.assertIn('dist/LocalAI.dmg', release)
        self.assertNotIn('go-version:', release)
        self.assertIn('go-version-file: go.mod', workflow)
        self.assertIn('ubuntu-22.04', workflow)
        self.assertIn("inputs.publish && github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')", workflow)
        self.assertIn('gh release view', workflow)
        self.assertIn('smoke.sh', workflow)

    def test_pinned_tools_and_no_library_injection(self):
        script = (PACK / 'build.sh').read_text()
        self.assertIn('sha256sum --check', script)
        self.assertIn('--appimage-extract', script)
        self.assertIn('--runtime-file', script)
        self.assertNotIn('/continuous/', script)
        self.assertNotIn('LD_LIBRARY_PATH=', (PACK / 'AppRun').read_text())


if __name__ == '__main__':
    unittest.main()
