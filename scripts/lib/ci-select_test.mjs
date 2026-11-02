// SPDX-License-Identifier: MIT
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { classify, domains, addonIDs, diffSpec } from './ci-select.mjs';
import { selectEvent } from '../ci/select.mjs';
const all = r => assert.ok(domains.every(d => r.selected[d]));
test('targeted domains do not select unrelated work', () => {
  for (const [path, yes, no] of [
    ['README.md', [], domains], ['docs/guide.md', [], domains],
    ['gallery/foo.yaml', ['contracts'], ['core', 'ui', 'packages']],
    ['backend/python/foo/run.py', ['backend'], ['cpp', 'core', 'build', 'ui', 'packages']],
    ['core/http/react-ui/src/main.css', ['ui', 'addons', 'build'], ['core', 'backend', 'distributed', 'packages']],
    ['addons/voice/main.go', ['core', 'addons', 'packages'], ['ui', 'backend', 'build']],
    ['addons/voice/ui/main.jsx', ['addons', 'packages'], ['core', 'ui', 'backend', 'build']],
  ]) {
    const r = classify([path]);
    for (const d of yes) assert.equal(r.selected[d], true, path + d);
    for (const d of no) assert.equal(r.selected[d], false, path + d);
  }
  assert.deepEqual(classify(['addons/voice/deleted.go']).addons, ['voice']);
  assert.deepEqual(classify(['addons/voice/old', 'addons/face/new']).addons, ['voice', 'face']);
  assert.deepEqual(classify(['core/http/react-ui/package-lock.json']).addons, addonIDs);
  for (const p of ['go.mod', 'go.sum', 'Makefile', 'backend/backend.proto', 'new-input', 'scripts/build/x.sh', '.github/workflows/test.yml', 'schema.json']) all(classify([p]));
});
const base = 'a'.repeat(40), head = 'b'.repeat(40);
test('PR uses real head/base merge-base; master uses before/after', () => {
  const calls = [];
  const git = args => { calls.push(args); return args[0] === 'merge-base' ? 'c'.repeat(40) : args[0] === 'diff' ? 'gallery/model.yaml\0' : ''; };
  const r = selectEvent('pull_request', { pull_request: { base: { sha: base }, head: { sha: head } } }, 'refs/pull/1/merge', git);
  assert.equal(r.selected.contracts, true);
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

test('documentation readers are explicit and select only their suites', () => {
  const addons = classify(['docs/content/features/addons.md']);
  assert.deepEqual(addons.groups, []);
  assert.ok(addons.contracts.includes('./tests/addons/recovery'));
  assert.ok(addons.contracts.includes('./tests/addons/tablesadmin'));
  assert.deepEqual(addons.addons, []);
  assert.equal(addons.selected.core, false);
  assert.ok(addons.contracts.includes('./core/http/endpoints/addons'));
  assert.ok(classify(['docs/content/features/tables.md']).contracts.includes('./tests/addons/tablesadmin'));
  assert.deepEqual(classify(['docs/content/features/diagnostics.md']).contracts, ['./pkg/diagnostics']);
  for (const p of ['docs/new.md', 'docs/deleted.md', 'docs/content/getting-started/install.md']) assert.ok(domains.every(d => !classify([p]).selected[d]));
});
test('real imports distinguish host compilation from independent package builds', async () => {
  const { dependencyIndex } = await import('../ci/dependencies.mjs');
  const deps = dependencyIndex();
  assert.deepEqual(deps['core/config'].filter(x => addonIDs.includes(x)), []);
  const config = classify(['core/config/config.go'], '', deps);
  assert.equal(config.selected.packages, false);
  assert.equal(config.selected.cpp, false);
  assert.equal(config.selected.launcher, false);
  const isolated = classify(['pkg/addons/model/proposalflow/flow.go'], '', deps);
  assert.deepEqual(domains.filter(d => isolated.selected[d]), ['core', 'build']);
});
test('dependency index includes root tests, all build tags, and generated imports', async () => {
  const { dependencyIndex } = await import('../ci/dependencies.mjs');
  const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const root = mkdtempSync(join(tmpdir(), 'ci-imports-'));
  function file(path, text) { mkdirSync(join(root, path, '..'), { recursive: true }); writeFileSync(join(root, path), text); }
  try {
    file('go.mod', 'module example.test/localai\n');
    file('tests/e2e-ui/main.go', 'package main\nimport "example.test/localai/pkg/shared"\n');
    file('tests/e2e-ui/main_test.go', 'package main\nimport "example.test/localai/pkg/testonly"\n');
    file('pkg/shared/posix.go', '//go:build linux\npackage shared\nimport "example.test/localai/pkg/tagged"\n');
    file('pkg/shared/windows.go', '//go:build windows\npackage shared\nimport "example.test/localai/pkg/windows"\n');
    file('pkg/shared/shared_test.go', 'package shared\nimport "example.test/localai/pkg/nottransitive"\n');
    file('addons/voice/main.go', 'package main\nimport "example.test/localai/pkg/shared"\n');
    for (const p of ['shared', 'testonly', 'tagged', 'windows', 'nottransitive', 'grpc/proto']) file(`pkg/${p}/p.go`, `package p\n`);
    file('pkg/tagged/p.go', 'package tagged\nimport "example.test/localai/pkg/grpc/proto"\n');
    const d = dependencyIndex(root);
    for (const p of ['shared', 'testonly', 'tagged', 'windows', 'grpc/proto']) assert.ok(d[`pkg/${p}`].includes('ui'), p);
    assert.ok(!d['pkg/nottransitive'].includes('ui'), 'dependency tests are not compiled into consumers');
    assert.ok(!d['pkg/testonly'].includes('voice'), 'host test imports are not package build inputs');
    assert.equal(classify(['pkg/shared/deleted.go'], '', d).selected.ui, true);
    assert.equal(classify(['pkg/shared/embed.html'], '', d).selected.ui, true);
    all(classify(['pkg/entirely-removed/deleted.go'], '', d));
  } finally { rmSync(root, { recursive:true, force:true }); }
});

test('executable consumers include transitive production imports, not fixture tests', async () => {
  const { dependencyIndex } = await import('../ci/dependencies.mjs');
  const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const root = mkdtempSync(join(tmpdir(), 'ci-executables-'));
  function file(path, text) { mkdirSync(join(root, path, '..'), { recursive: true }); writeFileSync(join(root, path), text); }
  try {
    file('go.mod', 'module example.test/localai\n');
    file('tests/e2e/mock-backend/main.go', 'package main\nimport "example.test/localai/pkg/mockdep"\n');
    file('pkg/mockdep/dep.go', 'package mockdep\nimport "example.test/localai/pkg/mockleaf"\n');
    file('pkg/mockleaf/leaf.go', 'package mockleaf\n');
    file('tests/e2e/mock-backend/main_test.go', 'package main\nimport "example.test/localai/pkg/mocktest"\n');
    file('pkg/mocktest/test.go', 'package mocktest\n');
    const deps = dependencyIndex(root);
    for (const pkg of ['pkg/mockdep', 'pkg/mockleaf']) {
      assert.ok(deps[pkg].includes('ui'), pkg);
      assert.ok(deps[pkg].includes('addons'), pkg);
      const r = classify([pkg+'/changed.go'], '', deps);
      assert.equal(r.selected.ui, true);
      assert.equal(r.selected.addons, true);
      assert.equal(r.selected.packages, false);
    }
    assert.ok(!deps['pkg/mocktest'].includes('ui'), 'executable tests are not UI runtime inputs');
  } finally { rmSync(root, {recursive:true, force:true}); }
});
