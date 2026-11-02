// SPDX-License-Identifier: MIT
// Pure dependency classification. Unknown source/build inputs fail open to full
// validation; known prose is inert unless named in the documentation contracts.
export const domains = ['core', 'cpp', 'ui', 'addons', 'distributed', 'backend', 'build', 'launcher', 'packages', 'contracts'];
export const addonIDs = ['tables', 'voice', 'face', 'fine-tuning', 'quantization', 'studio', 'talk', 'telegram', 'slack', 'office', 'sandbox', 'web'];
export const suiteGroups = [
  { group: 'contracts and gateway', suites: 'prerequisites contracts catalog packages lifecycle runtime gateway policy proposals artifacts apps' },
  { group: 'host pages', suites: 'gallery-ui esm-ui host-slots library-sources updates recovery' },
  { group: 'foundation', suites: 'foundation-acceptance' },
  { group: 'tables', suites: 'tables-databases tables-schema tables-rows tables-tools tables-admin' },
  { group: 'tables pages', suites: 'tables-ui tables-csv tables-acceptance' },
];
// These are file readers, not mentions/links. Website installation/container
// review is a separate script, not a consumer in these validation workflows.
export const docContracts = {
  'docs/content/features/addons.md': ['./pkg/addons/packaging', './addons/sandbox/internal/tools', './core/http/endpoints/addons', './tests/addons/tablesadmin', './tests/addons/recovery'],
  'docs/content/features/tables.md': ['./core/services/addons/tools', './tests/addons/tablesadmin'],
  'docs/content/features/image-generation.md': ['./core/gallery'],
  'docs/content/features/diagnostics.md': ['./pkg/diagnostics'],
  'docs/content/reference/cli-reference.md': ['./pkg/diagnostics'],
  'docs/content/getting-started/troubleshooting.md': ['./pkg/diagnostics'],
};
export const contractFocus = {
  './tests/addons/tablesadmin': 'lists every route in the instructions registry, the feature registry and the documentation',
  './tests/addons/recovery': 'documents every route and every emitted field in the Swagger spec, the instructions and the feature registry',
  './pkg/addons/packaging': 'publishes every add-on to the one documented repository',
  './addons/sandbox/internal/tools': 'installs and checks in the guest image what the note names',
  './core/http/endpoints/addons': 'documents the management actions and every emitted field',
  './core/services/addons/tools': 'keeps the table of the Tables documentation in step with the matrix',
  './pkg/diagnostics': 'Diagnostics documentation',
  './core/gallery': '', // Gallery-only changes retain every gallery invariant.
};
export function classify(paths, fallback = '', dependencies = {}) {
  const reasons = Object.fromEntries(domains.map(d => [d, []]));
  const addons = new Set(), suites = new Set(), contracts = new Set();
  const select = (ds, why, ids = []) => {
    for (const d of ds) reasons[d].push(why);
    for (const id of ids) addons.add(id);
  };
  const groups = (names, why) => {
    select(['addons'], why);
    for (const g of suiteGroups.filter(g => names.includes(g.group))) for (const s of g.suites.split(' ')) suites.add(s);
  };
  const host = why => groups(suiteGroups.map(g => g.group), why);
  const broad = why => { select(domains, why, addonIDs); host(why); for (const ps of Object.values(docContracts)) for (const p of ps) contracts.add(p); };
  if (fallback || !paths.length) broad(fallback || 'empty diff: conservative full validation');
  for (const p of paths) {
    const why = `dependency: ${p}`;
    if (!p || p.startsWith('/') || p.split('/').includes('..')) { broad(`unknown path: ${p}`); continue; }
    if (/^(go\.(mod|sum)|Makefile|CMakeLists.txt)$/.test(p) || /(^|\/)([^/]*\.proto|[^/]*schema[^/]*)$/.test(p) || /^(scripts\/|\.github\/|\.docker\/|Dockerfile|\.goreleaser|vendor\/)/.test(p)) {
      broad(`shared build/tool/schema input: ${p}`);
    } else if (p.startsWith('docs/')) {
      for (const pkg of docContracts[p] || []) contracts.add(pkg);
      if (docContracts[p]) select(['contracts'], `documentation reader: ${p}`);
    } else if (/^(website\/|\.agents\/)/.test(p) || /^[^/]+\.md$/.test(p)) {
      // Cheap lint/unit/YAML checks stay broad in their existing workflows.
    } else if (p.startsWith('gallery/')) {
      select(['contracts'], `gallery invariants: ${p}`); contracts.add('./core/gallery');
      if (p.startsWith('gallery/addons/')) { select(['packages'], why, addonIDs); groups(['contracts and gateway', 'host pages'], why); }
    } else if (p.startsWith('core/http/react-ui/')) {
      select(['ui', 'build'], why);
      groups(['host pages', 'foundation', 'tables pages'], why);
      // Add-on bundlers resolve dependencies here, never host CSS/src files.
      if (/^core\/http\/react-ui\/(package(-lock)?\.json|vite\.config\.[^/]+)$/.test(p)) select(['packages'], why, addonIDs);
    } else if (p.startsWith('addons/')) {
      const id = p.split('/')[1];
      if (!addonIDs.includes(id)) {
        select(['core', 'packages'], why, addonIDs); host(why);
      } else {
        select(['packages'], why, [id]);
        if (!p.includes('/ui/')) select(['core'], why);
        groups(['contracts and gateway', 'host pages'], why);
        if (id === 'tables') groups(['tables', 'tables pages', 'foundation'], why);
      }
    } else if (/^backend\/(cpp|python)\//.test(p)) {
      select(['backend'], why);
      if (p.startsWith('backend/cpp/')) select(['cpp'], why);
    } else if (p.startsWith('backend/go/')) {
      select(['core', 'backend'], why);
    } else if (/^(core\/|pkg\/|cmd\/|tests\/|api\/|internal\/)/.test(p)) {
      // All host Go changes retain the unchanged coverage gate. Additional
      // expensive consumers follow real local imports (including test imports),
      // indexed by the adapter, rather than treating every pkg as shared.
      if (p.startsWith('cmd/launcher/')) select(['launcher'], why);
      else if (p.startsWith('tests/addons/')) {
        const dir = p.split('/')[2];
        const group = dir.startsWith('tables') ? 'tables' : dir === 'foundation' ? 'foundation' : dir === 'recovery' ? 'host pages' : '';
        if (group) groups([group], why);
        else host(why); // Shared fixtures and unfamiliar suites can serve every group.
        if (dir === 'actione2e') select(['core'], why);
      } else {
        select(['core'], why);
        if (!p.startsWith('tests/')) select(['build'], why);
        const consumers = dependencies[p.slice(0, p.lastIndexOf('/'))];
        if (!consumers) { // Unknown/deleted package: preserve correctness.
          broad(`unknown host package: ${p}`);
        } else {
          select(consumers.filter(d => domains.includes(d)), why);
          if (consumers.includes('addons')) host(why);
          select([], why, consumers.filter(d => addonIDs.includes(d)));
          if (consumers.some(d => addonIDs.includes(d))) select(['packages'], why);
        }
      }
    } else broad(`unclassified input: ${p}`);
  }
  const selectedGroups = suiteGroups.map(g => ({ ...g, suites: g.suites.split(' ').filter(s => suites.has(s)).join(' ') })).filter(g => g.suites);
  return { selected: Object.fromEntries(domains.map(d => [d, reasons[d].length > 0])), addons: addonIDs.filter(id => addons.has(id)), groups: selectedGroups, contracts: [...contracts].sort(), reasons };
}
export function diffSpec(eventName, event, ref) {
  const sha = s => typeof s === 'string' && /^[a-f0-9]{40}$/i.test(s) && !/^0+$/.test(s);
  if (eventName === 'pull_request') {
    const base = event.pull_request?.base?.sha, head = event.pull_request?.head?.sha;
    return sha(base) && sha(head) ? { base, head, mergeBase: true } : { full: 'invalid PR base/head SHA' };
  }
  if (eventName === 'push' && ref === 'refs/heads/master') {
    return sha(event.before) && sha(event.after) ? { base: event.before, head: event.after, mergeBase: false } : { full: 'invalid push before/after SHA' };
  }
  return { full: `full validation event: ${eventName} ${ref}` };
}
