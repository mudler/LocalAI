#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Apply the reviewed patch only to its exact, otherwise unchanged source tree."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys

PIN = 'ad14f0c4dbd5b12b7711bb7b46abb8d94f78f067'
MANIFEST_SHA256 = '759ab67bc215af21b6cf62c1c9361f899613457db20f0b6505382c5689996422'
PATHS = frozenset(f'{root}/{name}' for root, names in (
    ('include/engine/framework/core', ('backend.h', 'execution_context.h')),
    ('src/framework/core', ('backend.cpp', 'execution_context.cpp')),
    ('include/engine/framework/runtime', ('graph_executor.h',)),
    ('src/framework/runtime', ('graph_executor.cpp',)),
) for name in names)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(source, *args):
    result = subprocess.run(['git', '-C', str(source), *args], capture_output=True)
    if result.returncode:
        raise ValueError(result.stderr.decode(errors='replace').strip())
    return result.stdout


def prepare(source, manifest_path, patch_path, manifest_digest, pin, allowed, verify_only=False):
    source = Path(source).resolve(strict=True)
    manifest_bytes = Path(manifest_path).read_bytes()
    if digest(manifest_bytes) != manifest_digest:
        raise ValueError('manifest digest mismatch')
    manifest = json.loads(manifest_bytes)
    patch = Path(patch_path).read_bytes()
    if manifest['base_commit'] != pin or digest(patch) != manifest['patch_sha256']:
        raise ValueError('patch digest or pin mismatch')
    files = manifest['files']
    if set(files) != set(allowed):
        raise ValueError('unexpected patch paths')
    if git(source, 'rev-parse', '--show-toplevel').decode().strip() != str(source):
        raise ValueError('source must be repository root')
    if git(source, 'rev-parse', 'HEAD').decode().strip() != pin:
        raise ValueError('wrong source HEAD')
    # Sparse checkouts may omit tracked assets; never trust index flags for
    # files that are present. Compare every present blob, including hidden dirt.
    entries = git(source, 'ls-files', '--stage', '-z').split(b'\0')
    flags = {e[2:].decode(): e[:1] for e in git(source, 'ls-files', '-v', '-z').split(b'\0') if e}
    for entry in entries:
        if not entry:
            continue
        meta, raw_name = entry.split(b'\t', 1)
        mode, blob, stage = meta.split()
        name = raw_name.decode()
        path = source / name
        if mode == b'160000':
            if path.is_dir() and not any(path.iterdir()):
                continue  # Uninitialized optional submodule, no acquired content.
            if git(path, 'rev-parse', 'HEAD').strip() != blob or git(path, 'status', '--porcelain'):
                raise ValueError('modified submodule: ' + name)
            continue
        if stage != b'0':
            raise ValueError('unmerged index')
        if not path.exists() and not path.is_symlink():
            if (flags[name] == b'S' and name not in allowed and
                    manifest.get('omitted_tracked_paths', {}).get(name) == blob.decode()):
                continue
            raise ValueError('missing tracked file: ' + name)
        if name not in allowed:
            if path.is_symlink() != (mode == b'120000'):
                raise ValueError('tracked file type differs: ' + name)
            if mode in (b'100644', b'100755') and bool(path.stat().st_mode & 0o111) != (mode == b'100755'):
                raise ValueError('tracked executable mode differs: ' + name)
            # Respect the pinned .gitattributes clean conversion (e.g. Windows
            # CRLF scripts), not a suffix heuristic or diff's index stat cache.
            if path.is_symlink():
                data = str(path.readlink()).encode()
                actual = hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest().encode()
            else:
                actual = git(source, 'hash-object', '--path=' + name, '--', name).strip()
            if actual != blob:
                raise ValueError('unrelated tracked blob differs: ' + name)
    if any(flag != b'H' for name, flag in flags.items() if name in allowed):
        raise ValueError('patch paths must not have index flags')
    if git(source, 'diff', '--cached', '--name-only'):
        raise ValueError('staged modifications are not allowed')
    # Include ignored files: an ignored collision is still a collision.
    untracked = git(source, 'ls-files', '--others', '-z').split(b'\0')
    if any(untracked):
        raise ValueError('untracked files are not allowed in prepared source')
    changed = set(git(source, 'diff', 'HEAD', '--name-only', '-z').decode().strip('\0').split('\0')) - {''}
    if not changed.issubset(allowed):
        raise ValueError('unrelated tracked modifications')
    states = []
    for name, hashes in files.items():
        path = source / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('expected regular tracked file: ' + name)
        if digest(git(source, 'show', 'HEAD:' + name)) != hashes['base_sha256']:
            raise ValueError('base digest mismatch: ' + name)
        current = digest(path.read_bytes())
        states.append('base' if current == hashes['base_sha256'] else
                      'final' if current == hashes['final_sha256'] else 'invalid')
    if all(state == 'final' for state in states):
        if git(source, 'diff', '--binary', 'HEAD', '--') != patch:
            raise ValueError('final diff differs from reviewed patch')
        return 'verified'
    if not all(state == 'base' for state in states) or changed:
        raise ValueError('mixed or modified patch state')
    if verify_only:
        raise ValueError('source is pristine; run make prepare-upstream first')
    git(source, 'apply', '--check', str(Path(patch_path).resolve()))
    git(source, 'apply', str(Path(patch_path).resolve()))
    return prepare(source, manifest_path, patch_path, manifest_digest, pin, allowed, True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    directory = Path(__file__).resolve().parent / 'patches'
    try:
        state = prepare(args.source, directory / 'core-run-cancellation.manifest.json',
                        directory / '0001-core-run-cancellation.patch',
                        MANIFEST_SHA256, PIN, PATHS, args.verify_only)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print('audio.cpp patch verification failed: ' + str(error), file=sys.stderr)
        return 1
    print('audio.cpp core patch: ' + state)
    return 0


if __name__ == '__main__':
    sys.exit(main())
