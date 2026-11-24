// Stand-in for `react-router-dom`: the hooks and the link the Studio and Talk
// pages use, over the SDK's navigation. An address under /app that is not a page
// of this add-on is opened with sdk.navigation.openApp; the pages of this
// add-on are reached with sdk.navigation.navigate. See ../route.js.
import { getSdk } from '../runtime.js'
import { paramsOf, resolveTarget } from '../route.js'
import { basePath } from '../utils/basePath.js'

const React = getSdk().React
const { createContext, createElement, useCallback, useContext, useMemo } = React

const RouteContext = createContext({ page: '', path: '', search: '' })

// The add-on wraps its page in this, with what the shell passed it.
export const RouteProvider = RouteContext.Provider

function go(to, options = {}) {
  const sdk = getSdk()
  const target = resolveTarget(to, sdk.addonId)
  if (!target) throw new Error(`Cannot open ${String(to)}`)
  if (target.own) {
    sdk.navigation.navigate(target.subpath, { replace: options.replace === true })
  } else {
    sdk.navigation.openApp(target.address, { replace: options.replace === true, state: options.state })
  }
}

function hrefOf(to) {
  const sdk = getSdk()
  const target = resolveTarget(to, sdk.addonId)
  if (!target) return String(to)
  return basePath + (target.own ? sdk.navigation.href(target.subpath) : target.address)
}

export function useNavigate() {
  return useCallback((to, options) => {
    if (typeof to === 'number') { window.history.go(to); return }
    go(to, options)
  }, [])
}

export function useParams() {
  const route = useContext(RouteContext)
  return useMemo(() => paramsOf(route), [route])
}

export function useSearchParams() {
  const route = useContext(RouteContext)
  const params = useMemo(() => new URLSearchParams(route.search), [route.search])
  const setSearchParams = useCallback((next, options) => {
    const sdk = getSdk()
    const value = typeof next === 'function' ? next(new URLSearchParams(route.search)) : next
    const query = new URLSearchParams(value).toString()
    const base = [route.page, ...String(route.path || '').split('/').filter(Boolean)].join('/')
    sdk.navigation.navigate(`${base}${query ? `?${query}` : ''}`, { replace: options?.replace === true })
  }, [route.page, route.path, route.search])
  return [params, setSearchParams]
}

export function useLocation() {
  const route = useContext(RouteContext)
  return useMemo(() => {
    const sdk = getSdk()
    const base = [route.page, ...String(route.path || '').split('/').filter(Boolean)].join('/')
    return { pathname: base ? sdk.navigation.href(base) : '', search: route.search ? `?${route.search}` : '', hash: '', state: null }
  }, [route])
}

// Pages call addToast(message, kind) on the host layout's outlet context.
export function useOutletContext() {
  return useMemo(() => ({
    addToast: (message, kind = 'info') => {
      const notify = getSdk().notify
      ;(notify[kind] || notify.info)(String(message))
    },
  }), [])
}

export function Link({ to, replace, state, onClick, children, ...rest }) {
  return createElement('a', {
    ...rest,
    href: hrefOf(to),
    onClick: (event) => {
      onClick?.(event)
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
      event.preventDefault()
      go(to, { replace, state })
    },
  }, children)
}
