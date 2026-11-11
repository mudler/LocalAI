// The well-known files of the user's home (persona.json, user.json,
// shortcuts.json, setups.json, defaults.json, home.json, ui.json and nav.json in LocalAI/, and the data
// files of Home widgets):
// the client of /api/chats/wellknown and the pure helpers the editors share.
// No browser globals at import time, so the node unit tests load it.

import { pageLinkTarget } from './pageLinks.js'
import { isSize } from './homeGrid.js'
import { templateProblem, avatarPath, STRING_KEYS } from './look.js'

// SETTINGS_DIR is the folder of the home that holds the settings files.
export const SETTINGS_DIR = 'LocalAI'

// The paths inside the home, without a leading slash, as the change feed names them.
export const WELL_KNOWN_FILES = {
  persona: `${SETTINGS_DIR}/persona.json`,
  user: `${SETTINGS_DIR}/user.json`,
  shortcuts: `${SETTINGS_DIR}/shortcuts.json`,
  home: `${SETTINGS_DIR}/home.json`,
  ui: `${SETTINGS_DIR}/ui.json`,
  setups: `${SETTINGS_DIR}/setups.json`,
  defaults: `${SETTINGS_DIR}/defaults.json`,
  nav: `${SETTINGS_DIR}/nav.json`,
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
// fields an editor shows, in schema order. A property with x-ui.hidden, or an
// object or an array that has no x-ui.widget of its own, is left to the Source
// view.
export function fieldsFromSchema(schema) {
  const props = schema?.properties || {}
  const required = new Set(schema?.required || [])
  const out = []
  for (const [key, p] of Object.entries(props)) {
    const ui = p['x-ui'] || {}
    if (ui.hidden) continue
    const type = Array.isArray(p.enum) ? 'enum' : p.type
    if ((type === 'object' || type === 'array') && !ui.widget) continue
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
      // The placeholders a text may use, and the contrast an accent must keep.
      placeholders: Array.isArray(p['x-placeholders']) ? p['x-placeholders'] : undefined,
      contrast: typeof ui.contrast === 'number' ? ui.contrast : undefined,
      properties: p.properties && typeof p.properties === 'object' ? p.properties : undefined,
    })
  }
  return out
}

// checkField is the client's quick check of one value, so a field says what is
// wrong before the save. The server validates again with the full schema.
export function checkField(field, value) {
  if (value === undefined || value === null || value === '') return field.required ? 'required' : ''
  const objectProblem = checkObjectField(field, value)
  if (objectProblem !== null) return objectProblem
  if (field.maxLength !== undefined && String(value).length > field.maxLength) return 'tooLong'
  if (field.pattern) {
    try { if (!new RegExp(field.pattern).test(String(value))) return 'format' } catch { /* the server decides */ }
  }
  if (field.options && !field.options.includes(value)) return 'format'
  if (field.placeholders && templateProblem(String(value), field.placeholders)) return 'placeholder'
  if (field.widget === 'avatar' && !avatarPath(value)) return 'format'
  return ''
}

// checkObjectField checks the fields whose value is an object: the empty chat
// of persona.json (its texts and their placeholders) and the text overrides of
// ui.json (keys on the allow-list, placeholders of each key). It answers null
// for any other field, so checkField goes on.
function checkObjectField(field, value) {
  if (field.widget === 'emptyChat') {
    if (typeof value !== 'object' || Array.isArray(value)) return 'format'
    for (const k of ['title', 'subtitle']) {
      const v = value[k]
      if (v === undefined || v === '') continue
      const max = field.properties?.[k]?.maxLength
      if (typeof v !== 'string') return 'format'
      if (max && v.length > max) return 'tooLong'
      if (templateProblem(v, field.properties?.[k]?.['x-placeholders'] || [])) return 'placeholder'
    }
    return ''
  }
  if (field.widget === 'strings') {
    if (typeof value !== 'object' || Array.isArray(value)) return 'format'
    for (const [k, v] of Object.entries(value)) {
      if (!(k in STRING_KEYS) || typeof v !== 'string') return 'format'
      if (templateProblem(v, STRING_KEYS[k])) return 'placeholder'
    }
    return ''
  }
  return null
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
    const isObj = after !== null && typeof after === 'object'
    const empty = after === undefined || after === null || after === '' || (isObj && Object.keys(after).length === 0)
    if (empty) {
      if (before !== undefined && before !== null && before !== '') out[f.key] = null
      continue
    }
    if (isObj ? JSON.stringify(after) !== JSON.stringify(before) : after !== before) out[f.key] = after
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

// wellKnownProblem turns a failed save into what the editor shows: a plain
// sentence, and the server's own words (field errors, or the raw message) for
// a collapsed Details. The caller answers a conflict itself. t is the
// translator of the workspace namespace.
export function wellKnownProblem(e, t) {
  if (e?.status === 413) return { message: t('wellKnown.saveTooLarge'), errors: [] }
  const errors = Array.isArray(e?.errors) ? e.errors : []
  if (e?.status === 400 && errors.length > 0) return { message: t('wellKnown.saveInvalid'), errors }
  return { message: t('wellKnown.saveFailed'), errors: e?.message ? [e.message] : [] }
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

export const WIDGET_TYPES = ['list', 'metric', 'chats', 'page']

// widgetList reads home.json: the widgets in order, each with a usable id.
export function widgetList(doc) {
  const list = doc?.data?.widgets
  if (!Array.isArray(list)) return []
  const seen = new Set()
  return list
    // A page widget may leave its title out: the tile shows the page's.
    .filter(w => w && WIDGET_TYPES.includes(w.type) && (typeof w.title === 'string' || w.type === 'page'))
    .map((w, i) => {
      let id = typeof w.id === 'string' && w.id ? w.id : `w${i}`
      while (seen.has(id)) id += '-x'
      seen.add(id)
      // The other keys stay (for example trigger, or a key the assistant
      // added), so a save from the layout editor does not drop them.
      return { ...w, id, type: w.type, title: typeof w.title === 'string' ? w.title : '', source: typeof w.source === 'string' ? w.source : '' }
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
    if (!WIDGET_TYPES.includes(r.type)) continue
    if (r.type === 'page') {
      // A page widget names its page; its title is optional.
      if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(r.page || '')) continue
      const id = /^[a-z0-9][a-z0-9-]{0,39}$/.test(r.id || '') && !ids.includes(r.id) ? r.id : widgetId(title || r.page, ids)
      ids.push(id)
      const w = { ...r, id, type: 'page', page: r.page }
      delete w.source
      delete w.trigger
      if (title) w.title = title
      else delete w.title
      if (!(Number.isInteger(r.folder) && r.folder > 0)) delete w.folder
      if (!isSize(w.size)) delete w.size
      out.push(w)
      continue
    }
    if (!title) continue
    const id = /^[a-z0-9][a-z0-9-]{0,39}$/.test(r.id || '') && !ids.includes(r.id) ? r.id : widgetId(title, ids)
    ids.push(id)
    // Keys the editor does not show are kept as they were.
    const w = { ...r, id, type: r.type, title }
    delete w.source
    delete w.page
    delete w.folder
    const source = (r.source || '').trim().replace(/^\/+/, '')
    if (r.type !== 'chats' && source) w.source = source
    if (r.type === 'chats') delete w.trigger
    if (!isSize(w.size)) delete w.size
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
  const items = Array.isArray(d.items) ? d.items.filter(it => it && typeof it.label === 'string').map(it => ({ ...it, link: itemLink(it.url) })) : []
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

// The in-app screens a widget item may link to; the server checks the same.
const ITEM_APP_PREFIXES = ['chat', 'chats', 'files', 'pages', 'agents', 'skills', 'triggers', 'collections', 'models', 'account', 'addons']

// itemLink reads the url of a widget item: {external: true, href, host} for an
// http or https address, {external: false, to} for a path of the app, or null
// for anything else (the file is then invalid on the server too, so this only
// guards a file written around it).
export function itemLink(url) {
  if (typeof url !== 'string' || url === '' || url.length > 2048 || /\s/.test(url)) return null
  if (url.startsWith('/app/')) {
    if (/\.\.|\/\/|\\/.test(url)) return null
    const rest = url.slice(5)
    const ok = ITEM_APP_PREFIXES.some((p) => rest.startsWith(p) && (rest.length === p.length || '/?#'.includes(rest[p.length])))
    return ok ? { external: false, to: url } : null
  }
  if (!/^https?:\/\/[^/?#@\\]+([/?#]|$)/.test(url)) return null
  try {
    const u = new URL(url)
    if (u.username || u.password) return null
    return { external: true, href: u.href, host: u.host }
  } catch {
    return null
  }
}

// itemLinkTarget is what the confirmation bar shows before an external item
// link opens: the same check as a link of a page (pageLinkTarget), so a widget
// that a trigger or the assistant wrote from web text cannot open a site with
// one click. Null for an in-app link (it navigates directly) or no link.
export function itemLinkTarget(link) {
  if (!link || !link.external) return null
  const target = pageLinkTarget(link.href)
  return target.error ? null : target
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

// ---- Backgrounds (ui.json: homeBackground, chatBackground) ----

const HEX = /^#[0-9a-fA-F]{6}$/
const BG_IMAGE_EXT = /\.(png|jpe?g|webp|gif|avif)$/i

// backgroundImagePath says whether p may be the image of a background: a relative
// path of a picture in the home, with no leading slash, no .. or . segment, no
// address, no backslash and a picture extension. The server checks the same
// rule (BackgroundImageOK in Go); this one keeps a bad value from being sent
// or shown.
export function backgroundImagePath(p) {
  if (typeof p !== 'string' || p === '' || p.length > 400) return false
  if (p.startsWith('/') || /[\\:?#\u0000-\u001f\u007f]/.test(p)) return false
  if (p.split('/').some(seg => seg === '' || seg === '.' || seg === '..')) return false
  return BG_IMAGE_EXT.test(p)
}

const num = (v, min, max) => (typeof v === 'number' && Number.isFinite(v) ? Math.min(max, Math.max(min, v)) : undefined)

// The keys of ui.json that hold a background, and the veil each one gets when
// the file names none. A veil keeps text readable: a stronger one over a
// picture, which can be busy, a lighter one over a gradient; a plain colour is
// the user's own pick and gets none unless they ask. A chat is mostly long
// text, so its veils are stronger than the ones of Home.
export const BACKGROUND_KEYS = ['homeBackground', 'chatBackground']
export const BACKGROUND_VEIL = {
  homeBackground: { image: 0.55, gradient: 0.35, color: 0 },
  chatBackground: { image: 0.8, gradient: 0.6, color: 0 },
}

// backgroundVeil is the default veil of a kind (image, gradient, color) for a key.
export function backgroundVeil(key, kind) {
  return (BACKGROUND_VEIL[key] || BACKGROUND_VEIL.homeBackground)[kind] ?? 0
}

// background reads the background that key of ui.json holds and keeps only the
// parts that are valid, so a half-valid value (from the assistant, or a file
// the server read before the schema knew a key) still shows what it can. It
// returns null when nothing is left to show.
export function background(data, key) {
  const v = data?.[key]
  if (!v || typeof v !== 'object' || Array.isArray(v)) return null
  const out = {}
  if (typeof v.color === 'string' && HEX.test(v.color)) out.color = v.color
  const g = v.gradient
  if (g && typeof g === 'object' && HEX.test(g.from) && HEX.test(g.to)) {
    out.gradient = { from: g.from, to: g.to, angle: Math.round(num(g.angle, 0, 360) ?? 180) }
  }
  if (backgroundImagePath(v.image)) out.image = v.image
  if (!out.color && !out.gradient && !out.image) return null
  out.fit = v.fit === 'contain' ? 'contain' : 'cover'
  out.opacity = num(v.opacity, 0, 1) ?? 1
  out.blur = Math.round(num(v.blur, 0, 40) ?? 0)
  out.overlay = num(v.overlay, 0, 1) ?? backgroundVeil(key, out.image ? 'image' : out.gradient ? 'gradient' : 'color')
  return out
}

export const homeBackground = (data) => background(data, 'homeBackground')
export const chatBackground = (data) => background(data, 'chatBackground')

// backgroundStyle turns a background (as background returns it) into the
// inline styles of the layers: art (colour, gradient and picture, with opacity
// and blur) and scrim (the veil in the colour of the page). imageUrl turns the
// path of the picture into the address that serves it.
export function backgroundStyle(bg, imageUrl) {
  if (!bg) return null
  const layers = []
  if (bg.image) layers.push(`url("${imageUrl(bg.image)}")`)
  if (bg.gradient) layers.push(`linear-gradient(${bg.gradient.angle}deg, ${bg.gradient.from}, ${bg.gradient.to})`)
  const pad = bg.blur ? -(bg.blur * 2) : 0
  const art = {
    inset: pad ? `${pad}px` : 0,
    backgroundColor: bg.color || 'transparent',
    backgroundImage: layers.length ? layers.join(', ') : undefined,
    backgroundSize: bg.image ? `${bg.fit}${bg.gradient ? ', cover' : ''}` : undefined,
    backgroundPosition: 'center',
    backgroundRepeat: 'no-repeat',
    opacity: bg.opacity,
    filter: bg.blur ? `blur(${bg.blur}px)` : undefined,
  }
  return { art, scrim: { opacity: bg.overlay } }
}
