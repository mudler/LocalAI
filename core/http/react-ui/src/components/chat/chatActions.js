// The slash actions on the Chat composer. They are the same shape as Home's
// (see components/home/homeActions.js) and every one has a function on the page
// today. `needs` hides an action the person cannot use right now.

export const CHAT_SLASH_GROUPS = ['chat', 'view']

export const CHAT_SLASH_ACTIONS = [
  { id: 'model', cmd: '/model', icon: 'cube', group: 'chat', needs: 'models' },
  { id: 'new', cmd: '/new', icon: 'plus', group: 'chat' },
  { id: 'chats', cmd: '/chats', icon: 'history', group: 'chat' },
  { id: 'assistant', cmd: '/assistant', icon: 'user-shield', group: 'chat', needs: 'assistant' },
  { id: 'find', cmd: '/find', icon: 'search', group: 'view', needs: 'thread' },
  { id: 'settings', cmd: '/settings', icon: 'sliders', group: 'view' },
  { id: 'export', cmd: '/export', icon: 'export', group: 'view', needs: 'thread' },
  { id: 'delete', cmd: '/delete', icon: 'trash', group: 'view', needs: 'chat' },
]

// `ctx`: { assistant (an admin, with the assistant on), hasModels, hasThread, hasChat }
export function availableChatActions(ctx) {
  return CHAT_SLASH_ACTIONS.filter(a => {
    if (a.needs === 'assistant') return !!ctx.assistant
    if (a.needs === 'models') return ctx.hasModels
    if (a.needs === 'thread') return ctx.hasThread
    if (a.needs === 'chat') return !!ctx.hasChat
    return true
  })
}

// A file chip in the transcript (a step that saved a file) asks the chat page
// to open the preview pane at that file. detail: { path }.
export const OPEN_CHAT_FILE_EVENT = 'localai:open-chat-file'

export function openChatFile(path) {
  if (!path || typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(OPEN_CHAT_FILE_EVENT, { detail: { path } }))
}
