import { useContext, useState, useEffect, useRef, useCallback } from 'react'
import { apiUrl } from '../utils/basePath'
import { AuthContext } from './authState'
import { addonUpgrades } from '../utils/addonUpgrades'
import { initialSession, sessionStep, retryDelay, isSessionExempt } from '../utils/authSession'
import { setUnauthorizedHandler } from '../utils/sessionEvents'

export function AuthProvider({ children }) {
  // The rules of the session are in utils/authSession.js; this applies them.
  const [state, setState] = useState(initialSession)
  const dispatch = useCallback((event) => setState(previous => sessionStep(previous, event)), [])
  const stateRef = useRef(state)
  stateRef.current = state

  // Background work can outlive its initiating view. Keep its non-secret
  // authority here, and invalidate before React schedules consumer renders.
  const authority = useRef({})
  const captureSessionAuthority = useCallback(() => {
    const origin = authority.current
    return () => origin !== null && authority.current === origin
  }, [])
  const invalidateAuthority = () => { if (authority.current !== null) authority.current = {} }

  const fetchStatus = () => {
    return fetch(apiUrl('/api/auth/status'))
      .then(r => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json()
      })
      .then(data => {
        invalidateAuthority()
        dispatch({ type: 'status-ok', data })
      })
      .catch(() => {
        // The server could not be asked. That says nothing about its auth
        // mode, so the UI shows "unreachable" and keeps asking.
        invalidateAuthority()
        dispatch({ type: 'status-failed' })
      })
  }

  useEffect(() => {
    authority.current = {}
    fetchStatus()
    return () => { authority.current = null }
  }, [])

  // While the server is unreachable, ask again with a growing wait.
  const retryNow = useCallback(() => {
    if (stateRef.current.status !== 'unreachable' || stateRef.current.retrying) return Promise.resolve()
    dispatch({ type: 'retry' })
    return fetchStatus()
  }, [])
  useEffect(() => {
    if (state.status !== 'unreachable' || state.retrying) return undefined
    const timer = setTimeout(retryNow, retryDelay(state.failures))
    return () => clearTimeout(timer)
  }, [state.status, state.retrying, state.failures, retryNow])

  // A 401 from the API while signed in may be an expired session. Ask the
  // status route once, however many calls failed together, and end the session
  // only when it says nobody is signed in. A 401 that came from something
  // else (an upstream service) leaves the session alone.
  const verifying = useRef(null)
  useEffect(() => setUnauthorizedHandler((url) => {
    const s = stateRef.current
    if (s.status !== 'signed-in' || !s.authEnabled || isSessionExempt(url) || verifying.current) return
    verifying.current = fetch(apiUrl('/api/auth/status'))
      .then(r => (r.ok ? r.json() : null))
      .then(data => {
        if (data && !data.user && data.authEnabled) {
          invalidateAuthority()
          let persistence
          try { persistence = window.sessionStorage } catch { /* storage blocked */ }
          addonUpgrades.clearSession(persistence)
          dispatch({ type: 'unauthorized', url })
        }
      })
      .catch(() => {})
      .finally(() => { verifying.current = null })
  }), [])

  const logout = async () => {
    // Detach observers and remove stored identities before leaving this page.
    // An auth-state effect may never run when navigation unloads the app.
    let persistence
    try { persistence = window.sessionStorage } catch { /* storage blocked */ }
    addonUpgrades.clearSession(persistence)
    // Invalidate in-flight observations before waiting for logout or navigation.
    invalidateAuthority()
    dispatch({ type: 'logout' })
    try {
      await fetch(apiUrl('/api/auth/logout'), { method: 'POST' })
    } catch (_) { /* ignore */ }
    // Clear cookies
    document.cookie = 'session=; path=/; max-age=-1'
    document.cookie = 'token=; path=/; max-age=-1'
    window.location.href = '/login'
  }

  const refresh = () => fetchStatus()

  // An unreachable server has not said that auth is off.
  const noAuthRequired = !state.authEnabled && !state.staticApiKeyRequired && state.status !== 'unreachable'

  const hasFeature = (name) => {
    if (state.user?.role === 'admin' || noAuthRequired) return true
    return !!state.permissions[name]
  }

  const value = {
    // A local, non-secret epoch: even same-user authentication refreshes invalidate observations.
    sessionEpoch: state.sessionEpoch,
    captureSessionAuthority,
    loading: state.status === 'loading',
    // The session's state: loading, unreachable, anonymous, signed-in or expired.
    status: state.status,
    unreachable: state.status === 'unreachable',
    retrying: state.retrying,
    retry: retryNow,
    authEnabled: state.authEnabled,
    staticApiKeyRequired: state.staticApiKeyRequired,
    user: state.user,
    permissions: state.permissions,
    isAdmin: state.user?.role === 'admin' || noAuthRequired,
    hasFeature,
    logout,
    refresh,
  }

  return (
    <AuthContext.Provider value={value}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
