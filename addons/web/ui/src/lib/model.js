// What the settings section of the Web add-on draws, and what it sends. The
// status tool answers the settings only to an administrator, and never the
// keys: a provider says whether its key is set.

export const PROVIDERS = ['brave', 'tavily', 'google', 'searxng', 'duckduckgo']
export const KEYED = ['brave', 'tavily', 'google']

const text = (v) => (typeof v === 'string' ? v : '')

export function settingsModel(status) {
  if (!status || typeof status !== 'object' || status.ok !== true || status.admin !== true) return { hidden: true }
  const s = status.settings && typeof status.settings === 'object' ? status.settings : {}
  const byName = {}
  for (const p of Array.isArray(s.providers) ? s.providers : []) {
    if (p && PROVIDERS.includes(p.name)) byName[p.name] = p
  }
  const order = (Array.isArray(s.order) ? s.order : []).filter(n => PROVIDERS.includes(n))
  const providers = PROVIDERS.map(name => {
    const p = byName[name] || {}
    return { name, keyed: KEYED.includes(name), keySet: p.keySet === true, ready: p.ready === true, missing: text(p.missing) }
  })
  return {
    hidden: false,
    order,
    providers,
    googleEngine: text(s.googleEngine),
    searxngUrl: text(s.searxngUrl),
    serving: (Array.isArray(status.serving) ? status.serving : []).filter(n => PROVIDERS.includes(n)),
    note: text(s.note),
  }
}

// The draft starts from the saved settings. Key fields start empty: an empty
// field keeps the saved key.
export function draftOf(model) {
  return {
    order: [...model.order],
    keys: Object.fromEntries(KEYED.map(n => [n, ''])),
    clear: [],
    googleEngine: model.googleEngine,
    searxngUrl: model.searxngUrl,
  }
}

// The listed providers: those of the order first, in order, then the others.
export function rows(model, draft) {
  const on = draft.order
  const rest = PROVIDERS.filter(n => !on.includes(n))
  return [...on, ...rest].map(name => ({ ...model.providers.find(p => p.name === name), on: on.includes(name), position: on.indexOf(name) }))
}

export function toggle(draft, name) {
  const order = draft.order.includes(name) ? draft.order.filter(n => n !== name) : [...draft.order, name]
  return { ...draft, order }
}

export function move(draft, name, by) {
  const i = draft.order.indexOf(name)
  const j = i + by
  if (i < 0 || j < 0 || j >= draft.order.length) return draft
  const order = [...draft.order]
  ;[order[i], order[j]] = [order[j], order[i]]
  return { ...draft, order }
}

export function setKey(draft, name, value) {
  return { ...draft, keys: { ...draft.keys, [name]: value }, clear: draft.clear.filter(n => n !== name) }
}

export function clearKey(draft, name) {
  return { ...draft, keys: { ...draft.keys, [name]: '' }, clear: draft.clear.includes(name) ? draft.clear : [...draft.clear, name] }
}

// The arguments of set-settings: only the keys that were typed, so a saved key
// is never sent back.
export function change(draft) {
  const keys = {}
  for (const n of KEYED) {
    const v = text(draft.keys[n]).trim()
    if (v) keys[n] = v
  }
  const out = { order: [...draft.order], googleEngine: draft.googleEngine.trim(), searxngUrl: draft.searxngUrl.trim() }
  if (Object.keys(keys).length > 0) out.keys = keys
  if (draft.clear.length > 0) out.clear = [...draft.clear]
  return out
}

// The rows of a test-search answer.
export function testRows(answer) {
  if (!answer || answer.ok !== true || !Array.isArray(answer.providers)) return []
  return answer.providers
    .filter(p => p && PROVIDERS.includes(p.name))
    .map(p => ({ name: p.name, ok: p.ok === true, ready: p.ready === true, results: Number.isInteger(p.results) ? p.results : 0, host: text(p.host), error: text(p.error) }))
}
