// Helpers for the create and edit flow: starting points, the list of what
// changed against the saved version, secret masking, and the one-line summary
// of a section.

// Starting points are plain presets of fields every agent config has. They
// fill the form; nothing is saved until the person saves.
export const AGENT_TEMPLATES = [
  {
    id: 'researcher',
    name: 'research-assistant',
    description: 'Searches, reads sources and writes short briefs.',
    system_prompt: 'You are a careful research assistant. Search first, read at least two sources, then summarise in plain language. Cite every claim with a numbered source. If a source cannot be read, say so.',
  },
  {
    id: 'reviewer',
    name: 'code-reviewer',
    description: 'Reviews a change and lists what to fix first.',
    system_prompt: 'You review code changes. Read the diff, find bugs and unclear code, and list the most important problems first. Quote the line you mean. Do not rewrite the change.',
  },
  {
    id: 'digest',
    name: 'daily-digest',
    description: 'Collects what is new on a topic and summarises it.',
    system_prompt: 'You collect what is new on the topics you are given and write a short digest: one line per item, newest first, each with its source.',
  },
  {
    id: 'blank',
    name: '',
    description: '',
    system_prompt: '',
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
