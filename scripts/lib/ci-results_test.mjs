// SPDX-License-Identifier: MIT
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkResults } from '../ci/package-result.mjs';
import { checkJobs } from '../ci/job-result.mjs';
const needs = (publish = false) => ({ classify: { result: 'success', outputs: { packages: 'true' } }, ...Object.fromEntries(['plan', 'validate', 'sandbox-image', 'package', 'publish', 'promote', 'catalog', 'release-index'].map(id => [id, { result: ['plan', 'validate', 'package', ...(publish ? ['publish', 'promote'] : [])].includes(id) ? 'success' : 'skipped', outputs: id === 'plan' ? { publish: String(publish), has_platforms: 'true', sandbox: 'false', aliases: publish ? 'development' : '' } : {} }])) });
test('package scenarios: PR/manual, selective master, full tag/release', () => {
  for (const event of ['pull_request', 'workflow_dispatch']) checkResults(needs(), event, 'refs/heads/master', false);
  checkResults(needs(true), 'push', 'refs/heads/master', false);
  const tag = needs(true); tag.catalog.result = 'success';
  checkResults(tag, 'push', 'refs/tags/v1.0.0', false);
  const release = needs(true); release['release-index'].result = 'success';
  checkResults(release, 'release', 'refs/tags/v1.0.0', false);
  const docs = needs(true); docs.plan.outputs.has_platforms = 'false'; docs.package.result = 'skipped'; docs.publish.result = 'skipped'; docs.promote.result = 'skipped';
  checkResults(docs, 'push', 'refs/heads/master', false);
});
test('each failure/cancellation rejects; expected active jobs may not skip', () => {
  for (const id of Object.keys(needs())) {
    for (const result of ['failure', 'cancelled']) {
      const n = needs(); n[id].result = result;
      assert.throws(() => checkResults(n, 'pull_request', '', false), new RegExp(id));
    }
  }
  for (const id of ['plan', 'validate', 'package', 'publish', 'promote']) {
    const n = needs(true); n[id].result = 'skipped';
    assert.throws(() => checkResults(n, 'push', 'refs/heads/master', false));
  }
  const n = needs(); n.plan.outputs.sandbox = 'true';
  assert.throws(() => checkResults(n, 'pull_request', '', false));
});
test('aggregate fails closed for classification and selected preparation/suites', () => {
  const mapping = { prepare: 'addons', suites: 'addons' };
  const n = { classify: { result: 'success', outputs: { addons: 'true' } }, prepare: { result: 'success' }, suites: { result: 'success' } };
  checkJobs(n, mapping);
  for (const id of Object.keys(n)) for (const result of ['skipped', 'failure', 'cancelled']) assert.throws(() => checkJobs({ ...n, [id]: { ...n[id], result } }, mapping));
  const skipped = { classify: { result: 'success', outputs: { addons: 'false' } }, prepare: { result: 'skipped' }, suites: { result: 'skipped' } };
  checkJobs(skipped, mapping);
  skipped.classify.outputs = {}; assert.throws(() => checkJobs(skipped, mapping));
});
test('packages allow only explicit unselected skips', () => {
  const n = needs(); n.classify.outputs.packages = 'false';
  for (const [id, job] of Object.entries(n)) if (id !== 'classify') job.result = 'skipped';
  checkResults(n, 'pull_request', '', false);
  n.classify.result = 'failure'; assert.throws(() => checkResults(n, 'pull_request', '', false));
});
test('every package dependency unexpected skip is rejected when applicable', () => {
  for (const id of ['classify', 'plan', 'validate', 'package', 'publish', 'promote']) {
    const n = needs(true); n[id].result = 'skipped';
    assert.throws(() => checkResults(n, 'push', 'refs/heads/master', false));
  }
});
