import { useState, useEffect, useCallback } from 'react'
import { p2pApi } from '../utils/api'

// useP2PMode reports whether p2p / swarm mode is available, mirroring
// useDistributedMode. Availability is "a network token exists" (the same signal
// the standalone P2P page used). One-shot probe on mount plus a manual refetch.
//
// Returns:
//   enabled  — true when a non-empty network token is present
//   loading  — true until the first probe completes
//   refetch  — manual trigger to re-run the probe
export function useP2PMode() {
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)

  const probe = useCallback(async () => {
    setLoading(true)
    try {
      const token = await p2pApi.getToken()
      setEnabled(!!(token && String(token).trim()))
    } catch {
      setEnabled(false)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { probe() }, [probe])

  return { enabled, loading, refetch: probe }
}
