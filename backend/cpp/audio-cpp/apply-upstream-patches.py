#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Deliver a reviewed ordered chain to an exact pinned, otherwise clean tree.

prepare() accepts an authenticated manifest, patch directory (or the legacy
single-patch filename), pin and path union. It returns 'verified' only at the
final boundary. Preparation upgrades pristine or exact whole-chain prefix
boundaries; --verify-only accepts final only and never writes source. Index
always remains pinned. Concurrent source writers are unsupported: stop builds
and editors before preparation. All chain steps are preflighted in isolation;
a single non-index git apply delivers the remaining delta without reset/clean.
Failures never report success or create a success marker.
"""
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
MANIFEST_SHA256 = '8da1c4ed7a9b6d975b00bbc984c08509e042cac854ad9b881ce29a976e92b54b'
PATHS = frozenset(f'{root}/{name}' for root, names in (
    ('include/engine/framework/core', ('backend.h', 'execution_context.h')),
    ('src/framework/core', ('backend.cpp', 'execution_context.cpp')),
    ('include/engine/framework/runtime', ('graph_executor.h', 'session.h')),
    ('include/engine/models/sheetsage', ('session.h', 'audio_frontend.h', 'processing.h')),
    ('src/models/sheetsage', ('session.cpp', 'runtime.cpp', 'audio_frontend.cpp', 'processing.cpp')),
    ('src/framework/runtime', ('graph_executor.cpp',)),
    ('include/engine/models/yue2', ('session.h',)),
    ('src/models/yue2', ('session.cpp', 'pipeline.cpp', 'ar_runtime.cpp', 'nar_runtime.cpp')),
    ('src/framework/modules/transformers', ('causal_decoder_runtime.cpp',)),
    ('include/engine/framework/audio', ('fft.h', 'dsp.h')),
    ('src/framework/audio', ('fft.cpp', 'dsp.cpp')),
    ('src/framework/audio/detail', ('speech_fft_internal.h',)),
    ('src/framework/codecs', ('oobleck_audio_vae_runtime.cpp',)),
) for name in names)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(source, *args, isolated_index=False):
    """Never let source config, info attributes, hooks, or inherited Git env execute.

    A disposable metadata view exposes only objects, refs and an index.
    Verification copies the original index bytes; refreshes stay disposable.
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
            index = metadata / 'index'
            if isolated_index:
                private_index = view / 'index'
                if index.exists():
                    shutil.copyfile(index, private_index)
                index = private_index
            env['GIT_INDEX_FILE'] = str(index)
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
    def source_git(*args):
        return git(source, *args, isolated_index=True)
    manifest_bytes = Path(manifest_path).read_bytes()
    if digest(manifest_bytes) != manifest_digest:
        raise ValueError('manifest digest mismatch')
    manifest = json.loads(manifest_bytes)
    if manifest['base_commit'] != pin:
        raise ValueError('manifest pin mismatch')
    files = manifest['files']
    if not files or set(files) != set(allowed):
        raise ValueError('unexpected patch paths')
    for name in files:
        if (Path(name).is_absolute() or any(p in ('', '.', '..', '.git') for p in name.split('/'))):
            raise ValueError('invalid patch path')
    # Legacy manifests remain supported for the original security fixtures.
    steps = manifest.get('patches')
    if steps is None:
        steps = [{'patch': Path(patch_path).name,
                  'patch_sha256': manifest['patch_sha256'], 'files': files}]
    if not isinstance(steps, list) or not 1 <= len(steps) <= 32:
        raise ValueError('expected bounded nonempty ordered patch list')
    directory = Path(patch_path)
    if not directory.is_dir():
        directory = directory.parent
    patches = []
    seen = set()
    union = set()
    for step in steps:
        name = step['patch']
        if not isinstance(name, str) or Path(name).name != name or name in ('', '.', '..') or name in seen:
            raise ValueError('invalid or duplicate patch filename')
        seen.add(name)
        if not step['files'] or not set(step['files']).issubset(files):
            raise ValueError('unexpected step paths')
        union.update(step['files'])
        data = (directory / name).read_bytes()
        if digest(data) != step['patch_sha256']:
            raise ValueError('patch digest mismatch: ' + name)
        patches.append(data)
    if union != set(files):
        raise ValueError('step path union mismatch')
    if source_git('rev-parse', '--show-toplevel').decode().strip() != str(source):
        raise ValueError('source must be repository root')
    if source_git('rev-parse', 'HEAD').decode().strip() != pin:
        raise ValueError('wrong source HEAD')
    # Sparse checkouts may omit tracked assets; never trust index flags for
    # files that are present. Compare every present blob, including hidden dirt.
    entries = source_git('ls-files', '--stage', '-z').split(b'\0')
    pinned_entries = source_git('ls-tree', '-r', '-z', pin).split(b'\0')
    pinned_index = sorted(e.replace(b' blob ', b' ').replace(b' commit ', b' ').replace(b'\t', b' 0\t', 1) for e in pinned_entries if e)
    if sorted(e for e in entries if e) != pinned_index:
        raise ValueError('index differs from pinned tree')
    flags = {e[2:].decode(): e[:1] for e in source_git('ls-files', '-v', '-z').split(b'\0') if e}
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
                attributes = source_git('ls-tree', '-r', '--name-only', pin).splitlines()
                attribute_paths = [n.decode() for n in attributes if n == b'.gitattributes' or n.endswith(b'/.gitattributes')]
                crlf = False
                for attribute in attribute_paths:
                    parent = str(Path(attribute).parent)
                    if parent != '.' and not name.startswith(parent + '/'):
                        continue
                    for line in source_git('show', pin + ':' + attribute).decode().splitlines():
                        # Minimal pinned policy: no macros, filters or heuristics.
                        if line.split() in [['*.bat', 'text', 'eol=crlf'], ['*.cmd', 'text', 'eol=crlf']]:
                            crlf = crlf or name.endswith(line.split()[0][1:])
                if crlf:
                    actual = blob_hash(data.replace(b'\r\n', b'\n'))
            if actual != blob:
                raise ValueError('unrelated tracked blob differs: ' + name)
    if any(flag != b'H' for name, flag in flags.items() if name in allowed):
        raise ValueError('patch paths must not have index flags')
    if source_git('diff', '--no-ext-diff', '--no-textconv', '--cached', '--name-only'):
        raise ValueError('staged modifications are not allowed')
    # Include ignored files: an ignored collision is still a collision.
    untracked = source_git('ls-files', '--others', '-z').split(b'\0')
    if any(untracked):
        raise ValueError('untracked files are not allowed in prepared source')
    changed = set(source_git('diff', '--no-ext-diff', '--no-textconv', 'HEAD', '--name-only', '-z').decode().strip('\0').split('\0')) - {''}
    if not changed.issubset(allowed):
        raise ValueError('unrelated tracked modifications')
    current = {}
    base = {}
    for name, hashes in files.items():
        path = source / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('expected regular tracked file: ' + name)
        base[name] = source_git('show', 'HEAD:' + name)
        if digest(base[name]) != hashes['base_sha256']:
            raise ValueError('base digest mismatch: ' + name)
        current[name] = path.read_bytes()

    # Only the small reviewed path union is materialized. No source attributes,
    # metadata, config, symlinks or optional large assets enter the preflight.
    with tempfile.TemporaryDirectory(prefix='audio-chain-') as temporary:
        sandbox = Path(temporary) / 'tree'
        sandbox.mkdir()
        git(sandbox, 'init', '-q')
        modes = {e.split(b'\t', 1)[1].decode(): e.split()[0] for e in entries if e}
        for name, data in base.items():
            if modes[name] not in (b'100644', b'100755'):
                raise ValueError('patch paths must be regular tracked files')
            path = sandbox / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
            path.chmod(0o755 if modes[name] == b'100755' else 0o644)
        git(sandbox, 'add', '.')
        boundaries = [base.copy()]
        diffs = [b'']
        for number, (step, data) in enumerate(zip(steps, patches)):
            previous = boundaries[-1]
            for name, hashes in step['files'].items():
                if digest(previous[name]) != hashes['base_sha256']:
                    raise ValueError('step base digest mismatch: ' + name)
            patch_file = Path(temporary) / ('step-' + str(number) + '.patch')
            patch_file.write_bytes(data)
            git(sandbox, 'apply', '--check', str(patch_file))
            git(sandbox, 'apply', str(patch_file))
            if any(git(sandbox, 'ls-files', '--others', '-z')):
                raise ValueError('patch introduces unreviewed paths')
            names = set(git(sandbox, 'diff', '--no-ext-diff', '--no-textconv', '--name-only', '-z').decode().strip('\0').split('\0')) - {''}
            if not names.issubset(files):
                raise ValueError('patch changes unreviewed paths')
            boundary = {}
            actual_step = set()
            for name in files:
                path = sandbox / name
                if (path.is_symlink() or not path.is_file() or
                        bool(path.stat().st_mode & 0o111) != (modes[name] == b'100755')):
                    raise ValueError('patch changes file type or mode: ' + name)
                boundary[name] = path.read_bytes()
                if boundary[name] != previous[name]:
                    actual_step.add(name)
                expected = step['files'].get(name)
                if expected and digest(boundary[name]) != expected['final_sha256']:
                    raise ValueError('step final digest mismatch: ' + name)
            if actual_step != set(step['files']):
                raise ValueError('step changed paths mismatch')
            boundaries.append(boundary)
            diffs.append(git(sandbox, 'diff', '--no-ext-diff', '--no-textconv', '--binary', '--'))
        if any(digest(boundaries[-1][n]) != h['final_sha256'] for n, h in files.items()):
            raise ValueError('final boundary digest mismatch')
        matches = [i for i, boundary in enumerate(boundaries) if current == boundary]
        if not matches:
            raise ValueError('mixed or modified patch state')
        boundary = matches[-1]
        if source_git('diff', '--no-ext-diff', '--no-textconv', '--binary', 'HEAD', '--') != diffs[boundary]:
            raise ValueError('boundary diff differs from reviewed chain')
        if boundary == len(steps):
            return 'verified'
        if verify_only:
            raise ValueError('source is not final (prepared prefix ' + str(boundary) +
                             '/' + str(len(steps)) + '); run make prepare-upstream first')
        # Build one remaining delta, so a later invalid step cannot leave an
        # earlier step applied. Never stage, reset or clean the caller's tree.
        for name, data in current.items():
            (sandbox / name).write_bytes(data)
        git(sandbox, 'add', '.')
        for name, data in boundaries[-1].items():
            (sandbox / name).write_bytes(data)
        remaining = Path(temporary) / 'remaining.patch'
        remaining.write_bytes(git(sandbox, 'diff', '--no-ext-diff', '--no-textconv', '--binary', '--'))
        source_git('apply', '--check', str(remaining))
        source_git('apply', str(remaining))
    return prepare(source, manifest_path, patch_path, manifest_digest, pin, allowed, True)



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    directory = Path(__file__).resolve().parent / 'patches'
    try:
        state = prepare(args.source, directory / 'core-run-cancellation.manifest.json',
                        directory,
                        MANIFEST_SHA256, PIN, PATHS, args.verify_only)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print('audio.cpp patch verification failed (invalid manifest, source state or chain); run make prepare-upstream on a clean pinned checkout', file=sys.stderr)
        return 1
    print('audio.cpp core patch: ' + state)
    return 0


if __name__ == '__main__':
    sys.exit(main())
