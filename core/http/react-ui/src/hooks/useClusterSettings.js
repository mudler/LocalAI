import { useCallback, useState } from 'react'
import { clusterApi } from '../utils/api'
import { usePolling } from './usePolling'

// The routes answer 404 on a build without them, 503 on a frontend that is not
// distributed and 401 or 403 to a person who is not an admin. In each case
// there is no cluster to set.
export function isClusterUnavailable(error) {
  return [401, 403, 404, 503].includes(error?.status)
}

// The settings of the cluster, read from GET /api/cluster/settings and kept
// fresh: the broadcast bus and the frontend replicas change on their own.
//
//   status  'loading' | 'ready' | 'unavailable' | 'error'
//
// A failed read after a good one keeps the last data and reports `error`, so a
// blip does not blank the form. `reload` reads again now.
export function useClusterSettings({ interval = 5000, enabled = true } = {}) {
  const [state, setState] = useState({ status: 'loading', data: null, error: '' })
  const read = useCallback(async () => {
    try {
      const data = await clusterApi.settings()
      setState({ status: 'ready', data, error: '' })
    } catch (error) {
      setState(current => (isClusterUnavailable(error)
        ? { status: 'unavailable', data: null, error: '' }
        : { status: 'error', data: current.data, error: error.message || '' }))
    }
  }, [])
  const { refetch } = usePolling(read, interval, { enabled })
  return { ...state, reload: refetch }
}
