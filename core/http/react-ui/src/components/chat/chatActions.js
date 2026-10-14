// The slash actions on the Chat composer. They are the same shape as Home's
// (see components/home/homeActions.js) and every one has a function on the page
// today. `needs` hides an action the person cannot use right now.

export const CHAT_SLASH_GROUPS = ['chat', 'view']

// The reasoning_effort values the API accepts. An empty choice sends none.
export const REASONING_EFFORTS = ['none', 'minimal', 'low', 'medium', 'high']

export const CHAT_SLASH_ACTIONS = [
  { id: 'model', cmd: '/model', icon: 'cube', group: 'chat', needs: 'models' },
  { id: 'new', cmd: '/new', icon: 'plus', group: 'chat' },
  { id: 'chats', cmd: '/chats', icon: 'history', group: 'chat' },
  { id: 'assistant', cmd: '/assistant', icon: 'user-shield', group: 'chat', needs: 'admin' },
  // /reasoning comes first so that Enter on "/reasoning" picks it, not "Reasoning effort".
  { id: 'reasoning', cmd: '/reasoning', icon: 'eye-off', group: 'chat' },
  { id: 'effort', cmd: '/effort', icon: 'brain', group: 'chat' },
  { id: 'addon-tools', cmd: '/addons', icon: 'puzzle', group: 'chat', needs: 'addon-tools' },
  { id: 'canvas', cmd: '/canvas', icon: 'columns', group: 'view' },
  { id: 'find', cmd: '/find', icon: 'search', group: 'view', needs: 'thread' },
  { id: 'settings', cmd: '/settings', icon: 'sliders', group: 'view' },
  { id: 'export', cmd: '/export', icon: 'export', group: 'view', needs: 'thread' },
  { id: 'clear', cmd: '/clear', icon: 'eraser', group: 'view', needs: 'thread' },
]

// `ctx`: { isAdmin, hasModels, hasThread, hasAddonTools }
export function availableChatActions(ctx) {
  return CHAT_SLASH_ACTIONS.filter(a => {
    if (a.needs === 'admin') return ctx.isAdmin
    if (a.needs === 'models') return ctx.hasModels
    if (a.needs === 'thread') return ctx.hasThread
    if (a.needs === 'addon-tools') return !!ctx.hasAddonTools
    return true
  })
}
