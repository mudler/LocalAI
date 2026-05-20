import { useState, useEffect, useCallback } from 'react'
import { nodesApi } from '../utils/api'

// useDistributedMode probes /api/nodes to decide whether the running LocalAI
// is in distributed mode. The endpoint returns 503 when distributed mode is
// disabled — we treat any failure as standalone, mirroring the detection
// pattern in pages/Nodes.jsx so UI behaviour matches the Nodes page.
//
// Returns:
//   enabled  — true when the cluster API answered OK at least once
//   nodes    — the most recent /api/nodes response (array; possibly empty)
//   loading  — true until the first probe completes
//   refetch  — manual trigger; the picker calls this after install/delete
//
// Components that need a live nodes list (e.g. install picker) re-call
// refetch after operations complete. The hook does not poll on its own —
// the Nodes page handles its own 5s polling and the Backends gallery only
// needs a one-shot read on mount.
export function useDistributedMode() {
  const [enabled, setEnabled] = useState(false)
  const [nodes, setNodes] = useState([])
  const [loading, setLoading] = useState(true)

  const probe = useCallback(async () => {
    try {
      const data = await nodesApi.list()
      setNodes(Array.isArray(data) ? data : [])
      setEnabled(true)
    } catch {
      setEnabled(false)
      setNodes([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { probe() }, [probe])

  return { enabled, nodes, loading, refetch: probe }
}
