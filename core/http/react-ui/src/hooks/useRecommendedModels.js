import { useState, useEffect } from 'react'
import { modelsApi } from '../utils/api'
import { splitRecommended } from '../utils/recommendedModels'

// The curated first-run choice for this machine, from the server. The server
// owns the list, the hardware reading and the fit, and answers for every
// signed-in user, so this hook has nothing to rank or estimate. It asks once:
// the answer only changes with the hardware or the gallery.
//
// `loading` is true until the answer arrives. `error` is set when the server
// could not answer; the screens then draw nothing, because a guess made in the
// browser would claim a fit nobody checked.
export function useRecommendedModels() {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    modelsApi.recommended()
      .then(answer => { if (!cancelled) setData(answer) })
      .catch(err => { if (!cancelled) setError(err.message || String(err)) })
    return () => { cancelled = true }
  }, [])

  return { ...splitRecommended(data), data, error, loading: data === null && error === null }
}
