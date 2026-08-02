import { createContext, useContext, useState, useCallback, useMemo } from 'react'
import { backendsApi, nodesApi, resourcesApi } from '../utils/api'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'

// The state of the installation, assembled once for everything in the Operate
// console: the rail's per-item signals and the overview's attention list.
//
// One poller, following OperationsContext — which exists because every
// consumer running its own setInterval against the same endpoint was the
// defect it was created to fix. Four endpoints doing that would be worse.
//
// The provider is mounted by ConsoleLayout for the Operate console only, so
// "poll only while the user is in Operate" needs no route check: away from
// Operate the provider is not mounted and nothing runs.
//
// Operations are NOT fetched here. OperationsContext already polls them for
// the sidebar badge and the operations strip, so this reads that instead of
// adding a second poll of /api/operations.
const OperateSummaryContext = createContext(null)

// Slower than the operations poll (1s) on purpose. Nothing here changes on a
// human timescale — a backend does not go stale mid-glance — and the values
// are ambient rather than awaited.
const POLL_INTERVAL_MS = 15_000

// A failed summary is not an event. Nobody asked for this data; it decorates a
// rail and fills a panel. Each source degrades to "no signal" on its own so one
// dead endpoint cannot blank the other three.
async function settle(promise, fallback) {
  try {
    return await promise
  } catch {
    return fallback
  }
}

export function OperateSummaryProvider({ children, pollInterval = POLL_INTERVAL_MS }) {
  const [upgrades, setUpgrades] = useState({})
  const [nodes, setNodes] = useState([])
  const [resources, setResources] = useState(null)
  const { operations } = useOperations()

  const fetchSummary = useCallback(async () => {
    const [u, n, r] = await Promise.all([
      // GET /api/backends/upgrades returns the upgrade checker's cached view.
      // Never POST /upgrades/check on a timer — that forces a real registry
      // check.
      settle(backendsApi.checkUpgrades(), {}),
      settle(nodesApi.list(), []),
      settle(resourcesApi.get(), null),
    ])
    setUpgrades(u && typeof u === 'object' ? u : {})
    setNodes(Array.isArray(n) ? n : (n?.nodes || []))
    setResources(r)
  }, [])

  usePolling(fetchSummary, pollInterval)

  const value = useMemo(() => {
    const upgradeList = Object.values(upgrades || {})
    const failedOps = operations.filter(op => op.error)
    const unhealthyNodes = nodes.filter(n => nodeUnhealthy(n))

    // Only things a person would act on. An operation in flight is not a
    // problem, so a running install belongs in the rail's activity count and
    // not in here.
    const attention = [
      ...upgradeList.map(u => ({
        id: `upgrade:${u.backend_name}`,
        kind: 'backend-update',
        name: u.backend_name,
        from: u.installed_version,
        to: u.available_version,
      })),
      ...failedOps.map(op => ({
        id: `op:${op.id || op.uid || op.name}`,
        kind: 'operation-failed',
        name: op.name || op.id,
        detail: op.error,
      })),
      ...unhealthyNodes.map(n => ({
        id: `node:${n.id || n.name}`,
        kind: 'node-unhealthy',
        name: n.id || n.name,
        detail: n.status,
      })),
    ]

    return {
      upgrades,
      nodes,
      resources,
      operations,
      attention,
      signals: {
        attention: attention.length || null,
        backends: upgradeList.length || null,
        activity: operations.length || null,
        nodes: nodes.length ? `${nodes.length - unhealthyNodes.length}/${nodes.length}` : null,
        host: memoryPercent(resources),
      },
    }
  }, [upgrades, nodes, resources, operations])

  return (
    <OperateSummaryContext.Provider value={value}>
      {children}
    </OperateSummaryContext.Provider>
  )
}

// Node payloads have carried `healthy`, `status` and neither at different
// points. Treat an explicit negative as unhealthy and anything unrecognised as
// fine, so a shape change makes the panel quiet rather than crying wolf.
function nodeUnhealthy(node) {
  if (node?.healthy === false) return true
  if (typeof node?.status === 'string') {
    return ['unhealthy', 'down', 'offline', 'error'].includes(node.status.toLowerCase())
  }
  return false
}

function memoryPercent(resources) {
  const ram = resources?.ram || resources?.aggregate?.ram
  const total = ram?.total ?? ram?.total_bytes
  const used = ram?.used ?? ram?.used_bytes
  if (!(total > 0) || !(used >= 0)) return null
  return `${Math.round((used / total) * 100)}%`
}

// Returns null outside the Operate console, where the provider is not mounted.
// Callers render nothing rather than guessing at a value.
export function useOperateSummary() {
  return useContext(OperateSummaryContext)
}
