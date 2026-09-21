import { useCallback, useMemo, useState } from 'react'
import { resourcesApi, systemApi } from '../utils/api'
import { localModelRows } from '../utils/localHost'
import { usePolling } from './usePolling'

// Models loaded by this LocalAI process, plus (optionally) the host readings
// the capacity gauges draw from.
//
// Five seconds, like the Nodes page's own poll: this is a surface people act
// on, and a model they just stopped should leave the list while they are still
// looking at it. The per-process CPU share is a delta between two server-side
// readings, so the poll interval is also the window that number covers.
//
// `state` is 'loading' until the first answer, then 'loaded' or 'error'. A
// failed refresh after a good one keeps the last rows on screen: a transient
// error should not blank a table someone is reading.
export function useLocalMachine({ withResources = true, intervalMs = 5000, enabled = true } = {}) {
  const [system, setSystem] = useState(null)
  const [resources, setResources] = useState(null)
  const [state, setState] = useState('loading')
  const [error, setError] = useState('')

  const fetchAll = useCallback(async () => {
    const [sys, res] = await Promise.allSettled([
      systemApi.info(),
      withResources ? resourcesApi.get() : Promise.resolve(null),
    ])
    if (sys.status === 'fulfilled') {
      setSystem(sys.value)
      setState('loaded')
      setError('')
    } else {
      setError(sys.reason?.message || 'Unable to read loaded models')
      setState(current => (current === 'loaded' ? current : 'error'))
    }
    if (res.status === 'fulfilled') setResources(res.value)
  }, [withResources])

  const { refetch } = usePolling(fetchAll, intervalMs, { enabled })
  const rows = useMemo(() => localModelRows(system), [system])

  return { rows, resources, state, error, refresh: refetch }
}
