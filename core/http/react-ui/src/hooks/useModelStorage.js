import { useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'
import { indexStorage } from '../utils/modelStorage'

// The on-disk size report, or the reason there is none.
//
//   status  'loading' until the first answer, then 'ready' or 'unavailable'
//   index   indexStorage() of the report; null unless ready
//
// The endpoint is for admins. A refusal, an old server without the endpoint
// and a failed read all end the same way, 'unavailable', and the callers fall
// back to the gallery's estimates. It is never an error on the page.
export function useModelStorage(enabled = true, refreshToken = 0) {
  const [state, setState] = useState({ status: 'loading', index: null })

  useEffect(() => {
    if (!enabled) return undefined
    let cancelled = false
    modelsApi.getStorage()
      .then(report => {
        if (cancelled) return
        const index = indexStorage(report)
        setState(index ? { status: 'ready', index } : { status: 'unavailable', index: null })
      })
      .catch(() => { if (!cancelled) setState({ status: 'unavailable', index: null }) })
    return () => { cancelled = true }
  }, [enabled, refreshToken])

  return state
}
