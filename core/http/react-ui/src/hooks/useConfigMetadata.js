import { useState, useEffect } from 'react'
import { modelsApi } from '../utils/api'

// Stable empty references so consumers that memoize on `sections`/`fields`
// (e.g. ModelEditor's leafPaths) don't see a new array every render while
// the metadata request is still in flight — which would thrash their effects.
const EMPTY = []

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
    sections: metadata?.sections || EMPTY,
    fields: metadata?.fields || EMPTY,
    loading,
    error,
  }
}
