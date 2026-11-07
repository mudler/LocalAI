#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Same-run fixture integrity and source freshness, without product builds."""
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]

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

    def test_toolchain_identity_ignores_patch_releases(self):
        ident = fixtures.toolchain_identity
        self.assertEqual(ident('go', 'go version go1.26.9 linux/amd64'), ident('go', 'go version go1.26.10 linux/amd64'))
        self.assertNotEqual(ident('go', 'go version go1.26.9 linux/amd64'), ident('go', 'go version go1.27.0 linux/amd64'))
        self.assertEqual(ident('node', 'v22.12.0'), ident('node', 'v22.23.1'))
        self.assertNotEqual(ident('node', 'v22.12.0'), ident('node', 'v24.0.0'))
        self.assertNotEqual(ident('protoc', 'libprotoc 26.1'), ident('protoc', 'libprotoc 26.2'))

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
            manifest = src/'addon-fixtures.json'
            manifest.write_text(manifest.read_text().replace('production-v1', 'production-v2'))
            with tarfile.open(root/'config.tgz', 'w:gz') as tar:
                for name in fixtures.PATHS + ['addon-fixtures.json']:
                    tar.add(src/name, arcname=name)
            with self.assertRaisesRegex(AssertionError, 'config mismatch'):
                fixtures.transfer('unpack', dst, root/'config.tgz', 'abc', {'go': '1.26'})
            with tarfile.open(archive, 'w:gz') as tar:
                payload = b'escape'; member = tarfile.TarInfo('../escape'); member.size = len(payload)
                tar.addfile(member, io.BytesIO(payload))
            with self.assertRaises(AssertionError):
                fixtures.transfer('unpack', dst, archive, 'abc', {'go': '1.26'})

    def test_restore_rebases_freshness_only_after_identity_validation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); src = root/'src'; dst = root/'dst'
            src.mkdir(); dst.mkdir(); self.make(src)
            old = time.time_ns()-10_000_000_000
            for name in fixtures.metadata(src): os.utime(src/name, ns=(old, old))
            archive = root/'fixtures.tgz'; fixtures.transfer('pack', src, archive, 'abc')
            # A separate checkout occurs after the producer built its files.
            sources = ['core/http/react-ui/src/components/addons/RecoveryPanel.jsx',
                       'core/http/react-ui/src/components/addons/recoveryState.js',
                       'core/http/react-ui/src/utils/addonsApi.js',
                       'core/services/addons/recovery/backup.go',
                       'core/http/endpoints/addons/recovery.go', 'tests/e2e/mock-backend/main.go']
            for name in sources:
                target = dst/name; target.parent.mkdir(parents=True, exist_ok=True); target.write_text('source')
            fixtures.transfer('unpack', dst, archive, 'abc')
            # Execute the exact freshness assertions from recovery.sh 33-39.
            text = (ROOT/'scripts/addons/recovery.sh').read_text()
            start = text.index('[ "$root/tests/e2e-ui/ui-test-server" -nt "$ui/dist/index.html" ]')
            end = text.index('\n\n', start)
            guard = text[start:end]
            env = dict(os.environ, root=str(dst), ui=str(dst/'core/http/react-ui'))
            self.assertEqual(subprocess.run(['sh', '-c', guard], env=env, capture_output=True).returncode, 0)
            source = dst/sources[3]
            later = (dst/'tests/e2e-ui/ui-test-server').stat().st_mtime_ns+1_000_000
            source.write_text('new edit'); os.utime(source, ns=(later, later))
            self.assertNotEqual(subprocess.run(['sh', '-c', guard], env=env, capture_output=True).returncode, 0)
            # Failed identity checks must not mark a stale binary fresh.
            with self.assertRaisesRegex(AssertionError, 'sha mismatch'):
                fixtures.transfer('unpack', dst, archive, 'different')
            self.assertLess((dst/'tests/e2e-ui/ui-test-server').stat().st_mtime_ns, source.stat().st_mtime_ns)

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
