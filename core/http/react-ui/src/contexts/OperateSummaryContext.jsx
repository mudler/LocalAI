import { createContext, useContext, useState, useCallback, useMemo } from 'react'
import { backendsApi, modelsApi, nodesApi, resourcesApi, tracesApi } from '../utils/api'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { useDistributedMode } from '../hooks/useDistributedMode'

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
  const [traces, setTraces] = useState(null)
  const [installed, setInstalled] = useState({ backends: null, models: null })
  const { operations } = useOperations()
  // The cluster API answers 503 when distributed mode is off, so asking for it
  // on a single-node install is a guaranteed miss on every tick. The rail gates
  // the Nodes entry the same way.
  const { enabled: distributed } = useDistributedMode()

  const fetchSummary = useCallback(async () => {
    const [u, n, r, tr, bi, mi] = await Promise.all([
      // GET /api/backends/upgrades returns the upgrade checker's cached view.
      // Never POST /upgrades/check on a timer — that forces a real registry
      // check.
      settle(backendsApi.checkUpgrades(), {}),
      distributed ? settle(nodesApi.list(), []) : Promise.resolve([]),
      settle(resourcesApi.get(), null),
      settle(tracesApi.summary(), null),
      settle(backendsApi.listInstalled?.() ?? Promise.resolve(null), null),
      settle(modelsApi.listCapabilities(), null),
    ])
    setUpgrades(u && typeof u === 'object' ? u : {})
    setNodes(Array.isArray(n) ? n : (n?.nodes || []))
    setResources(r)
    setTraces(tr)
    setInstalled({
      backends: Array.isArray(bi) ? bi.length : (bi?.backends?.length ?? null),
      models: mi?.data?.length ?? null,
    })
  }, [distributed])

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
      traces,
      installed,
      attention,
      signals: {
        attention: attention.length || null,
        backends: upgradeList.length || null,
        activity: operations.length || null,
        nodes: nodes.length ? `${nodes.length - unhealthyNodes.length}/${nodes.length}` : null,
        host: memoryPercent(resources),
        traces: traces?.errors || null,
        usage: traces?.total ? compact(traces.total) : null,
      },
    }
  }, [upgrades, nodes, resources, operations, traces, installed])

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

// 18402 -> 18.4k. A rail signal has room for a shape, not a full figure.
function compact(n) {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
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
