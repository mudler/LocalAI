import { useState, useEffect } from 'react'
import { useAuth } from '../../context/AuthContext'
import { apiUrl } from '../../utils/basePath'

// /api/features rarely changes; cache it across remounts so a hub renders the
// correct (gated) set immediately instead of flashing the wrong set while a
// fresh fetch resolves.
let featuresCache = {}

// The shape the gating helpers in hubConfig.js read.
export function useHubAuth() {
  const { isAdmin, authEnabled, hasFeature } = useAuth()
  const [features, setFeatures] = useState(featuresCache)
  // True once /api/features answered or failed, so a route that depends on a
  // switch does not act on the empty map of a first render.
  const [featuresLoaded, setFeaturesLoaded] = useState(Object.keys(featuresCache).length > 0)

  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(f => { featuresCache = f; setFeatures(f); setFeaturesLoaded(true) })
      .catch(() => { setFeaturesLoaded(true) })
  }, [])

  return { isAdmin, authEnabled, hasFeature, features, featuresLoaded }
}
