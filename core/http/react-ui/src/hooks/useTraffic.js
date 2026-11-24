import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import { useAuth } from '../context/AuthContext'
import { apiUrl } from '../utils/basePath'
import { settingsApi, tracesApi, usageApi } from '../utils/api'
import { DEFAULT_WINDOW, WINDOWS, windowById } from '../utils/traffic'
import { usePolling } from './usePolling'

// ---------------------------------------------------------------------------
// The time window, shared by every Traffic page
// ---------------------------------------------------------------------------

// A tiny external store, so the choice made on the Overview is still the one on
// Usage and on Models without a provider above the router. It lives for the
// session; a reload starts again on 24 hours.
const KEY = 'localai.traffic.window'
const listeners = new Set()
let current = DEFAULT_WINDOW
try {
  const saved = window.sessionStorage.getItem(KEY)
  if (WINDOWS.some(w => w.id === saved)) current = saved
} catch { /* no session storage: the default stands */ }

function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function setTrafficWindow(id) {
  if (!WINDOWS.some(w => w.id === id) || id === current) return
  current = id
  try { window.sessionStorage.setItem(KEY, id) } catch { /* ignore */ }
  listeners.forEach(fn => fn())
}

export function useTrafficWindow() {
  const id = useSyncExternalStore(subscribe, () => current, () => DEFAULT_WINDOW)
  return { id, window: windowById(id), setWindow: setTrafficWindow }
}

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

// The ledger rows the pages draw. An admin sees every user's; anyone else sees
// their own, which is all the server will give them. /api/usage works without
// auth (the local user), /api/auth/usage is the signed-in path, and both are
// kept as they were so quotas and per-key rows stay consistent.
export function useUsage(period, { sources = false } = {}) {
  const { isAdmin, authEnabled, loading: authLoading } = useAuth()
  const [state, setState] = useState({ loading: true, error: null, rows: [], totals: null, quotas: [], keys: null })
  const [nonce, setNonce] = useState(0)
  const reload = useCallback(() => setNonce(n => n + 1), [])

  useEffect(() => {
    if (authLoading) return undefined
    let cancelled = false
    setState(s => ({ ...s, loading: true, error: null }))
    ;(async () => {
      try {
        const own = authEnabled ? '/api/auth/usage' : '/api/usage'
        const admin = authEnabled ? '/api/auth/admin/usage' : '/api/usage/all'
        const get = async url => {
          const res = await fetch(apiUrl(`${url}?period=${period}`))
          if (!res.ok) throw new Error(`HTTP ${res.status}`)
          return res.json()
        }
        const [mine, quota, all, keys] = await Promise.all([
          isAdmin ? Promise.resolve(null) : get(own),
          authEnabled ? usageApi.getMyQuotas().catch(() => null) : Promise.resolve(null),
          isAdmin ? get(admin) : Promise.resolve(null),
          sources && authEnabled
            ? (isAdmin ? usageApi.getAdminSources(period) : usageApi.getMySources(period)).catch(() => null)
            : Promise.resolve(null),
        ])
        if (cancelled) return
        const data = isAdmin ? all : mine
        setState({
          loading: false,
          error: null,
          rows: data?.usage || [],
          totals: data?.totals || null,
          quotas: quota?.quotas || [],
          keys,
        })
      } catch (error) {
        if (!cancelled) setState(s => ({ ...s, loading: false, error }))
      }
    })()
    return () => { cancelled = true }
  }, [period, isAdmin, authEnabled, authLoading, sources, nonce])

  return { ...state, isAdmin, authEnabled, reload }
}

// ---------------------------------------------------------------------------
// Traces
// ---------------------------------------------------------------------------

// Whether the server records requests. Null until the settings answer, and
// after a failure: a page that does not know does not claim either state.
export function useTracingEnabled() {
  const [state, setState] = useState({ enabled: null, backend: null, settings: null })
  const reload = useCallback(() => {
    settingsApi.get()
      .then(data => setState({ enabled: !!data.enable_tracing, backend: !!data.enable_backend_logging, settings: data }))
      .catch(() => {})
  }, [])
  useEffect(() => { reload() }, [reload])
  return { ...state, reload, setSettings: settings => setState(s => ({ ...s, settings })) }
}

// The counted view of the trace buffer over `hours`, polled while the page is
// open. `error` is set when the endpoint cannot be read; the totals then stay
// null so no figure is drawn at zero.
export function useTraceSummary(hours, { intervalMs = 15_000, enabled = true } = {}) {
  const [state, setState] = useState({ loading: true, summary: null, error: null })
  const read = useCallback(async () => {
    try {
      const summary = await tracesApi.summary(hours)
      setState({ loading: false, summary, error: null })
    } catch (error) {
      setState({ loading: false, summary: null, error })
    }
  }, [hours])
  const { refetch } = usePolling(read, intervalMs, { enabled })
  return { ...state, refetch }
}
