// Fixtures for the Swarm pages: Nodes, node detail, Placement rules, Failover
// and Add a node. Plain data, plus one function that answers every call those
// pages make, so a spec says only what is different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const GB = 1024 ** 3

const ago = ms => new Date(Date.now() - ms).toISOString()

// A small cluster with one of everything the pages tell apart: a healthy GPU
// box, an agent worker, a CPU node, a node that stopped answering and a node
// waiting for approval.
export function clusterNodes() {
  return [
    {
      id: 'n-agent', name: 'agent-host', node_type: 'agent', address: '10.0.4.31:50051', status: 'healthy', version: 'v3.9.0',
      total_ram: 16 * GB, available_ram: 10 * GB, cpu_logical_cores: 8, cpu_usage_percent: 14, cpu_load_1: 0.8,
      total_disk: 500 * GB, available_disk: 410 * GB, model_count: 0, in_flight_count: 0, last_heartbeat: ago(8_000), labels: { 'node.role': 'agents' },
    },
    {
      id: 'n-edge', name: 'edge-cpu', node_type: 'backend', address: '10.0.4.20:50051', status: 'healthy', version: 'v3.8.4',
      total_ram: 32 * GB, available_ram: 20 * GB, cpu_logical_cores: 12, cpu_usage_percent: 33, cpu_load_1: 2.1,
      total_disk: 800 * GB, available_disk: 520 * GB, model_count: 2, in_flight_count: 0, last_heartbeat: ago(6_000), labels: { role: 'edge' },
      max_replicas_per_model: 1,
    },
    {
      id: 'n-gpu1', name: 'gpu-box-1', node_type: 'backend', address: '10.0.4.11:50051', status: 'healthy', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 48 * GB, available_vram: 19 * GB, total_ram: 128 * GB, available_ram: 68 * GB, cpu_logical_cores: 32, cpu_usage_percent: 52, cpu_load_1: 6.4,
      total_disk: 2000 * GB, available_disk: 1360 * GB, model_count: 2, in_flight_count: 3, last_heartbeat: ago(3_000), labels: { gpu: '4090', zone: 'a' },
      max_replicas_per_model: 1,
    },
    {
      id: 'n-gpu2', name: 'gpu-box-2', node_type: 'backend', address: '10.0.4.12:50051', status: 'unhealthy', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 24 * GB, available_vram: 5.8 * GB, total_ram: 64 * GB, available_ram: 30 * GB, cpu_logical_cores: 16, cpu_usage_percent: 0, cpu_load_1: 0,
      total_disk: 1000 * GB, available_disk: 790 * GB, model_count: 2, in_flight_count: 0, last_heartbeat: ago(252_000), labels: { gpu: '4090', zone: 'b' },
      max_replicas_per_model: 1,
    },
    {
      id: 'n-gpu3', name: 'gpu-box-3', node_type: 'backend', address: '10.0.4.13:50051', status: 'pending', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 24 * GB, available_vram: 24 * GB, total_ram: 64 * GB, available_ram: 56 * GB, cpu_logical_cores: 16, cpu_usage_percent: 4, cpu_load_1: 0.3,
      total_disk: 1000 * GB, available_disk: 900 * GB, model_count: 0, in_flight_count: 0, last_heartbeat: ago(9_000), labels: {},
    },
  ]
}

// Loaded replicas, as GET /api/nodes/models returns them.
export function clusterReplicas() {
  const row = (id, node, model, backend, extra = {}) => ({
    id, node_id: node, model_name: model, replica_index: 0, address: `10.0.4.1:5${id.length}00`, state: 'loaded', in_flight: 0, backend_type: backend,
    last_used: ago(40_000), ...extra,
  })
  return [
    row('r1', 'n-gpu1', 'qwen3-8b-instruct', 'llama-cpp', { in_flight: 2 }),
    row('r2', 'n-gpu1', 'llama-3.3-70b-q4', 'llama-cpp', { in_flight: 1 }),
    row('r3', 'n-gpu2', 'flux-dev', 'stablediffusion-ggml'),
    row('r4', 'n-gpu2', 'qwen3-8b-instruct', 'llama-cpp'),
    row('r5', 'n-edge', 'bge-m3', 'llama-cpp'),
    row('r6', 'n-edge', 'kokoro-82m', 'kokoro'),
  ]
}

export function clusterRules() {
  return [
    { id: 'u1', model_name: 'qwen3-8b-instruct', node_selector: { gpu: '4090' }, min_replicas: 2, max_replicas: 3, route_policy: 'prefix_cache' },
    { id: 'u2', model_name: 'llama-3.3-70b-q4', node_selector: { gpu: '4090' }, min_replicas: 0, max_replicas: 0 },
    { id: 'u3', model_name: 'flux-dev', node_selector: { zone: 'b' }, min_replicas: 0, max_replicas: 0 },
    { id: 'u4', model_name: 'bge-m3', node_selector: { role: 'edge' }, spread_all: true },
    { id: 'u5', model_name: 'kokoro-82m', node_selector: { role: 'edge' }, min_replicas: 0, max_replicas: 0 },
  ]
}

export const CHAINS = [
  {
    name: 'assistant-llm', state: 'primary', active: 'qwen3-8b-instruct', active_since: '2026-10-08T09:00:00Z', pinned: null,
    targets: [
      { model: 'qwen3-8b-instruct', kind: 'local', warm: true, state: 'healthy' },
      { model: 'gemma-local', kind: 'local', warm: false, state: 'healthy' },
    ],
  },
]

const json = body => ({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

// Routes every call the Swarm pages make. Anything it does not know goes on to
// the real server (`fallback`), so a spec can still use the test server for the
// rest of the app.
//
//   nodes      the roster, or null for a single install (404, as the real one)
//   replicas   loaded replicas, or null for an error
//   rules      placement rules
//   features   the /api/features answer
//   nodeModels / nodeBackends   per node id
//   upgrades   GET /api/backends/upgrades
//   chains     failover chains
//   log        an array that receives { method, path, body } for every write
export async function mockSwarm(page, {
  nodes = clusterNodes(), replicas = clusterReplicas(), rules = clusterRules(), features = { distributed: true },
  nodeModels = {}, nodeBackends = {}, upgrades = {}, chains = CHAINS, aliases = [], p2p = null, log = [], onNodes,
} = {}) {
  // The rules behave like the server's: a save adds or replaces, a delete removes.
  let current = [...rules]
  await page.route('**/api/auth/status', route => route.fulfill(json({
    authEnabled: true, staticApiKeyRequired: false, providers: ['local'],
    user: { id: 'admin', name: 'Admin', role: 'admin', provider: 'local' },
  })))
  await page.route('**/api/features', route => route.fulfill(json(features)))
  await page.route('**/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()
    const record = () => {
      let body = null
      try { body = request.postDataJSON() } catch { body = request.postData() }
      log.push({ method, path, body })
    }

    if (path === '/api/nodes' && method === 'GET') {
      if (nodes === null) return route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"not found"}' })
      return route.fulfill(json(onNodes ? onNodes() : nodes))
    }
    if (path === '/api/nodes/models') {
      return replicas === null ? route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"controller unavailable"}' }) : route.fulfill(json(replicas))
    }
    if (path === '/api/nodes/scheduling') {
      if (method === 'POST') {
        record()
        const saved = request.postDataJSON()
        current = current.some(r => r.model_name === saved.model_name)
          ? current.map(r => (r.model_name === saved.model_name ? { ...r, ...saved } : r))
          : [...current, { id: `new-${current.length}`, ...saved }]
        return route.fulfill(json({ message: 'saved' }))
      }
      return route.fulfill(json(current))
    }
    const schedulingDelete = path.match(/^\/api\/nodes\/scheduling\/(.+)$/)
    if (schedulingDelete && method === 'DELETE') {
      record()
      current = current.filter(r => r.model_name !== decodeURIComponent(schedulingDelete[1]))
      return route.fulfill(json({ message: 'removed' }))
    }

    const node = path.match(/^\/api\/nodes\/([^/]+)(?:\/(.+))?$/)
    if (node && nodes) {
      const [, id, rest] = node
      const found = nodes.find(n => n.id === id)
      if (!rest && method === 'GET') return found ? route.fulfill(json(found)) : route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"node not found"}' })
      if (!rest && method === 'DELETE') { record(); return route.fulfill(json({ message: 'removed' })) }
      if (rest === 'models' && method === 'GET') {
        const rows = nodeModels[id] ?? (replicas || []).filter(r => r.node_id === id)
        return route.fulfill(json(rows))
      }
      if (rest === 'backends' && method === 'GET') {
        return route.fulfill(json(nodeBackends[id] ?? [
          { name: 'llama-cpp', is_system: false, installed_at: ago(3 * 86_400_000) },
          { name: 'whisper', is_system: true, installed_at: ago(9 * 86_400_000) },
        ]))
      }
      if (['drain', 'resume', 'approve', 'models/unload', 'backends/upgrade', 'backends/delete', 'backends/install'].includes(rest) && method === 'POST') {
        record()
        return route.fulfill(json({ message: 'ok' }))
      }
      if (rest?.startsWith('labels') || rest === 'max-replicas-per-model' || rest === 'vram-budget') { record(); return route.fulfill(json({})) }
    }

    if (path === '/api/backends/upgrades') return route.fulfill(json(upgrades))
    if (path === '/api/aliases') return route.fulfill(json(aliases))
    if (path === '/api/models/capabilities') {
      return route.fulfill(json({ object: 'list', data: [...new Set((replicas || []).map(r => r.model_name))].map(id => ({ id })) }))
    }
    if (path === '/api/failover') return route.fulfill(json({ chains }))
    if (path === '/api/failover/events') {
      return route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: `event: snapshot\ndata: ${JSON.stringify({ chains })}\n\n` })
    }
    if (path === '/api/p2p/token') {
      return p2p ? route.fulfill({ status: 200, contentType: 'text/plain', body: p2p.token }) : route.fulfill({ status: 404, body: '' })
    }
    if (path === '/api/p2p/stats' && p2p) return route.fulfill(json(p2p.stats))
    if (path === '/api/p2p/workers' && p2p) return route.fulfill(json(p2p.workers || { nodes: [] }))
    if (path === '/api/p2p/federation' && p2p) return route.fulfill(json(p2p.federation || { nodes: [] }))
    return route.fallback()
  })
  return log
}
