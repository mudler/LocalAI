// Helpers of the connectors page. No React, so a node test drives them.
import { addonPageAddress } from '../addons/addresses.js'

// The page where a kind is managed: the page its add-on declares, else the
// add-on's entry in the gallery.
export function kindPageHref(k) {
  return k.page ? addonPageAddress({ addon: k.addon, page: k.page }) : `/app/addons?detail=${encodeURIComponent(k.addon)}`
}

// An address opened on one binding.
function withBinding(href, id) {
  return `${href}${href.includes('?') ? '&' : '?'}binding=${encodeURIComponent(id)}`
}

function withQuery(href, query) {
  return `${href}${href.includes('?') ? '&' : '?'}${query}`
}

// Groups the bindings of the user by the connector add-on that serves them. A
// kind that a running add-on serves gets a group with the address of the
// add-on's own page, where bindings are added and edited. A binding whose kind
// no running add-on serves goes to `waiting`: it is kept, and works once the
// add-on is installed.
export function groupConnectors(kinds, items) {
  const groups = (kinds || []).map((k) => ({
    key: `${k.addon}/${k.kind}`, addon: k.addon, kind: k.kind, title: k.title, description: k.description || '',
    // A kind whose add-on logs in with OAuth and needs no agent: the page
    // itself adds its connections, which the person then connects.
    addsLogin: k.auth_kind === 'oauth2' && !!k.agent_optional,
    href: kindPageHref(k),
    // The page of the kind opened on its add sheet.
    // A kind that logs in has no add sheet: the connections are added on this page.
    newHref: k.page && !(k.auth_kind === 'oauth2' && k.agent_optional) ? withQuery(kindPageHref(k), 'new=1') : '',
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
  return href ? withBinding(href, id) : ''
}

// The running add-on kind that serves a binding, or undefined. A binding names
// its add-on; one that does not (an older record) matches on the kind alone.
export function kindOfBinding(kinds, c) {
  return (kinds || []).find((k) => k.kind === c.kind && (!c.addon || k.addon === c.addon))
}

// The bindings that answer as one agent, each with the kind that serves it:
// [{ c, kind }]. Both the agent page and the Tools list of the agent start here.
export function bindingsWithKind(agentId, kinds, items) {
  if (!agentId) return []
  return (items || []).filter((c) => c.agent_id === agentId).map((c) => ({ c, kind: kindOfBinding(kinds, c) }))
}

// The bindings that answer as one agent, for the agent's page. Each row has the
// title of its kind (from the running add-on, else the kind ID), a state and the
// address of the binding's own settings. Any add-on works: nothing here names a
// kind. state is 'attention' when the host says the binding is not running or
// answers the same bot twice, 'off' when the owner switched it off, else 'ok';
// reason is the host's words for 'attention', when it gave any.
export function bindingsOfAgent(agentId, kinds, items) {
  return bindingsWithKind(agentId, kinds, items).map(({ c, kind }) => {
    const attention = c.active === false || !!duplicateOf(c)
    return {
      id: c.id,
      name: c.name,
      kindTitle: kind?.title || c.kind,
      state: attention ? 'attention' : c.enabled === false ? 'off' : 'ok',
      reason: c.active === false ? (c.reason || '') : '',
      // Without a running add-on there is no page to open; the gallery entry of
      // the add-on is the nearest place.
      href: kind ? withBinding(kindPageHref(kind), c.id) : `/app/addons?detail=${encodeURIComponent(c.addon || c.kind)}`,
    }
  })
}

// The state of a binding in a list row: 'attention' when the host says its
// add-on does not run, no token is saved (or the login of a connection that
// logs in is not working), or it answers the same bot as another
// connection; 'off' when the owner switched it off; else 'running'. A switched
// off binding is off, except that one whose add-on does not run still says so.
export function rowState(c) {
  if (c.active === false) return 'attention'
  if (c.enabled === false) return 'off'
  // A connection that logs in has no token: it needs attention until its
  // login works.
  if (c.auth_kind === 'oauth2') return c.oauth?.state === 'connected' ? 'running' : 'attention'
  if (c.has_token === false || duplicateOf(c)) return 'attention'
  return 'running'
}

// The facts of a connection a person reads in a list or a pane: the agent that
// answers, and who it accepts, as keys and counts for the translator. Nothing
// here shows the raw kind or a raw id.
export function connectionFacts(c, agents) {
  const agent = (agents || []).find((a) => a.id === c.agent_id)
  return { agentName: agent ? agent.name : '', accepts: acceptsOf(c) }
}

// The agent that answers and who it accepts, as words, for a connection row.
// `t` is the translator of the add-ons namespace. Never the raw kind or an id.
export function connectionLine(c, agents, t) {
  const { agentName, accepts } = connectionFacts(c, agents)
  const who = accepts.key === 'listed' ? t('connection.acceptsListed', { count: accepts.count }) : t(`connection.accepts.${accepts.key}`)
  return { agentName, who }
}
