// SPDX-License-Identifier: MIT
// Pure dependency classification: every omission must be an explicit safe case.
export const domains = ['core', 'cpp', 'ui', 'addons', 'distributed', 'backend', 'build', 'packages'];
export const addonIDs = ['tables', 'voice', 'face', 'fine-tuning', 'quantization', 'studio', 'talk', 'telegram', 'slack', 'office', 'sandbox', 'web'];
export function classify(paths, fallback = '') {
  const reasons = Object.fromEntries(domains.map(d => [d, []]));
  const addons = new Set();
  const select = (ds, why, ids = []) => {
    for (const d of ds) reasons[d].push(why);
    for (const id of ids) addons.add(id);
  };
  const broad = why => select(domains, why, addonIDs);
  if (fallback || !paths.length) broad(fallback || 'empty diff: conservative full validation');
  for (const p of paths) {
    if (!p || p.startsWith('/') || p.split('/').includes('..')) { broad(`unknown path: ${p}`); continue; }
    if (/^(go\.(mod|sum)|Makefile|CMakeLists.txt)$/.test(p) || /(^|\/)([^/]*\.proto|[^/]*schema[^/]*)$/.test(p) || /^(scripts\/|\.github\/|\.docker\/|Dockerfile|\.goreleaser|vendor\/)/.test(p)) {
      broad(`shared build/tool/schema input: ${p}`);
    } else if (/^(docs\/|website\/|\.agents\/)/.test(p) || /^[^/]+\.md$/.test(p)) {
      // YAML validation remains always present in its existing workflow.
    } else if (p.startsWith('gallery/')) {
      select(['core'], `gallery Go invariants: ${p}`);
      if (p.startsWith('gallery/addons/')) select(['packages', 'addons'], `add-on catalog: ${p}`, addonIDs);
    } else if (p.startsWith('core/http/react-ui/')) {
      // Go embeds dist; host suites and add-on bundlers consume the host UI.
      select(['core', 'ui', 'addons', 'distributed', 'backend', 'build', 'packages'], `host UI/embed/bundler input: ${p}`, addonIDs);
    } else if (/^addons\//.test(p)) {
      const id = p.split('/')[1];
      if (!addonIDs.includes(id)) broad(`shared or unknown add-on input: ${p}`);
      else select(['core', 'ui', 'addons', 'distributed', 'backend', 'packages', 'build'], `add-on ${id}, host integration and package: ${p}`, [id]);
    } else if (/^(core\/|pkg\/|cmd\/|tests\/)/.test(p)) {
      // Host binaries in browser/foundation suites can import arbitrary Go packages.
      broad(`host Go/test dependency (transitive imports): ${p}`);
    } else if (p.startsWith('backend/go/')) {
      // make test includes backend Go roots, whose imports may reach host packages.
      broad(`backend Go test/host dependency: ${p}`);
    } else if (/^backend\/(cpp|python)\//.test(p)) {
      select(['backend', 'cpp', 'core', 'build'], `backend implementation and conservative host validation: ${p}`);
    } else if (p.startsWith('backend/')) {
      broad(`shared backend input: ${p}`);
    } else {
      broad(`unclassified input: ${p}`);
    }
  }
  return { selected: Object.fromEntries(domains.map(d => [d, reasons[d].length > 0])), addons: addonIDs.filter(id => addons.has(id)), reasons };
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
