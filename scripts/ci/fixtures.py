#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Same-run production fixtures only. Tar retains executable bits and paths."""
import hashlib
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import tarfile
import time

PATHS = ['core/http/react-ui/dist', 'pkg/grpc/proto', 'pkg/motion/proto',
         'tests/e2e/mock-backend/mock-backend', 'tests/e2e-ui/ui-test-server']
BINARIES = PATHS[-2:]

def metadata(root):
    files = {}
    for name in PATHS:
        path = root / name
        assert path.exists(), f'missing fixture: {name}'
        for p in sorted(path.rglob('*')) if path.is_dir() else [path]:
            assert not p.is_symlink(), f'fixture symlink: {p}'
            if p.is_file():
                # tar's data filter removes group/other write bits.
                files[str(p.relative_to(root))] = [hashlib.sha256(p.read_bytes()).hexdigest(), p.stat().st_mode & 0o755]
    for name in BINARIES:
        assert os.access(root / name, os.X_OK), f'not executable: {name}'
    for name in ['core/http/react-ui/dist/index.html', 'pkg/grpc/proto/backend.pb.go',
                 'pkg/grpc/proto/backend_grpc.pb.go', 'pkg/motion/proto/motion.pb.go']:
        assert (root / name).stat().st_size > 0, f'missing generated input: {name}'
    return files

def toolchain_identity(name, version):
    # The producer and a consumer run on different runners that may hold
    # different patch releases of the same toolchain (go1.26.9 and go1.26.10 on
    # the same day). A patch release builds and runs the same fixtures, so the
    # identity keeps the series the build config accepts: Go minor, Node major.
    # protoc stays exact because generated code depends on it.
    if name == 'go':
        m = re.search(r'go(\d+\.\d+)(?:\.\d+)?', version)
        return 'go' + m.group(1) if m else version
    if name == 'node':
        m = re.match(r'v(\d+)\.', version)
        return 'v' + m.group(1) if m else version
    return version

def transfer(mode, root, archive, sha, tools=None):
    identity = {'sha': sha, 'config': 'ubuntu-24.04-amd64-go1.26-auth-production-v1', 'tools': tools or {}}
    if mode == 'pack':
        manifest = dict(identity, files=metadata(root))
        (root / 'addon-fixtures.json').write_text(json.dumps(manifest, sort_keys=True))
        with tarfile.open(archive, 'w:gz') as tar:
            for name in PATHS + ['addon-fixtures.json']:
                tar.add(root / name, arcname=name)
        assert archive.stat().st_size <= 512 * 1024 * 1024, 'fixture artifact exceeds 512 MiB'
    elif mode == 'unpack':
        with tarfile.open(archive) as tar:
            for member in tar.getmembers():
                name = member.name
                assert any(name == p or name.startswith(p + '/') for p in PATHS + ['addon-fixtures.json']), f'unexpected path: {name}'
                assert '..' not in Path(name).parts and not Path(name).is_absolute()
                assert member.isfile() or member.isdir(), f'unsupported member: {name}'
            tar.extractall(root, filter='data')
        manifest = json.loads((root / 'addon-fixtures.json').read_text())
        for key, value in identity.items():
            assert manifest[key] == value, f'fixture {key} mismatch'
        assert metadata(root) == manifest['files'], 'fixture checksum/mode mismatch'
        # The consumer checks out the same SHA later than the producer. Only
        # after identity and content verification, rebase generated mtimes onto
        # that checkout. Keep the server newer than its embedded dist, and leave
        # all source files untouched so a subsequent edit still fails -nt guards.
        stamp = time.time_ns()
        for name in manifest['files']:
            if name not in BINARIES: os.utime(root / name, ns=(stamp, stamp))
        for offset, name in enumerate(BINARIES, 1):
            os.utime(root / name, ns=(stamp + offset, stamp + offset))
    else:
        raise ValueError(mode)

if __name__ == '__main__':
    root = Path.cwd()
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    tools = {name: toolchain_identity(name, subprocess.check_output(command, text=True).strip()) for name, command in
             {'go': ['go', 'version'], 'node': ['node', '--version'], 'protoc': ['protoc', '--version']}.items()}
    transfer(sys.argv[1], root, Path(os.environ['RUNNER_TEMP']) / 'addon-fixtures.tgz', sha, tools)
