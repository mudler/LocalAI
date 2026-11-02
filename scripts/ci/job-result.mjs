// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
// Completed dependency results are the entire input: only an explicit false
// from a successful classifier permits a skip. Preparation is checked as an
// ordinary selected dependency, so a failed producer cannot hide behind suites.
export function checkJobs(needs, domains) {
  assert.equal(needs.classify?.result, 'success', 'classifier must succeed');
  for (const [id, domain] of Object.entries(domains)) {
    const selected = needs.classify.outputs[domain];
    assert.ok(['true', 'false'].includes(selected), `invalid selection: ${domain}`);
    assert.equal(needs[id]?.result, selected === 'true' ? 'success' : 'skipped', `${id}: unexpected dependency result`);
  }
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) checkJobs(JSON.parse(process.env.RESULTS), JSON.parse(process.env.JOB_DOMAINS));
