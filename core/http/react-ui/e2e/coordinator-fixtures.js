// Stubbed API of the coordinators: the owner's routes, the administrator's
// routes and the audit log with its filters and paging. Every call is recorded
// in `s.calls`. The agents API comes from agents-fixtures.js.
import { installAgents } from './agents-fixtures.js'

const iso = (minutesAgo) => new Date(Date.now() - minutesAgo * 60_000).toISOString()

export const COORDINATOR = {
  id: 'k1', name: 'daily-ops', description: 'Watches the queue.', model: 'qwen3-8b', instructions: 'Keep the queue short.',
  mcp_servers: [], addons: [], collections: [], skills: [], network: 'off', builtins: ['act.files'], coordinator: true,
  chat_id: 'cc1', page: 'daily-ops',
  contacts: [
    { id: 'sam', name: 'Sam', kind: 'user', user: 'u-sam' },
    { id: 'ops-team', name: 'Ops team', description: 'The on-call group', kind: 'external', binding: 'b1', chat: 'C42' },
  ],
  wake_every: 3600, wake_note: 'Check the queue first.', send_per_minute: 10, send_per_day: 100, max_helpers: 8, max_depth: 2,
  paused: false, next_wake: new Date(Date.now() + 40 * 60_000).toISOString(), last_wake: iso(20),
  usage: { runs_today: 3, sent_today: 5, runs_limit: 48, send_limit: 100 },
}

export const BINDINGS = [
  { id: 'b1', name: 'Team chat', kind: 'chatapp', addon: 'chatapp', enabled: true, conversation: 'chat', allow: ['C42', 'C77'], credentials_only: false },
  { id: 'b2', name: 'Threads', kind: 'chatapp', addon: 'chatapp', enabled: true, conversation: 'thread', allow: [], credentials_only: false },
]

const ENTRY = (id, extra = {}) => ({
  id, at: iso(id), coordinator_id: 'k1', owner: 'alice', kind: 'contact', outcome: 'sent', reason: 'ok', actor: 'coordinator', target: 'sam', via: 'inbox', length: 120, hash: 'abc', ...extra,
})

// Newest first. 60 rows so a page of 25 has two older pages.
export function auditRows(n = 60) {
  return Array.from({ length: n }, (_, i) => {
    const id = n - i
    if (id % 5 === 0) return ENTRY(id, { outcome: 'denied', reason: 'not_a_contact', target: 'stranger', via: '', length: 0 })
    if (id % 7 === 0) return ENTRY(id, { kind: 'wake', outcome: 'info', reason: 'schedule', target: '', via: '', length: 0, actor: 'schedule' })
    return ENTRY(id)
  })
}

// The audit routes answer with the server's rules: filters, newest first,
// `before` is the id of the last row of the previous page.
function pageOf(rows, search) {
  const p = new URL(`http://x/${search}`).searchParams
  let list = rows
  for (const k of ['kind', 'outcome', 'coordinator_id']) if (p.get(k === 'coordinator_id' ? 'coordinator' : k)) list = list.filter(r => r[k] === p.get(k === 'coordinator_id' ? 'coordinator' : k))
  if (p.get('since')) list = list.filter(r => Date.parse(r.at) >= Date.parse(p.get('since')))
  if (p.get('until')) list = list.filter(r => Date.parse(r.at) < Date.parse(p.get('until')))
  if (p.get('before')) list = list.filter(r => r.id < Number(p.get('before')))
  const limit = Number(p.get('limit') || 50)
  const entries = list.slice(0, limit)
  return { entries, ...(entries.length === limit && list.length > limit ? { next_before: entries[entries.length - 1].id } : {}) }
}

export async function installCoordinators(page, {
  coordinators = [COORDINATOR], bindings = BINDINGS, audit = auditRows(), admin = null, agents,
  createError = null, auditError = false,
} = {}) {
  const base = agents || [
    ...coordinators.map(c => ({ ...c })),
    { id: 'a2', name: 'code-reviewer', description: 'Reviews a change.', model: '', instructions: 'Review.', mcp_servers: [], addons: [], collections: [], skills: [], network: 'off', coordinator: false },
  ]
  const s = await installAgents(page, { agents: base, bindings })
  s.coordinators = structuredClone(coordinators)
  s.admin = admin && structuredClone(admin)
  s.audit = audit
  const rec = (req) => { const u = new URL(req.url()); s.calls.push({ method: req.method(), url: u.pathname, search: u.search, body: req.postData() ? req.postDataJSON() : null }) }
  const err = (route, status, message, type = 'invalid_request_error') => route.fulfill({ status, json: { error: { message, type, code: status } } })
  const find = (id) => s.coordinators.find(c => c.id === id)

  await page.route(/\/api\/chats\/coordinators(\?.*)?$/, (route) => {
    const req = route.request()
    rec(req)
    if (req.method() === 'POST') {
      if (createError) return err(route, 400, createError)
      const c = { ...COORDINATOR, ...req.postDataJSON(), id: `k${s.coordinators.length + 1}`, coordinator: true, chat_id: 'cc9', paused: false, contacts: req.postDataJSON().contacts || [], usage: COORDINATOR.usage }
      s.coordinators.push(c)
      return route.fulfill({ status: 201, json: c })
    }
    return route.fulfill({ json: { coordinators: s.coordinators } })
  })
  await page.route(/\/api\/chats\/coordinators\/[^/?]+$/, (route) => {
    const req = route.request()
    rec(req)
    const c = find(new URL(req.url()).pathname.split('/').pop())
    if (!c) return err(route, 404, 'no such coordinator', 'not_found')
    if (req.method() === 'PUT') {
      if (createError) return err(route, 400, createError)
      Object.assign(c, req.postDataJSON())
    }
    return route.fulfill({ json: c })
  })
  await page.route(/\/api\/chats\/coordinators\/[^/?]+\/(pause|resume)$/, (route) => {
    const req = route.request()
    rec(req)
    const parts = new URL(req.url()).pathname.split('/')
    const verb = parts.pop()
    const c = find(parts.pop())
    c.paused = verb === 'pause'
    if (c.paused) { c.paused_by = 'alice'; c.next_wake = undefined } else { c.paused_by = undefined; c.next_wake = new Date(Date.now() + 30 * 60_000).toISOString() }
    return route.fulfill({ json: { id: c.id, name: c.name, paused: c.paused } })
  })
  await page.route(/\/api\/chats\/coordinators\/[^/?]+\/audit(\?.*)?$/, (route) => {
    const req = route.request()
    rec(req)
    if (auditError) return err(route, 500, 'the audit store is down', 'internal_error')
    return route.fulfill({ json: pageOf(s.audit.filter(r => r.coordinator_id === new URL(req.url()).pathname.split('/')[4]), new URL(req.url()).search) })
  })

  // The administrator's routes.
  if (admin) {
    await page.route(/\/api\/chats\/admin\/coordinators(\?.*)?$/, (route) => {
      rec(route.request())
      if (admin.status) return err(route, admin.status, 'coordinators are off', admin.status === 404 ? 'coordinators_off' : 'internal_error')
      return route.fulfill({ json: { coordinators: s.admin.list } })
    })
    await page.route(/\/api\/chats\/admin\/coordinators\/[^/?]+\/(pause|resume)$/, (route) => {
      const req = route.request()
      rec(req)
      const parts = new URL(req.url()).pathname.split('/')
      const verb = parts.pop()
      const id = parts.pop()
      const c = s.admin.list.find(x => x.id === id)
      c.paused = verb === 'pause'
      c.paused_by = c.paused ? 'root' : undefined
      return route.fulfill({ json: c })
    })
    await page.route(/\/api\/chats\/admin\/coordinators\/audit(\?.*)?$/, (route) => {
      rec(route.request())
      return route.fulfill({ json: pageOf(s.audit, new URL(route.request().url()).search) })
    })
  }
  return s
}

export const ADMIN_LIST = [
  { id: 'k1', owner: 'alice', name: 'daily-ops', description: '', model: 'qwen3-8b', chat_id: 'cc1', contacts: 2, wake_every: 3600, paused: false, next_wake: new Date(Date.now() + 40 * 60_000).toISOString(), last_wake: iso(20), usage: { runs_today: 3, sent_today: 5, runs_limit: 48, send_limit: 100 } },
  { id: 'k2', owner: 'bob', name: 'release-watch', description: '', model: 'qwen3-8b', chat_id: 'cc2', contacts: 1, wake_every: 0, paused: true, paused_by: 'alice', paused_at: iso(30), usage: { runs_today: 0, sent_today: 0, runs_limit: 48, send_limit: 100 } },
  { id: 'k3', owner: 'carol', name: 'idle-one', description: '', model: '', chat_id: 'cc3', contacts: 0, wake_every: 0, paused: false, usage: { runs_today: 1, sent_today: 0, runs_limit: 48, send_limit: 100 } },
]
