// What the Swarm pages work out from the data the cluster API already returns:
// how a node's state reads, which nodes a rule may use, what a drain or a lost
// node would leave without service, and the commands that add a worker.
//
// Everything here is a reading of the roster, the loaded replicas and the
// scheduling rules the page already fetched. It runs in the browser. The
// scheduler decides at load time using free memory and disk, which none of
// these functions can see, so every answer that depends on that says so
// instead of guessing.

import { capacityReading, summarizeFleet } from './nodeFleet.js'

// ---- State ------------------------------------------------------------------

// The state words the pages use, one per /api/nodes status. `level` picks the
// colour of the mark; the word always carries the meaning.
export function nodeState(node) {
  switch (String(node?.status || '').toLowerCase()) {
    case 'healthy': return { key: 'healthy', level: 'ok' }
    case 'draining': return { key: 'draining', level: 'warn' }
    case 'pending': return { key: 'pending', level: 'warn' }
    case 'unhealthy': return { key: 'down', level: 'error' }
    case 'offline': return { key: 'offline', level: 'error' }
    case 'registering': return { key: 'registering', level: 'idle' }
    default: return { key: 'unknown', level: 'idle' }
  }
}

export function isDown(node) {
  const status = String(node?.status || '').toLowerCase()
  return status === 'unhealthy' || status === 'offline'
}

// Nodes that can take work: healthy backend workers. Agent workers run agent
// jobs and never host models.
export function isPlaceable(node) {
  const type = node?.node_type
  return String(node?.status || '').toLowerCase() === 'healthy' && (!type || type === 'backend')
}

// Ids of nodes that need a look, with the reasons. A node with no reading for a
// resource is never flagged for it: unknown is not low.
export function attentionOf(nodes) {
  const summary = summarizeFleet(nodes)
  const reasons = new Map()
  const add = (ids, reason) => ids.forEach(id => reasons.set(id, [...(reasons.get(id) || []), reason]))
  add(summary.attention.pending, 'pending')
  add(summary.attention.offlineOrUnhealthy, 'down')
  add(summary.attention.lowVRAM, 'lowVram')
  add(summary.attention.lowRAM, 'lowRam')
  add(summary.attention.lowDisk, 'lowDisk')
  return reasons
}

// The memory a node offers for models: GPU memory when it reports any, system
// memory otherwise. Null when the node reports neither.
export function memoryOf(node) {
  const vram = capacityReading(node?.total_vram, node?.available_vram)
  if (vram) return { kind: 'gpu', ...vram }
  const ram = capacityReading(node?.total_ram, node?.available_ram)
  if (ram) return { kind: 'ram', ...ram }
  return null
}

// ---- Rules ------------------------------------------------------------------

export function selectorOf(rule) {
  const raw = rule?.node_selector
  if (!raw) return {}
  if (typeof raw === 'object') return raw
  try { return JSON.parse(raw) || {} } catch { return {} }
}

// The scheduler's own test: a node matches when every key=value of the
// selector is one of its labels. An empty selector matches every node.
export function matchesSelector(node, selector) {
  const labels = node?.labels || {}
  return Object.entries(selector || {}).every(([key, value]) => labels[key] === value)
}

export function eligibleNodes(nodes, selector) {
  return (nodes || []).filter(node => isPlaceable(node) && matchesSelector(node, selector))
}

// 'spread' | 'autoscale' | 'placement' | 'inactive', as the page lists them.
export function ruleKind(rule) {
  if (!rule) return 'placement'
  if (rule.spread_all) return 'spread'
  if (rule.min_replicas > 0 || rule.max_replicas > 0) return 'autoscale'
  if (Object.keys(selectorOf(rule)).length > 0) return 'placement'
  return 'inactive'
}

// The model a rule governs: the rule's own name, or what an alias points at.
export function ruleTarget(rule) {
  return rule?.target_model || rule?.model_name || ''
}

// What a rule would ask for, given the nodes that exist now. It reads the
// roster and labels and nothing else.
//
//   eligible   healthy backend nodes whose labels match
//   planned    [{ node, replicas }] where the rule would put replicas
//   wanted     the replicas it asks for (null for a single loaded-on-demand one)
//   shortfall  replicas it asks for that no eligible node has room for under
//              its replica cap
export function rulePlan(rule, nodes) {
  const kind = ruleKind(rule)
  const selector = selectorOf(rule)
  const eligible = eligibleNodes(nodes, selector)
  const cap = node => Math.max(1, Number(node.max_replicas_per_model) || 1)
  const byRoom = [...eligible].sort((a, b) => (Number(b.available_vram) || 0) - (Number(a.available_vram) || 0))
  let planned = []
  let wanted = null
  let shortfall = 0

  if (kind === 'spread') {
    planned = eligible.map(node => ({ node, replicas: 1 }))
    wanted = eligible.length
  } else if (kind === 'autoscale') {
    wanted = Math.max(1, Number(rule.min_replicas) || 1)
    const room = new Map(byRoom.map(node => [node.id, cap(node)]))
    const placed = new Map()
    let left = wanted
    while (left > 0 && [...room.values()].some(v => v > 0)) {
      for (const node of byRoom) {
        if (left === 0) break
        if (room.get(node.id) > 0) {
          room.set(node.id, room.get(node.id) - 1)
          placed.set(node.id, (placed.get(node.id) || 0) + 1)
          left -= 1
        }
      }
    }
    planned = byRoom.filter(node => placed.has(node.id)).map(node => ({ node, replicas: placed.get(node.id) }))
    shortfall = left
  }
  return { kind, selector, eligible, planned, wanted, shortfall }
}

// The nodes where a model is loaded right now.
export function loadedOn(rows, nodes, modelName) {
  const byId = new Map((nodes || []).map(node => [node.id, node]))
  const seen = new Set()
  for (const row of rows || []) {
    if (row.model_name === modelName && (!row.state || row.state === 'loaded') && row.node_id) seen.add(row.node_id)
  }
  return [...seen].map(id => byId.get(id)).filter(Boolean)
}

// ---- Leaving the cluster ------------------------------------------------------

// What would happen to each model on a node if it stopped taking requests (a
// drain) or went away (a lost node). One item per model plus the requests in
// flight.
//
//   { kind: 'inflight', count }
//   { kind: 'stays',    model, on: [node names] }       another node has it loaded
//   { kind: 'blocked',  model, selector }               its rule allows no other node
//   { kind: 'reload',   model, candidates }             loads on demand on another node
//
// `reload` does not claim the model fits: replica memory is not in the API.
export function leavePreview({ node, rows, nodes, rules }) {
  if (!node) return []
  const items = []
  const inFlight = Number(node.in_flight_count) || 0
  if (inFlight > 0) items.push({ kind: 'inflight', count: inFlight })

  const others = (nodes || []).filter(n => n.id !== node.id)
  const mine = [...new Set((rows || []).filter(r => r.node_id === node.id).map(r => r.model_name))].sort()
  for (const model of mine) {
    const elsewhere = loadedOn((rows || []).filter(r => r.node_id !== node.id), others, model).filter(isPlaceable)
    if (elsewhere.length > 0) {
      items.push({ kind: 'stays', model, on: elsewhere.map(n => n.name) })
      continue
    }
    const rule = (rules || []).find(r => !r.shadowed && (r.model_name === model || r.target_model === model))
    const selector = selectorOf(rule)
    const candidates = eligibleNodes(others, selector)
    if (Object.keys(selector).length > 0 && candidates.length === 0) {
      items.push({ kind: 'blocked', model, selector })
    } else {
      items.push({ kind: 'reload', model, candidates: candidates.length })
    }
  }
  return items
}

// Models with one replica in the whole cluster: if their node goes, they stop
// until it returns or a load lands elsewhere.
export function singleReplicaModels(rows, nodes) {
  const placeable = new Set((nodes || []).filter(isPlaceable).map(n => n.id))
  const counts = new Map()
  for (const row of rows || []) {
    if (!row.model_name || !placeable.has(row.node_id)) continue
    if (row.state && row.state !== 'loaded') continue
    counts.set(row.model_name, (counts.get(row.model_name) || 0) + 1)
  }
  return [...counts].filter(([, count]) => count === 1).map(([model]) => model).sort()
}

// ---- Backend updates ------------------------------------------------------------

// The (backend, node) pairs a bulk update touches. `upgrades` is the map from
// GET /api/backends/upgrades: each entry names a backend with a newer version,
// and `node_drift` the nodes that differ from the cluster majority. With no
// drift reported, every healthy backend node (or the selected ones) takes the
// update. Agent workers install no backends.
export function updateTargets({ upgrades, nodes, selectedIds }) {
  const list = Object.values(upgrades || {}).filter(u => u?.backend_name && u.available_version)
  const pool = (nodes || []).filter(n => isPlaceable(n) && (!selectedIds || selectedIds.size === 0 || selectedIds.has(n.id)))
  const poolIds = new Set(pool.map(n => n.id))
  const pairs = []
  for (const u of list) {
    const drift = (u.node_drift || []).map(d => d.node_id).filter(id => poolIds.has(id))
    const ids = drift.length > 0 ? drift : pool.map(n => n.id)
    ids.forEach(id => pairs.push({ backend: u.backend_name, nodeId: id }))
  }
  return {
    pairs,
    backends: [...new Set(pairs.map(p => p.backend))],
    nodeIds: [...new Set(pairs.map(p => p.nodeId))],
  }
}

// ---- Adding a node -------------------------------------------------------------

// `option` is one of useImageSelector().options: the Docker flags and image tag
// of one hardware choice.
function dockerPrefix(option) {
  const flags = option?.dockerFlags
  return flags ? `docker run --net host ${flags} \\\n  ` : 'docker run --net host \\\n  '
}

const image = (option, dev) => `localai/localai:${dev ? option.devTag : option.tag}`

// The command that starts a registered worker. The token is never read by the
// page: the command names a variable and the person fills it in.
export function workerCommand({ flavor, nodeType, option, dev, frontend, natsUrl = 'nats://nats:4222' }) {
  const sub = nodeType === 'agent' ? 'agent-worker' : 'worker'
  if (flavor === 'cli') {
    return `local-ai ${sub} \\\n  --register-to "${frontend}" \\\n  --nats-url "${natsUrl}" \\\n  --registration-token "$LOCALAI_REGISTRATION_TOKEN"`
  }
  return `${dockerPrefix(option)}-e LOCALAI_REGISTER_TO="${frontend}" \\\n  -e LOCALAI_NATS_URL="${natsUrl}" \\\n  -e LOCALAI_REGISTRATION_TOKEN="$TOKEN" \\\n  ${image(option, dev)} ${sub}`
}

// Peer instances and memory shards join with the P2P network token. The
// federated server is the entry point they balance behind.
export function p2pCommand({ method, option, dev, token, flavor }) {
  const suffix = method === 'peer' ? 'run --federated --p2p'
    : method === 'server' ? 'federated'
      : method === 'mlx' ? 'worker p2p-mlx'
        : 'worker p2p-llama-cpp-rpc'
  const name = method === 'peer' ? 'local-ai' : method === 'server' ? 'local-ai-federated' : method === 'mlx' ? 'local-ai-mlx-worker' : 'local-ai-worker'
  const tokenValue = token || 'your-token-here'
  if (flavor === 'cli') return `TOKEN="${tokenValue}" local-ai ${suffix}`
  const opt = method === 'mlx' ? { dockerFlags: '', tag: 'latest-metal-darwin-arm64', devTag: 'latest-metal-darwin-arm64' } : option
  return `${dockerPrefix(opt)}-e TOKEN="${tokenValue}" \\\n  --name ${name} \\\n  ${image(opt, dev)} ${suffix}`
}

export const DISTRIBUTED_COMMAND = 'local-ai run --distributed \\\n  --distributed-db "postgres://user:pass@host/db" \\\n  --distributed-nats "nats://host:4222"'
