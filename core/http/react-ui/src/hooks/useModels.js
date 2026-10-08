import { useState, useEffect, useCallback } from 'react'
import { modelsApi } from '../utils/api'

// `enabled: false` skips the request, for a caller that already holds the list.
export function useModels(capability, { enabled = true } = {}) {
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const fetchModels = useCallback(async ({ silent = false } = {}) => {
    try {
      if (!silent) setLoading(true)
      const data = await (capability === 'decisions' ? modelsApi.listNativeCapabilities() : modelsApi.listCapabilities())
      let items = (data?.data || []).filter(m => !capability || !m.disabled)
      if (capability) {
        items = items.filter(m =>
          m.capabilities?.includes(capability) || m.capabilities?.includes(capability.replace(/^FLAG_/, '').toLowerCase()) ||
          // Models without config (loose files) have no capabilities — show them only when no filter
          false
        )
      }
      setModels(items)
      setError(null)
    } catch {
      // Fallback to /v1/models if capabilities endpoint unavailable
      try {
        const data = await modelsApi.listV1()
        setModels(capability ? [] : (data?.data || []).map(m => ({ id: m.id, capabilities: [] })))
        setError(null)
      } catch (err) {
        setError(err.message)
      }
    } finally {
      if (!silent) setLoading(false)
    }
  }, [capability])

  // Subsequent refetches stay silent so consumers don't blank their tables
  // (e.g. the Manage page auto-refreshes every 10s in distributed mode).
  const refetch = useCallback(() => fetchModels({ silent: true }), [fetchModels])

  useEffect(() => {
    if (!enabled) { setLoading(false); return }
    fetchModels()
  }, [fetchModels, enabled])

  return { models, loading, error, refetch }
}

export function useGalleryModels(params = {}) {
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [totalPages, setTotalPages] = useState(1)

  const fetchModels = useCallback(async (fetchParams) => {
    try {
      setLoading(true)
      const data = await modelsApi.list(fetchParams || params)
      setModels(data?.models || [])
      setTotalPages(data?.total_pages || 1)
      setError(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchModels(params)
  }, [params.page, params.search, params.filter, params.sort, params.order])

  return { models, loading, error, totalPages, refetch: fetchModels }
}
