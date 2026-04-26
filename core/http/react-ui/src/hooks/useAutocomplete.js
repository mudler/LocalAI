import { useState, useEffect } from 'react'
import { modelsApi } from '../utils/api'

// Module-level cache so each provider is fetched once per page load
const cache = {}

// Shared fetch-with-cache for use outside React hooks (e.g. CodeMirror completions)
export async function fetchCachedAutocomplete(provider) {
  if (cache[provider]) return cache[provider].values
  try {
    const data = await modelsApi.getAutocomplete(provider)
    const vals = data?.values || []
    cache[provider] = { values: vals }
    return vals
  } catch {
    return []
  }
}

export function useAutocomplete(provider) {
  const [values, setValues] = useState(cache[provider]?.values || [])
  const [loading, setLoading] = useState(!cache[provider])

  useEffect(() => {
    if (!provider) {
      setValues([])
      setLoading(false)
      return
    }
    if (cache[provider]) {
      setValues(cache[provider].values)
      setLoading(false)
      return
    }
    setLoading(true)
    modelsApi.getAutocomplete(provider)
      .then(data => {
        const vals = data?.values || []
        cache[provider] = { values: vals }
        setValues(vals)
      })
      .catch(() => setValues([]))
      .finally(() => setLoading(false))
  }, [provider])

  return { values, loading }
}
