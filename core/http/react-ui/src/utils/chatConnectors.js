// Helpers of the connectors page. No React, so a node test drives them.
import { addonPageAddress } from '../addons/addresses.js'

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

// The other enabled binding of the same bot that the host names on a binding
// (`duplicate`), or null. Both then answer the same message.
export function duplicateOf(c) {
  return c?.duplicate?.others?.[0] || null
}

// The page of the add-on that serves a binding, opened on one binding of it.
// groups come from groupConnectors; '' when no running add-on serves the kind.
export function bindingAddress(groups, c, id) {
  const href = (groups || []).find((g) => g.kind === c.kind && g.addon === c.addon)?.href
  return href ? `${href}${href.includes('?') ? '&' : '?'}binding=${encodeURIComponent(id)}` : ''
}

// The bindings that answer as one agent, for the agent's page. Each row has the
// title of its kind (from the running add-on, else the kind ID), a state and the
// address of the binding's own settings. Any add-on works: nothing here names a
// kind. state is 'attention' when the host says the binding is not running or
// answers the same bot twice, 'off' when the owner switched it off, else 'ok';
// reason is the host's words for 'attention', when it gave any.
export function bindingsOfAgent(agentId, kinds, items) {
  if (!agentId) return []
  const { groups } = groupConnectors(kinds, items)
  return (items || []).filter((c) => c.agent_id === agentId).map((c) => {
    const group = groups.find((g) => g.kind === c.kind && g.addon === c.addon)
    const attention = c.active === false || !!duplicateOf(c)
    return {
      id: c.id,
      name: c.name,
      kindTitle: group?.title || c.kind,
      state: attention ? 'attention' : c.enabled === false ? 'off' : 'ok',
      reason: c.active === false ? (c.reason || '') : '',
      // Without a running add-on there is no page to open; the gallery entry of
      // the add-on is the nearest place.
      href: group ? bindingAddress(groups, c, c.id) : `/app/addons?detail=${encodeURIComponent(c.addon || c.kind)}`,
    }
  })
}
