// The old chat page became Home, the front door where a chat starts, and the
// old agent-jobs pages became the triggers. The pages of the chats that moved
// back to the agents, skills and triggers addresses redirect too. This is
// where an old address opens now. The query string and the hash are kept. The
// model of /app/chat/<model> travels as ?model=<id> and Home selects it.
export function chatsRedirect(pathname, search = '', hash = '') {
  const path = pathname.replace(/\/+$/, '')
  const params = new URLSearchParams(search)
  const model = /^\/app\/chat\/([^/]+)$/.exec(path)
  if (model) {
    // A stray % is not an error worth a crash: keep the name as it was typed.
    let name = model[1]
    try { name = decodeURIComponent(name) } catch { /* not valid encoding */ }
    params.set('model', name)
  }
  let target = '/app'
  if (/^\/app\/chats\/agents(\/|$)/.test(path)) target = '/app/agents'
  else if (/^\/app\/(chats\/triggers|agent-jobs)(\/|$)/.test(path)) target = '/app/triggers'
  else if (/^\/app\/chats\/skills(\/|$)/.test(path)) target = '/app/skills'
  const query = params.toString()
  return `${target}${query ? `?${query}` : ''}${hash}`
}

// One row per old route of the spec's migration table that changes or keeps
// its path. `kind` is 'same' (no redirect), 'chats' (a page of the chats),
// 'studio-tab' (a Studio tab) or 'addon-page' (a page of an add-on).
export const MIGRATION_ROUTES = Object.freeze([
  { from: '/app/chat', to: '/app', kind: 'chats' },
  { from: '/app/chats/agents', to: '/app/agents', kind: 'chats' },
  { from: '/app/chats/triggers', to: '/app/triggers', kind: 'chats' },
  { from: '/app/chats/skills', to: '/app/skills', kind: 'chats' },
  { from: '/app/agent-jobs', to: '/app/triggers', kind: 'chats' },
  { from: '/app/agents', to: '/app/agents', kind: 'same' },
  { from: '/app/skills', to: '/app/skills', kind: 'same' },
  { from: '/app/models', to: '/app/models', kind: 'same' },
  { from: '/app/settings', to: '/app/settings', kind: 'same' },

])

// /app/chats without an id. Opened from inside the app with navigation state
// (a hand-off: Home's first message, an agent, a draft) it is the empty chat,
// and the chat page shows it. Opened as an address (a bookmark, an old link, a
// reload of a page that had no state) it goes to Home, the front door, with
// the query and the hash: Home selects the ?model= it names. Returns where to
// go, or '' to stay.
export function bareChatsRedirect(pathname, search = '', hash = '', state = null) {
  if (pathname.replace(/\/+$/, '') !== '/app/chats') return ''
  if (state && typeof state === 'object') return ''
  return `/app${search}${hash}`
}
