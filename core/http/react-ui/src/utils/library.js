// The Library: memory collections, and where they are used.
//
// Nothing here is guessed. A saved agent of the chats lists the collections and
// the skills it may use (`collections`, `skills`): those lists are the only
// pointers. A chat without an agent counts as no user.

// Characters divided by four. It is an estimate and every screen says so.
const CHARS_PER_TOKEN = 4

export function estimateTokens(text) {
  const n = typeof text === 'string' ? text.length : 0
  return n === 0 ? 0 : Math.max(1, Math.round(n / CHARS_PER_TOKEN))
}

// The skills an agent may use. The list is exact: an empty list grants none.
export function agentSkills(config) {
  const names = Array.isArray(config?.skills) ? config.skills.filter(Boolean) : []
  return { on: names.length > 0, all: false, names }
}

// The index of the skills is in the prompt; the model reads a skill when a task
// matches, so a skill adds nothing up front.
export function skillsMode() {
  return 'tools'
}

// Collections are searched by the model when it asks, never on every message.
export function kbSearchesEveryMessage() {
  return false
}

export function kbResults() {
  return 5
}

// Agents that may use a skill. `agents` is [{ name, config }].
export function skillUsers(agents, skill) {
  return agents.filter(agent => agentSkills(agent.config).names.includes(skill)).map(agent => ({ name: agent.name, all: false }))
}

// Agents that may search a collection.
export function collectionUsers(agents, collection) {
  return agents
    .filter(agent => (agent.config?.collections || []).includes(collection))
    .map(agent => ({ name: agent.name, all: false }))
}

// Config after granting a collection. Returns null when it is granted already.
export function withCollection(config, collection) {
  const have = config?.collections || []
  return have.includes(collection) ? null : { ...config, collections: [...have, collection] }
}

// Config after taking a collection away. Returns null when it was not granted.
export function withoutCollection(config, collection) {
  const have = config?.collections || []
  return have.includes(collection) ? { ...config, collections: have.filter(c => c !== collection) } : null
}

// A skill adds no tokens before the model asks for it.
export function skillLoadTokens() {
  return 0
}

export function sumTokens(items) {
  return items.reduce((n, item) => n + item, 0)
}

// "research-assistant +2" from a list of names.
export function usedByParts(users, limit = 1) {
  return { shown: users.slice(0, limit), more: Math.max(0, users.length - limit) }
}

// The text of a passage from a search result, whatever shape the server used.
export function passageText(result) {
  if (typeof result === 'string') return result
  return result?.content || result?.text || ''
}

export function passageScore(result) {
  if (typeof result?.similarity === 'number') return result.similarity
  if (result?.score != null && Number.isFinite(Number(result.score))) return Number(result.score)
  return null
}

// Where a passage came from, when the server says.
export function passageSource(result) {
  const m = result?.metadata
  if (!m || typeof m !== 'object') return ''
  return m.filename || m.file || m.source || m.url || m.title || ''
}

// A source entry is a URL string or { url, update_interval (minutes), last_update }.
export function normaliseSource(source) {
  if (typeof source === 'string') return { url: source, interval: 0, lastUpdate: null }
  const last = source?.last_update ? Date.parse(source.last_update) : NaN
  return {
    url: source?.url || '',
    interval: Number(source?.update_interval) || 0,
    // The zero time Go sends for "never" is year 1.
    lastUpdate: Number.isFinite(last) && last > Date.parse('2000-01-01') ? last : null,
  }
}

export function entryName(entry) {
  return typeof entry === 'string' ? entry : (entry?.name || entry?.filename || JSON.stringify(entry))
}
