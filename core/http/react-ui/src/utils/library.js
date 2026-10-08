// The Library: skills and memory collections, and where they are used.
//
// Nothing here is guessed. An agent reads skills through its own config
// (`enable_skills`, `selected_skills`, `skills_mode`) and reads one collection,
// the one that carries the agent's own name (`enable_kb`, `kb_mode`,
// `kb_results`). The Chat page reads neither, so Chat never counts as a user.

// Characters divided by four. It is an estimate and every screen says so.
const CHARS_PER_TOKEN = 4

export function estimateTokens(text) {
  const n = typeof text === 'string' ? text.length : 0
  return n === 0 ? 0 : Math.max(1, Math.round(n / CHARS_PER_TOKEN))
}

function selectedOf(config) {
  return Array.isArray(config?.selected_skills) ? config.selected_skills.filter(Boolean) : []
}

// The skills an agent loads. An empty selection means every skill, which is
// how the server filters them. `names` may hold a name whose skill was deleted.
export function agentSkills(config, allNames = []) {
  if (!config || !config.enable_skills) return { on: false, all: false, names: [] }
  const selected = selectedOf(config)
  if (selected.length === 0) return { on: true, all: true, names: [...allNames] }
  return { on: true, all: false, names: selected }
}

// 'prompt' puts the whole skill in the system prompt, 'tools' only lets the
// model ask for it, 'both' does both.
export function skillsMode(config) {
  return config?.skills_mode || 'prompt'
}

// 'auto_search' searches on every message, 'tools' leaves it to the model.
export function kbMode(config) {
  if (config?.kb_mode) return config.kb_mode
  if (config?.kb_auto_search && config?.kb_as_tools) return 'both'
  if (config?.kb_as_tools) return 'tools'
  return 'auto_search'
}

export function kbSearchesEveryMessage(config) {
  const mode = kbMode(config)
  return !!config?.enable_kb && (mode === 'auto_search' || mode === 'both')
}

export function kbResults(config) {
  const n = Number(config?.kb_results)
  return Number.isFinite(n) && n > 0 ? n : 5
}

// Agents that load a skill. `agents` is [{ name, config }].
export function skillUsers(agents, skill, allNames) {
  const out = []
  for (const agent of agents) {
    const s = agentSkills(agent.config, allNames)
    if (s.names.includes(skill)) out.push({ name: agent.name, all: s.all })
  }
  return out
}

// A collection is read by the agent that has the same name, when its
// knowledge base is on. The agent config holds no other pointer.
export function collectionUsers(agents, collection) {
  return agents
    .filter(agent => agent.name === collection && agent.config?.enable_kb)
    .map(agent => ({ name: agent.name, all: false }))
}

// Config after adding a skill to an agent. Returns null when the agent
// already loads it.
export function withSkill(config, skill, allNames) {
  const s = agentSkills(config, allNames)
  if (s.names.includes(skill)) return null
  const base = selectedOf(config)
  return { ...config, enable_skills: true, selected_skills: [...(s.on ? s.names : base), skill] }
}

// Config after taking a skill away. An empty selection would mean "every
// skill", so removing the last one switches skills off instead.
export function withoutSkill(config, skill, allNames) {
  const s = agentSkills(config, allNames)
  if (!s.names.includes(skill)) return null
  const rest = s.names.filter(n => n !== skill)
  if (rest.length === 0) return { config: { ...config, enable_skills: false, selected_skills: [] }, switchedOff: true }
  return { config: { ...config, selected_skills: rest }, switchedOff: false }
}

export function withKb(config) {
  return config?.enable_kb ? null : { ...config, enable_kb: true }
}

export function withoutKb(config) {
  return config?.enable_kb ? { ...config, enable_kb: false } : null
}

// Estimated tokens a skill adds to every message of an agent. In 'tools' mode
// the content is read only when the model asks for it, so nothing is added up
// front.
export function skillLoadTokens(skill, config) {
  const mode = skillsMode(config)
  return mode === 'tools' ? 0 : estimateTokens(skill?.content || '')
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
