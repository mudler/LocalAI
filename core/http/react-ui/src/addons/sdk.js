// The host SDK: the only object an add-on UI module receives. It is an
// allowlist of wrappers over the host, never a window onto it. This file has
// no JSX and no host imports so the node unit tests drive it directly; the
// shell supplies the real host services (see hostServices.jsx).
//
// Everything an add-on can do is bound to its own identity and installed
// generation. After `signal` aborts (disabled, updated, crashed, uninstalled,
// the page closed) every service refuses, and the host server denies a stale
// generation anyway, so a copy of old code that stays in memory can do nothing.

export const SDK_VERSION = 1

// Semantic icon names an add-on may draw. The host Icon wrapper falls back to a
// neutral glyph for any other name, so a module cannot probe the sprite.
export const ICON_NAMES = Object.freeze([
  'puzzle', 'file', 'file-text', 'folder', 'table', 'database', 'search', 'plus', 'minus', 'close', 'check',
  'check-circle', 'alert-circle', 'warning', 'info', 'edit', 'trash', 'download', 'upload', 'refresh',
  'settings', 'list', 'grid', 'chart-line', 'clock', 'calendar', 'tag', 'star', 'link', 'external-link',
  'copy', 'eye', 'lock', 'user', 'users', 'message', 'sparkles', 'chevron-down', 'chevron-up', 'chevron-right',
])

// Theme tokens as stable semantic names. Values are CSS custom properties of
// the host theme, valid in the add-on's own scoped style sheet.
export const THEME_TOKENS = Object.freeze({
  text: 'var(--dk-text)',
  muted: 'var(--dk-text-muted, var(--dk-text))',
  surface: 'var(--dk-surface, transparent)',
  inset: 'var(--dk-inset)',
  edge: 'var(--dk-edge)',
  hover: 'var(--dk-hover)',
  accent: 'var(--dk-accent)',
  onAccent: 'var(--dk-on-accent)',
  errorText: 'var(--dk-error-text)',
  errorWash: 'var(--dk-error-wash)',
  space1: 'var(--dk-space-1, 4px)',
  space2: 'var(--dk-space-2, 8px)',
  space3: 'var(--dk-space-3, 12px)',
  space4: 'var(--dk-space-4, 16px)',
  radius: 'var(--dk-radius-1)',
})

export class AddonSdkError extends Error {
  constructor(code, message) {
    super(message || code)
    this.name = 'AddonSdkError'
    this.code = code
  }
}

// sdkAccepts is the compatibility check run before an add-on is activated.
export function sdkAccepts(range, version = SDK_VERSION) {
  return Number.isInteger(range?.min) && Number.isInteger(range?.max)
    && range.min >= 1 && range.max >= range.min && version >= range.min && version <= range.max
}

export function addonPrefix(addonId) {
  return `/app/addons/${encodeURIComponent(addonId)}`
}

const SEGMENT = /^[A-Za-z0-9._~-]{1,64}$/
const QUERY = /^[A-Za-z0-9._~%&=+,:@-]{0,512}$/

// resolveSubpath turns an address an add-on asked for into a page and a rest
// path under its own prefix, or null. Absolute addresses, traversal, other
// schemes, fragments and undeclared pages never resolve.
export function resolveSubpath(input, pages) {
  if (typeof input !== 'string' || input.length === 0 || input.length > 512) return null
  let text = input
  let search = ''
  const q = text.indexOf('?')
  if (q >= 0) {
    search = text.slice(q + 1)
    text = text.slice(0, q)
    if (!QUERY.test(search)) return null
  }
  if (text.startsWith('/')) text = text.slice(1)
  if (text === '' || text.length > 256) return null
  const segments = text.split('/')
  if (segments.length > 8) return null
  for (const s of segments) {
    if (!SEGMENT.test(s) || s === '.' || s === '..') return null
  }
  if (!Array.isArray(pages) || !pages.includes(segments[0])) return null
  return { page: segments[0], rest: segments.slice(1).join('/'), path: segments.join('/'), search }
}

function newOperationId() {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') return c.randomUUID()
  return 'op-' + Math.random().toString(16).slice(2) + Date.now().toString(16)
}

// One signal that aborts when either input does.
function anySignal(a, b) {
  if (!b) return a
  const ctl = new AbortController()
  const abort = (s) => () => ctl.abort(s.reason)
  for (const s of [a, b]) {
    if (s.aborted) { ctl.abort(s.reason); break }
    s.addEventListener('abort', abort(s), { once: true })
  }
  return ctl.signal
}

// createHostSDK builds the object one add-on module receives.
//
//   addonId, generation   the identity this SDK is bound to
//   signal                aborted by the shell when the add-on is withdrawn
//   pages                 the declared page ids (navigation stays inside them)
//   bindings              instance callbacks supplied by the shell: navigate,
//                         notify, setDirty, openLayer, current
//   host                  the host services (React, components, theme, ...)
export function createHostSDK({ addonId, generation, signal, pages = [], bindings = {}, host } = {}) {
  if (!addonId || !Number.isInteger(generation) || generation < 1 || !signal || !host || !host.React) {
    throw new AddonSdkError('invalid_sdk', 'The host SDK needs an add-on, a generation, a signal and host services')
  }
  const { React } = host
  const disposers = []
  let disposed = false
  let dirty = false
  const bundles = new Map()

  const alive = () => {
    if (disposed || signal.aborted) throw new AddonSdkError('withdrawn', 'This add-on was withdrawn')
  }
  const runDisposers = () => {
    if (disposed) return
    disposed = true
    for (const fn of disposers.splice(0).reverse()) {
      try { fn() } catch { /* a failing disposer never blocks the others */ }
    }
    bundles.clear()
  }
  if (signal.aborted) runDisposers()
  else signal.addEventListener('abort', runDisposers, { once: true })

  const subscribeGuard = (subscribe) => (fn) => {
    alive()
    if (typeof fn !== 'function') throw new TypeError('A listener must be a function')
    const off = subscribe(fn)
    let done = false
    const stop = () => { if (!done) { done = true; try { off && off() } catch { /* ignore */ } } }
    disposers.push(stop)
    return stop
  }

  // Wrappers are created once per SDK so React sees a stable component type.
  const wrap = (Component, fixed) => {
    if (!Component) return undefined
    const Wrapped = (props) => React.createElement(Component, { ...props, ...fixed })
    Wrapped.displayName = `Addon(${Component.displayName || Component.name || 'Component'})`
    return Wrapped
  }
  const ui = {}
  for (const [name, Component] of Object.entries(host.components || {})) {
    ui[name] = wrap(Component, {})
  }
  const known = new Set(ICON_NAMES)
  const Icon = (props) => {
    const name = known.has(props?.name) ? props.name : 'puzzle'
    return React.createElement(host.Icon, { name, size: props?.size, title: props?.title, spin: props?.spin === true, className: props?.className })
  }
  Icon.displayName = 'AddonIcon'

  const translate = (key, fallback, vars) => {
    const lang = String(host.language?.() || 'en')
    const order = [lang, lang.split('-')[0], 'en']
    let text
    for (const l of order) {
      const bundle = bundles.get(l)
      if (bundle && typeof bundle[key] === 'string') { text = bundle[key]; break }
    }
    if (text === undefined) text = typeof fallback === 'string' ? fallback : String(key)
    if (vars && typeof vars === 'object') {
      text = text.replace(/\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g, (m, name) => (name in vars ? String(vars[name]) : m))
    }
    return text
  }

  const callApi = async (kind, request, options) => {
    alive()
    const body = {
      operationId: options?.operationId || newOperationId(),
      addonId,
      generation,
      action: request.action,
      arguments: request.arguments === undefined ? {} : request.arguments,
    }
    if (request.resourceId) body.resourceId = String(request.resourceId)
    const merged = anySignal(signal, options?.signal)
    return host.api[kind](body, merged)
  }

  const notify = (kind) => (message) => {
    if (disposed || signal.aborted) return
    bindings.notify?.(kind, String(message).slice(0, 500))
  }

  const layer = (kind) => (spec) => {
    alive()
    if (!spec || typeof spec !== 'object') throw new TypeError('A layer needs a spec')
    if (typeof spec.render !== 'function') throw new TypeError('A layer needs a render function')
    const handle = bindings.openLayer?.(kind, {
      title: String(spec.title || '').slice(0, 200),
      description: spec.description === undefined ? undefined : String(spec.description).slice(0, 500),
      render: spec.render,
      onClose: typeof spec.onClose === 'function' ? spec.onClose : undefined,
      wide: spec.wide === true,
    })
    if (handle && typeof handle.close === 'function') disposers.push(handle.close)
    return handle || { close() {} }
  }

  const sdk = {
    version: SDK_VERSION,
    addonId,
    generation,
    signal,
    React,
    ui: Object.freeze(ui),
    Icon,
    iconNames: ICON_NAMES,
    theme: Object.freeze({
      tokens: THEME_TOKENS,
      mode: () => String(host.theme?.mode?.() || 'light'),
      subscribe: subscribeGuard((fn) => host.theme?.subscribe?.(fn)),
    }),
    locale: Object.freeze({
      language: () => String(host.language?.() || 'en'),
      subscribe: subscribeGuard((fn) => host.subscribeLanguage?.(fn)),
      // Bundles stay in this SDK and are dropped with it.
      define(language, bundle) {
        alive()
        if (typeof language !== 'string' || !bundle || typeof bundle !== 'object') throw new TypeError('A bundle is a map of text')
        const clean = {}
        for (const [k, v] of Object.entries(bundle)) if (typeof v === 'string') clean[k] = v
        bundles.set(language, { ...(bundles.get(language) || {}), ...clean })
      },
      t: translate,
    }),
    navigation: Object.freeze({
      navigate(subpath, options) {
        alive()
        const r = resolveSubpath(subpath, pages)
        if (!r) throw new AddonSdkError('invalid_path', 'An add-on may navigate only to its own declared pages')
        bindings.navigate?.(`${addonPrefix(addonId)}/${r.path}${r.search ? `?${r.search}` : ''}`, { replace: options?.replace === true })
        return true
      },
      href(subpath) {
        const r = resolveSubpath(subpath, pages)
        if (!r) throw new AddonSdkError('invalid_path', 'An add-on may link only to its own declared pages')
        return `${addonPrefix(addonId)}/${r.path}${r.search ? `?${r.search}` : ''}`
      },
      current: () => {
        const c = bindings.current?.() || {}
        return { page: String(c.page || ''), path: String(c.path || ''), search: String(c.search || '') }
      },
    }),
    // Typed calls through the host gateway, bound to this add-on and its
    // generation. There is no raw request and no other add-on's address.
    api: Object.freeze({
      read: (request, options) => callApi('dispatch', { ...request, action: 'read' }, options),
      // A write is only ever stored as a proposal; the host confirms it.
      propose: (request, options) => callApi('propose', request || {}, options),
    }),
    notify: Object.freeze({
      info: notify('info'),
      success: notify('success'),
      warning: notify('warning'),
      error: notify('error'),
    }),
    // Unsaved edits: the shell asks before it replaces or leaves this page.
    markDirty(flag = true) {
      if (disposed || signal.aborted) return
      const next = flag !== false
      if (next === dirty) return
      dirty = next
      bindings.setDirty?.(next)
    },
    isDirty: () => dirty,
    layers: Object.freeze({ openDialog: layer('dialog'), openPanel: layer('panel') }),
    onDispose(fn) {
      if (typeof fn !== 'function') throw new TypeError('A disposer must be a function')
      if (disposed) { try { fn() } catch { /* ignore */ } return }
      disposers.push(fn)
    },
  }
  return Object.freeze(sdk)
}
