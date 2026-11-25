// Incognito chats. The server keeps an incognito chat in its memory only and
// never lists it (see the incognito docs); the page keeps it in component
// state and in this module, never in localStorage, sessionStorage or
// IndexedDB, so nothing of it outlives the tab.
//
// Every request the page makes while an incognito chat is open carries the
// marker header, so the speech and transcription requests of Live voice keep
// what the level allows too. The marker counts only for the web UI session:
// the server ignores it from an API key.

export const INCOGNITO_HEADER = 'X-LocalAI-Incognito'
export const MEMORY_OFF = 'memory-off'
export const ZERO_RETENTION = 'zero-retention'
export const INCOGNITO_LEVELS = [MEMORY_OFF, ZERO_RETENTION]

// The levels the server offers, lowest first, from the incognito field of
// GET /api/chats ({ policy, max_level }). None when the server forbids
// incognito or did not say.
export function incognitoLevels(setting) {
  if (!setting || setting.policy !== 'allow') return []
  const max = INCOGNITO_LEVELS.indexOf(setting.max_level)
  return INCOGNITO_LEVELS.slice(0, max < 0 ? INCOGNITO_LEVELS.length : max + 1)
}

// The level a new chat starts with: memory-off when the server offers it.
export function defaultIncognitoLevel(setting) {
  return incognitoLevels(setting)[0] || ''
}

// A level the server offers, or the default one.
export function offeredLevel(setting, level) {
  return incognitoLevels(setting).includes(level) ? level : defaultIncognitoLevel(setting)
}

// What a level does not allow on the page: a zero-retention chat takes no
// attachment (it would be saved in the person's files), and no incognito chat
// can be forked or moved to a project.
export function incognitoBlocks(level) {
  return {
    attach: level === ZERO_RETENTION,
    fork: Boolean(level),
    project: Boolean(level),
  }
}

// The level of the open conversation, in memory only.
let marker = ''
const chats = new Map()

// setIncognitoMarker sets the level that every request carries; '' clears it.
export function setIncognitoMarker(level) {
  marker = INCOGNITO_LEVELS.includes(level) ? level : ''
}

export function incognitoMarker() {
  return marker
}

// incognitoHeaders adds the marker to headers (a plain object) when one is
// set. It returns a new object and leaves the argument alone.
export function incognitoHeaders(headers = {}) {
  if (!marker) return headers
  return { ...headers, [INCOGNITO_HEADER]: marker }
}

// The incognito chats this tab started, by id.
export function rememberIncognitoChat(id, level) {
  if (id && INCOGNITO_LEVELS.includes(level)) chats.set(id, level)
}

export function forgetIncognitoChat(id) {
  chats.delete(id)
}

export function isIncognitoChat(id) {
  return Boolean(id) && chats.has(id)
}

// The level of an incognito chat of the tab, '' for any other chat.
export function incognitoChatLevel(id) {
  return chats.get(id) || ''
}
