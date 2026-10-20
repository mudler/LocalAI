// The host SDK: the only object an add-on UI module receives. It is an
// allowlist of wrappers over the host, never a window onto it. This file has
// no JSX and no host imports so the node unit tests drive it directly; the
// shell supplies the real host services (see hostServices.jsx).
//
// Everything an add-on can do is bound to its own identity and installed
// generation. After `signal` aborts (disabled, updated, crashed, uninstalled,
// the page closed) every service refuses, and the host server denies a stale
// generation anyway, so a copy of old code that stays in memory can do nothing.

import { resourceOperationId, resourceRequest } from './resourceCalls.js'

export const SDK_VERSION = 2

// Semantic icon names an add-on may draw. The host Icon wrapper falls back to a
// neutral glyph for any other name, so a module cannot probe the sprite.
export const ICON_NAMES = Object.freeze([
  'puzzle', 'file', 'file-text', 'folder', 'table', 'database', 'search', 'plus', 'minus', 'close', 'check',
  'check-circle', 'alert-circle', 'warning', 'info', 'edit', 'trash', 'download', 'upload', 'refresh',
  'settings', 'list', 'grid', 'chart-line', 'clock', 'calendar', 'tag', 'star', 'link', 'external-link',
  'copy', 'eye', 'lock', 'user', 'users', 'message', 'sparkles', 'chevron-down', 'chevron-up', 'chevron-right',
  'arrow-left', 'arrow-right', 'chart-bar', 'circle-dot', 'clipboard', 'code', 'cube', 'filter-off', 'gender',
  'globe', 'headphones', 'help-circle', 'mic', 'play', 'plug-off', 'save', 'shield', 'smile', 'waveform',
  'camera', 'stop', 'circle', 'image', 'spinner', 'user-shield', 'mask',
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

// Core routes an add-on page may call. The add-on calls them with the signed-in
// user's own session, through the same authentication and authorization as any
// browser request, so it can do nothing the user cannot. Two prefixes are
// refused: the add-on gateway has its own identity-bound typed calls, and the
// auth routes hold account credentials.
const CORE_DENIED = ['/api/addons', '/api/auth']

export function coreRequestUrl(path, basePath = '') {
  if (typeof path !== 'string' || path.length === 0 || path.length > 2048) return null
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\')) return null
  // A browser would fold dot segments, written plain or as %2e, and strip tabs
  // and newlines. Work on the raw text so none of that can hide a segment.
  if (/[\u0000-\u0020\u007f]/.test(path)) return null
  const hash = path.indexOf('#')
  const noHash = hash >= 0 ? path.slice(0, hash) : path
  const q = noHash.indexOf('?')
  const pathname = q >= 0 ? noHash.slice(0, q) : noHash
  const search = q >= 0 ? noHash.slice(q) : ''
  // Decode every segment. The denied prefixes are compared on the decoded path,
  // so /api/%61ddons/apps is refused. An encoded slash or backslash in the
  // first two segments (the namespace) can hide a boundary from that check, so
  // it is refused there; later it is a legitimate identifier (a%2Fb).
  const segments = pathname.split('/')
  const decodedSegments = []
  for (let i = 0; i < segments.length; i++) {
    let decoded
    try { decoded = decodeURIComponent(segments[i]) } catch { return null }
    if (decoded === '.' || decoded === '..') return null
    if (i <= 2 && (decoded.includes('/') || decoded.includes('\\'))) return null
    decodedSegments.push(decoded)
  }
  const lower = decodedSegments.join('/').toLowerCase()
  if (CORE_DENIED.some(p => lower === p || lower.startsWith(`${p}/`))) return null
  return `${basePath}${pathname}${search}`
}

// An operation identifier is a namespace name to the owner: it starts with a
// letter and holds lower-case letters, digits and hyphens. A bare UUID that
// starts with a digit is refused, so the prefix is part of the identifier.
function newOperationId() {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') return `op-${c.randomUUID()}`
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
  // Shared hooks of the host, exposed as the host's own functions. They run in
  // the host's React, so a hook of an add-on page and a hook of a core page are
  // the same code.
  const hooks = {}
  for (const [name, fn] of Object.entries(host.hooks || {})) {
    if (typeof fn === 'function') hooks[name] = fn
  }
  // What the add-on may know about the signed-in user: the role and the
  // feature flags the host already shows in its own UI. The server still
  // decides every call.
  const viewer = Object.freeze({
    isAdmin: () => host.viewer?.isAdmin?.() === true,
    hasFeature: (name) => host.viewer?.hasFeature?.(String(name)) === true,
  })
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

  // The resource management of this add-on: its own resources, sharing,
  // the conversation selection and the changes a person makes in its pages.
  // Each call is bound to this add-on and generation and is shaped by the
  // host (resourceCalls.js); the host server decides every one.
  const resourceCall = async (op, params, options) => {
    alive()
    const body = { ...params, op, addonId, generation }
    // A malformed call is refused here, before the host sees it.
    resourceRequest(body)
    return host.api.resource(body, anySignal(signal, options?.signal))
  }
  const operation = (options) => (typeof options?.operationId === 'string' && options.operationId ? options.operationId : resourceOperationId())
  const text = (v) => (typeof v === 'string' ? v : '')
  const resources = Object.freeze({
    list: (options) => resourceCall('list', {}, options),
    create: (spec, options) => resourceCall('create', { arguments: spec?.arguments, operationId: operation(options) }, options),
    rename: (resourceId, spec, options) => resourceCall('rename', { resourceId, name: text(spec?.name), revision: spec?.revision, operationId: operation(options) }, options),
    remove: (resourceId, spec, options) => resourceCall('delete', { resourceId, confirm: text(spec?.confirm), revision: spec?.revision, operationId: operation(options) }, options),
    // One data or schema change by the person's own action. The host
    // checks the role, the declared tool, the revision and the receipt.
    apply: (resourceId, spec, options) => resourceCall('apply', { resourceId, tool: text(spec?.tool), arguments: spec?.arguments, revision: spec?.revision, operationId: operation(options) }, options),
    operation: (resourceId, kind, operationId, options) => resourceCall('status', { resourceId, kind, operationId }, options),
    grants: (resourceId, options) => resourceCall('grants', { resourceId }, options),
    setGrant: (resourceId, userId, role, options) => resourceCall('grant-set', { resourceId, userId, role, operationId: operation(options) }, options),
    revokeGrant: (resourceId, userId, options) => resourceCall('grant-revoke', { resourceId, userId, operationId: operation(options) }, options),
    searchUsers: (resourceId, query, options) => resourceCall('users', { resourceId, query, limit: options?.limit }, options),
    // The resource a conversation works on. Chat tool calls that name no
    // resource use it; the owner authorizes it again on every use.
    selection: Object.freeze({
      get: (conversationId, options) => resourceCall('selection-get', { conversationId }, options),
      set: (conversationId, resourceId, options) => resourceCall('selection-put', { conversationId, resourceId }, options),
      clear: (conversationId, options) => resourceCall('selection-clear', { conversationId }, options),
    }),
  })

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

  const core = Object.freeze({
    // fetch for a core route, with the user's own session. No token, cookie
    // value or header of the add-on's choosing reaches the request, and the
    // call ends when the add-on is withdrawn.
    async fetch(path, init = {}) {
      alive()
      const url = coreRequestUrl(path, host.basePath || '')
      if (!url) throw new AddonSdkError('invalid_path', 'An add-on may call only same-origin core API paths')
      const headers = new Headers(init.headers || {})
      headers.delete('authorization')
      headers.delete('cookie')
      return (host.fetch || globalThis.fetch)(url, {
        method: init.method,
        headers,
        body: init.body,
        signal: anySignal(signal, init.signal),
        credentials: 'same-origin',
        mode: 'same-origin',
        redirect: 'error',
        cache: 'no-store',
      })
    },
  })
  const sdk = {
    version: SDK_VERSION,
    addonId,
    generation,
    signal,
    React,
    ui: Object.freeze(ui),
    hooks: Object.freeze(hooks),
    viewer,
    core,
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
    resources,
    // Reads that answer with private files. The result is the host's own
    // description of each file (identifier, name, media type, size): the
    // add-on's handle, a path or a URL never reaches the add-on's page. The
    // file is drawn by sdk.ui.ArtifactCard from the identifier.
    files: Object.freeze({
      read: (request, options) => callApi('dispatchFile', { ...request, action: 'read' }, options),
    }),
    // Asks the host to show the stored proposal of this add-on in the host's
    // approval frame. The frame, Confirm and Cancel stay the host's: the
    // add-on only names the proposal and may supply the card of its details.
    approvals: Object.freeze({
      present(proposalId) {
        alive()
        if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(String(proposalId))) throw new TypeError('A proposal identifier is required')
        bindings.presentApproval?.(String(proposalId))
      },
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
