import { createContext, useContext, useState, useEffect } from 'react'
import { apiUrl } from '../utils/basePath'

const AuthContext = createContext(null)

export function AuthProvider({ children }) {
  const [state, setState] = useState({
    loading: true,
    authEnabled: false,
    staticApiKeyRequired: false,
    user: null,
    permissions: {},
  })

  const fetchStatus = () => {
    return fetch(apiUrl('/api/auth/status'))
      .then(r => r.json())
      .then(data => {
        const user = data.user || null
        const permissions = user?.permissions || {}
        setState({
          loading: false,
          authEnabled: data.authEnabled || false,
          staticApiKeyRequired: data.staticApiKeyRequired || false,
          user,
          permissions,
        })
      })
      .catch(() => {
        setState({ loading: false, authEnabled: false, staticApiKeyRequired: false, user: null, permissions: {} })
      })
  }

  useEffect(() => {
    fetchStatus()
  }, [])

  const logout = async () => {
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
