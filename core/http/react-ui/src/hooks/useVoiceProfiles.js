import { useCallback, useEffect, useState } from 'react'
import { modelsApi, voiceProfilesApi } from '../utils/api'

export function useVoiceProfiles({ enabled = true } = {}) {
  const [profiles, setProfiles] = useState([])
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState(null)

  const fetchProfiles = useCallback(async ({ silent = false } = {}) => {
    if (!enabled) {
      setProfiles([])
      setLoading(false)
      setError(null)
      return
    }
    if (!silent) setLoading(true)
    try {
      const response = await voiceProfilesApi.list()
      setProfiles(response?.data || [])
      setError(null)
    } catch (err) {
      setError(err?.message || 'Could not load voice profiles')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [enabled])

  useEffect(() => {
    fetchProfiles()
  }, [fetchProfiles])

  const refetch = useCallback(() => fetchProfiles({ silent: true }), [fetchProfiles])
  return { profiles, loading, error, refetch }
}

export function useVoiceCloningGallery({ enabled = true, limit = 4 } = {}) {
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState(null)

  const fetchModels = useCallback(async ({ silent = false } = {}) => {
    if (!enabled) {
      setModels([])
      setLoading(false)
      setError(null)
      return
    }
    if (!silent) setLoading(true)
    try {
      const response = await modelsApi.list({
        capability: 'voice_cloning',
        items: limit,
        page: 1,
        sort: 'name',
        order: 'asc',
      })
      setModels(response?.models || [])
      setError(null)
    } catch (err) {
      setError(err?.message || 'Could not load compatible models')
    } finally {
      if (!silent) setLoading(false)
    }
  }, [enabled, limit])

  useEffect(() => {
    fetchModels()
  }, [fetchModels])

  const refetch = useCallback(() => fetchModels({ silent: true }), [fetchModels])
  return { models, loading, error, refetch }
}
