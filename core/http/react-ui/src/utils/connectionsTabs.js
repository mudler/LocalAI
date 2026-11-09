// The tabs of the Connections item: accounts and messaging are two views of
// the connectors page (/app/chats/connectors, ?tab=), MCP servers is its own
// page (/app/mcp-connectors). No React, so a node test drives it.

export const CONNECTIONS_PATH = '/app/chats/connectors'
export const MCP_PATH = '/app/mcp-connectors'

// The tabs, in order. `feature` is the feature a viewer needs to see the tab.
// `labelKey` is under the `chats` namespace.
export const CONNECTION_TABS = Object.freeze([
  { id: 'accounts', to: CONNECTIONS_PATH, labelKey: 'connectionsTabs.accounts', feature: 'connectors' },
  { id: 'messaging', to: `${CONNECTIONS_PATH}?tab=messaging`, labelKey: 'connectionsTabs.messaging', feature: 'connectors' },
  { id: 'mcp', to: MCP_PATH, labelKey: 'connectionsTabs.mcp', feature: 'chats' },
])

// The tabs this viewer may open.
export function visibleConnectionTabs(hasFeature) {
  return CONNECTION_TABS.filter((t) => !t.feature || hasFeature(t.feature))
}

// The tab of the connectors page for its query. An unknown or missing tab is
// Accounts. A link with ?kind=<kind> (an older link to one kind) opens the tab
// that holds the kind, which the page works out from the kinds (tabOfKind).
export function tabOfSearch(search) {
  const q = new URLSearchParams(search || '')
  return q.get('tab') === 'messaging' ? 'messaging' : 'accounts'
}

// A kind belongs to Accounts when it needs no agent: it only holds the
// credentials of an account that tools use. A kind that needs an agent (a
// bot that answers messages) belongs to Messaging.
export function isAccountKind(k) {
  return Boolean(k?.tool_only || k?.agent_optional)
}

export function tabOfKind(k) {
  return isAccountKind(k) ? 'accounts' : 'messaging'
}

// The groups and the waiting bindings for one tab. `groups` come from
// groupConnectors. A binding whose kind no running add-on serves has no kind
// to decide by, so it is listed on Messaging, where the bots were before.
export function forTab(tab, groups, waiting, kinds) {
  const byKind = new Map((kinds || []).map((k) => [`${k.addon}/${k.kind}`, k]))
  const mine = (groups || []).filter((g) => tabOfKind(byKind.get(g.key)) === tab)
  return { groups: mine, waiting: tab === 'messaging' ? (waiting || []) : [] }
}

// The tab that holds ?kind=<kind>, or '' when the query names no kind that is
// known. The page then moves to that tab.
export function tabOfKindParam(search, kinds) {
  const kind = new URLSearchParams(search || '').get('kind')
  if (!kind) return ''
  const k = (kinds || []).find((x) => x.kind === kind)
  return k ? tabOfKind(k) : ''
}
