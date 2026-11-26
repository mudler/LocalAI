import { useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'

const DEBOUNCE_MS = 500

// Asks POST /api/models/vram-estimate for several bodies at once (the current
// settings and each quick setup) and answers { status, answers } where
// answers maps each id to the response. A body of null is skipped. The
// requests wait until the bodies stop changing, and a newer set aborts the
// older one.
//
//   bodies   { [id]: body | null }
export function useRuntimeEstimates(bodies) {
  const key = JSON.stringify(bodies || {})
  const [state, setState] = useState({ status: 'idle', answers: {} })

  useEffect(() => {
    const wanted = Object.entries(JSON.parse(key)).filter(([, b]) => b)
    if (wanted.length === 0) {
      setState({ status: 'idle', answers: {} })
      return undefined
    }
    const controller = new AbortController()
    setState(s => ({ ...s, status: 'loading' }))
    const timer = setTimeout(async () => {
      try {
        const got = await Promise.all(wanted.map(([id, body]) =>
          modelsApi.estimateVram(body, { signal: controller.signal }).then(a => [id, a])))
        if (!controller.signal.aborted) setState({ status: 'ready', answers: Object.fromEntries(got) })
      } catch {
        if (!controller.signal.aborted) setState({ status: 'error', answers: {} })
      }
    }, DEBOUNCE_MS)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [key])

  return state
}
