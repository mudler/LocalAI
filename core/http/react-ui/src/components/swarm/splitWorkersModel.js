import { limitParts } from '../p2psettings/p2pSettingsModel.js'

// The model-split workers of the Nodes page and the "Model-split worker" way
// of Add a node: llama.cpp RPC and MLX workers that lend their memory to
// models that run on this server. No React here, so the unit tests read it.

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
