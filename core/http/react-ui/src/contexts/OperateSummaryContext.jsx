import { createContext, useContext, useState, useCallback, useMemo } from 'react'
import { adminUsersApi, backendsApi, modelsApi, nodesApi, resourcesApi, systemApi, tracesApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { useUpdates } from './UpdatesContext'
import { useDistributedMode } from '../hooks/useDistributedMode'
import { galleryUpdateMap, needsPersonRows } from '../utils/updates'
import { appendSample, capacityOf } from '../utils/operateStatus'

// The state of the installation, assembled once for everything in the Operate
// console: the rail's per-item signals and the overview's attention list.
//
// One poller, following OperationsContext — which exists because every
// consumer running its own setInterval against the same endpoint was the
// defect it was created to fix. Four endpoints doing that would be worse.
//
// The provider sits above the sidebar, which draws the Administration badges,
// so it is always mounted. `enabled` is true only while an administrator is on
// an Administration page: otherwise it asks nothing and no timer runs.
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

export function OperateSummaryProvider({ children, pollInterval = POLL_INTERVAL_MS, enabled = true }) {
  const [nodes, setNodes] = useState([])
  const [resources, setResources] = useState(null)
  const [resourcesLoaded, setResourcesLoaded] = useState(false)
  const [traces, setTraces] = useState(null)
  const [installed, setInstalled] = useState({ backends: null, models: null })
  const [running, setRunning] = useState(null)
  // The sign-ups that wait for an administrator, when sign-in is on.
  const [pendingUsers, setPendingUsers] = useState([])
  // How many people there are and how many administer, for the Overview.
  const [userCounts, setUserCounts] = useState(null)
  const { authEnabled, isAdmin } = useAuth()
  // The models this process holds in memory, for the Status page's capacity
  // row. Null until read, and on a cluster controller, whose models live on
  // the workers.
  const [loadedModels, setLoadedModels] = useState(null)
  // Readings of the memory pool taken while Operate is open, newest last. The
  // API keeps no history, so this is all the chart can honestly draw.
  const [samples, setSamples] = useState([])
  const [ready, setReady] = useState(false)
  const { operations } = useOperations()
  // The updates come from the shell-level provider (UpdatesContext), which
  // reads the cached read model once a minute for the whole app. This provider
  // used to poll /api/backends/upgrades itself every 15 s for the same fact.
  const updates = useUpdates()
  const updateRows = updates && updates.rows
  // The gallery backends that have a newer build, by name, with the nodes
  // that differ.
  const upgrades = useMemo(() => galleryUpdateMap(updateRows), [updateRows])
  // The cluster API answers 503 when distributed mode is off, so asking for it
  // on a single-node install is a guaranteed miss on every tick. The rail gates
  // the Nodes entry the same way.
  const { enabled: distributed, loading: distributedLoading } = useDistributedMode({ enabled })

  const fetchSummary = useCallback(async () => {
    const [n, r, tr, bi, mi, sys, us] = await Promise.all([
      distributed ? settle(nodesApi.list(), []) : Promise.resolve([]),
      settle(resourcesApi.get(), null),
      settle(tracesApi.summary(), null),
      settle(backendsApi.listInstalled?.() ?? Promise.resolve(null), null),
      settle(modelsApi.listCapabilities(), null),
      // Only this process's loader: on a distributed controller the models
      // live on workers, and the Nodes entry carries the cluster signal.
      distributed ? Promise.resolve(null) : settle(systemApi.info(), null),
      authEnabled && isAdmin ? settle(adminUsersApi.list(), null) : Promise.resolve(null),
    ])
    const userList = Array.isArray(us) ? us : (us?.users || [])
    setPendingUsers(userList.filter(u => u.status === 'pending'))
    setUserCounts(Array.isArray(us) || us?.users ? { total: userList.length, admins: userList.filter(u => u.role === 'admin').length } : null)
    setNodes(Array.isArray(n) ? n : (n?.nodes || []))
    setResources(r)
    setResourcesLoaded(true)
    setTraces(tr)
    setInstalled({
      backends: Array.isArray(bi) ? bi.length : (bi?.backends?.length ?? null),
      models: mi?.data?.length ?? null,
    })
    setRunning(Array.isArray(sys?.loaded_models) ? sys.loaded_models.length : null)
    setLoadedModels(Array.isArray(sys?.loaded_models) ? sys.loaded_models : null)
    const nodeList = Array.isArray(n) ? n : (n?.nodes || [])
    setSamples(current => appendSample(current, capacityOf({ resources: r, nodes: nodeList, distributed }), Date.now()))
    setReady(true)
  }, [distributed, authEnabled, isAdmin])

  // Wait for the cluster probe: several sources depend on the mode, and a
  // first tick taken before it answers would ask a cluster for local state.
  const { refetch } = usePolling(fetchSummary, pollInterval, { enabled: enabled && !distributedLoading })

  const value = useMemo(() => {
    // Backends and add-ons, the rows the Updates tab counts: this item's number
    // and the names under it are the same set the Updates page lists.
    const upgradeList = needsPersonRows(updateRows)
    const failedOps = operations.filter(op => op.error)
    const unhealthyNodes = nodes.filter(n => nodeUnhealthy(n))

    // Only things a person would act on. An operation in flight is not a
    // problem, so a running install belongs in the rail's activity count and
    // not in here.
    const attention = [
      ...(upgradeList.length > 0 ? [{
        id: 'updates',
        kind: 'backend-update',
        count: upgradeList.length,
        names: upgradeList.map(u => u.name),
        // Only what is ready can be picked in the update-ready list; the rest
        // is under Needs you.
        ready: upgradeList.some(u => u.state === 'ready'),
      }] : []),
      ...failedOps.map(op => ({
        id: `op:${op.id || op.uid || op.name}`,
        kind: 'operation-failed',
        name: op.name || op.id,
        detail: op.error,
      })),
      ...(pendingUsers.length > 0 ? [{
        id: 'signups',
        kind: 'signups-waiting',
        count: pendingUsers.length,
        users: pendingUsers,
      }] : []),
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
      resourcesLoading: !resourcesLoaded,
      resourcesUnavailable: resourcesLoaded && !resources,
      operations,
      traces,
      installed,
      loadedModels,
      samples,
      ready,
      refresh: refetch,
      // null until the cluster probe answers, so a consumer can wait rather
      // than render the single-node view on a cluster for one frame.
      distributed: distributedLoading ? null : distributed,
      attention,
      pendingUsers,
      userCounts,
      signals: {
        attention: attention.length || null,
        updates: updates && updates.badge && updates.badge.count ? updates.badge.count : null,
        updatesLevel: updates && updates.badge && updates.badge.failed ? 'error' : 'update',
        activity: operations.length || null,
        nodes: nodes.length ? `${nodes.length - unhealthyNodes.length}/${nodes.length}` : null,
        host: memoryPercent(resources),
        traces: traces?.errors || null,
        usage: traces?.total ? compact(traces.total) : null,
        running: running || null,
        pendingUsers: pendingUsers.length || null,
      },
    }
  }, [upgrades, updateRows, updates, nodes, resources, resourcesLoaded, operations, traces, installed, running, loadedModels, samples, ready, refetch, distributed, distributedLoading, pendingUsers, userCounts])

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
