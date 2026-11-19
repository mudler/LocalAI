// Fixtures for Swarm, P2P, "Devices and invites", the Share dialog and the
// invite step of "connect a remote". Plain data, plus one function that
// answers every P2P call those screens make, so a spec says only what is
// different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const SERVER_PEER = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
export const DEVICE_PEER = '12D3KooWLp7mQe3Vx2cYpTzS8mFJuX3aWbLdQe5Gh6iKo2Qd'
export const REMOTE_PEER = '12D3KooWBn9sPqT4vX2mHcR8yLdQe5Gh6iKoPzWa7Ha'
export const RELAY_ADDR = `/dns4/relay.example.org/udp/4001/quic-v1/p2p/${SERVER_PEER}`

const ago = ms => new Date(Date.now() - ms).toISOString()
const inSec = s => new Date(Date.now() + s * 1000).toISOString()

// The models of the server, as GET /api/models/capabilities lists them.
export const SHARE_MODELS = ['llama-3.2-3b', 'qwen3-8b', 'gemma-3-4b', 'mistral-7b']

export function p2pStatus(mode = 'share', over = {}) {
  return {
    mode, enabled: mode !== 'off', running: mode !== 'off', peer_id: mode === 'off' ? undefined : SERVER_PEER,
    addrs: [], reachability: 'private', public_dht: { on: true, peers: 12 }, reservations: [],
    relay_service: { running: mode === 'share' }, pending_invites: 0, devices: 0, ...over,
  }
}

export function pendingInviteRows() {
  return [
    { id: 'inv-1', kind: 'device', label: "Alex's laptop", created_at: ago(20_000), created_by: 'u1', expires_at: inSec(582), status: 'pending', relay_pass: 'relay.example.org' },
    { id: 'inv-2', kind: 'remote', label: 'Branch LocalAI', created_at: ago(300_000), created_by: 'u1', expires_at: inSec(250), status: 'pending', all_models: true },
    { id: 'inv-3', kind: 'remote', label: 'Lab LocalAI', created_at: ago(120_000), created_by: 'u1', expires_at: inSec(400), status: 'pending', models: ['llama-3.2-3b', 'qwen3-8b'] },
    { id: 'inv-old', kind: 'device', label: 'Used one', created_at: ago(900_000), created_by: 'u1', expires_at: inSec(100), status: 'used', device_id: 'd-1' },
  ]
}

export function deviceRows() {
  return [
    { id: 'd-1', kind: 'device', label: "Alex's laptop", peer_id: DEVICE_PEER, created_at: ago(3_600_000), last_seen_at: ago(120_000), status: 'active', online: true, path: { kind: 'direct', limited: false, rtt_ms: 12 } },
    { id: 'd-2', kind: 'remote', label: 'Branch LocalAI', peer_id: REMOTE_PEER, created_at: ago(7_200_000), last_seen_at: ago(3_600_000), status: 'active', online: true, all_models: true, path: { kind: 'hole_punched', limited: false } },
    { id: 'd-6', kind: 'remote', label: 'Lab LocalAI', peer_id: '12D3KooWMm4sPqT4vX2mHcR8yLdQe5Gh6iKoPzWb8Jc', created_at: ago(5_000_000), last_seen_at: ago(600_000), status: 'active', online: true, models: ['llama-3.2-3b', 'qwen3-8b'], path: { kind: 'direct', limited: false } },
    { id: 'd-3', kind: 'device', label: "Sam's phone", peer_id: '12D3KooWQx8tR2vN6mHcR8yLdQe5Gh6iKoPzTz1', created_at: ago(9_000_000), last_seen_at: ago(300_000), status: 'active', online: true, path: { kind: 'relayed', relay_label: 'relay.example.org', limited: true, limit_bytes: 131072, limit_seconds: 120 } },
    { id: 'd-4', kind: 'device', label: 'Old tablet', peer_id: '12D3KooWTt5rN6mHcR8yLdQe5Gh6iKoPz5Rb', created_at: ago(900_000_000), last_seen_at: ago(259_200_000), status: 'active', online: false, path: { kind: 'none' } },
    { id: 'd-5', kind: 'device', label: 'Lost phone', peer_id: '12D3KooWZz9rN6mHcR8yLdQe5Gh6iKoPz7Kq', created_at: ago(900_000_000), status: 'revoked', online: false, path: { kind: 'none' } },
  ]
}

// The answer of a mint: a link with a relay pass by default.
export function mintAnswer(body, over = {}) {
  const expires = Math.floor(Date.now() / 1000) + (body.ttl_seconds || 600)
  const pass = over.relay?.pass ?? true
  const relay = over.relay ? Boolean(over.relay.id) : true
  const link = `localai://pair?v=1&k=${body.kind}&p=${SERVER_PEER}&c=K7QF29MXAB&e=${expires}&n=${encodeURIComponent(body.label || '')}` +
    `${relay ? `&r=${encodeURIComponent(RELAY_ADDR)}` : ''}${pass ? '&x=7HC2KQ9W' : ''}`
  return {
    id: 'inv-new', link, kind: body.kind, expires_at: new Date(expires * 1000).toISOString(), qr: ['device', 'remote'].includes(body.kind),
    relay: { id: 'r1', label: 'relay.example.org', pass: true, open: false }, warnings: [], ...over,
  }
}

// Answers every /api/p2p call. `mint` replaces the answer of a mint (an
// object merged into mintAnswer, or a function of the body). `redeemAfter`
// turns the minted invite into a used one after that many invite reads, as
// when the device redeems it while the dialog waits.
export async function mockP2PShare(page, { mode = 'share', status = {}, invites = pendingInviteRows(), devices = deviceRows(), mint = {}, models = SHARE_MODELS, redeemAfter = 0, phrase = 'amber canal dune echo', fail = {} } = {}) {
  const state = { invites: invites.map(i => structuredClone(i)), devices: devices.map(d => structuredClone(d)), calls: [], reads: 0 }
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models.map(id => ({ id, capabilities: ['FLAG_CHAT'] })) } }))
  await page.route('**/api/p2p/status', route => route.fulfill({ json: p2pStatus(mode, status) }))
  await page.route(/\/api\/p2p\/(invites|devices)(\/[^?]*)?(\?.*)?$/, async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace(/^.*\/api\/p2p/, '')
    const method = req.method()
    let body = null
    try { body = req.postDataJSON() } catch { body = null }
    state.calls.push({ method, path, body })
    const forced = fail[`${method} ${path}`]
    if (forced) return route.fulfill({ status: forced.status, json: { error: { message: forced.message || 'failed', code: forced.status, type: forced.type || 'p2p_error' } } })
    if (mode !== 'share') return route.fulfill({ status: 409, json: { error: { message: `P2P mode is "${mode}"` } } })
    if (path === '/invites' && method === 'POST') {
      const answer = typeof mint === 'function' ? mint(body) : mintAnswer(body, mint)
      state.invites.push({ id: answer.id, kind: body.kind, label: body.label, created_at: new Date().toISOString(), created_by: 'u1', expires_at: answer.expires_at, status: 'pending', relay_pass: answer.relay?.pass ? answer.relay.label : undefined, models: body.models, all_models: body.all_models })
      return route.fulfill({ status: 201, json: answer })
    }
    if (path === '/invites' && method === 'GET') {
      state.reads++
      const fresh = state.invites.find(i => i.id === 'inv-new')
      if (fresh && redeemAfter > 0 && state.reads >= redeemAfter && fresh.status === 'pending') {
        Object.assign(fresh, { status: 'used', device_id: 'd-new', device_peer_id: DEVICE_PEER, phrase })
        state.devices.push({ id: 'd-new', kind: fresh.kind, label: fresh.label, peer_id: DEVICE_PEER, created_at: new Date().toISOString(), last_seen_at: new Date().toISOString(), status: 'active', online: true, models: fresh.models, all_models: fresh.all_models, path: { kind: 'direct', rtt_ms: 12 } })
      }
      return route.fulfill({ json: { invites: state.invites } })
    }
    if (path === '/devices' && method === 'GET') return route.fulfill({ json: { devices: state.devices } })
    const [, coll, id] = path.match(/^\/(invites|devices)\/(.+)$/) || []
    if (coll === 'invites' && method === 'DELETE') {
      const inv = state.invites.find(i => i.id === id)
      if (inv) inv.status = 'canceled'
      return route.fulfill({ json: { ok: true } })
    }
    if (coll === 'devices' && method === 'DELETE') {
      const dev = state.devices.find(d => d.id === id)
      if (dev) dev.status = 'revoked'
      return route.fulfill({ json: { revoked: true } })
    }
    return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
  })
  return state
}

// Authentication on, the viewer an administrator.
export async function mockAdmin(page) {
  const user = { id: 'u1', name: 'Admin', role: 'admin', permissions: {} }
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user } }))
  await page.route('**/api/auth/me', route => route.fulfill({ json: { user, permissions: {} } }))
  await page.route('**/api/auth/quota', route => route.fulfill({ json: { quotas: [] } }))
}

// A remote invite link as the other LocalAI would make it.
export function remoteInviteLink({ kind = 'remote', ttl = 480, pass = true, label = 'Branch LocalAI' } = {}) {
  const e = Math.floor(Date.now() / 1000) + ttl
  return `localai://pair?v=1&k=${kind}&p=${REMOTE_PEER}&c=R2DX7KQMAB&e=${e}&n=${encodeURIComponent(label)}&r=${encodeURIComponent(RELAY_ADDR)}${pass ? '&x=4MPT8XQC' : ''}`
}
