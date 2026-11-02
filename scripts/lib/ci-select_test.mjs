// SPDX-License-Identifier: MIT
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { classify, domains, addonIDs, diffSpec } from './ci-select.mjs';
import { selectEvent } from '../ci/select.mjs';
const all = r => assert.ok(domains.every(d => r.selected[d]));
test('known path fixtures explain selections', () => {
  for (const [path, yes, no] of [
    ['README.md', [], domains], ['website/content/guide.md', [], domains],
    ['gallery/foo.yaml', ['core'], ['ui', 'packages']],
    ['gallery/addons/index.yaml', ['core', 'addons', 'packages'], ['build']],
    ['backend/python/foo/run.py', ['backend', 'cpp', 'core', 'build'], ['ui', 'packages']],
    ['core/http/react-ui/src/main.jsx', ['core', 'ui', 'addons', 'build', 'packages'], []],
    ['addons/voice/main.go', ['core', 'ui', 'addons', 'packages'], []],
  ]) {
    const r = classify([path]);
    for (const d of yes) { assert.equal(r.selected[d], true, path + d); assert.ok(r.reasons[d].some(x => x.includes(path))); }
    for (const d of no) assert.equal(r.selected[d], false, path + d);
  }
});
test('arbitrary shared Go imports and build changes run broadly', () => {
  for (const p of ['pkg/foo/x.go', 'core/services/chats/foo.go', 'go.mod', 'go.sum', 'Makefile', 'backend/backend.proto', 'new-input', 'scripts/build/x.sh', 'addons/internal/x.go', '.github/workflows/test.yml', 'schema.json', 'examples/fixture.go', 'backend/go/cloud-proxy/main.go']) all(classify([p]));
});
test('per-addon legs, deleted addon and cross-domain rename union', () => {
  assert.deepEqual(classify(['addons/voice/deleted.go']).addons, ['voice']);
  assert.deepEqual(classify(['addons/voice/old', 'addons/face/new']).addons, ['voice', 'face']);
  assert.deepEqual(classify(['core/http/react-ui/package-lock.json']).addons, addonIDs);
  all(classify(['docs/new.md', 'pkg/old.go']));
});
const base = 'a'.repeat(40), head = 'b'.repeat(40);
test('PR uses real head/base merge-base; master uses before/after', () => {
  const calls = [];
  const git = args => { calls.push(args); return args[0] === 'merge-base' ? 'c'.repeat(40) : args[0] === 'diff' ? 'gallery/model.yaml\0' : ''; };
  const r = selectEvent('pull_request', { pull_request: { base: { sha: base }, head: { sha: head } } }, 'refs/pull/1/merge', git);
  assert.equal(r.selected.core, true);
  assert.deepEqual(calls[2], ['merge-base', base, head]);
  assert.ok(calls[3].includes('--no-renames'));
  calls.length = 0;
  selectEvent('push', { before: base, after: head }, 'refs/heads/master', git);
  assert.equal(calls[2][0], 'diff'); assert.ok(calls[2].includes(base)); assert.ok(calls[2].includes(head));
});
test('zero/bad SHAs, missing objects, empty diff and full events are conservative', () => {
  for (const before of ['', '0'.repeat(40), 'bad']) all(selectEvent('push', { before, after: head }, 'refs/heads/master'));
  all(selectEvent('push', { before: base, after: head }, 'refs/heads/master', () => { throw Error('missing object'); }));
  all(selectEvent('push', { before: base, after: head }, 'refs/heads/master', () => ''));
  for (const event of ['workflow_dispatch', 'schedule', 'release']) all(selectEvent(event, {}, 'refs/heads/master'));
  assert.ok(diffSpec('push', {}, 'refs/tags/v1.0.0').full);
});
test('real git deletion and rename paths retain old/new domains', async () => {
  const { mkdtempSync, mkdirSync, writeFileSync, rmSync, renameSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { execFileSync } = await import('node:child_process');
  const dir = mkdtempSync(join(tmpdir(), 'ci-selection-'));
  const git = args => execFileSync('git', args, { cwd: dir }).toString();
  try {
    git(['init', '-q']); git(['config', 'user.name', 'CI test']); git(['config', 'user.email', 'ci@example.invalid']);
    mkdirSync(join(dir, 'addons/voice'), { recursive: true });
    writeFileSync(join(dir, 'addons/voice/old'), 'old\n');
    git(['add', '.']); git(['commit', '-qm', 'base']); const before = git(['rev-parse', 'HEAD']).trim();
    mkdirSync(join(dir, 'addons/face'), { recursive: true });
    renameSync(join(dir, 'addons/voice/old'), join(dir, 'addons/face/new'));
    git(['add', '-A']); git(['commit', '-qm', 'rename']); const after = git(['rev-parse', 'HEAD']).trim();
    assert.deepEqual(selectEvent('push', { before, after }, 'refs/heads/master', git).addons, ['voice', 'face']);
    rmSync(join(dir, 'addons/face/new')); git(['add', '-A']); git(['commit', '-qm', 'delete']);
    assert.deepEqual(selectEvent('push', { before: after, after: git(['rev-parse', 'HEAD']).trim() }, 'refs/heads/master', git).addons, ['face']);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
test('malformed event JSON is a hard CLI failure, never a green omission', async () => {
  const { mkdtempSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { spawnSync } = await import('node:child_process');
  const dir = mkdtempSync(join(tmpdir(), 'ci-bad-event-'));
  try {
    const event = join(dir, 'event.json'); writeFileSync(event, '{bad');
    const result = spawnSync(process.execPath, [new URL('../ci/select.mjs', import.meta.url).pathname], { env: { ...process.env, GITHUB_EVENT_PATH: event } });
    assert.notEqual(result.status, 0);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('documentation is a test contract, including deleted and unfamiliar docs', () => {
  for (const path of ['docs/content/features/addons.md', 'docs/content/features/tables.md', 'docs/content/features/image-generation.md', 'docs/content/getting-started/install.md', 'docs/content/getting-started/containers.md', 'docs/guide.md', 'docs/deleted-contract.md']) {
    const result = classify([path]);
    all(result);
    assert.deepEqual(result.addons, addonIDs);
    for (const d of domains) assert.ok(result.reasons[d].includes(`documentation contract/test input: ${path}`));
  }
  all(classify(['website/content/guide.md', 'docs/content/features/addons.md']));
});
