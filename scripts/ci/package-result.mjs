// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
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
