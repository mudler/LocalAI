// Helpers for the create and edit flow: starting points, the list of what
// changed against the saved version, secret masking, and the one-line summary
// of a section.

// Starting points are plain presets of fields every saved agent has. They
// fill the form; nothing is saved until the person saves.
export const AGENT_TEMPLATES = [
  {
    id: 'researcher',
    name: 'research-assistant',
    description: 'Searches, reads sources and writes short briefs.',
    instructions: 'You are a careful research assistant. Search first, read at least two sources, then summarise in plain language. Cite every claim with a numbered source. If a source cannot be read, say so.',
  },
  {
    id: 'reviewer',
    name: 'code-reviewer',
    description: 'Reviews a change and lists what to fix first.',
    instructions: 'You review code changes. Read the diff, find bugs and unclear code, and list the most important problems first. Quote the line you mean. Do not rewrite the change.',
  },
  {
    id: 'digest',
    name: 'daily-digest',
    description: 'Collects what is new on a topic and summarises it.',
    instructions: 'You collect what is new on the topics you are given and write a short digest: one line per item, newest first, each with its source.',
  },
  {
    id: 'blank',
    name: '',
    description: '',
    instructions: '',
  },
]

const SECRET_KEY = /(token|key|secret|password|passwd|authorization)/i

// Show a config without its secrets. The saved value is untouched.
export function maskSecrets(value, keyName = '') {
  if (Array.isArray(value)) return value.map(v => maskSecrets(v, keyName))
  if (value && typeof value === 'object') {
    const out = {}
    for (const [k, v] of Object.entries(value)) out[k] = maskSecrets(v, k)
    return out
  }
  if (typeof value === 'string') {
    if (value && SECRET_KEY.test(keyName)) return '••••••'
    // A config value that holds JSON (connector config, MCP servers) may hold
    // secrets one level down.
    if (/^[{[]/.test(value.trim())) {
      try { return JSON.stringify(maskSecrets(JSON.parse(value), keyName)) } catch { /* plain text */ }
    }
  }
  return value
}

// A config value that is JSON text (the MCP servers) is the same value however
// it is spaced, so compare what it says, not how it is written.
function normal(v) {
  if (typeof v === 'string' && /^\s*[{[]/.test(v)) {
    try { return JSON.parse(v) } catch { /* plain text */ }
  }
  return v ?? null
}

function same(a, b) {
  return JSON.stringify(normal(a)) === JSON.stringify(normal(b))
}

function empty(v) {
  return v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0) || v === false
}

// The fields that differ, in the order of `after`, then fields only in
// `before`. An empty value and a missing one are the same thing.
export function diffConfig(before, after) {
  const b = before || {}
  const a = after || {}
  const keys = [...Object.keys(a), ...Object.keys(b).filter(k => !(k in a))]
  const out = []
  for (const key of keys) {
    if (empty(a[key]) && empty(b[key])) continue
    if (same(a[key], b[key])) continue
    out.push({ key, before: b[key], after: a[key] })
  }
  return out
}

export function displayValue(value) {
  if (value === undefined || value === null || value === '') return ''
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}

// "name, description" style line for a folded section: the first few fields
// that hold something. A switch that is on reads as its label.
export function summarise(fields, form, max = 3) {
  const parts = []
  for (const f of fields) {
    const v = form[f.name]
    if (v === undefined || v === null || v === '' || v === false) continue
    if (f.type === 'checkbox') { parts.push(f.label); continue }
    if (f.type === 'password') continue
    const text = String(v).replace(/\s+/g, ' ').trim()
    if (text) parts.push(text.length > 60 ? `${text.slice(0, 59).trimEnd()}…` : text)
    if (parts.length >= max) break
  }
  return parts.slice(0, max).join(', ')
}

// ---- Export and import -----------------------------------------------------

// Export format version of a saved agent. The file holds the definition only: no
// id, no dates, no secrets (a connector is named by its id, its token stays on
// the server).
export const EXPORT_KIND = 'localai.chats.agent'
export const EXPORT_VERSION = 1

const LISTS = ['mcp_servers', 'addons', 'collections', 'skills']

export function exportAgent(agent) {
  const out = { kind: EXPORT_KIND, version: EXPORT_VERSION, name: agent.name || '', description: agent.description || '', instructions: agent.instructions || '', model: agent.model || '' }
  for (const k of LISTS) out[k] = [...(agent[k] || [])]
  return out
}

const str = (v) => (typeof v === 'string' ? v : '')
const names = (v) => (Array.isArray(v) ? v.map(x => (typeof x === 'string' ? x : x?.name)).filter(x => typeof x === 'string' && x.trim()) : [])

// Reads an export file into the fields of the form. It accepts the export of a
// saved agent and, best effort, the JSON export of the old agents (name,
// description, model, system_prompt, mcp_servers by URL, selected_skills,
// enable_kb). What has no counterpart in a saved agent (actions, connectors,
// filters, dynamic prompts, schedules, memory switches, STDIO servers, tokens)
// is not imported; `dropped` names those keys, so the page can say so.
export function importAgent(data) {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return null
  const legacy = !data.kind && ('system_prompt' in data || 'selected_skills' in data || 'actions' in data || 'enable_kb' in data)
  if (data.kind && data.kind !== EXPORT_KIND) return null
  const agent = {
    name: str(data.name).trim(),
    description: str(data.description).trim(),
    instructions: str(legacy ? data.system_prompt : data.instructions),
    model: str(data.model).trim(),
    mcp_servers: legacy ? [] : names(data.mcp_servers),
    addons: legacy ? [] : names(data.addons),
    collections: legacy ? [] : names(data.collections),
    skills: legacy ? names(data.selected_skills) : names(data.skills),
  }
  const known = new Set(['kind', 'version', 'name', 'description', 'instructions', 'model', 'mcp_servers', 'addons', 'collections', 'skills', 'system_prompt', 'selected_skills'])
  const dropped = Object.keys(data).filter(k => !known.has(k) && !empty(data[k]))
  if (legacy && Array.isArray(data.mcp_servers) && data.mcp_servers.length) dropped.push('mcp_servers')
  if (!agent.name && !agent.instructions && !agent.model) return null
  return { agent, dropped: [...new Set(dropped)], legacy }
}
