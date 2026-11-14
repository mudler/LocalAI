// Pure helpers of the remote connections UI: what the form offers for each
// kind, which models start ticked, how a connection shows, and how a failed
// test reads. No React here, so tests/unit/connections.test.mjs can run it.

export const KINDS = ['localai', 'openai', 'anthropic']

export const PRESETS = [
  { id: 'openai', url: 'https://api.openai.com/v1' },
  { id: 'openrouter', url: 'https://openrouter.ai/api/v1' },
  { id: 'groq', url: 'https://api.groq.com/openai/v1' },
  { id: 'custom', url: '' },
]

// The name a connection gets until the person types another one.
const PRESET_NAMES = { openai: 'OpenAI', openrouter: 'OpenRouter', groq: 'Groq' }

export function presetFor(url) {
  const clean = String(url || '').trim().replace(/\/+$/, '')
  return PRESETS.find(p => p.url && p.url === clean)?.id || 'custom'
}

// A host without credentials, path or query: "api.example.com:8080".
export function hostOf(url) {
  const text = String(url || '').trim()
  if (!text) return ''
  // "user:pw@host" parses as a scheme with no host, so an empty host falls
  // through to the text rules below.
  try {
    const host = new URL(text).host
    if (host) return host
  } catch { /* not a full address */ }
  // Not a full address (no scheme). Drop what could carry a secret.
  return text.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').replace(/^[^@/]*@/, '').split(/[/?#]/)[0]
}

export function defaultName(kind, preset, url) {
  if (kind === 'anthropic') return 'Anthropic'
  if (kind === 'openai' && PRESET_NAMES[preset]) return PRESET_NAMES[preset]
  return hostOf(url)
}

// What the person must fill in before the test can run.
export function canTest({ kind, url, key }) {
  if (kind === 'anthropic') return key.trim() !== ''
  if (kind === 'openai') return url.trim() !== '' && key.trim() !== ''
  return url.trim() !== ''
}

// Anthropic needs only a key, so the form hides the address and sends none:
// the server uses the Anthropic address when it is empty.
export function testBody({ kind, url, key }) {
  if (kind === 'anthropic') return { kind, api_key: key.trim() }
  return { kind, url: url.trim(), api_key: key.trim() }
}

// A LocalAI remote starts with every model ticked. The cloud kinds list many
// models the person never asked for, so they start with none.
export function initialTicked(kind, models) {
  return kind === 'localai' ? models.map(m => m.id) : []
}

// The models the person ticked, in the order the remote listed them.
export function chosenModels(models, ticked) {
  const set = new Set(ticked)
  return models.filter(m => set.has(m.id)).map(m => ({ id: m.id, usecases: m.usecases || [] }))
}

export function filterModels(models, query) {
  const q = String(query || '').trim().toLowerCase()
  return q ? models.filter(m => m.id.toLowerCase().includes(q)) : models
}

// The steps a failed test can name, each with its words under
// connections.fail.<step>. An unknown step reads as the general failure.
export const FAIL_STEPS = ['invalid_url', 'unreachable', 'auth', 'no_models']
export function failStep(step) {
  return FAIL_STEPS.includes(step) ? step : 'other'
}

// The state of a connection as the card shows it.
export function statusOf(connection) {
  switch (connection?.status) {
    case 'ok': return 'ok'
    case 'auth_failed': return 'auth_failed'
    case 'unreachable': return 'unreachable'
    default: return 'never'
  }
}

export function needsAttention(connection) {
  const s = statusOf(connection)
  return s === 'auth_failed' || s === 'unreachable'
}

// connection id -> connection, for the Installed list.
export function connectionIndex(list) {
  const map = new Map()
  for (const c of Array.isArray(list) ? list : []) map.set(c.id, c)
  return map
}

// The sub line of a remote row: "backend · connection name", or the backend
// alone while the connection is not known (still loading, or removed).
export function remoteSubline(model, index, fallbackBackend) {
  const backend = model?.backend || fallbackBackend
  const name = index?.get(model?.connection)?.name
  return [backend, name].filter(Boolean).join(' · ')
}

// Seconds since an ISO time, or null when it does not parse.
export function ageSeconds(iso, now = Date.now()) {
  const at = Date.parse(iso || '')
  if (Number.isNaN(at)) return null
  return Math.max(0, Math.round((now - at) / 1000))
}

// A value and unit for Intl.RelativeTimeFormat, or null for "just now".
export function ageParts(iso, now = Date.now()) {
  const s = ageSeconds(iso, now)
  if (s == null) return undefined
  if (s < 60) return null
  const m = Math.floor(s / 60)
  if (m < 60) return { value: m, unit: 'minute' }
  const h = Math.floor(m / 60)
  if (h < 24) return { value: h, unit: 'hour' }
  return { value: Math.floor(h / 24), unit: 'day' }
}

// What the server says about a failed create or adopt, as a short key.
export function createErrorKind(err) {
  if (err?.status === 409) return 'nameTaken'
  if (err?.status === 400) return 'invalid'
  return 'other'
}
