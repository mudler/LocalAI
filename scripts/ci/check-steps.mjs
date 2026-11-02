// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
export function checkSteps(steps, domain, required) {
  assert.equal(steps.affected?.outcome, 'success', 'selector must succeed');
  const selected = steps.affected.outputs[domain];
  assert.ok(['true', 'false'].includes(selected), 'missing/malformed selector output');
  if (selected === 'true') for (const id of required) assert.equal(steps[id]?.outcome, 'success', `${id} did not succeed`);
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) checkSteps(JSON.parse(process.env.STEPS), process.env.DOMAIN, process.env.REQUIRED.split(' '));
