import en from './i18n/en.js'
import { modelFrom as pickModel } from './lib/model.js'

// The one object every factory of the quantization add-on receives. It binds the
// SDK once: React and the JSX helper, the shared components, the
// session-bound core fetch, the translation and the small adapters that replace
// the host router and toasts. The shared job views (addons/internal/tools-ui)
// read h, Fragment, t, Icon, React, viewer and basePath from it.
export function createContext(sdk) {
  const { React } = sdk
  const h = React.createElement
  const Fragment = React.Fragment
  sdk.locale.define('en', Object.fromEntries(Object.entries(en).filter(([, v]) => typeof v === 'string')))

  const t = (key, vars) => sdk.locale.t(key, key, vars)
  // The text of a key, or the fallback when the bundle has none.
  const tOr = (key, fallback) => {
    const text = t(key)
    return text === key ? fallback : text
  }
  // The SDK translator has no plural rule: the host's `_one` and `_other`
  // variants are picked here.
  const plural = (key, count, vars) => t(`${key}_${count === 1 ? 'one' : 'other'}`, { count, ...vars })

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
  // The model a page works on: ?model=<id> first, then the first path segment.
  const modelFrom = (path) => pickModel(sdk.navigation.current().search, path)

  const toast = (message, kind = 'info') => (sdk.notify[kind] || sdk.notify.info)(message)

  // The path prefix LocalAI is served under, read the way the host reads it
  // (utils/basePath.js: the <base href> element).
  const basePath = (() => {
    if (typeof document === 'undefined') return ''
    const el = document.querySelector('base[href]')
    if (!el) return ''
    try { return new URL(el.getAttribute('href')).pathname.replace(/\/+$/, '') } catch { return '' }
  })()

  // A JSON call of a core route. A failure keeps the server's message.
  const json = async (path, init) => {
    const response = await sdk.core.fetch(path, init)
    if (!response.ok) {
      const body = await response.json().catch(() => ({}))
      throw new Error(body?.error?.message || body?.error || `HTTP ${response.status}`)
    }
    return response.json()
  }

  return {
    sdk, React, h, Fragment, t, tOr, plural, Icon: sdk.Icon, ui: sdk.ui, hooks: sdk.hooks, viewer: sdk.viewer, core: sdk.core,
    Link, useSearch, modelFrom, toast, json, basePath,
    openPanel: (title, render) => sdk.layers.openPanel({ title, render }),
    // A dialog the host draws: focus, Escape and the veil are the host's.
    openDialog: (spec) => sdk.layers.openDialog(spec),
    markDirty: (flag) => sdk.markDirty(flag),
  }
}
