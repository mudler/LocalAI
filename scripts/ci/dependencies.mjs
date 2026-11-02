// SPDX-License-Identifier: MIT
// Conservative union of all Go files/tags and test imports. No go toolchain,
// generated stubs, module downloads or execution of repository code required.
import { readdirSync, readFileSync } from 'node:fs';
import { addonIDs } from '../lib/ci-select.mjs';
// Build/exec seams are not Go imports. Keep this inventory alongside roots:
// Playwright webServer and add-on verify/readiness start ui-test-server, which
// starts mock-backend. Host-slots/ESM/foundation also build the protocol fixture;
// Tables host suites build the Tables executable. Backend E2E's Make target
// builds both proxies. MCP's fixture belongs to test-e2e-platform, not UI or
// packaging; do not give it unrelated UI/package consumers.
export const executableRoots = {
  ui: ['tests/e2e-ui', 'tests/e2e/mock-backend'],
  addons: ['tests/e2e-ui', 'tests/e2e/mock-backend', 'tests/addons/fixture/cmd',
    'tests/addons/foundation/cmd/foundation-cluster',
    'tests/addons/tablesacceptance/cmd/tables-cluster', 'addons/tables/cmd/tables'],
  backend: ['cmd/local-ai', 'tests/e2e/mock-backend', 'backend/go/cloud-proxy', 'backend/go/localai-proxy'],
};
export function dependencyIndex(root = '.') {
  const module = readFileSync(`${root}/go.mod`, 'utf8').match(/^module\s+(\S+)/m)[1];
  const graph = new Map(), tests = new Map();
  function walk(dir) {
    for (const entry of readdirSync(`${root}/${dir}`, { withFileTypes: true })) {
      const path = dir ? `${dir}/${entry.name}` : entry.name;
      if (entry.isDirectory() && !['.git', '.cache', 'node_modules', 'vendor', 'dist', 'docs', 'website'].includes(entry.name)) walk(path);
      else if (entry.isFile() && entry.name.endsWith('.go')) {
        if (!graph.has(dir)) graph.set(dir, new Set());
        if (!tests.has(dir)) tests.set(dir, new Set());
        const edges = entry.name.endsWith('_test.go') ? tests : graph;
        const text = readFileSync(`${root}/${path}`, 'utf8');
        for (const block of text.matchAll(/^import\s+(?:\([\s\S]*?\)|[^\n]+)/gm)) {
          for (const match of block[0].matchAll(/"([^"\n]+)"/g)) {
            if (match[1].startsWith(`${module}/`)) edges.get(dir).add(match[1].slice(module.length + 1));
          }
        }
      }
    }
  }
  walk('');
  const consumers = Object.fromEntries([...graph.keys()].map(p => [p, []]));
  const roots = {
    ui: ['tests/e2e-ui'],
    backend: ['tests/e2e'],
    distributed: ['tests/e2e/distributed', 'core/services/testutil', 'core/services/messaging', 'core/services/studio', 'core/services/collections', 'core/services/syncstate', 'core/services/modeladmin', 'core/services/failover'],
    addons: ['tests/addons', 'tests/e2e-ui'],
    launcher: ['cmd/launcher'],
    ...Object.fromEntries(addonIDs.map(id => [id, [`addons/${id}`]])),
  };
  for (const [consumer, prefixes] of Object.entries(roots)) {
    const seen = new Set();
    function visit(p) {
      if (seen.has(p)) return;
      seen.add(p);
      if (consumers[p]) consumers[p].push(consumer);
      for (const dep of graph.get(p) || []) visit(dep);
    }
    // Executables contribute production imports only, never their own tests.
    for (const p of executableRoots[consumer] || []) visit(p);
    for (const p of graph.keys()) if (prefixes.some(prefix => p === prefix || p.startsWith(`${prefix}/`))) {
      visit(p);
      if (!addonIDs.includes(consumer)) for (const dep of tests.get(p) || []) visit(dep);
    }
  }
  return consumers;
}
