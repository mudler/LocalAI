import { useCallback, useEffect, useState } from 'react'
import { chatMemoryApi } from '../utils/api'

// Your memory choices (GET and PUT /api/chats/memory): the switch for all
// chats and how a note is saved. A change goes to the server first and the
// screen shows what the server answers, so the page never shows a choice that
// was not stored.
export function useMemorySettings() {
  const [settings, setSettings] = useState(null)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      setSettings(await chatMemoryApi.get())
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [])
  useEffect(() => { load() }, [load])

  // Returns true when the change was stored.
  const change = useCallback(async (body) => {
    setSaving(true)
    try {
      setSettings(await chatMemoryApi.set(body))
      setError(null)
      return true
    } catch (e) {
      setError(e)
      return false
    } finally {
      setSaving(false)
    }
  }, [])

  return { settings, error, saving, reload: load, change }
}
