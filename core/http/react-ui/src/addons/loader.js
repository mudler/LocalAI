// Loads one add-on UI: its scoped style sheets and its ES module. The loader
// trusts nothing the registry says beyond the shape it checks here:
//
//   - the SDK range must contain the host SDK version, before any import;
//   - every URL must be a same-origin immutable asset URL of this add-on's own
//     package, all under the same package digest;
//   - the module is imported by URL and receives only the host SDK object. No
//     import map exists, so a bare `react` import fails and no second React is
//     ever resolved for it;
//   - the import and the factory are bounded by a timeout and an abort.

import { sdkAccepts } from './sdk.js'
import { scopeCss } from './cssScope.js'
import { declaredComponents } from './slots.js'

export class LoadError extends Error {
  constructor(code, message) {
    super(message || code)
    this.name = 'LoadError'
    this.code = code
  }
}

const DIGEST = /^[0-9a-f]{64}$/
const ID = /^[a-z][a-z0-9-]{0,63}$/

// assetPrefix is the path every asset of this add-on's package starts with.
export function assetPrefix(addonId, basePath = '') {
  return `${basePath}/api/addons/${encodeURIComponent(addonId)}/assets/`
}

// assertAssetUrl returns { url, digest } for a registry URL that is a
// same-origin immutable asset of this add-on, or throws untrusted_asset.
//
// The registry gives server-absolute paths (`/api/addons/...`). When the app
// is served under a path prefix, the page's base path is put in front of them
// here, exactly as for every other API call of the host; a URL that already
// carries another prefix does not match and is refused.
export function assertAssetUrl(value, { addonId, origin, basePath = '' }) {
  const text = String(value)
  let u
  try { u = new URL(text.startsWith('/') && !text.startsWith('//') ? basePath + text : text, origin) } catch { throw new LoadError('untrusted_asset', 'Asset address is not a URL') }
  if (u.origin !== origin || u.username || u.password || u.search || u.hash) throw new LoadError('untrusted_asset', 'Asset is not a plain same-origin address')
  const prefix = assetPrefix(addonId, basePath)
  if (!ID.test(addonId) || !u.pathname.startsWith(prefix)) throw new LoadError('untrusted_asset', 'Asset is outside the add-on package')
  const rest = u.pathname.slice(prefix.length).split('/')
  const digest = rest.shift()
  if (!DIGEST.test(digest || '') || rest.length === 0 || rest.some(s => s === '' || s === '.' || s === '..')) {
    throw new LoadError('untrusted_asset', 'Asset address is malformed')
  }
  return { url: u.href, digest }
}

export function withTimeout(promise, ms, signal, code = 'timeout') {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new LoadError(code, 'The add-on did not answer in time')), ms)
    const onAbort = () => reject(new LoadError('aborted', 'The load was canceled'))
    if (signal) {
      if (signal.aborted) { clearTimeout(timer); reject(new LoadError('aborted', 'The load was canceled')); return }
      signal.addEventListener('abort', onAbort, { once: true })
    }
    Promise.resolve(promise).then(
      (v) => { clearTimeout(timer); signal?.removeEventListener('abort', onAbort); resolve(v) },
      (e) => { clearTimeout(timer); signal?.removeEventListener('abort', onAbort); reject(e) },
    )
  })
}

const MAX_ACTIONS = 8
const ACTION_ID = /^[a-z][a-z0-9-]{0,63}$/

// chatActionContext keeps exactly what a chat action may be told: which
// conversation it was opened from and an opaque reference of the host's.
// There is no history, no message and no send hook in it.
export function chatActionContext(input) {
  if (!input || typeof input !== 'object') return null
  const { conversationId, resourceRef } = input
  if (typeof conversationId !== 'string' || conversationId === '' || conversationId.length > 128) return null
  const out = { conversationId }
  if (resourceRef !== undefined) {
    if (typeof resourceRef !== 'string' || resourceRef.length > 256) return null
    out.resourceRef = resourceRef
  }
  return out
}

// validateChatActions returns the actions an add-on may contribute: ids the
// manifest declared, a label, a known icon name and an open function. The
// `open` handed to the host is a wrapper: it passes only the sanitized
// context and never lets an error of the add-on out.
export function validateChatActions(raw, declared, iconNames = []) {
  if (raw === undefined || raw === null) return []
  if (!Array.isArray(raw)) throw new LoadError('invalid_module', 'chatActions must be a list')
  const allowed = new Set(declared || [])
  const known = new Set(iconNames)
  const seen = new Set()
  const out = []
  for (const action of raw.slice(0, MAX_ACTIONS)) {
    if (!action || typeof action !== 'object') continue
    const { id, label, icon, open } = action
    if (typeof id !== 'string' || !ACTION_ID.test(id) || !allowed.has(id) || seen.has(id)) continue
    if (typeof label !== 'string' || label.trim() === '' || label.length > 80) continue
    if (typeof open !== 'function') continue
    seen.add(id)
    out.push(Object.freeze({
      id,
      label,
      icon: known.has(icon) ? icon : 'puzzle',
      open(context) {
        const clean = chatActionContext(context)
        if (!clean) return { ok: false, error: new TypeError('A chat action needs a conversation') }
        try {
          open(clean)
          return { ok: true }
        } catch (error) {
          return { ok: false, error }
        }
      },
    }))
  }
  return out
}

function isComponent(value) {
  if (typeof value === 'function') return true
  return Boolean(value) && typeof value === 'object' && '$$typeof' in value
}

const devWarn = (message) => {
  try {
    if (import.meta.env && import.meta.env.DEV) console.warn(`[addons] ${message}`)
  } catch { /* no dev flag outside the bundler */ }
}

// validateComponents returns the components the module supplies for the names
// its manifest declared, and nothing else. A name the manifest does not
// declare is ignored and, in development, logged: a module cannot add a
// contribution by exporting a component.
export function validateComponents(raw, declared, warn = devWarn) {
  if (raw === undefined || raw === null) return Object.freeze({})
  if (typeof raw !== 'object' || Array.isArray(raw)) throw new LoadError('invalid_module', 'components must be an object')
  const allowed = declared instanceof Set ? declared : new Set(declared || [])
  const out = {}
  for (const name of Object.keys(raw)) {
    if (!allowed.has(name)) {
      warn(`component "${name}" is not declared in the manifest and was ignored`)
      continue
    }
    if (!isComponent(raw[name])) {
      warn(`component "${name}" is not a React component and was ignored`)
      continue
    }
    out[name] = raw[name]
  }
  for (const name of allowed) {
    if (!(name in out) && !(raw && Object.keys(raw).includes(name))) warn(`component "${name}" is declared in the manifest but the module does not supply it`)
  }
  return Object.freeze(out)
}

async function fetchCss(url, fetchImpl, signal, maxBytes) {
  const response = await fetchImpl(url, { credentials: 'same-origin', signal, headers: { Accept: 'text/css' } })
  if (!response.ok) throw new LoadError('css_failed', `Style sheet answered ${response.status}`)
  const type = String(response.headers?.get?.('Content-Type') || '')
  if (!/^text\/css\b/i.test(type)) throw new LoadError('css_failed', 'Style sheet has the wrong media type')
  const text = await response.text()
  if (text.length > maxBytes) throw new LoadError('css_failed', 'Style sheet is too large')
  return text
}

// loadAddonUI loads the UI of one registry app. It returns the pieces the
// shell keeps; `dispose` is safe to call more than once.
//
//   app        the registry entry: { id, generation, ui: { entry, css, sdk, ... } }
//   createSDK  ({ signal }) => HostSDK; called once, after every check passed
//   importer   (url) => Promise<module>; defaults to the native import()
export async function loadAddonUI({
  app, createSDK, importer, fetchImpl, document: doc, origin, basePath = '', timeoutMs = 15000, cssBytes = 256 * 1024, iconNames = [], signal,
}) {
  const ui = app && app.ui
  if (!ui || typeof ui.entry !== 'string') throw new LoadError('no_ui', 'This add-on has no UI')
  if (!sdkAccepts(ui.sdk)) throw new LoadError('incompatible_sdk', 'This add-on needs another host SDK version')
  const entry = assertAssetUrl(ui.entry, { addonId: app.id, origin, basePath })
  const sheets = (ui.css || []).map(href => assertAssetUrl(href, { addonId: app.id, origin, basePath }))
  for (const s of sheets) {
    if (s.digest !== entry.digest) throw new LoadError('untrusted_asset', 'Assets of two packages cannot be mixed')
  }
  const controller = new AbortController()
  const abortAll = (reason) => { if (!controller.signal.aborted) controller.abort(reason) }
  if (signal) {
    if (signal.aborted) abortAll(signal.reason)
    else signal.addEventListener('abort', () => abortAll(signal.reason), { once: true })
  }
  const styles = []
  let addon = null
  let disposed = false
  const dispose = () => {
    if (disposed) return
    disposed = true
    abortAll('disposed')
    if (addon) {
      try { addon.dispose() } catch { /* an add-on's failing dispose never blocks the host */ }
    }
    for (const el of styles.splice(0)) el.remove()
  }
  try {
    const fetcher = fetchImpl || globalThis.fetch.bind(globalThis)
    for (const sheet of sheets) {
      const text = await withTimeout(fetchCss(sheet.url, fetcher, controller.signal, cssBytes), timeoutMs, controller.signal)
      if (controller.signal.aborted) throw new LoadError('aborted', 'The load was canceled')
      const { css } = scopeCss(text, app.id, { maxBytes: cssBytes })
      const el = doc.createElement('style')
      el.setAttribute('data-addon-style', app.id)
      el.setAttribute('data-generation', String(app.generation))
      el.textContent = css
      doc.head.appendChild(el)
      styles.push(el)
    }
    const load = importer || ((url) => import(/* @vite-ignore */ url))
    let mod
    try {
      mod = await withTimeout(load(entry.url), timeoutMs, controller.signal)
    } catch (e) {
      if (e instanceof LoadError) throw e
      throw new LoadError('import_failed', e && e.message)
    }
    if (controller.signal.aborted) throw new LoadError('aborted', 'The load was canceled')
    if (!mod || typeof mod.createAddon !== 'function') throw new LoadError('invalid_module', 'The module does not export createAddon')
    const sdk = createSDK({ signal: controller.signal })
    let created
    try {
      created = await withTimeout(mod.createAddon(sdk), timeoutMs, controller.signal)
    } catch (e) {
      if (e instanceof LoadError) throw e
      throw new LoadError('create_failed', e && e.message)
    }
    if (controller.signal.aborted) {
      try { created?.dispose?.() } catch { /* ignore */ }
      throw new LoadError('aborted', 'The load was canceled')
    }
    if (!created || typeof created !== 'object' || typeof created.dispose !== 'function') {
      throw new LoadError('invalid_module', 'createAddon must return an object with dispose()')
    }
    addon = created
    if (created.Component !== undefined && !isComponent(created.Component)) {
      throw new LoadError('invalid_module', 'Component is not a React component')
    }
    const chatActions = validateChatActions(created.chatActions, ui.chatActions, iconNames)
    const components = validateComponents(created.components, declaredComponents(ui))
    return {
      component: created.Component || null,
      components,
      chatActions,
      sdk,
      styles,
      signal: controller.signal,
      abort: abortAll,
      dispose,
    }
  } catch (e) {
    dispose()
    if (e instanceof LoadError) throw e
    throw new LoadError('import_failed', e && e.message)
  }
}
