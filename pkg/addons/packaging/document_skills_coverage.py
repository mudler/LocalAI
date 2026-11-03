#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Deterministic one-time consolidation and offline migration fingerprint check.

Generate before removing source trees. Check uses only committed fingerprints,
not Git/network or executable source copies. Each original Markdown page is a
bounded source block, including comments, examples and all headings. Only local
Markdown link destinations and the documented guest helper paths change.
"""
import argparse
import hashlib
import json
import pathlib
import posixpath
import re
import tempfile
import shutil

FORMATS = ('docx', 'pptx', 'pdf', 'xlsx')
LINK = re.compile(r'\]\(([^)\n]+\.md(?:#[^)\n]*)?)\)')
MARK = re.compile(r'<a id="(source-[a-f0-9]{16})"></a>\n<!-- source: ([^\n]+) -->\n')
GUEST = '/usr/local/lib/localai/document-skills/pdf/'


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def normalize(text):
    text = LINK.sub('](LOCAL-MARKDOWN-TARGET)', text)
    text = text.replace(GUEST, 'scripts/')
    text = text.replace('The guest directory `/usr/local/lib/localai/document-skills/pdf/` contains', 'The `scripts/` directory contains')
    text = text.replace('The guest directory `scripts/` contains', 'The `scripts/` directory contains')
    text = text.replace('python3 scripts/', 'python scripts/')
    return text


def blocks(text):
    # Heading-delimited blocks retain every byte except blank edge lines.
    return [digest(b.strip()) for b in re.split(r'(?m)(?=^#{1,6} )', text) if b.strip()]


def anchor(path):
    return 'source-' + digest(path)[:16]


def rewrite(text, source, mapping):
    def link(m):
        target, _, fragment = m[1].partition('#')
        if ':' in target or target.startswith('/'):
            return m[0]
        resolved = posixpath.normpath(posixpath.join(posixpath.dirname(source), target))
        if resolved not in mapping:
            raise ValueError(f'{source}: unknown link {m[1]}')
        dest = mapping[resolved]
        # Original input has no Markdown fragments. Fail closed if that changes.
        if fragment:
            raise ValueError(f'{source}: unexpected original fragment {m[1]}')
        return '](' + posixpath.relpath(dest['file'], posixpath.dirname(mapping[source]['file'])) + '#' + dest['anchor'] + ')'
    text = LINK.sub(link, text)
    if source.startswith('pdf/'):
        text = text.replace('python scripts/', 'python3 scripts/')
        text = text.replace('scripts/content_qa.py', GUEST + 'content_qa.py').replace('scripts/verify_pdf.py', GUEST + 'verify_pdf.py')
        text = text.replace('The `scripts/` directory contains', 'The guest directory `/usr/local/lib/localai/document-skills/pdf/` contains')
    return text


def generate(args):
    source = pathlib.Path(args.source)
    dest = pathlib.Path(args.skills)
    mapping, pages = {}, []
    for skill in FORMATS:
        chunk, size = 1, 0
        for f in sorted((source / skill).rglob('*.md')):
            p = f.relative_to(source).as_posix()
            text = f.read_text()
            if '/references/api/' in p:
                if size + len(text.encode()) + 2048 > 200000:
                    chunk, size = chunk + 1, 0
                out = f'{skill}/references/api-{chunk:03}.md'
                size += len(text.encode()) + 2048
                a = anchor(p)
            else:
                out, a = p, anchor(p)
            mapping[p] = {'file': out, 'anchor': a}
            pages.append((p, text))
    proof = {'schema': 1, 'counts': {}, 'pages': []}
    outputs = {}
    for p, text in pages:
        info = mapping[p]
        changed = rewrite(text, p, mapping).rstrip("\n") + "\n"
        assert blocks(normalize(text)) == blocks(normalize(changed)), p
        record = dict(source=p, source_sha256=digest(text), source_blocks=blocks(normalize(text)),
                      output_sha256=digest(changed), **info)
        proof['pages'].append(record)
        proof['counts'][p.split('/')[0]] = proof['counts'].get(p.split('/')[0], 0) + 1
        # SKILL frontmatter must stay first. Other pages have explicit stable anchors.
        if p.endswith('/SKILL.md'):
            outputs[info['file']] = changed
        else:
            outputs[info['file']] = outputs.get(info['file'], '') + f'<a id="{info["anchor"]}"></a>\n<!-- source: {p} -->\n' + changed + '\n'
    for p, text in outputs.items():
        out = dest / p
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(text.rstrip("\n") + "\n")
    pathlib.Path(args.proof).write_text(json.dumps(proof, indent=2) + '\n')
    check(args)
    print('source comparison: all original bytes fingerprinted; only declared link/helper rewrites')


def check(args):
    root = pathlib.Path(args.skills)
    proof = json.loads(pathlib.Path(args.proof).read_text())
    assert proof['schema'] == 1
    # Inventory cardinalities and source digest set are themselves pinned in Go.
    assert set(proof['counts']) == set(FORMATS)
    pages = proof['pages']
    assert len({p['source'] for p in pages}) == len(pages)
    expected_files = {p['file'] for p in pages}
    assert {p.relative_to(root).as_posix() for p in root.rglob('*.md')} == expected_files
    counts, extracted = {}, {}
    for filename in sorted(expected_files):
        text = (root / filename).read_text()
        if filename.endswith('/SKILL.md'):
            extracted[filename] = text
        else:
            markers = list(MARK.finditer(text))
            assert markers and markers[0].start() == 0, filename
            for i, m in enumerate(markers):
                assert m[2] not in extracted, m[2]
                body = text[m.end():markers[i+1].start() if i+1 < len(markers) else len(text)]
                assert body.endswith('\n')
                extracted[m[2]] = body.rstrip("\n") + "\n"
                assert m[1] == anchor(m[2])
        for m in LINK.finditer(text):
            target, _, frag = m[1].partition('#')
            if ':' in target or target.startswith('/'):
                continue
            dest = root / posixpath.normpath(posixpath.join(posixpath.dirname(filename), target))
            assert dest.is_file(), (filename, target)
            if frag:
                assert f'<a id="{frag}"></a>' in dest.read_text(), (filename, m[1])
    assert set(extracted) == {p['source'] for p in pages}
    for p in pages:
        text = extracted[p['source']]
        assert digest(text) == p['output_sha256'], p['source']
        assert blocks(normalize(text)) == p['source_blocks'], p['source']
        assert re.fullmatch('[a-f0-9]{64}', p['source_sha256'])
        # Rewrites may only affect links/guest helper references, not block count.
        assert p['source_blocks'], p['source']
        skill = p['source'].split('/')[0]
        counts[skill] = counts.get(skill, 0) + 1
    assert counts == proof['counts']
    print('verified source pages:', counts, 'files:', len(expected_files))
    if args.self_test:
        with tempfile.TemporaryDirectory() as tmp:
            copy = pathlib.Path(tmp) / 'skills'
            shutil.copytree(root, copy)
            original_root = args.skills
            args.skills, args.self_test = str(copy), False
            file = copy / pages[0]['file']
            original = file.read_text()
            for bad in (original.replace('\n', '\nOMITTED BLOCK\n', 1), original + '\n[broken](missing.md)\n'):
                file.write_text(bad)
                try:
                    check(args)
                except (AssertionError, ValueError):
                    pass
                else:
                    raise AssertionError('negative coverage/link check unexpectedly passed')
            args.skills = original_root
        print('negative coverage and broken-link fixtures rejected')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('generate', 'check'))
    parser.add_argument('--source')
    parser.add_argument('--skills', required=True)
    parser.add_argument('--proof', required=True)
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    (generate if args.command == 'generate' else check)(args)


if __name__ == '__main__':
    main()
