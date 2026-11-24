import { limitParts } from '../p2psettings/p2pSettingsModel.js'

// The model-split workers of the Nodes page and the "Model-split worker" way
// of Add a node: llama.cpp RPC and MLX workers that lend their memory to
// models that run on this server or, in a cluster, on the backend node each
// one is attached to. No React here, so the unit tests read it.

export const ENGINES = ['llama', 'mlx']

// The tone and i18n key (swarm:split.state.<key>) of a worker's state.
const STATES = {
  online: 'ok',
  offline: 'idle',
  slow: 'warn',
  limited: 'error',
  refused: 'error',
}

export function stateOf(worker) {
  const key = STATES[worker?.state] ? worker.state : 'offline'
  return { key, tone: STATES[key] }
}

// What the administrator can do about a worker now:
//   useAnyway  a relay is the only path and it can carry model layers
//   stopUsing  it is used over a relay because "Use anyway" was chosen
//   null       nothing to choose: a direct path, offline, limited or refused
export function slowAction(worker) {
  if (!worker) return null
  if (worker.state === 'slow') return 'useAnyway'
  if (worker.state === 'online' && worker.allow_slow_path && worker.path?.kind === 'relayed') return 'stopUsing'
  return null
}

// The words of a worker's path, as a key under swarm:split.path and its values.
export function pathOf(worker) {
  const path = worker?.path || {}
  const rtt = path.rtt_ms > 0 ? path.rtt_ms : null
  switch (path.kind) {
    case 'direct': return { key: 'direct', rtt }
    case 'hole_punched': return { key: 'holePunched', rtt }
    case 'relayed': return { key: path.relay_label ? 'relayed' : 'relayedUnknown', relay: path.relay_label || '', rtt }
    default: return { key: 'none', rtt: null }
  }
}

// The limits after which a relay of the user ends each connection of a
// worker that is slow or used anyway: an empty list when the path has none,
// and for a public relay, which is never used.
export function cutLimits(worker) {
  const path = worker?.path || {}
  if (!worker?.own_relay || path.kind !== 'relayed' || !path.limited) return []
  return limitParts({ circuit_bytes: path.limit_bytes, circuit_seconds: path.limit_seconds })
}

// The relay whose limits the limits dialog edits, for a worker behind a relay
// of the user; null otherwise. The dialog reads the current limits itself.
export function limitsRelay(worker) {
  if (!worker?.own_relay || !worker.relay_id) return null
  const path = worker.path || {}
  return {
    id: worker.relay_id, label: path.relay_label || '', mode: 'gated',
    limits: { circuit_bytes: path.limit_bytes || 0, circuit_seconds: path.limit_seconds || 0 },
  }
}

// The counts of the group heading: workers online of all workers.
export function summarize(workers) {
  const list = Array.isArray(workers) ? workers : []
  return {
    total: list.length,
    online: list.filter(w => w.state === 'online').length,
    slow: list.filter(w => w.state === 'slow'),
    limited: list.filter(w => w.state === 'limited'),
    cut: list.filter(w => (w.state === 'slow' || w.state === 'online') && cutLimits(w).length > 0),
  }
}

// The callouts of the group, the most important first. The group shows one at
// a time: two stacked warnings push the table off a phone screen, and the
// first one is the one to fix first. The state of each worker stays in the
// table.
export const CALLOUT_ORDER = ['noServer', 'refused', 'limited', 'cut', 'offline', 'unattached', 'slow']

// The key of the callout to show: the first of CALLOUT_ORDER that is true in
// `active` (an object of key to boolean), or null.
export function topCallout(active) {
  return CALLOUT_ORDER.find(key => active?.[key]) || null
}

// The workers that paired after the page opened (created_at after since, in
// milliseconds), newest first: the Add a node flow waits for one of them.
export function arrivedSince(workers, since) {
  const list = Array.isArray(workers) ? workers : []
  return list
    .filter(w => Date.parse(w.created_at) >= since)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
}

// The command that starts a split worker with its invite link. The state
// directory keeps the identity and the pairing, so the same command works
// again after a restart; with Docker it is a named volume. MLX runs on Apple
// Silicon, where a container has no GPU, so MLX is always the CLI.
export function splitCommand({ engine, link, flavor, option, dev }) {
  const sub = engine === 'mlx' ? 'mlx-distributed' : 'llama-cpp-rpc'
  const invite = link || '<invite link>'
  if (flavor === 'cli' || engine === 'mlx') return `local-ai worker ${sub} \\\n  --join "${invite}"`
  const opt = option || {}
  const flags = opt.dockerFlags ? `docker run --net host ${opt.dockerFlags} \\\n  ` : 'docker run --net host \\\n  '
  const tag = dev ? opt.devTag : opt.tag
  return `${flags}-v localai-${sub}:/p2p \\\n  -e LOCALAI_P2P_STATE_DIR=/p2p \\\n  --name local-ai-${sub} \\\n  localai/localai:${tag} worker ${sub} \\\n  --join "${invite}"`
}

function nodeList(nodes) {
  return Array.isArray(nodes) ? nodes : Array.isArray(nodes?.nodes) ? nodes.nodes : []
}

// A node takes work while it is healthy or draining.
function online(node) {
  return node.status === 'healthy' || node.status === 'draining'
}

// The backend nodes of the cluster (an agent node runs no models), by name,
// with their split server declaration: the nodes the admin can make a split
// server. Accepts the list of GET /api/nodes, or an object that holds it.
export function backendNodes(nodes) {
  return nodeList(nodes)
    .filter(n => n && n.node_type !== 'agent')
    .map(n => ({ id: n.id, name: n.name || n.id, splitServer: Boolean(n.split_server), online: online(n) }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

// The nodes a worker can be attached to: the split servers, by name. A node
// becomes one only by an explicit choice (its start flag, or the admin).
export function nodeChoices(nodes) {
  return backendNodes(nodes)
    .filter(n => n.splitServer)
    .map(({ id, name, online }) => ({ id, name, online }))
}

// The node a new worker attaches to by default: the only online split
// server. With none or several, '' and the admin picks.
export function defaultSplitServer(nodes) {
  const found = nodeChoices(nodes).filter(n => n.online)
  return found.length === 1 ? found[0].id : ''
}

// The tone and i18n key (swarm:split.relay.<key>) of the relay admission of
// the node a worker is attached to; null for a worker attached to no node.
const RELAY = {
  admitted: ['admitted', 'ok'],
  refused: ['refused', 'error'],
  pending: ['pending', 'warn'],
  not_needed: ['notNeeded', 'idle'],
}

export function relayOf(worker) {
  const r = worker?.node_id ? worker.node_relay : null
  if (!r || !RELAY[r.state]) return null
  const [key, tone] = RELAY[r.state]
  return { key, tone, reason: r.reason || '' }
}

// The workers that no model uses because they are attached to this instance
// while the models run on the nodes of the cluster.
export function unattached(workers, distributed) {
  if (!distributed) return []
  return (Array.isArray(workers) ? workers : []).filter(w => !w.node_id)
}
