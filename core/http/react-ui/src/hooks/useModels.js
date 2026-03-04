import { useState, useEffect, useCallback } from 'react'
import { modelsApi } from '../utils/api'

export function useModels() {
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const fetchModels = useCallback(async () => {
    try {
      setLoading(true)
      const data = await modelsApi.listV1()
      setModels(data?.data || [])
      setError(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchModels()
  }, [fetchModels])

  return { models, loading, error, refetch: fetchModels }
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
