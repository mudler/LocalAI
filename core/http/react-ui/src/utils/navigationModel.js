// Pure rules of the primary sidebar items. No React, no imports, so a node
// test drives it directly.

// Chats, Files, Agents, MCP connectors, Memory, Models and Add-ons. Home and the
// Operate hub keep the place they have today (see hubConfig.js and
// destinations.js). Settings is a tab of the Operate hub, not a primary item.
// `match` lists other paths that light the item up: the pages that belong to it.
export const primaryDestinations = Object.freeze([
  // Chats the server keeps. Shown to a viewer who holds the chats feature.
  { path: '/app/chats', icon: 'chat', labelKey: 'items.chats', feature: 'chats', exclude: ['/app/chats/connectors', '/app/chats/conversion'] },
  // The user's own files: a workspace of folders, with a Trash and history.
  { path: '/app/files', icon: 'folder', labelKey: 'items.files', feature: 'chats' },
  // Saved agents, with what runs them by itself: triggers and messaging
  // connectors, and the skills they use.
  {
    path: '/app/agents', icon: 'robot', labelKey: 'items.agents', feature: 'agents',
    match: ['/app/triggers', '/app/skills', '/app/chats/connectors'],
  },
  // The user's own MCP servers, which chats and agents can use.
  { path: '/app/mcp-connectors', icon: 'plug', labelKey: 'items.mcpConnectors', feature: 'chats' },
  // Collections the chats search. Off when the server answers collections false.
  { path: '/app/collections', icon: 'database', labelKey: 'items.memory', feature: 'collections', requiresCollections: true },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  // Shown to a viewer who holds the addons feature or sees a built-in tile.
  { path: '/app/addons', icon: 'puzzle', labelKey: 'items.addons', addonsEntry: true },
])

// `features` is the /api/features map. An absent `addons` key (an older server)
// counts as on; only an explicit false hides the Add-ons entry.
export function visiblePrimary(items, { isAdmin, hasFeature, hasBuiltin, features }) {
  return items.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.requiresCollections && features?.collections === false) return false
    if (item.feature && !hasFeature(item.feature)) return false
    if (item.addonsEntry && features?.addons === false) return false
    if (item.addonsEntry && !isAdmin && !hasFeature('addons') && !hasBuiltin) return false
    return true
  })
}

function under(pathname, p) {
  return pathname === p || pathname.startsWith(p + '/')
}

// Whether the item is the current place: its own path or one of its `match`
// paths, unless the path belongs to another item (`exclude`).
export function primaryActive(item, pathname) {
  if ((item.exclude || []).some(p => under(pathname, p))) return false
  return under(pathname, item.path) || (item.match || []).some(p => under(pathname, p))
}
