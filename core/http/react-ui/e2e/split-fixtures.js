// Fixtures for the model-split workers: the Nodes group and the "Model-split
// worker" way of Add a node. Pure of Playwright imports so a script that is
// not a test can use it too.
import { GB, clusterNodes } from './swarm-fixtures.js'

// The cluster of the Swarm fixtures with split servers: gpu-box-1 (healthy)
// and gpu-box-2 (not answering) by default.
export function splitServerNodes(ids = ['n-gpu1', 'n-gpu2']) {
  return clusterNodes().map(n => (ids.includes(n.id) ? { ...n, split_server: true } : n))
}

export const PEER = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
export const LINK = `localai://pair?v=1&k=split&p=${PEER}&c=S9QK2LWD8e&e=1794842400`
const json = body => ({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
const ago = ms => new Date(Date.now() - ms).toISOString()

// A slow worker behind a relay of the user that ends each connection after
// 1 GiB and 30 minutes: Use anyway, with the limits dialog of the relay.
export function ownRelayWorker() {
  return {
    id: 'w-home', label: 'home-box', peer_id: `${PEER}h`, engine: 'llama', state: 'slow', allow_slow_path: false,
    own_relay: true, relay_id: 'r1',
    path: { kind: 'relayed', relay_label: 'home relay', limited: true, limit_bytes: 1024 ** 3, limit_seconds: 1800, rtt_ms: 61 },
    memory_bytes: 16 * GB, created_at: ago(300_000),
  }
}

export function splitWorkers() {
  return [
    {
      id: 'w-attic', label: 'attic-3090', peer_id: `${PEER}a`, engine: 'llama', state: 'online', allow_slow_path: false,
      path: { kind: 'direct', limited: false, rtt_ms: 3 }, memory_bytes: 24 * GB, addr: '127.0.0.1:40001', created_at: ago(86_400_000),
    },
    {
      id: 'w-mac', label: 'mac-studio-2', peer_id: `${PEER}b`, engine: 'mlx', state: 'online', allow_slow_path: false, rank: 1,
      path: { kind: 'hole_punched', limited: false, rtt_ms: 9 }, memory_bytes: 96 * GB, addr: '127.0.0.1:40002', created_at: ago(3_600_000),
    },
    {
      id: 'w-old', label: 'old-rig', peer_id: `${PEER}c`, engine: 'llama', state: 'slow', allow_slow_path: false,
      path: { kind: 'relayed', relay_label: 'home relay', limited: false, rtt_ms: 52 }, memory_bytes: 12 * GB, created_at: ago(600_000),
    },
  ]
}

// Answers the P2P routes these pages call. Registered after mockSwarm, so it
// sees the P2P calls first and passes the rest on.
export async function mockSplit(page, { mode = 'share', workers = splitWorkers(), invites = () => [], onPatch, log = [], distributed = false } = {}) {
  let current = workers
  await page.route('**/api/p2p/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    if (path === '/api/p2p/status') return route.fulfill(json({ mode, enabled: mode !== 'off', running: mode !== 'off' }))
    if (mode !== 'share' && (path.startsWith('/api/p2p/split-workers') || path.startsWith('/api/p2p/invites'))) {
      return route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: { message: `P2P mode is "${mode}"`, code: 409 } }) })
    }
    if (path === '/api/p2p/split-workers' && method === 'GET') {
      const list = typeof current === 'function' ? current() : current
      return route.fulfill(json({ workers: list, distributed, memory_in_use: list.filter(w => w.state === 'online').reduce((sum, w) => sum + w.memory_bytes, 0) }))
    }
    const one = path.match(/^\/api\/p2p\/split-workers\/(.+)$/)
    if (one && method === 'PATCH') {
      const body = request.postDataJSON()
      log.push({ method, path, body })
      const id = decodeURIComponent(one[1])
      const list = typeof current === 'function' ? current() : current
      const change = w => ('node_id' in body
        ? { ...w, node_id: body.node_id, node_name: body.node_id ? `node ${body.node_id}` : '' }
        : { ...w, allow_slow_path: body.allow_slow_path, state: body.allow_slow_path ? 'online' : 'slow' })
      const updated = list.map(w => (w.id === id ? change(w) : w))
      if (typeof current !== 'function') current = updated
      onPatch?.(id, body)
      return route.fulfill(json(updated.find(w => w.id === id)))
    }
    if (path === '/api/p2p/invites' && method === 'POST') {
      const body = request.postDataJSON()
      log.push({ method, path, body })
      return route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 'inv-1', link: LINK, kind: 'split', expires_at: new Date(Date.now() + 600_000).toISOString(), qr: false, relay: { pass: false, reason: 'no_relay' }, warnings: ['no_relay'] }) })
    }
    if (path === '/api/p2p/invites' && method === 'GET') return route.fulfill(json({ invites: invites() }))
    if (path === '/api/p2p/relays/r1/limits' && method === 'GET') {
      log.push({ method, path })
      return route.fulfill(json({ limits: { circuit_bytes: 1024 ** 3, circuit_seconds: 1800, rate_bps: 0 }, can_change: true }))
    }
    const device = path.match(/^\/api\/p2p\/devices\/(.+)$/)
    if (device && method === 'DELETE') {
      log.push({ method, path })
      return route.fulfill(json({ revoked: true }))
    }
    return route.fallback()
  })
  return log
}
