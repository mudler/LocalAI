import { nodeRoute } from './swarm.js'
import { pathOf, stateOf } from '../components/swarm/splitWorkersModel.js'

// The roster of the Workers page: one list of every machine of the cluster,
// whatever way it connects. A backend or agent worker is a node. A model-split
// worker (it lends its memory to one big model) is a roster row too, marked as
// such. No React here, so the unit tests read it.

// The order the connections of a worker are drawn in.
const ORDER = ['nats', 'websocket', 'p2p', 'heartbeat']
const STATE_TONES = { live: 'ok', reconnecting: 'warn', down: 'error' }
// The key of the words of a P2P path (cluster:conn.path.<key>), from the path
// a worker reports. A relayed path does not name its relay here.
const PATH_KEYS = { direct: 'direct', hole_punched: 'holePunched', relayed: 'relayedUnknown' }

// The connections of one node, from the route state the controller judged
// (route_state of GET /api/nodes). Each one is
//   { key, kind, state, tone, reason, path, limited }
// where kind is nats, websocket, p2p or heartbeat (a heartbeat shows only when
// it is lost, because a live heartbeat is what every worker has), state is
// live, reconnecting or down, and path and limited describe a P2P tunnel. An
// older controller sends no route state: the node's own report decides, and a
// node that reports nothing has no connection to show (the page says so).
export function nodeConnections(node) {
  const list = []
  if (Array.isArray(node?.route_state)) {
    for (const route of node.route_state) {
      if (!route) continue
      const state = STATE_TONES[route.state] ? route.state : 'down'
      if (route.route === 'heartbeat') {
        if (state !== 'live') list.push({ key: 'heartbeat', kind: 'heartbeat', state, tone: STATE_TONES[state], reason: route.reason || '' })
        continue
      }
      const kind = route.route === 'nats' ? 'nats' : route.transport === 'p2p' ? 'p2p' : 'websocket'
      list.push({
        key: kind, kind, state, tone: STATE_TONES[state], reason: route.reason || '',
        ...(kind === 'p2p' ? { path: node.tunnel_path || null, pathKey: PATH_KEYS[node.tunnel_path] || null, limited: Boolean(node.tunnel_limited) } : {}),
      })
    }
  }
  if (!list.some(c => c.kind !== 'heartbeat')) {
    const reported = nodeRoute(node)
    if (reported) list.push({ key: reported.kind, kind: reported.kind, state: 'live', tone: 'ok', reason: '', path: reported.path, pathKey: PATH_KEYS[reported.path] || null, limited: reported.limited })
  }
  return list.sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind))
}

// The one P2P connection of a model-split worker: how the server reaches it
// now, with the round trip. A worker that is offline has no path.
export function splitConnections(worker) {
  const path = pathOf(worker)
  const state = stateOf(worker)
  const down = path.key === 'none'
  return [{
    key: 'p2p', kind: 'p2p',
    state: down ? 'down' : 'live',
    tone: down ? 'idle' : state.tone === 'ok' ? 'ok' : state.tone,
    reason: '',
    path: worker?.path?.kind || null,
    pathKey: path.key,
    relay: path.relay || '',
    rtt: path.rtt,
    limited: Boolean(worker?.path?.limited),
  }]
}

// A model-split worker as a roster row. It carries the fields the filter, the
// sort and the grouping of nodes read, so they work on it unchanged.
export function splitAsRow(worker, nodeNames = new Map()) {
  const state = stateOf(worker)
  return {
    id: `split:${worker.id}`,
    name: worker.label || worker.name || worker.peer_id || worker.id,
    node_type: 'model-split',
    status: null,
    address: worker.peer_id || '',
    labels: {},
    total_vram: 0,
    total_ram: worker.memory_bytes || 0,
    model_count: 0,
    attached_to: worker.node_id ? nodeNames.get(worker.node_id) || worker.node_name || worker.node_id : '',
    _split: worker,
    _splitState: state,
  }
}

export const isSplit = row => Boolean(row?._split)

// The health chip a row belongs to: healthy, attention, offline, pending or
// other. A node needs attention when the page flagged it (reasons, from
// attentionOf); a split worker when it is slow, limited or refused.
export function rowHealth(row, reasons) {
  if (isSplit(row)) {
    const key = row._splitState.key
    if (key === 'online') return ['healthy']
    if (key === 'offline') return ['offline']
    return ['attention']
  }
  const out = []
  const status = String(row?.status || '').toLowerCase()
  if (status === 'healthy') out.push('healthy')
  if (status === 'offline' || status === 'unhealthy') out.push('offline')
  if (status === 'pending') out.push('pending')
  if (reasons?.has(row.id)) out.push('attention')
  return out
}

export function matchesChip(row, chip, reasons) {
  return chip === 'all' || rowHealth(row, reasons).includes(chip)
}

export function chipCounts(rows, reasons) {
  const counts = { all: rows.length, healthy: 0, attention: 0, offline: 0, pending: 0 }
  for (const row of rows) for (const key of rowHealth(row, reasons)) if (key in counts) counts[key] += 1
  return counts
}

// True when the query (a search of the roster) names the row: its name, its
// address, its labels, or a model it runs. `models` is the model names of the
// row, or null while they are not read yet.
export function matchesQuery(row, query, models) {
  const q = String(query || '').trim().toLowerCase()
  if (!q) return true
  const hay = [row.name, row.address, row.node_type, row.version, row.attached_to,
    ...Object.entries(row.labels || {}).flatMap(([k, v]) => [k, v, `${k}=${v}`]), ...(models || [])]
  return hay.some(value => String(value ?? '').toLowerCase().includes(q))
}

// The figures of the summary strip: how many workers are online of all,
// models loaded and on how many workers, requests in flight, and the workers
// that need attention. `models` is a Map of node id to model names (null while
// not read); a model-split worker has none of its own.
export function rosterSummary(rows, reasons, models) {
  const nodes = rows.filter(row => !isSplit(row))
  const online = rows.filter(row => rowHealth(row, reasons).includes('healthy')).length
  const attention = rows.filter(row => rowHealth(row, reasons).includes('attention'))
  const offline = rows.filter(row => rowHealth(row, reasons).includes('offline')).length
  let loaded = 0
  let hosts = 0
  if (models) {
    const names = new Set()
    for (const node of nodes) {
      const list = models.get(node.id) || []
      list.forEach(name => names.add(name))
      if (list.length) hosts += 1
    }
    loaded = names.size
  } else {
    for (const node of nodes) {
      loaded += node.model_count || 0
      if ((node.model_count || 0) > 0) hosts += 1
    }
  }
  return {
    total: rows.length,
    online,
    loaded,
    hosts,
    inFlight: nodes.reduce((sum, node) => sum + (node.in_flight_count || 0), 0),
    attention: attention.length,
    offline,
    degraded: attention.length - attention.filter(row => rowHealth(row, reasons).includes('offline')).length,
  }
}
