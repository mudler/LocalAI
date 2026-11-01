#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Apply the reviewed patch only to its exact, otherwise unchanged source tree."""
import argparse
import hashlib
import json
import os
import shutil
import tempfile
from pathlib import Path
import subprocess
import sys

PIN = 'ad14f0c4dbd5b12b7711bb7b46abb8d94f78f067'
MANIFEST_SHA256 = '5232e1926600cb4e0b3305f0aa29c63fdb53586157136276ae8ded7ec7c218da'
PATHS = frozenset(f'{root}/{name}' for root, names in (
    ('include/engine/framework/core', ('backend.h', 'execution_context.h')),
    ('src/framework/core', ('backend.cpp', 'execution_context.cpp')),
    ('include/engine/framework/runtime', ('graph_executor.h',)),
    ('src/framework/runtime', ('graph_executor.cpp',)),
) for name in names)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(source, *args):
    """Never let source config, info attributes, hooks, or inherited Git env execute.

    A disposable metadata view exposes only objects, refs and the real index.
    No config (including worktree config), info/, hooks or replace refs is used.
    Worktree attributes cannot invoke drivers: hashing is done as raw bytes below
    and every diff explicitly disables external diff and textconv.
    """
    source = Path(source).resolve()
    env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
    env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
               GIT_CONFIG_SYSTEM=os.devnull, GIT_ATTR_NOSYSTEM='1',
               GIT_TERMINAL_PROMPT='0', GIT_NO_REPLACE_OBJECTS='1',
               GIT_PAGER='cat', GIT_NO_LAZY_FETCH='1')
    with tempfile.TemporaryDirectory(prefix='audio-verify-git-') as temporary:
        metadata = source / '.git'
        if metadata.is_file():
            text = metadata.read_text().strip()
            if not text.startswith('gitdir: '):
                raise ValueError('invalid gitdir')
            metadata = (source / text[8:]).resolve(strict=True)
        if not metadata.is_dir() and args != ('init', '-q'):
            raise ValueError('source must have its own Git metadata')
        command = ['git', '--no-pager']
        if metadata.is_dir():
            common = metadata
            if (metadata / 'commondir').exists():
                common = (metadata / (metadata / 'commondir').read_text().strip()).resolve(strict=True)
            view = Path(temporary)
            for name in ('objects', 'refs'):
                (view / name).symlink_to(common / name, target_is_directory=True)
            for name in ('HEAD',):
                shutil.copyfile(metadata / name, view / name)
            for name in ('packed-refs', 'shallow'):
                if (common / name).exists(): shutil.copyfile(common / name, view / name)
            env['GIT_INDEX_FILE'] = str(metadata / 'index')
            command += ['--git-dir=' + str(view), '--work-tree=' + str(source)]
        command += ['-c', 'core.hooksPath=/dev/null', '-c', 'core.fsmonitor=false',
                    '-c', 'core.attributesFile=/dev/null', '-c', 'core.autocrlf=false',
                    '-c', 'core.safecrlf=false', '-C', str(source), *args]
        result = subprocess.run(command, env=env, capture_output=True)
    if result.returncode:
        raise ValueError(result.stderr.decode(errors='replace').strip())
    return result.stdout


def blob_hash(data):
    return hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest().encode()


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
    pinned_entries = git(source, 'ls-tree', '-r', '-z', pin).split(b'\0')
    pinned_index = sorted(e.replace(b' blob ', b' ').replace(b' commit ', b' ').replace(b'\t', b' 0\t', 1) for e in pinned_entries if e)
    if sorted(e for e in entries if e) != pinned_index:
        raise ValueError('index differs from pinned tree')
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
            raise ValueError('initialized submodules are not accepted: ' + name)
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
            data = str(path.readlink()).encode() if path.is_symlink() else path.read_bytes()
            # Only the pinned attributes may authorize CRLF normalization; no
            # attributes interpreter or external clean driver is ever called.
            actual = blob_hash(data)
            if actual != blob and not path.is_symlink():
                attributes = git(source, 'ls-tree', '-r', '--name-only', pin).splitlines()
                attribute_paths = [n.decode() for n in attributes if n == b'.gitattributes' or n.endswith(b'/.gitattributes')]
                crlf = False
                for attribute in attribute_paths:
                    parent = str(Path(attribute).parent)
                    if parent != '.' and not name.startswith(parent + '/'):
                        continue
                    for line in git(source, 'show', pin + ':' + attribute).decode().splitlines():
                        # Minimal pinned policy: no macros, filters or heuristics.
                        if line.split() in [['*.bat', 'text', 'eol=crlf'], ['*.cmd', 'text', 'eol=crlf']]:
                            crlf = crlf or name.endswith(line.split()[0][1:])
                if crlf:
                    actual = blob_hash(data.replace(b'\r\n', b'\n'))
            if actual != blob:
                raise ValueError('unrelated tracked blob differs: ' + name)
    if any(flag != b'H' for name, flag in flags.items() if name in allowed):
        raise ValueError('patch paths must not have index flags')
    if git(source, 'diff', '--no-ext-diff', '--no-textconv', '--cached', '--name-only'):
        raise ValueError('staged modifications are not allowed')
    # Include ignored files: an ignored collision is still a collision.
    untracked = git(source, 'ls-files', '--others', '-z').split(b'\0')
    if any(untracked):
        raise ValueError('untracked files are not allowed in prepared source')
    changed = set(git(source, 'diff', '--no-ext-diff', '--no-textconv', 'HEAD', '--name-only', '-z').decode().strip('\0').split('\0')) - {''}
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
        if git(source, 'diff', '--no-ext-diff', '--no-textconv', '--binary', 'HEAD', '--') != patch:
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
