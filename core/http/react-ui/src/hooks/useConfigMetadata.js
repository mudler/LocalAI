import { useState, useEffect } from 'react'
import { modelsApi } from '../utils/api'

export function useConfigMetadata() {
  const [metadata, setMetadata] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  useEffect(() => {
    modelsApi.getConfigMetadata('all')
      .then(data => setMetadata(data))
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  return {
    sections: metadata?.sections || [],
    fields: metadata?.fields || [],
    loading,
    error,
  }
}
