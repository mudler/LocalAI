// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import { addonIDs } from '../lib/ci-select.mjs';
import { pathToFileURL } from 'node:url';
export function checkResults(needs, event, ref, draft) {
  for (const [id, job] of Object.entries(needs)) assert.ok(['success', 'skipped'].includes(job.result), `${id}: ${job.result}`);
  const requireSuccess = id => assert.equal(needs[id]?.result, 'success', `${id} unexpectedly skipped`);
  requireSuccess('classify');
  const selected = needs.classify.outputs.packages;
  assert.ok(['true', 'false'].includes(selected), 'invalid package selection');
  if (selected === 'false') {
    for (const [id, job] of Object.entries(needs)) if (id !== 'classify') assert.equal(job.result, 'skipped', `${id} unexpectedly ran`);
    return;
  }
  requireSuccess('plan'); requireSuccess('validate');
  const plan = needs.plan.outputs;
  for (const key of ['publish', 'has_platforms', 'sandbox']) assert.ok(['true', 'false'].includes(plan[key]), `invalid plan ${key}`);
  for (const key of ['immutable', 'aliases', 'channel', 'version', 'matrix', 'addons', 'registry']) assert.equal(typeof plan[key], 'string', `missing plan ${key}`);
  assert.match(plan.version, /^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/, 'invalid version');
  assert.ok(['', 'development', 'latest'].includes(plan.aliases), 'invalid aliases');
  assert.ok(['', 'development', 'latest'].includes(plan.channel) || /^version-\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(plan.channel), 'invalid channel');
  const matrix = JSON.parse(plan.matrix), addons = JSON.parse(plan.addons);
  assert.ok(matrix && Array.isArray(matrix.include), 'invalid matrix');
  assert.ok(Array.isArray(addons) && addons.every(id => addonIDs.includes(id)) && new Set(addons).size === addons.length, 'invalid addons');
  const keys = new Set();
  for (const row of matrix.include) {
    assert.ok(row && addonIDs.includes(row.addon) && addons.includes(row.addon), 'invalid matrix addon');
    assert.match(row.platform || '', /^(linux|darwin)\/(amd64|arm64)$/, 'invalid platform');
    for (const key of ['runner', 'slug']) assert.ok(typeof row[key] === 'string' && row[key].length, `invalid matrix ${key}`);
    const key = `${row.addon}/${row.platform}`; assert.ok(!keys.has(key), 'duplicate matrix leg'); keys.add(key);
  }
  assert.deepEqual([...new Set(matrix.include.map(row => row.addon))].sort(), [...addons].sort(), 'matrix/addons mismatch');
  assert.equal(plan.has_platforms, String(matrix.include.length > 0), 'matrix/has_platforms mismatch');
  if (event === 'pull_request' || event === 'workflow_dispatch') assert.equal(plan.publish, 'false', 'untrusted/manual publication');
  if (event === 'push' && ref === 'refs/heads/master') assert.equal(plan.publish, 'true', 'master must publish');
  if ((event === 'push' && ref.startsWith('refs/tags/v')) || (event === 'release' && !draft)) assert.equal(plan.publish, 'true', 'release must publish');
  if (event === 'push' && ref === 'refs/heads/master') {
    assert.equal(plan.channel, 'development', 'master channel');
    assert.equal(plan.aliases, 'development', 'master aliases');
  }
  if (plan.aliases) assert.equal(plan.aliases, plan.channel, 'channel/aliases mismatch');
  if (plan.publish === 'true') {
    assert.ok(matrix.include.length, 'publishing needs platforms');
    assert.match(plan.registry, /^[A-Za-z0-9][A-Za-z0-9.:-]*$/, 'missing registry');
    assert.match(plan.immutable, /^(sha-[a-f0-9]{12}|\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)$/, 'invalid immutable');
    assert.ok(plan.channel.length, 'missing channel');
  } else {
    assert.equal(plan.immutable, '', 'validation immutable');
    assert.equal(plan.aliases, '', 'validation aliases');
    assert.equal(plan.channel, '', 'validation channel');
  }
  if (plan.sandbox === 'true') requireSuccess('sandbox-image');
  if (plan.has_platforms === 'true') requireSuccess('package');
  if (plan.publish === 'true' && plan.has_platforms === 'true') {
    requireSuccess('publish');
    if (plan.aliases) requireSuccess('promote');
    if (event === 'push' && ref.startsWith('refs/tags/v')) requireSuccess('catalog');
  }
  if (event === 'release' && !draft) requireSuccess('release-index');
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) checkResults(JSON.parse(process.env.RESULTS), process.env.EVENT, process.env.REF, process.env.DRAFT === 'true');
