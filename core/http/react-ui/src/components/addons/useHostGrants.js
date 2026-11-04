import { useCallback, useEffect, useRef, useState } from 'react'
import { hostGrantsApi } from '../../utils/addonsClient'
import { normalizeOverview } from './hostGrantsState'

// The overview of the host access of every add-on. `mutate(fn)` runs one
// write of the administrator, then reads the overview again; a refusal comes
// back as { error } and nothing is hidden: the page reads again either way, so
// a stale tab shows what the owner holds now.
export function useHostGrants({ api = hostGrantsApi, enabled = true } = {}) {
  const [state, setState] = useState({ status: 'loading', data: null, error: null })
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const load = useCallback(async (signal) => {
    try {
      const data = normalizeOverview(await api.overview(signal))
      if (alive.current) setState({ status: 'ready', data, error: null })
      return data
    } catch (error) {
      if (error && error.name === 'AbortError') return null
      if (alive.current) setState(s => ({ status: s.data ? 'ready' : 'error', data: s.data, error, stale: Boolean(s.data) }))
      return null
    }
  }, [api])
  useEffect(() => {
    if (!enabled) return undefined
    const ctl = new AbortController()
    load(ctl.signal)
    return () => ctl.abort()
  }, [load, enabled])
  const mutate = useCallback(async (fn) => {
    setBusy(true)
    let result = {}
    try {
      result = { value: await fn(state.data) }
    } catch (error) {
      result = { error }
    }
    await load()
    if (alive.current) setBusy(false)
    return result
  }, [load, state.data])
  return { ...state, busy, reload: () => load(), mutate }
}
