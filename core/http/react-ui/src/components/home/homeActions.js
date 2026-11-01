// The slash actions on the Home command bar. Every entry has a destination in
// the product today; the page decides what each one does (navigate, open the
// model list, start a chat). `needs` hides an action the person cannot use:
// the admin pages, stopping a model when none is loaded, the LocalAI
// assistant when it is off, or
// Studio while that add-on is not installed.

export const SLASH_GROUPS = ['chat', 'models', 'go']

export const SLASH_ACTIONS = [
  { id: 'model', cmd: '/model', icon: 'cube', group: 'chat', needs: 'models' },
  { id: 'new', cmd: '/new', icon: 'plus', group: 'chat' },
  { id: 'chat', cmd: '/chat', icon: 'chat', group: 'chat' },
  { id: 'assistant', cmd: '/assistant', icon: 'robot', group: 'chat', needs: 'assistant' },
  { id: 'gallery', cmd: '/gallery', icon: 'download', group: 'models', needs: 'admin' },
  { id: 'installed', cmd: '/installed', icon: 'boxes', group: 'models', needs: 'admin' },
  { id: 'import', cmd: '/import', icon: 'upload', group: 'models', needs: 'admin' },
  { id: 'stop', cmd: '/stop', icon: 'stop', group: 'models', needs: 'loaded' },
  { id: 'agents', cmd: '/agents', icon: 'robot', group: 'go', needs: 'chats' },
  { id: 'mcp', cmd: '/mcp', icon: 'plug', group: 'go', needs: 'chats' },
  { id: 'memory', cmd: '/memory', icon: 'database', group: 'go', needs: 'collections' },
  { id: 'studio', cmd: '/studio', icon: 'palette', group: 'go', needs: 'studio' },
  // The same command for an administrator while Studio is not installed.
  { id: 'studioInstall', cmd: '/studio', icon: 'palette', group: 'go', needs: 'studio-missing' },
  { id: 'settings', cmd: '/settings', icon: 'settings', group: 'go', needs: 'admin' },
  { id: 'docs', cmd: '/docs', icon: 'book', group: 'go' },
]

// `ctx`: { isAdmin, hasModels, loadedCount, studioInstalled, assistantAvailable,
// hasChats, hasCollections }
export function availableActions(ctx) {
  return SLASH_ACTIONS.filter(a => {
    if (a.needs === 'admin') return ctx.isAdmin
    if (a.needs === 'models') return ctx.hasModels
    if (a.needs === 'loaded') return ctx.isAdmin && ctx.loadedCount > 0
    // The server says whether the LocalAI assistant is on; only an admin may use it.
    if (a.needs === 'assistant') return ctx.isAdmin && ctx.assistantAvailable === true
    if (a.needs === 'studio') return ctx.studioInstalled === true
    if (a.needs === 'studio-missing') return ctx.isAdmin && ctx.studioInstalled === false
    if (a.needs === 'chats') return ctx.hasChats === true
    if (a.needs === 'collections') return ctx.hasCollections === true
    return true
  })
}

// `query` is what follows the slash, lowercase. A command matches when its name
// starts with the query or its label contains it. Order stays grouped.
export function filterActions(actions, query, labelOf, groups = SLASH_GROUPS) {
  const q = query.trim()
  const matched = q
    ? actions.filter(a => a.cmd.slice(1).startsWith(q) || labelOf(a).toLowerCase().includes(q))
    : actions
  return groups.flatMap(g => matched.filter(a => a.group === g))
}
