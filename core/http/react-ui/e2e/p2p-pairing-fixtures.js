// Fixtures for a user's own devices (Account, My devices, feature p2p_pair),
// the User pairing section of the P2P settings and the owner column of the
// admin device table. Plain data, plus one function that answers the calls of
// My devices and records them.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const SERVER_PEER = '12D3KooWRk4eHq7vN2cYpTzS8mFJuX3aWbLdQe5Gh6iKoPzXk9'
export const PHONE_PEER = '12D3KooWPh9nQe3Vx2cYpTzS8mFJuX3aWbLdQe5Gh6iKo4Ph'
export const PUBLIC_RELAY = `/ip4/203.0.113.7/tcp/4001/p2p/12D3KooWHn7sPqT4vX2mHcR8yLdQe5Gh6iKoPzWa7Hn`

const ago = ms => new Date(Date.now() - ms).toISOString()
const inSec = s => new Date(Date.now() + s * 1000).toISOString()

export const LIMITS = { max_devices: 5, max_pending: 2, max_ttl_seconds: 3600, max_mints_per_hour: 10 }

// GET /api/p2p/me.
export function myPairing(over = {}) {
  return { available: true, mode: 'share', limits: LIMITS, devices: 0, pending_invites: 0, ...over }
}

// The phones of alice: one online, one away, one revoked.
export function myDeviceRows() {
  return [
    { id: 'md-1', kind: 'device', label: 'Pixel 9', peer_id: PHONE_PEER, created_at: ago(86_400_000), last_seen_at: ago(60_000), status: 'active', online: true, sign_in: true, owner_id: 'u-alice', path: { kind: 'hole_punched', limited: false } },
    { id: 'md-2', kind: 'device', label: 'Work tablet', peer_id: '12D3KooWTb5rN6mHcR8yLdQe5Gh6iKoPz5Tb', created_at: ago(5 * 86_400_000), last_seen_at: ago(2 * 86_400_000), status: 'active', online: false, sign_in: true, owner_id: 'u-alice', path: { kind: 'none' } },
    { id: 'md-3', kind: 'device', label: 'Old phone', peer_id: '12D3KooWOp9rN6mHcR8yLdQe5Gh6iKoPz7Op', created_at: ago(40 * 86_400_000), status: 'revoked', online: false, sign_in: true, owner_id: 'u-alice', path: { kind: 'none' } },
  ]
}

export function myInviteRows() {
  return [
    { id: 'mi-1', kind: 'device', label: 'Kitchen tablet', created_at: ago(60_000), created_by: 'u-alice', expires_at: inSec(540), status: 'pending' },
  ]
}

// A mint answer for a user: a sign-in device invite whose link names a public
// relay for hole punching only (punch_only), as on a server with no relay.
export function myMintAnswer(body, over = {}) {
  const expires = Math.floor(Date.now() / 1000) + (body.ttl_seconds || 600)
  const link = `localai://pair?v=1&k=device&p=${SERVER_PEER}&c=K7QF29MXAB&e=${expires}&n=${encodeURIComponent(body.label || '')}&h=${encodeURIComponent(PUBLIC_RELAY)}`
  return {
    id: 'mi-new', link, kind: 'device', expires_at: new Date(expires * 1000).toISOString(), qr: true,
    relay: {}, warnings: ['punch_only', 'no_public_addr'], hints: 1, ...over,
  }
}

// Answers /api/p2p/me and its invites and devices. `me` is merged into the
// answer of GET /api/p2p/me; `fail` forces an answer for "METHOD /path";
// `redeemAfter` turns the minted invite into a device after that many invite
// reads, as when the phone pairs while the dialog waits.
export async function mockMyPairing(page, { me = {}, devices = [], invites = [], fail = {}, redeemAfter = 0, mint = {} } = {}) {
  const state = { devices: devices.map(d => structuredClone(d)), invites: invites.map(i => structuredClone(i)), calls: [], reads: 0 }
  const meAnswer = () => myPairing({
    devices: state.devices.filter(d => d.status !== 'revoked').length,
    pending_invites: state.invites.filter(i => i.status === 'pending').length,
    ...me,
  })
  await page.route(/\/api\/p2p\/me(\/[^?]*)?(\?.*)?$/, async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace(/^.*\/api\/p2p\/me/, '') || '/'
    const method = req.method()
    let body = null
    try { body = req.postDataJSON() } catch { body = null }
    state.calls.push({ method, path, body })
    const forced = fail[`${method} ${path}`]
    if (forced) return route.fulfill({ status: forced.status, json: { error: { message: forced.message || 'failed', code: forced.status, type: forced.type || 'p2p_error' } } })
    if (path === '/' && method === 'GET') return route.fulfill({ json: meAnswer() })
    if (path === '/invites' && method === 'POST') {
      const answer = { ...myMintAnswer(body), ...mint }
      state.invites.push({ id: answer.id, kind: 'device', label: body.label, created_at: new Date().toISOString(), created_by: 'u-alice', expires_at: answer.expires_at, status: 'pending' })
      return route.fulfill({ status: 201, json: answer })
    }
    if (path === '/invites' && method === 'GET') {
      state.reads++
      const fresh = state.invites.find(i => i.id === 'mi-new')
      if (fresh && redeemAfter > 0 && state.reads >= redeemAfter && fresh.status === 'pending') {
        fresh.status = 'used'
        state.devices.push({ id: 'md-new', kind: 'device', label: fresh.label, peer_id: PHONE_PEER, created_at: new Date().toISOString(), last_seen_at: new Date().toISOString(), status: 'active', online: true, sign_in: true, owner_id: 'u-alice', path: { kind: 'direct' } })
      }
      return route.fulfill({ json: { invites: state.invites.filter(i => i.status === 'pending') } })
    }
    if (path === '/devices' && method === 'GET') return route.fulfill({ json: { devices: state.devices } })
    const [, coll, id] = path.match(/^\/(invites|devices)\/(.+)$/) || []
    if (coll === 'invites' && method === 'DELETE') {
      const inv = state.invites.find(i => i.id === id)
      if (inv) inv.status = 'canceled'
      return route.fulfill({ json: { ok: true } })
    }
    if (coll === 'devices' && method === 'PATCH') {
      const dev = state.devices.find(d => d.id === id)
      if (dev) dev.label = body?.label || dev.label
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

// The admin device table with owners: alice's phone signs in, bob's laptop
// is admission only, and a device the API key paired has no owner.
export function ownedDeviceRows() {
  return [
    { id: 'od-1', kind: 'device', label: 'Pixel 9', peer_id: PHONE_PEER, created_at: ago(86_400_000), last_seen_at: ago(60_000), status: 'active', online: true, sign_in: true, owner_id: 'u-alice', owner_name: 'alice', path: { kind: 'direct', rtt_ms: 18 } },
    { id: 'od-2', kind: 'device', label: "Bob's laptop", peer_id: '12D3KooWBl8tR2vN6mHcR8yLdQe5Gh6iKoPzBl', created_at: ago(9_000_000), last_seen_at: ago(300_000), status: 'active', online: true, sign_in: false, owner_id: 'u-bob', owner_name: 'bob', path: { kind: 'hole_punched' } },
    { id: 'od-3', kind: 'device', label: 'Lab kiosk', peer_id: '12D3KooWKk5rN6mHcR8yLdQe5Gh6iKoPz5Kk', created_at: ago(90_000_000), last_seen_at: ago(7_200_000), status: 'active', online: false, sign_in: false, owner_id: '', path: { kind: 'none' } },
    { id: 'od-4', kind: 'device', label: 'Galaxy S24', peer_id: '12D3KooWGs9rN6mHcR8yLdQe5Gh6iKoPz7Gs', created_at: ago(3_600_000), last_seen_at: ago(120_000), status: 'active', online: true, sign_in: true, owner_id: 'u-bob', owner_name: 'bob', path: { kind: 'relayed', relay_label: 'relay.example.org', limited: false } },
  ]
}

// GET /api/p2p/user-pairing, and PUT merging the patch.
export async function mockUserPairing(page, initial = {}) {
  const state = { current: { relays: 'auto', allow_open_relay: false, omit_lan_addrs: false, max_devices: 5, max_pending: 2, max_ttl_seconds: 3600, ...initial }, puts: [] }
  await page.route('**/api/p2p/user-pairing', async route => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON()
      state.puts.push(body)
      state.current = { ...state.current, ...body }
    }
    return route.fulfill({ json: state.current })
  })
  return state
}

// /api/auth/admin/features with p2p_pair, whose default is configurable.
export function featuresWithPair(defaultOn = false) {
  return {
    api_features: [{ key: 'chat', label: 'Chat completions', default: true, configurable: false }],
    agent_features: [{ key: 'agents', label: 'Agents', default: false, configurable: false }],
    general_features: [
      { key: 'fine_tuning', label: 'Fine-tuning', default: false, configurable: false },
      { key: 'p2p_pair', label: 'Pair own devices (P2P)', default: defaultOn, configurable: true },
    ],
    models: ['qwen3-8b-instruct'],
  }
}
