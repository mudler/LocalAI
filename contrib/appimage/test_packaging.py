# SPDX-License-Identifier: MIT
import os
import re
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

    def test_publish_permission_isolation(self):
        workflow = (ROOT / '.github/workflows/launcher-appimage.yaml').read_text()
        header, jobs = workflow.split('jobs:\n', 1)
        self.assertIn('permissions:\n  contents: read', header)
        sections = re.split(r'^  ([a-z]+):\n', jobs, flags=re.MULTILINE)
        jobs = dict(zip(sections[1::2], sections[2::2]))
        self.assertEqual(set(jobs), {'appimage', 'publish'})
        build, publish = jobs['appimage'], jobs['publish']
        self.assertIn('    permissions:\n      contents: read', build)
        self.assertNotIn('contents: write', build)
        self.assertNotIn('GH_TOKEN', build)
        self.assertIn('persist-credentials: false', build)
        self.assertIn('actions/upload-artifact@', build)
        self.assertIn('    needs: appimage\n', publish)
        self.assertIn("    if: inputs.publish && github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')\n", publish)
        self.assertIn('    permissions:\n      contents: write', publish)
        self.assertIn('actions/download-artifact@', publish)
        self.assertIn('name: launcher-appimage-x86_64', build)
        self.assertIn('name: launcher-appimage-x86_64', publish)
        self.assertNotIn('checkout@', publish)
        self.assertNotIn('git fetch', publish)
        self.assertNotIn('gh release create', publish)
        self.assertLess(publish.index('gh release view'), publish.index('gh release upload'))
        # The only shell commands in the privileged job inspect/upload releases.
        commands = publish.split('        run: |\n', 1)[1].strip().splitlines()
        self.assertEqual(len(commands), 2)
        self.assertTrue(all(line.strip().startswith('gh release ') for line in commands))

    def test_smoke_startup_contract(self):
        # Run the actual smoke script with fake extraction and external tools.
        # No GUI, AppImage build, network or host dist directory is needed.
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            root = Path(tmp)
            pack = root / 'contrib/appimage'
            pack.mkdir(parents=True)
            smoke = pack / 'smoke.sh'
            smoke.write_bytes((PACK / 'smoke.sh').read_bytes())
            tools = root / 'tools'
            tools.mkdir()
            dist = root / 'dist'
            dist.mkdir()

            def executable(path, body):
                path.write_text('#!/bin/sh\nset -eu\n' + body)
                path.chmod(0o755)

            executable(dist / 'LocalAI-Launcher-x86_64.AppImage',
                       'test "$1" = --appimage-extract\n'
                       'mkdir -p squashfs-root/usr/bin\n'
                       'touch squashfs-root/local-ai-launcher.desktop squashfs-root/local-ai-launcher.png\n'
                       'touch squashfs-root/AppRun squashfs-root/usr/bin/local-ai-launcher\n'
                       'chmod +x squashfs-root/AppRun\n')
            executable(tools / 'desktop-file-validate', 'exit 0\n')
            executable(tools / 'ldd', 'echo "${SMOKE_LDD:-libc.so => /lib/libc.so}"\n')
            executable(tools / 'timeout',
                       'test "$1" = --kill-after=5s\n'
                       'test "$2" = 15s\n'
                       'test "$3" = xvfb-run\n'
                       'mkdir -p "$HOME/.localai/models" "$HOME/.localai/backends"\n'
                       'printf "%s\\n" "$SMOKE_LOG"\n'
                       'exit "$SMOKE_STATUS"\n')
            ready = 'No LocalAI installation found'
            cases = [
                ('startup', ready, 124, True),
                ('silent hang', '', 124, False),
                ('incomplete initialization', 'Initializing launcher...', 124, False),
                ('initialization failure', ready + '\nFailed to initialize launcher: bad config', 124, False),
                ('panic', ready + '\npanic: broken', 124, False),
                ('GLFW failure', ready + '\nfailed to initialize GLFW', 124, False),
                ('early success', ready, 0, False),
                ('early failure', ready, 1, False),
                ('forced kill', ready, 137, False),
                ('missing dependency', ready, 124, False),
            ]
            for name, log, status, success in cases:
                with self.subTest(name=name):
                    env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'],
                               SMOKE_LOG=log, SMOKE_STATUS=str(status))
                    if name == 'missing dependency':
                        env['SMOKE_LDD'] = 'libGL.so => not found'
                    result = subprocess.run(['bash', str(smoke)], env=env,
                                            capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)

    def test_pinned_tools_and_no_library_injection(self):
        script = (PACK / 'build.sh').read_text()
        self.assertIn('sha256sum --check', script)
        self.assertIn('--appimage-extract', script)
        self.assertIn('--runtime-file', script)
        self.assertNotIn('/continuous/', script)
        self.assertNotIn('LD_LIBRARY_PATH=', (PACK / 'AppRun').read_text())


if __name__ == '__main__':
    unittest.main()
