// The folders of the home that LocalAI manages, as the files list marks them
// (entry.managed: 'localai' for LocalAI/ and the settings files in it, and for
// the folders LocalAI creates itself 'chats', 'memory', 'pages' and 'skills').
// The server decides which folders these are; nothing here looks at a name. A
// value this file does not know is still managed. Pure helpers, so the node
// unit tests load them.

// The order of the managed folders under the user's own files; a value not
// listed goes last, in the order the server gave.
const ORDER = { localai: 0, chats: 1, memory: 2, pages: 3, skills: 4 }

// The page of the product that works on what a managed folder holds, and the
// features the person needs to open it. The folder stays openable in Files.
const OWN_PAGE = {
  memory: { to: '/app/account?tab=memory', features: ['chats'] },
  pages: { to: '/app/pages', features: ['chats', 'pages'] },
  skills: { to: '/app/skills', features: ['chats'] },
}

// pageOf returns { to, features } for a managed folder that has its own page
// in the product, or null.
export function pageOf(entry) {
  if (!entry?.managed || entry.kind !== 'dir') return null
  return OWN_PAGE[entry.managed] || null
}

// The settings files of LocalAI/ and what a user loses without each one: the
// key of the sentence in the workspace namespace (managed.loses.*).
const SETTINGS_LOSS = {
  'persona.json': 'persona',
  'user.json': 'user',
  'shortcuts.json': 'shortcuts',
  'home.json': 'home',
  'ui.json': 'ui',
}

// splitEntries puts the user's own files first, in the order the server gave
// (by name), and the managed folders after them, LocalAI/ then Chats/. Only
// the top of the home has managed folders; inside LocalAI/ every entry is a
// settings file, so nothing is split there.
export function splitEntries(entries, path = '/') {
  const list = entries || []
  if (path !== '/') return { own: list, managed: [] }
  const own = []
  const managed = []
  for (const e of list) {
    if (e?.managed && e.kind === 'dir') managed.push(e)
    else own.push(e)
  }
  // Array.sort is stable, so values with the same rank keep the server order.
  managed.sort((a, b) => (ORDER[a.managed] ?? 9) - (ORDER[b.managed] ?? 9))
  return { own, managed }
}

// isSettingsFolder says whether path is LocalAI/ itself, whose header shows
// what the folder is for.
export function isSettingsFolder(path) {
  return path === '/LocalAI'
}

// guardOf says what a delete or a rename of entry breaks, as the keys of the
// confirmation (managed.loses.<what>), or null when no extra confirmation is
// needed. A file in LocalAI/ that LocalAI does not read needs none.
export function guardOf(entry) {
  if (!entry?.managed) return null
  if (entry.path === '/LocalAI') return { what: 'settings', name: 'LocalAI' }
  if (entry.path === '/Chats') return { what: 'chats', name: 'Chats' }
  const m = /^\/LocalAI\/([^/]+)$/.exec(entry.path || '')
  if (m && SETTINGS_LOSS[m[1]]) return { what: SETTINGS_LOSS[m[1]], name: `LocalAI/${m[1]}` }
  return null
}
