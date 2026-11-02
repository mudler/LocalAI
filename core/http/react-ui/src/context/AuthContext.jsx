import { useContext, useState, useEffect, useRef, useCallback } from 'react'
import { apiUrl } from '../utils/basePath'
import { AuthContext } from './authState'
import { addonUpgrades } from '../utils/addonUpgrades'

export function AuthProvider({ children }) {
  const [state, setState] = useState({
    sessionEpoch: 0,
    loading: true,
    authEnabled: false,
    staticApiKeyRequired: false,
    user: null,
    permissions: {},
  })

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
      .then(r => r.json())
      .then(data => {
        invalidateAuthority()
        const user = data.user || null
        const permissions = user?.permissions || {}
        setState(previous => ({
          sessionEpoch: previous.sessionEpoch + 1,
          loading: false,
          authEnabled: data.authEnabled || false,
          staticApiKeyRequired: data.staticApiKeyRequired || false,
          user,
          permissions,
        }))
      })
      .catch(() => {
        invalidateAuthority()
        setState(previous => ({ sessionEpoch: previous.sessionEpoch + 1, loading: false, authEnabled: false, staticApiKeyRequired: false, user: null, permissions: {} }))
      })
  }

  useEffect(() => {
    authority.current = {}
    fetchStatus()
    return () => { authority.current = null }
  }, [])

  const logout = async () => {
    // Detach observers and remove stored identities before leaving this page.
    // An auth-state effect may never run when navigation unloads the app.
    let persistence
    try { persistence = window.sessionStorage } catch { /* storage blocked */ }
    addonUpgrades.clearSession(persistence)
    // Invalidate in-flight observations before waiting for logout or navigation.
    invalidateAuthority()
    setState(previous => ({ ...previous, sessionEpoch: previous.sessionEpoch + 1, user: null, permissions: {} }))
    try {
      await fetch(apiUrl('/api/auth/logout'), { method: 'POST' })
    } catch (_) { /* ignore */ }
    // Clear cookies
    document.cookie = 'session=; path=/; max-age=-1'
    document.cookie = 'token=; path=/; max-age=-1'
    window.location.href = '/login'
  }

  const refresh = () => fetchStatus()

  const noAuthRequired = !state.authEnabled && !state.staticApiKeyRequired

  const hasFeature = (name) => {
    if (state.user?.role === 'admin' || noAuthRequired) return true
    return !!state.permissions[name]
  }

  const value = {
    // A local, non-secret epoch: even same-user authentication refreshes invalidate observations.
    sessionEpoch: state.sessionEpoch,
    captureSessionAuthority,
    loading: state.loading,
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
