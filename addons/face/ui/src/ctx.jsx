import en from './i18n/en.js'
import { modelFrom as pickModel } from './lib/model.js'

// The one object every page factory of the Face add-on receives. It binds the
// SDK once: React, the shared components, the session-bound core fetch, the
// translation and the small adapters that replace the host router and toasts.
export function createContext(sdk) {
  const { React } = sdk
  const h = React.createElement
  const Fragment = React.Fragment
  sdk.locale.define('en', Object.fromEntries(Object.entries(en).filter(([, v]) => typeof v === 'string')))
  const lists = Object.fromEntries(Object.entries(en).filter(([, v]) => Array.isArray(v)))

  const t = (key, vars) => sdk.locale.t(key, key, vars)
  // A list of strings or objects (the "why" and "stored" notes).
  const list = (key) => lists[key] || []

  // A link to a page of this add-on, as an anchor the shell navigates.
  function Link({ page, query, className, children, ...rest }) {
    const href = sdk.navigation.href(query ? `${page}?${query}` : page)
    return h('a', {
      href, className,
      onClick: (e) => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; e.preventDefault(); sdk.navigation.navigate(query ? `${page}?${query}` : page) },
      ...rest,
    }, children)
  }

  const useSearch = () => new URLSearchParams(sdk.navigation.current().search)
  // The model a page works on: ?model=<id> first (a moved link carries it
  // there, because a model id may hold "/" or ":" that a path segment cannot),
  // then the first path segment of a link typed by hand.
  const modelFrom = (path) => pickModel(sdk.navigation.current().search, path)

  const toast = (message, kind = 'info') => (sdk.notify[kind] || sdk.notify.info)(message)

  // The path prefix LocalAI is served under, read the way the host reads it
  // (utils/basePath.js: the <base href> element). document.baseURI would be the
  // address of the current page when there is no such element.
  const basePath = (() => {
    if (typeof document === 'undefined') return ''
    const el = document.querySelector('base[href]')
    if (!el) return ''
    try { return new URL(el.getAttribute('href')).pathname.replace(/\/+$/, '') } catch { return '' }
  })()

  const json = async (path, init) => {
    const response = await sdk.core.fetch(path, init)
    if (!response.ok) {
      const body = await response.json().catch(() => ({}))
      throw new Error(body?.error?.message || `HTTP ${response.status}`)
    }
    return response.json()
  }

  // The undo bar of a delayed action: text and an Undo button. Replaces the
  // host's toast with an undo, which the SDK does not expose.
  function UndoBar({ text, undoLabel, onUndo }) {
    return h('div', { className: 'vc-undo', role: 'status' }, h('span', null, text), h(sdk.ui.Button, { variant: 'ghost', size: 'sm', onClick: onUndo }, undoLabel))
  }

  return {
    sdk, React, h, Fragment, t, list, Icon: sdk.Icon, ui: sdk.ui, hooks: sdk.hooks, viewer: sdk.viewer, core: sdk.core,
    Link, useSearch, modelFrom, toast, json, UndoBar, basePath,
    openPanel: (title, render) => sdk.layers.openPanel({ title, render }),
    markDirty: (flag) => sdk.markDirty(flag),
  }
}
