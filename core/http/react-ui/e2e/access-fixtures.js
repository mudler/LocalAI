// Fixtures for Users and keys, Account, sign-in and the invite page: auth
// status in each variant the server can answer, five users, invites, keys,
// quota rules and a month of usage, and one function that answers the calls
// those pages make and records the writes.
//
// Pure of Playwright imports so a script that is not a test can use it too.

const iso = (offsetMs) => new Date(Date.now() + offsetMs).toISOString()
const DAY = 86_400_000
const HOUR = 3_600_000

export const ADMIN = { id: 'u-alice', email: 'alice@lab.example', name: 'alice', role: 'admin', provider: 'local' }
export const MEMBER = { id: 'u-bob', email: 'bob@lab.example', name: 'bob', role: 'user', provider: 'github' }

export function statusFixture(variant = 'admin', extra = {}) {
  const base = { authEnabled: true, staticApiKeyRequired: false, providers: ['local', 'github'], hasUsers: true, registrationMode: 'approval', user: null }
  const variants = {
    admin: { user: { ...ADMIN, permissions: {} } },
    member: { user: { ...MEMBER, provider: 'local', permissions: { chat: true } } },
    oauthMember: { user: { ...MEMBER, permissions: { chat: true } } },
    signedOut: {},
    localOnly: { providers: ['local'] },
    oauthOnly: { providers: ['github', 'oidc'] },
    firstAdmin: { providers: ['local'], hasUsers: false, registrationMode: 'approval' },
    keyOnly: { authEnabled: false, staticApiKeyRequired: true, providers: [], hasUsers: false, registrationMode: '' },
    inviteOnly: { providers: ['local'], registrationMode: 'invite' },
  }
  return { ...base, ...variants[variant], ...extra }
}

export const FEATURES = {
  api_features: [
    { key: 'chat', label: 'Chat completions', default: true },
    { key: 'images', label: 'Image generation', default: true },
    { key: 'audio_speech', label: 'Audio speech', default: true },
    { key: 'embeddings', label: 'Embeddings', default: true },
  ],
  agent_features: [
    { key: 'agents', label: 'Agents', default: false },
    { key: 'skills', label: 'Skills', default: false },
  ],
  general_features: [{ key: 'fine_tuning', label: 'Fine-tuning', default: false }],
  models: ['qwen3-8b-instruct', 'bge-m3', 'kokoro-82m', 'gemma-3-12b-it'],
}

const allOn = { chat: true, images: true, audio_speech: true, embeddings: true, agents: false, skills: false, fine_tuning: false }

export function usersFixture() {
  return [
    { id: 'u-alice', email: 'alice@lab.example', name: 'alice', role: 'admin', status: 'active', provider: 'local', createdAt: iso(-90 * DAY), permissions: allOn },
    { id: 'u-bob', email: 'bob@lab.example', name: 'bob', role: 'user', status: 'active', provider: 'github', createdAt: iso(-60 * DAY),
      permissions: { ...allOn, agents: true, skills: true }, allowed_models: { enabled: true, models: ['qwen3-8b-instruct', 'bge-m3'] }, quotas: [] },
    { id: 'u-carol', email: 'carol@lab.example', name: 'carol', role: 'user', status: 'active', provider: 'oidc', createdAt: iso(-30 * DAY),
      permissions: { ...allOn, agents: true }, allowed_models: { enabled: false, models: [] },
      quotas: [{ id: 'q1', model: '', max_requests: null, max_total_tokens: 2_000_000, window: '1d', current_requests: 0, current_total_tokens: 1_180_000 }] },
    { id: 'u-dave', email: 'dave@lab.example', name: 'dave', role: 'user', status: 'pending', provider: 'local', createdAt: iso(-1 * DAY), permissions: allOn, allowed_models: { enabled: false, models: [] } },
    { id: 'u-erin', email: 'erin@lab.example', name: '', role: 'user', status: 'disabled', provider: 'local', createdAt: iso(-120 * DAY), permissions: allOn, allowed_models: { enabled: false, models: [] } },
  ]
}

export function invitesFixture() {
  return [
    { id: 'i1', codePrefix: '3f9a1c2d', createdAt: iso(-1 * DAY), expiresAt: iso(6 * DAY), usedAt: null, usedBy: null, createdBy: { id: 'u-alice', name: 'alice' } },
    { id: 'i2', codePrefix: '77be0a14', createdAt: iso(-5 * DAY), expiresAt: iso(2 * DAY), usedAt: iso(-3 * DAY), usedBy: { id: 'u-dave', name: 'dave' }, createdBy: { id: 'u-alice', name: 'alice' } },
    { id: 'i3', codePrefix: '9c11d5e0', createdAt: iso(-9 * DAY), expiresAt: iso(-2 * DAY), usedAt: null, usedBy: null, createdBy: { id: 'u-alice', name: 'alice' } },
  ]
}

export function keysFixture() {
  return [
    { id: 'k1', name: 'ci-bot', keyPrefix: 'lai-3f9a1c', role: 'admin', createdAt: iso(-35 * DAY), lastUsed: iso(-2 * HOUR), disabled: false, expiresAt: iso(55 * DAY) },
    { id: 'k2', name: 'home-assistant', keyPrefix: 'lai-71bc09', role: 'admin', createdAt: iso(-270 * DAY), lastUsed: iso(-3 * DAY), disabled: true },
    { id: 'k3', name: 'notebook', keyPrefix: 'lai-a02d44', role: 'admin', createdAt: iso(-200 * DAY), lastUsed: null, disabled: false },
  ]
}

export const QUOTAS = [
  { id: 'q1', model: '', max_requests: null, max_total_tokens: 2_000_000, window: '1d', current_requests: 0, current_total_tokens: 1_180_000, resets_at: iso(5 * HOUR) },
  { id: 'q2', model: '', max_requests: 300, max_total_tokens: null, window: '1h', current_requests: 212, current_total_tokens: 0, resets_at: iso(HOUR / 2) },
  { id: 'q3', model: 'qwen3-8b-instruct', max_requests: null, max_total_tokens: 1_100_000, window: '1d', current_requests: 0, current_total_tokens: 1_030_000, resets_at: iso(5 * HOUR) },
]

export function myUsageFixture() {
  const usage = []
  const models = [['qwen3-8b-instruct', 181_000, 120], ['bge-m3', 44_000, 90], ['kokoro-82m', 31_000, 30], ['gemma-3-12b-it', 35_000, 35]]
  for (const [model, total, requests] of models) {
    usage.push({ bucket: '2026-10-07', model, prompt_tokens: Math.round(total * 0.74), completion_tokens: Math.round(total * 0.26), total_tokens: total, request_count: requests })
  }
  const totals = usage.reduce((t, b) => ({
    prompt_tokens: t.prompt_tokens + b.prompt_tokens, completion_tokens: t.completion_tokens + b.completion_tokens,
    total_tokens: t.total_tokens + b.total_tokens, request_count: t.request_count + b.request_count,
  }), { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, request_count: 0 })
  return { usage, totals }
}

// Answers the auth calls the pages make. `writes` collects every call that
// changes something, as { method, path, body }. Pass `status` to pick the
// auth status variant.
export async function mockAccess(page, { status = 'admin', statusExtra = {}, users = usersFixture(), invites = invitesFixture(), keys = keysFixture(), quotas = QUOTAS } = {}) {
  const state = { writes: [], users, invites, keys, quotas, failNext: null }
  const body = (route) => { try { return route.request().postDataJSON() } catch { return null } }
  const record = (route, path) => state.writes.push({ method: route.request().method(), path, body: body(route) })
  const json = (route, data, status = 200) => route.fulfill({ status, json: data })

  await page.route('**/api/auth/status', route => json(route, statusFixture(status, statusExtra)))
  await page.route('**/api/auth/me', route => json(route, { user: statusFixture(status).user }))
  await page.route('**/api/auth/quota', route => json(route, { quotas: state.quotas }))
  await page.route('**/api/auth/usage?*', route => json(route, myUsageFixture()))
  await page.route('**/api/auth/admin/features', route => json(route, FEATURES))
  await page.route('**/api/auth/admin/users', route => json(route, { users: state.users }))
  await page.route('**/api/auth/admin/users/*/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.method() === 'GET' && path.endsWith('/quotas')) return json(route, state.quotas.slice(0, 1))
    record(route, path)
    if (state.failNext) { const error = state.failNext; state.failNext = null; return json(route, { error }, 400) }
    return json(route, { message: 'ok' })
  })
  await page.route('**/api/auth/admin/users/*', async route => {
    const req = route.request()
    if (req.method() === 'GET') return route.fallback()
    record(route, new URL(req.url()).pathname)
    return json(route, { message: 'ok' })
  })
  await page.route('**/api/auth/admin/invites', async route => {
    if (route.request().method() === 'POST') {
      record(route, '/api/auth/admin/invites')
      return json(route, { id: 'i-new', code: 'c0ffee00c0ffee00c0ffee00c0ffee00', expiresAt: iso(7 * DAY), createdAt: iso(0) }, 201)
    }
    return json(route, { invites: state.invites })
  })
  await page.route('**/api/auth/admin/invites/*', async route => {
    record(route, new URL(route.request().url()).pathname)
    return json(route, { message: 'ok' })
  })
  await page.route('**/api/auth/api-keys', async route => {
    if (route.request().method() === 'POST') {
      record(route, '/api/auth/api-keys')
      return json(route, { key: 'lai-9d41f0c27be84a0c92aa5d6b', id: 'k-new', name: body(route)?.name || 'key', keyPrefix: 'lai-9d41f0', role: 'user', createdAt: iso(0) }, 201)
    }
    return json(route, { keys: state.keys })
  })
  await page.route('**/api/auth/api-keys/*', async route => {
    const req = route.request()
    record(route, new URL(req.url()).pathname)
    if (req.method() === 'DELETE') state.keys = state.keys.filter(k => !req.url().endsWith(`/${k.id}`))
    return json(route, { message: 'ok' })
  })
  await page.route('**/api/auth/profile', async route => { record(route, '/api/auth/profile'); return json(route, { message: 'ok' }) })
  await page.route('**/api/auth/password', async route => {
    record(route, '/api/auth/password')
    if (state.failNext) { const e = state.failNext; state.failNext = null; return json(route, e, 400) }
    return json(route, { message: 'ok' })
  })
  return state
}
