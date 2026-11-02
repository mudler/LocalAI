// SPDX-License-Identifier: MIT
import { readFileSync, appendFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { dependencyIndex } from './dependencies.mjs';
import { classify, diffSpec, contractFocus } from '../lib/ci-select.mjs';
// No API permissions, secrets, path truncation or shell interpolation. Checkout
// fetch-depth: 0 supplies history; unavailable fork objects conservatively run all.
export function selectEvent(name, event, ref, git = args => execFileSync('git', args, { maxBuffer: 64 * 1024 * 1024 }).toString()) {
  const spec = diffSpec(name, event, ref);
  if (spec.full) return classify([], spec.full);
  try {
    for (const sha of [spec.base, spec.head]) git(['cat-file', '-e', `${sha}^{commit}`]);
    const base = spec.mergeBase ? git(['merge-base', spec.base, spec.head]).trim() : spec.base;
    // --no-renames returns both deleted and added paths, including cross-domain moves.
    const paths = git(['diff', '--name-only', '-z', '--no-renames', base, spec.head, '--']).split('\0').filter(Boolean);
    return classify(paths, '', dependencyIndex());
  } catch (error) {
    return classify([], `diff unavailable; full validation: ${error.message}`);
  }
}
if (process.argv[1]?.endsWith('/select.mjs')) {
  // Malformed event files are errors, not green skips.
  const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'));
  const result = selectEvent(process.env.GITHUB_EVENT_NAME, event, process.env.GITHUB_REF);
  console.log(JSON.stringify(result, null, 2));
  if (process.env.GITHUB_OUTPUT) {
    for (const [key, value] of Object.entries(result.selected)) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
    appendFileSync(process.env.GITHUB_OUTPUT, `contract-tests-json=${JSON.stringify(result.contracts.map(pkg => ({ package: pkg, focus: contractFocus[pkg] })))}\ngroups-json=${JSON.stringify({include: result.groups})}\nvalidation-json=${JSON.stringify(result.addons.length ? result.addons : ['rules'])}\n`);
    appendFileSync(process.env.GITHUB_OUTPUT, `addons-json=${JSON.stringify(result.addons)}\naddons-list=${result.addons.join(' ')}\nsandbox=${result.addons.includes('sandbox')}\n`);
  }
}
