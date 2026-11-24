// Fixtures for the P2P settings page: the P2P settings, the status, the relays
// and one function that answers every call the page makes. A spec says only
// what differs about its case. Pure of Playwright imports so a script that is
// not a test can use it too.

export const PEER_ID = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
export const RELAY_ID = '12D3KooWRl8cXbQ2vY7mPz4sNdKfE1aHjTgUo3iC6wLr5B9Zz'
export const OTHER_ID = '12D3KooWZq9a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8Lp'
export const RELAY_ADDR = `/dns4/relay.example.org/udp/4001/quic-v1/p2p/${RELAY_ID}`

const ago = (ms) => new Date(Date.now() - ms).toISOString()

export function p2pSettings(mode = 'share', extra = {}) {
  return {
    p2p_mode: mode, p2p_public_dht: true, p2p_mdns: true, p2p_bootstrap_peers: [],
    p2p_relay_paired: true, p2p_relay_mode: 'auto', p2p_relay_rate_limit: 0, ...extra,
  }
}

export function p2pStatus(mode = 'share', extra = {}) {
  const on = mode !== 'off'
  return {
    mode, enabled: on, running: on, peer_id: on ? PEER_ID : '',
    addrs: on ? [`/ip4/192.0.2.10/udp/4001/quic-v1/p2p/${PEER_ID}`] : [],
    reachability: 'public', public_dht: { on: true, peers: 41 }, reservations: [],
    relay_service: mode === 'share' ? { running: true } : { running: false, reason: 'not_share' },
    pending_invites: 1, devices: 12, ...extra,
  }
}

const limits = (bytes, seconds, rate) => ({ circuit_bytes: bytes, circuit_seconds: seconds, rate_bps: rate })

// What the operator of each gated relay allows at most: r-gated has no
// ceiling, r-sync has the defaults of the relay command (8 GiB and 24 h).
const CEILINGS = { 'r-sync': limits(8 * 2 ** 30, 86400, 0) }
const NO_CEILING = limits(0, 0, 0)

// One relay of each kind the table tells apart.
export function relayList() {
  return [
    { id: 'r-gated', label: 'Home relay', addr: RELAY_ADDR, mode: 'gated', state: 'in_sync', health: 'in sync', latency_ms: 34, admins: 2, members: 12, limits: limits(0, 0, 0), checked_at: ago(120_000) },
    { id: 'r-sync', label: 'Office relay', addr: `/dns4/relay2.example.net/tcp/4001/p2p/${OTHER_ID}`, mode: 'gated', state: 'out_of_sync', health: "the relay's list differs from the paired devices", latency_ms: 61, admins: 1, members: 11, limits: limits(1 << 30, 3600, 10 << 20), checked_at: ago(300_000) },
    { id: 'r-open', label: '', addr: `/ip4/198.51.100.20/udp/4001/quic-v1/p2p/${OTHER_ID}`, mode: 'open', state: 'open', health: 'open relay: anyone can use it', latency_ms: 88, admins: 0, members: 0, limits: limits(1 << 30, 1800, 64 << 10), checked_at: ago(3_600_000) },
    { id: 'r-down', label: 'Old relay', addr: `/dns4/relay-old.example.com/tcp/4001/p2p/${OTHER_ID}`, mode: 'unknown', state: 'unreachable', health: 'cannot connect: no good addresses', admins: 0, members: 0, limits: limits(0, 0, 0), checked_at: ago(7_200_000) },
  ]
}

// The answers of POST /api/p2p/relays/{id}/check for the Run a relay cases.
export const checks = {
  ok: {
    connected: true, reserved: true, admin_ok: true, circuit_ok: true, in_sync: true,
    detail: ['connect: ok in 12 ms', 'reserve: ok, until 2026-11-17T10:00:00Z', 'admin: in_sync (in sync)', 'circuit: ok', 'digest: equal'],
    relay: { id: 'r-new', state: 'in_sync', mode: 'gated' },
  },
  unreachable: {
    connected: false, detail: ['connect: failed: no answer on UDP or TCP 4001 in 10 s'],
    relay: { id: 'r-new', state: 'unreachable', health: 'cannot connect: no answer on UDP or TCP 4001 in 10 s' },
  },
  wrongPeer: {
    connected: false, detail: [`connect: failed: peer id mismatch: expected ${RELAY_ID}, but remote key matches ${OTHER_ID}`],
    relay: { id: 'r-new', state: 'unreachable' },
  },
  unclaimed: {
    connected: true, reserved: true, detail: ['connect: ok in 9 ms', 'reserve: ok, until 2026-11-17T10:00:00Z', 'admin: unclaimed (the relay has no admin yet)', 'circuit: skipped, this server cannot add a test peer to the relay'],
    relay: { id: 'r-new', state: 'unclaimed', mode: 'unclaimed' },
  },
}

// mockP2PSettings answers every call of the page and records the writes in
// `calls`. `auth` signs in an administrator with authentication on.
export async function mockP2PSettings(page, { mode = 'share', settings = {}, status = {}, relays = relayList(), check = checks.ok, auth = true, refuseLimits = '', ceilings = CEILINGS } = {}) {
  const calls = { settings: [], added: [], removed: [], synced: [], claimed: [], limits: [], regenerated: 0 }
  let current = p2pSettings(mode, settings)
  let list = relays
  if (auth) {
    const user = { id: 'admin-uuid', name: 'Admin', role: 'admin', provider: 'local' }
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user } }))
    await page.route('**/api/auth/me', route => route.fulfill({ json: { user, permissions: {} } }))
    await page.route('**/api/auth/quota', route => route.fulfill({ json: { quotas: [] } }))
  }
  await page.route('**/api/settings', async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      calls.settings.push(body)
      current = { ...current, ...body }
      return route.fulfill({ json: { success: true, message: 'Settings updated successfully' } })
    }
    return route.fulfill({ json: current })
  })
  await page.route('**/api/p2p/status', route => route.fulfill({ json: p2pStatus(current.p2p_mode, status) }))
  await page.route('**/api/p2p/identity/regenerate', route => {
    calls.regenerated++
    return route.fulfill({ json: p2pStatus(current.p2p_mode, { peer_id: OTHER_ID }) })
  })
  await page.route(/\/api\/p2p\/relays(\/.*)?$/, async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    const m = /\/api\/p2p\/relays(?:\/([^/]+))?(?:\/(check|sync|claim|limits))?$/.exec(path)
    const id = m?.[1] && decodeURIComponent(m[1])
    const op = m?.[2]
    if (!id && req.method() === 'GET') return route.fulfill({ json: { relays: list } })
    if (!id && req.method() === 'POST') {
      const body = req.postDataJSON()
      calls.added.push(body)
      const relay = { id: 'r-new', label: '', addr: body.addr, mode: 'unknown', state: 'checking', admins: 0, members: 0, limits: limits(0, 0, 0) }
      list = [...list, relay]
      return route.fulfill({ status: 201, json: relay })
    }
    if (op === 'limits') {
      const relay = list.find(r => r.id === id)
      const can = relay?.mode === 'gated' && relay?.state !== 'unclaimed'
      if (req.method() === 'PUT') {
        const body = req.postDataJSON()
        calls.limits.push({ id, ...body })
        if (refuseLimits) return route.fulfill({ status: 400, json: { error: { message: refuseLimits } } })
        const restarted = body.circuit_bytes !== relay.limits.circuit_bytes || body.circuit_seconds !== relay.limits.circuit_seconds
        list = list.map(r => (r.id === id ? { ...r, limits: { circuit_bytes: body.circuit_bytes, circuit_seconds: body.circuit_seconds, rate_bps: body.rate_bps } } : r))
        return route.fulfill({ json: { limits: list.find(r => r.id === id).limits, ceiling: ceilings[id] || NO_CEILING, can_change: true, restarted } })
      }
      return route.fulfill({ json: { limits: relay.limits, ceiling: ceilings[id] || NO_CEILING, can_change: can } })
    }
    if (op === 'check') return route.fulfill({ json: id === 'r-new' ? check : { ...checks.ok, relay: list.find(r => r.id === id) } })
    if (op === 'sync') { calls.synced.push(id); return route.fulfill({ json: { ...list.find(r => r.id === id), state: 'in_sync' } }) }
    if (op === 'claim') {
      calls.claimed.push({ id, ...req.postDataJSON() })
      check = checks.ok
      return route.fulfill({ json: { id, state: 'in_sync' } })
    }
    if (req.method() === 'DELETE') {
      calls.removed.push(id)
      list = list.filter(r => r.id !== id)
      return route.fulfill({ json: { ok: true } })
    }
    return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
  })
  return calls
}
