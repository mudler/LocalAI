// The slash actions on the Chat composer. They are the same shape as Home's
// (see components/home/homeActions.js) and every one has a function on the page
// today. `needs` hides an action the person cannot use right now.

export const CHAT_SLASH_GROUPS = ['chat', 'view']

export const CHAT_SLASH_ACTIONS = [
  { id: 'model', cmd: '/model', icon: 'cube', group: 'chat', needs: 'models' },
  { id: 'new', cmd: '/new', icon: 'plus', group: 'chat' },
  { id: 'chats', cmd: '/chats', icon: 'history', group: 'chat' },
  { id: 'assistant', cmd: '/assistant', icon: 'user-shield', group: 'chat', needs: 'admin' },
  { id: 'canvas', cmd: '/canvas', icon: 'columns', group: 'view' },
  { id: 'find', cmd: '/find', icon: 'search', group: 'view', needs: 'thread' },
  { id: 'settings', cmd: '/settings', icon: 'sliders', group: 'view' },
  { id: 'export', cmd: '/export', icon: 'export', group: 'view', needs: 'thread' },
  { id: 'clear', cmd: '/clear', icon: 'eraser', group: 'view', needs: 'thread' },
]

// `ctx`: { isAdmin, hasModels, hasThread }
export function availableChatActions(ctx) {
  return CHAT_SLASH_ACTIONS.filter(a => {
    if (a.needs === 'admin') return ctx.isAdmin
    if (a.needs === 'models') return ctx.hasModels
    if (a.needs === 'thread') return ctx.hasThread
    return true
  })
}
