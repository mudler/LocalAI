import { useCallback, useEffect, useState } from 'react'
import { chatProvidersApi } from '../utils/api'

// One provider slot (GET and PUT /api/chats/providers/{slot}). A pick goes to
// the server first and the screen shows what the server answers. `version`
// reloads the slot when something else changed it (the On or Off switch of the
// notes, say).
export function useProviderSlot(slot, version) {
  const [view, setView] = useState(null)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      setView(await chatProvidersApi.get(slot))
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [slot])
  useEffect(() => { load() }, [load, version])

  // Returns true when the pick was stored.
  const pick = useCallback(async (provider) => {
    setSaving(true)
    try {
      setView(await chatProvidersApi.set(slot, provider))
      setError(null)
      return true
    } catch (e) {
      setError(e)
      return false
    } finally {
      setSaving(false)
    }
  }, [slot])

  return { view, error, saving, reload: load, pick }
}
