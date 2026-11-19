import { useCallback, useState } from 'react'
import { p2pSplitApi } from '../utils/api'
import { usePolling } from './usePolling'

const REFRESH_MS = 5000

// Reads the model-split workers. A server that does not share over P2P
// answers 409: the group is then not shown at all.
export function useSplitWorkers(enabled = true) {
  const [state, setState] = useState({ status: 'loading', workers: [], memory: 0, distributed: false, error: null })
  const read = useCallback(async () => {
    try {
      const data = await p2pSplitApi.list()
      setState({ status: 'ready', workers: data?.workers || [], memory: data?.memory_in_use || 0, distributed: Boolean(data?.distributed), error: null })
    } catch (error) {
      setState(current => ({ ...current, status: error?.status === 409 ? 'off' : 'error', error }))
    }
  }, [])
  const { refetch } = usePolling(read, REFRESH_MS, { enabled })
  return { ...state, refetch }
}
