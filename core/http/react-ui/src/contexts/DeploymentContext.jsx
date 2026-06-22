import { createContext, useContext, useState, useEffect } from 'react'
import { apiUrl } from '../utils/basePath'
import { p2pApi } from '../utils/api'

const DeploymentContext = createContext(null)

// One shared fetch of the deployment-shape signals the adaptive UI keys off:
// server features (/api/features) and whether a P2P network token exists.
// Components used to fetch /api/features independently (Sidebar, Home); this
// centralises it so the landing resolver, sidebar policy, and navbar agree on
// one snapshot and we issue a single request.
export function DeploymentProvider({ children }) {
  const [features, setFeatures] = useState({})
  const [p2pEnabled, setP2pEnabled] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    const featuresP = fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .catch(() => ({}))
    // P2P has no /api/features flag: it is "enabled" when a network token
    // exists (mirrors pages/P2P.jsx). A 404/disabled endpoint throws and we
    // treat that as not-enabled.
    const p2pP = p2pApi.getToken()
      .then(tok => (typeof tok === 'string' ? tok : (tok?.token || '')).trim())
      .catch(() => '')
    Promise.all([featuresP, p2pP]).then(([f, tok]) => {
      if (cancelled) return
      setFeatures(f || {})
      setP2pEnabled(!!tok)
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [])

  const value = {
    features,
    distributed: !!features.distributed,
    p2pEnabled,
    loading,
  }

  return (
    <DeploymentContext.Provider value={value}>
      {children}
    </DeploymentContext.Provider>
  )
}

export function useDeployment() {
  const ctx = useContext(DeploymentContext)
  if (!ctx) throw new Error('useDeployment must be used within DeploymentProvider')
  return ctx
}
