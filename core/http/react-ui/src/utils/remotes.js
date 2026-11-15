// Pure helpers of the remotes UI: what the form offers for each
// kind, which models start ticked, how a remote shows, and how a failed
// test reads. No React here, so tests/unit/remotes.test.mjs can run it.

export const KINDS = ['localai', 'openai', 'anthropic']

export const PRESETS = [
  { id: 'openai', url: 'https://api.openai.com/v1' },
  { id: 'openrouter', url: 'https://openrouter.ai/api/v1' },
  { id: 'groq', url: 'https://api.groq.com/openai/v1' },
  { id: 'custom', url: '' },
]

// The name a remote gets until the person types another one.
const PRESET_NAMES = { openai: 'OpenAI', openrouter: 'OpenRouter', groq: 'Groq' }

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
export function chosenModels(models, ticked, kind = 'localai') {
  const set = new Set(ticked)
  return models.filter(m => set.has(m.id)).map(m => ({ id: m.id, usecases: usecasesOf(kind, m) }))
}

// The uses of a listed model. The cloud kinds list ids only and serve chat, so
// a model without uses is a chat model there. A LocalAI remote says what each
// model does, and an empty list stays empty.
export function usecasesOf(kind, model) {
  const list = Array.isArray(model?.usecases) ? model.usecases : []
  if (list.length > 0) return list
  return kind === 'localai' ? [] : ['chat']
}

// One create or adopt call takes at most this many models.
export const MAX_ADOPT = 500

export function filterModels(models, query) {
  const q = String(query || '').trim().toLowerCase()
  return q ? models.filter(m => m.id.toLowerCase().includes(q)) : models
}

// The steps a failed test can name, each with its words under
// remotes.fail.<step>. An unknown step reads as the general failure.
const FAIL_STEPS = ['invalid_url', 'unreachable', 'auth', 'no_models', 'unsupported']
export function failStep(step) {
  return FAIL_STEPS.includes(step) ? step : 'other'
}

// The state of a remote as the card shows it.
export function statusOf(remote) {
  switch (remote?.status) {
    case 'ok': return 'ok'
    case 'auth_failed': return 'auth_failed'
    case 'unreachable': return 'unreachable'
    default: return 'never'
  }
}

export function needsAttention(remote) {
  const s = statusOf(remote)
  return s === 'auth_failed' || s === 'unreachable'
}

// remote id -> remote, for the Installed list.
export function remoteIndex(list) {
  const map = new Map()
  for (const c of Array.isArray(list) ? list : []) map.set(c.id, c)
  return map
}

// The sub line of a remote row: "backend · remote name", or the backend
// alone while the remote is not known (still loading, or removed).
export function remoteSubline(model, index, fallbackBackend) {
  const backend = model?.backend || fallbackBackend
  const name = index?.get(model?.remote_id)?.name
  return [backend, name].filter(Boolean).join(' · ')
}

// Seconds since an ISO time, or null when it does not parse.
function ageSeconds(iso, now = Date.now()) {
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
  if (isMissingRoute(err)) return 'unsupported'
  if (err?.status === 409) return 'nameTaken'
  if (err?.status === 400) return 'invalid'
  return 'other'
}

// The server has no remote routes (it runs without the model gallery routes),
// so the call answers 404. A call for one remote that is gone answers 404 too,
// so only the list, the test and the create call use this.
export function isMissingRoute(err) {
  return err?.status === 404
}

// The ways a model can be skipped, as the server words them, and the key of
// their text in remotes.skipped.reason. Another reason shows as the server sent it.
const SKIP_REASONS = {
  'listed twice': 'listedTwice',
  'already adopted': 'alreadyAdopted',
  'no free model name': 'noFreeName',
  'a model with that name exists': 'nameExists',
  'the model name or settings are not valid': 'invalid',
  'could not build the entry': 'buildFailed',
  'could not write the entry': 'writeFailed',
}
export function skipReasonKey(reason) {
  return SKIP_REASONS[String(reason || '').trim().toLowerCase()] || ''
}

// A create or an adopt answer as lists: the models added, the ones skipped
// with a reason, and the warning of a partial result ('' when there is none).
export function resultParts(result) {
  return {
    created: Array.isArray(result?.created) ? result.created : [],
    skipped: Array.isArray(result?.skipped) ? result.skipped.filter(s => s && s.id) : [],
    warning: typeof result?.warning === 'string' ? result.warning : '',
  }
}

// "a, b, c +2": the first few ids for a toast, which has no room for a list.
export function idsText(skipped, limit = 3) {
  const ids = skipped.map(s => s.id)
  const shown = ids.slice(0, limit).join(', ')
  return ids.length > limit ? `${shown} +${ids.length - limit}` : shown
}

// The answer of adopting the models a partial create missed, folded into the
// answer of that create: the added models of both, the skips of the retry.
export function mergeAdopt(first, second) {
  const a = resultParts(first)
  const b = resultParts(second)
  return {
    remote: second?.remote || first?.remote,
    created: [...a.created, ...b.created.filter(id => !a.created.includes(id))],
    skipped: b.skipped,
    ...(b.warning ? { warning: b.warning } : {}),
  }
}

// The models a partial create still lacks: the ones chosen that no entry
// carries yet.
export function missingModels(chosen, result) {
  const done = new Set(resultParts(result).created)
  return chosen.filter(m => !done.has(m.id))
}

// A remote that answered, yet gives nothing to use: the refresh ended with an
// error text, or every model it lists is gone or none is listed. A remote that
// failed or was never refreshed shows its own state instead.
export function noUsableModels(remote) {
  if (statusOf(remote) !== 'ok') return false
  if (remote.last_error) return true
  const gone = new Set(remote.unavailable || [])
  const live = (remote.entries || []).filter(id => !gone.has(id)).length
  return live + (remote.new?.length || 0) === 0
}
