// Helpers of the connectors page. No React, so a node test drives them.
import { addonPageAddress } from './legacyRoutes.js'

// Groups the bindings of the user by the connector add-on that serves them. A
// kind that a running add-on serves gets a group with the address of the
// add-on's own page, where bindings are added and edited. A binding whose kind
// no running add-on serves goes to `waiting`: it is kept, and works once the
// add-on is installed.
export function groupConnectors(kinds, items) {
  const groups = (kinds || []).map((k) => ({
    key: `${k.addon}/${k.kind}`, addon: k.addon, kind: k.kind, title: k.title, description: k.description || '',
    href: k.page ? addonPageAddress({ addon: k.addon, page: k.page }) : `/app/addons?detail=${encodeURIComponent(k.addon)}`,
    items: [],
  }))
  const waiting = []
  for (const c of items || []) {
    const g = groups.find((x) => x.kind === c.kind && x.addon === c.addon)
    if (g) g.items.push(c)
    else waiting.push(c)
  }
  return { groups, waiting }
}

// Who the binding accepts, as a key and a count for the translator.
export function acceptsOf(c) {
  if (c.accept_any) return { key: 'anyone' }
  const n = (c.allow || []).length
  return n === 0 ? { key: 'nobody' } : { key: 'listed', count: n }
}
