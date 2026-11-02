// SPDX-License-Identifier: MIT
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { contractFocus } from '../lib/ci-select.mjs';
const tests = JSON.parse(process.env.CONTRACT_TESTS);
assert.ok(Array.isArray(tests) && tests.length, 'missing contract tests');
for (const test of tests) {
  assert.ok(Object.hasOwn(contractFocus, test.package), 'unknown contract package');
  assert.equal(test.focus, contractFocus[test.package], 'unexpected contract focus');
  const args = ['test', '-count=1', '-tags', 'auth', test.package, '-ginkgo.fail-on-empty'];
  if (test.focus) args.push(`-ginkgo.focus=${test.focus}`);
  execFileSync('go', args, { stdio: 'inherit' });
}
