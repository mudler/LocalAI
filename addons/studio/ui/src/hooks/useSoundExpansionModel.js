import { useCallback, useEffect, useState } from 'react'
import { useModels } from './useModels'
import { CAP_CHAT } from '../utils/capabilities'

export const BACKEND_DEFAULT = '__backend_default__'
const PREFERENCE_KEY = 'localai_sound_expansion_model'

function readPreference() {
  try { return localStorage.getItem(PREFERENCE_KEY) || '' } catch { return '' }
}

function writePreference(value) {
  try {
    if (value) localStorage.setItem(PREFERENCE_KEY, value)
    else localStorage.removeItem(PREFERENCE_KEY)
  } catch { /* private mode or storage quota: keep the current session's choice */ }
}

// Sound must ask again when a selected model disappears, rather than silently
// switching the LLM. It also must not change the general chat preference.
export function useSoundExpansionModel() {
  const { models, loading, error, refetch } = useModels(CAP_CHAT)
  const [selected, setSelected] = useState(readPreference)
  const valid = selected === BACKEND_DEFAULT || (!loading && !error && models.some(m => m.id === selected))

  useEffect(() => {
    if (!loading && !error && selected && !valid) {
      setSelected('')
      writePreference('')
    }
  }, [loading, error, selected, valid])

  // Installation and capability edits may happen in another tab. Validate the
  // saved choice against the refreshed list when the user returns to Sound.
  useEffect(() => {
    window.addEventListener('focus', refetch)
    return () => window.removeEventListener('focus', refetch)
  }, [refetch])

  const choose = useCallback(value => {
    setSelected(value)
    writePreference(value)
  }, [])

  return { value: valid ? selected : '', choose, models, loading, error }
}
