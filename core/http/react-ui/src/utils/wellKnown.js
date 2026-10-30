// The well-known files of the user's home (persona.json, user.json,
// shortcuts.json, home.json, ui.json, and the data files of Home widgets):
// the client of /api/chats/wellknown and the pure helpers the editors share.
// No browser globals at import time, so the node unit tests load it.

export const WELL_KNOWN_FILES = {
  persona: 'persona.json',
  user: 'user.json',
  shortcuts: 'shortcuts.json',
  home: 'home.json',
  ui: 'ui.json',
}

export class WellKnownError extends Error {
  constructor(status, body) {
    super(body?.error?.message || `HTTP ${status}`)
    this.name = 'WellKnownError'
    this.status = status
    this.type = body?.error?.type || ''
    this.errors = Array.isArray(body?.errors) ? body.errors : []
    this.current = body?.current || null
  }

  get conflict() { return this.status === 409 && !!this.current }
}

// createWellKnownApi takes fetch and a function that turns a path into a URL
// (apiUrl in the app), so the unit tests drive it without a browser.
export function createWellKnownApi({ fetch, url = (p) => p }) {
  const path = (name, rest = '', file) => {
    const q = file ? `?path=${encodeURIComponent(file)}` : ''
    return url(`/api/chats/wellknown/${encodeURIComponent(name)}${rest}${q}`)
  }
  const call = async (u, init = {}) => {
    const res = await fetch(u, { credentials: 'same-origin', ...init })
    let body = null
    try { body = await res.json() } catch { body = null }
    if (!res.ok) throw new WellKnownError(res.status, body)
    return body
  }
  return {
    get: (name, file) => call(path(name, '', file), { headers: { Accept: 'application/json' } }),
    // version is the one the editor read (0: the file did not exist).
    put: (name, values, { version, replace = false, file } = {}) => call(path(name, '', file), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        ...(version === undefined || version === null ? {} : { 'If-Match': `"${version}"` }),
      },
      body: JSON.stringify({ values, replace }),
    }),
    revert: (name, file) => call(path(name, '/revert', file), { method: 'POST' }),
    // revertTo writes back the version rev that versions listed.
    revertTo: (name, rev, file) => call(path(name, '/revert', file), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ rev }),
    }),
    // versions lists the versions of the file, newest first.
    versions: (name, file) => call(path(name, '/versions', file), { headers: { Accept: 'application/json' } }),
    schema: (name) => call(path(name, '/schema'), { headers: { Accept: 'application/schema+json' } }),
  }
}

// wellKnownPath is the path in the home that a document lives at, without a
// leading slash, as the change feed names it.
export function wellKnownPath(name, file) {
  return file ? String(file).replace(/^\/+/, '') : (WELL_KNOWN_FILES[name] || '')
}

const EMPTY = Object.freeze({ doc: null, error: null, loaded: false })

// createWellKnownStore keeps one copy of each document for the whole tab. The
// shortcuts row and its editor, the Appearance tab, the theme and the pinned
// pages all read ui.json or shortcuts.json at the same time: with one copy
// each, a save shows in every reader at once, and the next save of any of them
// starts from the version the last one wrote, so two editors of one file do not
// conflict with each other.
//
// onOwnWrite(path) is called after a save or a revert, so the change feed can
// skip the echo of a write this tab already shows.
export function createWellKnownStore({ api, onOwnWrite = () => {} }) {
  const entries = new Map()
  const keyOf = (name, file) => `${name}\n${file || ''}`
  const entry = (name, file) => {
    const k = keyOf(name, file)
    let e = entries.get(k)
    if (!e) {
      e = { snap: EMPTY, subs: new Set(), seq: 0, inflight: null, inflightSeq: -1 }
      entries.set(k, e)
    }
    return e
  }
  const publish = (e, patch) => {
    e.snap = { ...e.snap, ...patch }
    e.subs.forEach((fn) => fn())
  }
  // A write bumps seq, so a read that started before it is dropped.
  const wrote = (e, doc, name, file) => {
    e.seq++
    publish(e, { doc, error: null, loaded: true })
    onOwnWrite(wellKnownPath(name, file))
    return doc
  }

  return {
    snapshot: (name, file) => entry(name, file).snap,
    subscribe(name, file, fn) {
      const e = entry(name, file)
      e.subs.add(fn)
      return () => { e.subs.delete(fn) }
    },
    // load reads the file again. Readers that ask at the same time share one
    // request; it resolves to the document (null after an error).
    load(name, file) {
      const e = entry(name, file)
      if (e.inflight && e.inflightSeq === e.seq) return e.inflight
      const mine = e.seq
      const p = api.get(name, file)
        .then((doc) => {
          if (mine === e.seq) publish(e, { doc, error: null, loaded: true })
          return e.snap.doc
        }, (error) => {
          if (mine === e.seq) publish(e, { error, loaded: true })
          return e.snap.doc
        })
        .finally(() => { if (e.inflight === p) e.inflight = null })
      e.inflight = p
      e.inflightSeq = mine
      return p
    },
    // save writes values from version (by default the version every reader
    // shows). A conflict or a refusal is thrown and changes nothing here.
    async save(name, values, { replace = false, version, file } = {}) {
      const e = entry(name, file)
      const d = await api.put(name, values, { version: version ?? e.snap.doc?.version ?? 0, replace, file })
      return wrote(e, d, name, file)
    },
    async revert(name, file) {
      const d = await api.revert(name, file)
      return wrote(entry(name, file), d, name, file)
    },
  }
}

// fieldsFromSchema turns the top-level properties of a JSON Schema into the
// fields an editor shows, in schema order. A property with x-ui.hidden, or one
// that is an object or an array, is left to the Source view.
export function fieldsFromSchema(schema) {
  const props = schema?.properties || {}
  const required = new Set(schema?.required || [])
  const out = []
  for (const [key, p] of Object.entries(props)) {
    const ui = p['x-ui'] || {}
    if (ui.hidden) continue
    const type = Array.isArray(p.enum) ? 'enum' : p.type
    if (type === 'object' || type === 'array') continue
    let widget = ui.widget || 'text'
    if (type === 'enum') widget = ui.widget || 'select'
    if (type === 'boolean') widget = 'checkbox'
    out.push({
      key,
      label: p.title || key,
      help: p.description || '',
      widget,
      options: Array.isArray(p.enum) ? p.enum : null,
      maxLength: typeof p.maxLength === 'number' ? p.maxLength : undefined,
      pattern: typeof p.pattern === 'string' ? p.pattern : undefined,
      required: required.has(key),
    })
  }
  return out
}

// checkField is the client's quick check of one value, so a field says what is
// wrong before the save. The server validates again with the full schema.
export function checkField(field, value) {
  if (value === undefined || value === null || value === '') return field.required ? 'required' : ''
  if (field.maxLength !== undefined && String(value).length > field.maxLength) return 'tooLong'
  if (field.pattern) {
    try { if (!new RegExp(field.pattern).test(String(value))) return 'format' } catch { /* the server decides */ }
  }
  if (field.options && !field.options.includes(value)) return 'format'
  return ''
}

// changedValues returns what a save sends: the fields whose value differs from
// the file. An emptied field removes the key (null), so the file stays small and
// the assistant sees no empty strings. Keys the editor does not show are never
// sent, so the server keeps them.
export function changedValues(fields, original, edited) {
  const out = {}
  for (const f of fields) {
    const before = original?.[f.key]
    let after = edited?.[f.key]
    if (typeof after === 'string') after = after.replace(/\s+$/, '')
    const empty = after === undefined || after === null || after === ''
    if (empty) {
      if (before !== undefined && before !== null && before !== '') out[f.key] = null
      continue
    }
    if (after !== before) out[f.key] = after
  }
  return out
}

// editMeetsChange says whether another writer changed one of the keys an edit
// changes: pending holds the keys the edit sends (null removes one), base is
// the file the edit started from and current is the newer file of a 409. When
// it is false the edit can go on top of the newer file without losing the
// other change (a pin next to an Appearance edit of ui.json, for example).
export function editMeetsChange(pending, base, current) {
  const same = (a, b) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)
  return Object.keys(pending || {}).some(k => !same(base?.[k], current?.[k]))
}

// parseSource reads the text of the Source view. It returns {value} for a JSON
// object and {error} for anything else.
export function parseSource(text) {
  try {
    const v = JSON.parse(text)
    if (!v || typeof v !== 'object' || Array.isArray(v)) return { error: 'notObject' }
    return { value: v }
  } catch (e) {
    return { error: 'notJson', detail: e.message }
  }
}

export function sourceText(data) {
  return `${JSON.stringify(data || {}, null, 2)}\n`
}

// shortcutList is the list of composer shortcuts: the ones of shortcuts.json
// when the file has a list (even an empty one), else the default starters.
// It answers {items, fromFile}.
export function shortcutList(doc, defaults) {
  const list = doc?.data?.shortcuts
  if (doc?.exists && Array.isArray(list)) {
    return {
      fromFile: true,
      items: list
        .filter(s => s && typeof s.label === 'string' && typeof s.prompt === 'string' && s.label.trim() && s.prompt.trim())
        .map(s => ({ label: s.label, prompt: s.prompt, icon: s.icon || '', send: !!s.send })),
    }
  }
  const items = (Array.isArray(defaults) ? defaults : [])
    .filter(p => typeof p === 'string' && p.trim())
    .map(p => ({ label: p, prompt: p, icon: '', send: false }))
  return { fromFile: false, items }
}

// cleanShortcuts drops the empty rows of the editor and the keys that hold
// nothing, for the save.
export function cleanShortcuts(rows) {
  return rows
    .map(r => ({ label: (r.label || '').trim(), prompt: (r.prompt || '').trim(), icon: (r.icon || '').trim(), send: !!r.send }))
    .filter(r => r.label && r.prompt)
    .map(r => {
      const o = { label: r.label, prompt: r.prompt }
      if (r.icon) o.icon = r.icon
      if (r.send) o.send = true
      return o
    })
}

export function move(list, from, to) {
  if (to < 0 || to >= list.length || from === to) return list
  const next = list.slice()
  const [it] = next.splice(from, 1)
  next.splice(to, 0, it)
  return next
}

export const WIDGET_TYPES = ['list', 'metric', 'chats']

// widgetList reads home.json: the widgets in order, each with a usable id.
export function widgetList(doc) {
  const list = doc?.data?.widgets
  if (!Array.isArray(list)) return []
  const seen = new Set()
  return list
    .filter(w => w && WIDGET_TYPES.includes(w.type) && typeof w.title === 'string')
    .map((w, i) => {
      let id = typeof w.id === 'string' && w.id ? w.id : `w${i}`
      while (seen.has(id)) id += '-x'
      seen.add(id)
      // The other keys stay (for example trigger, or a key the assistant
      // added), so a save from the layout editor does not drop them.
      return { ...w, id, type: w.type, title: w.title, source: typeof w.source === 'string' ? w.source : '' }
    })
}

// widgetId makes an id for a new widget from its title, unique in the list.
export function widgetId(title, taken) {
  const base = String(title || 'widget').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32) || 'widget'
  let id = base
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`
  return id
}

// cleanWidgets shapes the editor's rows for home.json.
export function cleanWidgets(rows) {
  const out = []
  const ids = []
  for (const r of rows) {
    const title = (r.title || '').trim()
    if (!title || !WIDGET_TYPES.includes(r.type)) continue
    const id = /^[a-z0-9][a-z0-9-]{0,39}$/.test(r.id || '') && !ids.includes(r.id) ? r.id : widgetId(title, ids)
    ids.push(id)
    // Keys the editor does not show are kept as they were.
    const w = { ...r, id, type: r.type, title }
    delete w.source
    const source = (r.source || '').trim().replace(/^\/+/, '')
    if (r.type !== 'chats' && source) w.source = source
    if (r.type === 'chats') delete w.trigger
    out.push(w)
  }
  return out
}

// widgetData reads the answer for a widget's data file. A missing file is
// 'empty', a broken one that has a last good version is 'stale', a broken one
// without it is 'error'.
export function widgetData(doc) {
  if (!doc) return { state: 'loading' }
  const d = doc.data || {}
  const items = Array.isArray(d.items) ? d.items.filter(it => it && typeof it.label === 'string') : []
  const series = Array.isArray(d.series) ? d.series.filter(n => typeof n === 'number' && Number.isFinite(n)) : []
  const base = {
    title: typeof d.title === 'string' ? d.title : '',
    updatedAt: typeof d.updatedAt === 'string' ? d.updatedAt : '',
    items,
    value: typeof d.value === 'number' || typeof d.value === 'string' ? d.value : null,
    note: typeof d.note === 'string' ? d.note : '',
    series,
    source: d.source && typeof d.source === 'object' && typeof d.source.kind === 'string' ? d.source : null,
    errors: doc.errors || [],
  }
  if (!doc.exists) return { state: 'empty', ...base }
  if (!doc.valid) return { state: doc.fallback ? 'stale' : 'error', ...base }
  return { state: 'ok', ...base }
}

// sparkPath draws the points of a series as an SVG path in a w by h box.
export function sparkPath(series, w = 240, h = 48) {
  if (!series || series.length < 2) return ''
  const min = Math.min(...series)
  const max = Math.max(...series)
  const span = max - min || 1
  const step = w / (series.length - 1)
  return series.map((v, i) => `${i ? 'L' : 'M'}${(i * step).toFixed(1)},${(h - 2 - ((v - min) / span) * (h - 4)).toFixed(1)}`).join(' ')
}

export function formatMetric(value) {
  if (typeof value !== 'number') return value === null || value === undefined ? '' : String(value)
  const abs = Math.abs(value)
  if (abs >= 1e9) return `${+(value / 1e9).toFixed(1)}B`
  if (abs >= 1e6) return `${+(value / 1e6).toFixed(1)}M`
  if (abs >= 1e4) return `${+(value / 1e3).toFixed(0)}k`
  return String(+value.toFixed(2))
}
