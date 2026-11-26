// Fixtures for the Swarm pages: Nodes, node detail, Placement rules, Failover
// and Add a node. Plain data, plus one function that answers every call those
// pages make, so a spec says only what is different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const GB = 1024 ** 3

// Recent load readings in percent, oldest first, as the controller sends them.
const wave = (base, swing, count = 24) => Array.from({ length: count }, (_, i) => Math.round(Math.min(100, Math.max(0, base + Math.sin(i * 1.3) * swing + (i % 5)))))

const ago = ms => new Date(Date.now() - ms).toISOString()
const live = route => ({ route, state: 'live' })

// A small cluster with one of everything the pages tell apart: a healthy GPU
// box, an agent worker, a CPU node, a node that stopped answering and a node
// waiting for approval.
export function clusterNodes() {
  return [
    {
      id: 'n-agent', name: 'agent-host', node_type: 'agent', address: '10.0.4.31:50051', status: 'healthy', version: 'v3.9.0',
      total_ram: 16 * GB, available_ram: 10 * GB, cpu_logical_cores: 8, cpu_usage_percent: 14, cpu_load_1: 0.8,
      total_disk: 500 * GB, available_disk: 410 * GB, model_count: 0, in_flight_count: 0, last_heartbeat: ago(8_000), labels: { 'node.role': 'agents' }, load_history: wave(14, 6),
      attached: 'tunnel', route_state: [live('heartbeat'), { route: 'tunnel', transport: 'websocket', state: 'live' }],
    },
    {
      id: 'n-edge', name: 'edge-cpu', node_type: 'backend', address: '10.0.4.20:50051', status: 'healthy', version: 'v3.8.4',
      total_ram: 32 * GB, available_ram: 20 * GB, cpu_logical_cores: 12, cpu_usage_percent: 33, cpu_load_1: 2.1,
      total_disk: 800 * GB, available_disk: 520 * GB, model_count: 2, in_flight_count: 0, last_heartbeat: ago(6_000), labels: { role: 'edge' }, load_history: wave(33, 12),
      max_replicas_per_model: 1,
      attached: 'nats,tunnel', route_state: [live('heartbeat'), live('nats'), { route: 'tunnel', transport: 'websocket', state: 'live' }],
    },
    {
      id: 'n-gpu1', name: 'gpu-box-1', node_type: 'backend', address: '10.0.4.11:50051', status: 'healthy', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 48 * GB, available_vram: 19 * GB, total_ram: 128 * GB, available_ram: 68 * GB, cpu_logical_cores: 32, cpu_usage_percent: 52, cpu_load_1: 6.4,
      total_disk: 2000 * GB, available_disk: 1360 * GB, model_count: 2, in_flight_count: 3, last_heartbeat: ago(3_000), labels: { gpu: '4090', zone: 'a' }, load_history: wave(55, 14),
      max_replicas_per_model: 1,
      attached: 'nats', route_state: [live('heartbeat'), live('nats')],
    },
    {
      id: 'n-gpu2', name: 'gpu-box-2', node_type: 'backend', address: '10.0.4.12:50051', status: 'unhealthy', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 24 * GB, available_vram: 5.8 * GB, total_ram: 64 * GB, available_ram: 30 * GB, cpu_logical_cores: 16, cpu_usage_percent: 0, cpu_load_1: 0,
      total_disk: 1000 * GB, available_disk: 790 * GB, model_count: 2, in_flight_count: 0, last_heartbeat: ago(252_000), labels: { gpu: '4090', zone: 'b' },
      max_replicas_per_model: 1,
      route_state: [{ route: 'heartbeat', state: 'down', reason: 'the heartbeat is stale' }, { route: 'nats', state: 'down', reason: 'no recent report from the worker' }],
    },
    {
      id: 'n-gpu3', name: 'gpu-box-3', node_type: 'backend', address: '10.0.4.13:50051', status: 'pending', version: 'v3.9.0', gpu_vendor: 'nvidia',
      total_vram: 24 * GB, available_vram: 24 * GB, total_ram: 64 * GB, available_ram: 56 * GB, cpu_logical_cores: 16, cpu_usage_percent: 4, cpu_load_1: 0.3,
      total_disk: 1000 * GB, available_disk: 900 * GB, model_count: 0, in_flight_count: 0, last_heartbeat: ago(9_000), labels: {}, load_history: [4],
      route_state: [live('heartbeat'), { route: 'tunnel', transport: 'websocket', state: 'down', reason: 'not attached' }],
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

// The cluster settings, as GET /api/cluster/settings answers.
export function clusterSettingsFixture(overrides = {}) {
  return {
    nats_url: 'nats://nats.example.org:4222',
    nats_worker_url: '',
    carrier_weights: { nats: 100, websocket: 50, p2p: 25 },
    carrier_weights_default: { nats: 100, websocket: 50, p2p: 25 },
    join_auto_approve: false,
    bus: { bus: 'nats', nats_configured: true, nats_connected: true },
    replicas: [
      { id: 'frontend-1', version: 'v5.0.1', last_seen: new Date().toISOString() },
      { id: 'frontend-2', version: 'v5.0.1', last_seen: new Date().toISOString() },
    ],
    ...overrides,
  }
}

// The answer of POST /api/cluster/join-tokens: the plain token once, and the
// commands that carry it, built as the server builds them from the routes.
export function mintedToken(body = {}, index = 1) {
  const token = `lai_join_${String(index).padStart(2, '0')}SECRETSECRETSECRET`
  const routes = body.routes || []
  const wr = routes.map(r => (r === 'websocket' ? 'tunnel' : r)).join(',')
  const base = 'https://localai.example.org'
  const q = `?t=${token}${routes.length ? `&routes=${routes.join(',')}` : ''}`
  const ttl = body.ttl_seconds || 600
  const uses = body.uses || 1
  return {
    id: `jt-${index}`, label: body.label || '', state: 'unused', uses: 0, max_uses: uses, routes, auto_approve: body.auto_approve ?? false,
    created_by: 'admin', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + ttl * 1000).toISOString(),
    token,
    commands: {
      install_script: `curl -fsSL '${base}/join.sh${q}' | sudo sh`,
      windows: `irm '${base}/join.ps1${q}' | iex`,
      docker: `docker run -d --name localai-worker \\\n  -e LOCALAI_REGISTER_TO='${base}' \\\n  -e LOCALAI_JOIN_TOKEN='${token}' \\\n${wr ? `  -e LOCALAI_WORKER_ROUTES='${wr}' \\\n` : ''}  localai/localai:latest worker`,
      docker_compose: `services:\n  localai-worker:\n    environment:\n      LOCALAI_JOIN_TOKEN: "${token}"${wr ? `\n      LOCALAI_WORKER_ROUTES: "${wr}"` : ''}`,
      kubernetes: `kubectl apply -f '${base}/join.yaml${q}'`,
      manual: `LOCALAI_REGISTER_TO='${base}' LOCALAI_JOIN_TOKEN='${token}'${wr ? ` LOCALAI_WORKER_ROUTES='${wr}'` : ''} local-ai worker`,
    },
  }
}

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
//   chains     failover chains
//   log        an array that receives { method, path, body } for every write
export async function mockSwarm(page, {
  nodes = clusterNodes(), replicas = clusterReplicas(), rules = clusterRules(), features = { distributed: true },
  nodeModels = {}, nodeBackends = {}, chains = CHAINS, aliases = [], log = [], onNodes,
  cluster = clusterSettingsFixture(), clusterStatus = 200, onMint,
} = {}) {
  let settings = { ...cluster }
  let minted = 0
  const tokens = new Map()
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
    if (path === '/api/cluster/settings') {
      if (clusterStatus !== 200) return route.fulfill({ status: clusterStatus, contentType: 'application/json', body: '{"error":"distributed mode is not enabled on this frontend"}' })
      if (method === 'PUT') {
        record()
        const body = request.postDataJSON()
        const next = { ...settings }
        if ('nats_url' in body) next.nats_url = body.nats_url
        if ('nats_worker_url' in body) next.nats_worker_url = body.nats_worker_url
        if ('join_auto_approve' in body) next.join_auto_approve = body.join_auto_approve
        if (body.carrier_weights) {
          next.carrier_weights = { ...settings.carrier_weights }
          for (const [k, v] of Object.entries(body.carrier_weights)) next.carrier_weights[k] = v === null ? settings.carrier_weights_default[k] : v
        }
        settings = next
        return route.fulfill(json({ saved: true, ...(body.nats_url ? { nats: { reachable: true } } : {}), settings }))
      }
      return route.fulfill(json(settings))
    }
    if (path === '/api/cluster/join-tokens' && method === 'POST') {
      record()
      minted += 1
      const body = request.postDataJSON()
      const answer = onMint ? onMint(body, minted) : null
      if (answer?.error) return route.fulfill({ status: answer.status || 400, contentType: 'application/json', body: JSON.stringify({ error: answer.error }) })
      const token = answer || mintedToken(body, minted)
      tokens.set(token.id, { id: token.id, state: 'unused', uses: 0, max_uses: token.max_uses, routes: token.routes, expires_at: token.expires_at })
      return route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(token) })
    }
    if (path === '/api/cluster/join-tokens' && method === 'GET') return route.fulfill(json([...tokens.values()]))
    const revoked = path.match(/^\/api\/cluster\/join-tokens\/([^/]+)$/)
    if (revoked && method === 'DELETE') {
      record()
      tokens.delete(decodeURIComponent(revoked[1]))
      return route.fulfill(json({ message: 'join token revoked', id: revoked[1] }))
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
      if (rest === 'limits') {
        if (method !== 'GET') record()
        return route.fulfill(json({ max_active_backends: 0, vram_budget: '', memory_reclaimer_enabled: false, memory_reclaimer_threshold: 0.95, model_swap: true,
          sources: { max_active_backends: 'cluster', vram_budget: 'cluster', memory_reclaimer_threshold: 'cluster', model_swap: 'cluster' } }))
      }
      if (rest === 'split-server' && method === 'PUT') {
        record()
        const on = request.postDataJSON().split_server
        found && (found.split_server = on)
        return route.fulfill(json({ split_server: on, detached: on ? 0 : 1 }))
      }
    }

    if (path === '/api/aliases') return route.fulfill(json(aliases))
    if (path === '/api/models/capabilities') {
      return route.fulfill(json({ object: 'list', data: [...new Set((replicas || []).map(r => r.model_name))].map(id => ({ id })) }))
    }
    if (path === '/api/failover') return route.fulfill(json({ chains }))
    if (path === '/api/failover/events') {
      return route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: `event: snapshot\ndata: ${JSON.stringify({ chains })}\n\n` })
    }
    return route.fallback()
  })
  return log
}
