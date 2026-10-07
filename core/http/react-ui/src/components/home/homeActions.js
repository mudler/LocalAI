// The slash actions on the Home command bar. Every entry has a destination in
// the product today; the page decides what each one does (navigate, open the
// model list, start a chat). `needs` hides an action the person cannot use:
// the admin pages, the assistant, or stopping a model when none is loaded.

export const SLASH_GROUPS = ['chat', 'models', 'go']

export const SLASH_ACTIONS = [
  { id: 'model', cmd: '/model', icon: 'cube', group: 'chat', needs: 'models' },
  { id: 'new', cmd: '/new', icon: 'plus', group: 'chat' },
  { id: 'chat', cmd: '/chat', icon: 'chat', group: 'chat' },
  { id: 'assistant', cmd: '/assistant', icon: 'user-shield', group: 'chat', needs: 'assistant' },
  { id: 'gallery', cmd: '/gallery', icon: 'download', group: 'models', needs: 'admin' },
  { id: 'installed', cmd: '/installed', icon: 'boxes', group: 'models', needs: 'admin' },
  { id: 'import', cmd: '/import', icon: 'upload', group: 'models', needs: 'admin' },
  { id: 'stop', cmd: '/stop', icon: 'stop', group: 'models', needs: 'loaded' },
  { id: 'studio', cmd: '/studio', icon: 'palette', group: 'go' },
  { id: 'settings', cmd: '/settings', icon: 'settings', group: 'go', needs: 'admin' },
  { id: 'docs', cmd: '/docs', icon: 'book', group: 'go' },
]

// `ctx`: { isAdmin, assistantAvailable, hasModels, loadedCount }
export function availableActions(ctx) {
  return SLASH_ACTIONS.filter(a => {
    if (a.needs === 'admin') return ctx.isAdmin
    if (a.needs === 'assistant') return ctx.isAdmin && ctx.assistantAvailable
    if (a.needs === 'models') return ctx.hasModels
    if (a.needs === 'loaded') return ctx.isAdmin && ctx.loadedCount > 0
    return true
  })
}

// `query` is what follows the slash, lowercase. A command matches when its name
// starts with the query or its label contains it. Order stays grouped.
export function filterActions(actions, query, labelOf) {
  const q = query.trim()
  const matched = q
    ? actions.filter(a => a.cmd.slice(1).startsWith(q) || labelOf(a).toLowerCase().includes(q))
    : actions
  return SLASH_GROUPS.flatMap(g => matched.filter(a => a.group === g))
}
