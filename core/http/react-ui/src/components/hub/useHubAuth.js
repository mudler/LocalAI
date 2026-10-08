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

  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(f => { featuresCache = f; setFeatures(f) })
      .catch(() => {})
  }, [])

  return { isAdmin, authEnabled, hasFeature, features }
}
