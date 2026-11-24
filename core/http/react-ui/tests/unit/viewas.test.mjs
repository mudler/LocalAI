// "View as": the state machine, what follows it, which requests get user_id,
// and the read-only write block. Pure data and a fake fetch, no browser.
import test from 'node:test'
import assert from 'node:assert/strict'

const as = await import('../../src/utils/viewAs.js')
const routes = await import('../../src/utils/viewAsRoutes.js')
const storeMod = await import('../../src/utils/viewAsStore.js')

const alice = { id: 'u-alice', name: 'alice' }
const step = (s, e) => as.viewAsStep(s, e)
const viewing = (now = 1000) => step(as.initialViewAs, { type: 'enter', target: alice, now })
const acting = (now = 1000) => step(viewing(now), { type: 'allow', now })

test('enter starts read-only on the person; a person without an id changes nothing', () => {
  const s = viewing()
  assert.deepEqual(s.target, alice)
  assert.equal(s.mode, as.READONLY)
  assert.equal(as.isActive(s), true)
  assert.equal(as.isActing(s), false)
  assert.equal(step(as.initialViewAs, { type: 'enter', target: { name: 'x' } }), as.initialViewAs)
})

test('a name is optional: the id stands in', () => {
  assert.equal(step(as.initialViewAs, { type: 'enter', target: { id: 'u-9' } }).target.name, 'u-9')
})

test('allow acts as the person, revoke goes back to read-only, neither works without a person', () => {
  assert.equal(acting().mode, as.ACTAS)
  assert.equal(as.isActing(acting()), true)
  assert.equal(step(acting(), { type: 'revoke' }).mode, as.READONLY)
  assert.equal(step(as.initialViewAs, { type: 'allow', now: 1 }), as.initialViewAs)
  assert.equal(step(as.initialViewAs, { type: 'revoke' }), as.initialViewAs)
})

test('exit clears everything', () => {
  assert.equal(step(acting(), { type: 'exit' }), as.initialViewAs)
})

test('a reload brings the person back, read-only, whatever the mode was', () => {
  const kept = as.persisted(acting())
  assert.equal(JSON.parse(kept).mode, undefined, 'the mode is not stored')
  const s = step(as.initialViewAs, { type: 'restore', target: as.restored(kept) })
  assert.deepEqual(s.target, alice)
  assert.equal(s.mode, as.READONLY)
  assert.equal(as.persisted(as.initialViewAs), '')
  assert.equal(as.restored('not json'), null)
  assert.equal(as.restored(''), null)
  assert.equal(as.restored('{"name":"no id"}'), null)
})

test('acting returns to read-only after 15 minutes without input, not before', () => {
  assert.equal(as.IDLE_MS, 15 * 60 * 1000)
  const s = acting(1000)
  assert.equal(as.idleDeadline(s), 1000 + as.IDLE_MS)
  assert.equal(step(s, { type: 'tick', now: 1000 + as.IDLE_MS - 1 }), s)
  const idle = step(s, { type: 'tick', now: 1000 + as.IDLE_MS })
  assert.equal(idle.mode, as.READONLY)
  assert.equal(idle.reverted, 'idle')
  assert.deepEqual(idle.target, alice)
  assert.equal(as.idleDeadline(idle), null)
  // Read-only never expires.
  assert.equal(step(viewing(), { type: 'tick', now: 1e12 }).mode, as.READONLY)
  assert.equal(step(viewing(), { type: 'tick', now: 1e12 }).reverted, null)
})

test('input while acting pushes the deadline back; input while read-only is ignored', () => {
  const s = acting(1000)
  const later = step(s, { type: 'input', now: 5000 })
  assert.equal(as.idleDeadline(later), 5000 + as.IDLE_MS)
  assert.equal(step(later, { type: 'tick', now: 1000 + as.IDLE_MS }).mode, as.ACTAS)
  const ro = viewing()
  assert.equal(step(ro, { type: 'input', now: 9 }), ro)
})

test('allowing again after an idle return clears the reason', () => {
  const idle = step(acting(0), { type: 'tick', now: as.IDLE_MS })
  assert.equal(step(idle, { type: 'allow', now: as.IDLE_MS + 1 }).reverted, null)
})

test('gone ends the mode and keeps the name for the message; clear drops the message', () => {
  const s = step(acting(), { type: 'gone' })
  assert.equal(s.target, null)
  assert.deepEqual(s.ended, { reason: 'gone', name: 'alice' })
  assert.equal(step(s, { type: 'clear' }).ended, null)
  assert.equal(step(as.initialViewAs, { type: 'gone' }), as.initialViewAs)
})

test('only a 403, or a 404 that says the user is gone, ends the mode', () => {
  assert.equal(as.endsTheMode(403), true)
  assert.equal(as.endsTheMode(404, as.USER_NOT_FOUND), true)
  assert.equal(as.endsTheMode(404, 'project not found'), false)
  assert.equal(as.endsTheMode(404), false)
  assert.equal(as.endsTheMode(500), false)
})

// --- which routes and pages follow -----------------------------------------

test('the project routes follow, the shared ones and everything else do not', () => {
  for (const p of ['/api/chats/projects', '/api/chats/projects/p1', '/api/chats/projects/p1/memory', '/api/chats/projects/p1/memory/notes.md',
    '/api/chats/projects/p1/chats', '/api/chats/projects/p1/agents', '/api/chats/projects/p1/connections', '/api/chats/projects/p1/triggers',
    '/api/chats/projects/p1/loops', '/api/chats/projects/p1/archive', '/api/chats/projects/p1/restore']) {
    assert.equal(routes.followsApi(p), true, p)
  }
  for (const p of ['/api/chats/projects/shared', '/api/chats/projects/shared/abc', '/api/chats/projects/shared/abc/memory',
    '/api/chats', '/api/chats/c1', '/api/chats/c1/project', '/api/collections', '/api/collections/docs/entries', '/api/agents',
    '/api/auth/admin/users', '/api/chats/projectsx', '/v1/models']) {
    assert.equal(routes.followsApi(p), false, p)
  }
})

test('the project pages follow; shared projects, Administration and the rest do not', () => {
  for (const p of ['/app/projects', '/app/projects/', '/app/projects/p1']) assert.equal(routes.followsPage(p), true, p)
  for (const p of ['/app', '/app/models', '/app/users', '/app/operate', '/app/projects/shared/abc', '/app/projects/p1/extra', '/app/collections', '/app/chats']) {
    assert.equal(routes.followsPage(p), false, p)
  }
  assert.equal(routes.followsNav('projects'), true)
  for (const id of ['files', 'agents', 'memory', 'models', 'home', undefined]) assert.equal(routes.followsNav(id), false, String(id))
})

// --- which requests get user_id, and the read-only block --------------------

test('decideRequest: untouched when off or off the list', () => {
  assert.deepEqual(as.decideRequest(as.initialViewAs, 'POST', '/api/chats/projects'), { follow: false, blocked: false })
  assert.deepEqual(as.decideRequest(viewing(), 'POST', '/api/chats/c1/project'), { follow: false, blocked: false })
  assert.deepEqual(as.decideRequest(viewing(), 'GET', '/api/chats/projects/shared'), { follow: false, blocked: false })
})

test('decideRequest: read-only reads with user_id and blocks every write', () => {
  assert.deepEqual(as.decideRequest(viewing(), 'GET', '/api/chats/projects'), { follow: true, blocked: false })
  assert.deepEqual(as.decideRequest(viewing(), 'get', '/api/chats/projects/p1'), { follow: true, blocked: false })
  assert.deepEqual(as.decideRequest(viewing(), 'HEAD', '/api/chats/projects/p1'), { follow: true, blocked: false })
  for (const m of ['POST', 'PUT', 'PATCH', 'DELETE', 'post']) {
    assert.deepEqual(as.decideRequest(viewing(), m, '/api/chats/projects/p1/archive'), { follow: true, blocked: true }, m)
  }
})

test('decideRequest: acting sends writes, with user_id', () => {
  assert.deepEqual(as.decideRequest(acting(), 'POST', '/api/chats/projects'), { follow: true, blocked: false })
  assert.deepEqual(as.decideRequest(acting(), 'PUT', '/api/chats/projects/p1/memory/a.md'), { follow: true, blocked: false })
})

test('decideRequest: a request that names a user keeps it, but read-only still blocks its writes', () => {
  assert.deepEqual(as.decideRequest(viewing(), 'GET', '/api/chats/projects', '?user_id=u-other'), { follow: false, blocked: false })
  assert.equal(as.decideRequest(viewing(), 'POST', '/api/chats/projects', '?user_id=u-other').blocked, true)
})

// --- the fetch wrapper ------------------------------------------------------

function harness({ respond = () => ({ ok: true, status: 200 }), base = '' } = {}) {
  const sent = []
  const win = {
    location: { origin: 'http://host' },
    fetch: async (input, init) => { sent.push({ url: input instanceof Request ? input.url : String(input), init }); return respond(input, init) },
  }
  const store = storeMod.createViewAsStore({ storage: null })
  storeMod.installViewAsFetch(store, { win, base })
  return { win, store, sent }
}

test('fetch: off means untouched', async () => {
  const { win, sent } = harness()
  await win.fetch('/api/chats/projects', { method: 'POST' })
  assert.deepEqual(sent.map(s => s.url), ['/api/chats/projects'])
})

test('fetch: read-only adds user_id to a following read and leaves the rest alone', async () => {
  const { win, store, sent } = harness()
  store.enter(alice)
  await win.fetch('/api/chats/projects?x=1')
  await win.fetch('/api/chats/projects/p1/memory')
  await win.fetch('/api/chats/c1')
  await win.fetch('/api/chats/projects/shared')
  assert.deepEqual(sent.map(s => s.url), [
    '/api/chats/projects?x=1&user_id=u-alice',
    '/api/chats/projects/p1/memory?user_id=u-alice',
    '/api/chats/c1',
    '/api/chats/projects/shared',
  ])
})

test('fetch: read-only blocks a write before it leaves, says why, and tells the shell', async () => {
  const { win, store, sent } = harness()
  store.enter(alice)
  const blocked = []
  store.onBlocked(b => blocked.push(b))
  await assert.rejects(() => win.fetch('/api/chats/projects', { method: 'POST', body: '{}' }), (e) => e.viewAsBlocked === true && /alice/.test(e.message))
  await assert.rejects(() => win.fetch(new Request('http://host/api/chats/projects/p1', { method: 'DELETE' })), /alice/)
  assert.equal(sent.length, 0)
  assert.deepEqual(blocked.map(b => [b.method, b.path]), [['POST', '/api/chats/projects'], ['DELETE', '/api/chats/projects/p1']])
  // A write to a route that does not follow is not this guard's business.
  await win.fetch('/api/chats/c1/project', { method: 'PUT' })
  assert.equal(sent.length, 1)
})

test('fetch: acting lets the write through, as the person', async () => {
  const { win, store, sent } = harness()
  store.enter(alice)
  store.allow()
  await win.fetch('/api/chats/projects', { method: 'POST', body: '{}' })
  await win.fetch(new Request('http://host/api/chats/projects/p1', { method: 'PATCH', body: '{}' }))
  assert.equal(sent[0].url, '/api/chats/projects?user_id=u-alice')
  assert.equal(sent[1].url, 'http://host/api/chats/projects/p1?user_id=u-alice')
})

test('fetch: a base path is stripped to match and kept in the request', async () => {
  const { win, store, sent } = harness({ base: '/ui' })
  store.enter(alice)
  await win.fetch('/ui/api/chats/projects')
  assert.equal(sent[0].url, '/ui/api/chats/projects?user_id=u-alice')
})

test('fetch: another origin is never touched', async () => {
  const { win, store, sent } = harness()
  store.enter(alice)
  await win.fetch('http://elsewhere/api/chats/projects', { method: 'POST' })
  assert.equal(sent[0].url, 'http://elsewhere/api/chats/projects')
})

test('fetch: 403 ends the mode, a 404 only when the user is gone', async () => {
  const answers = [
    { ok: false, status: 404, clone() { return { json: async () => ({ error: { message: 'project not found' } }) } } },
    { ok: false, status: 404, clone() { return { json: async () => ({ error: { message: as.USER_NOT_FOUND } }) } } },
  ]
  const { win, store } = harness({ respond: () => answers.shift() })
  store.enter(alice)
  await win.fetch('/api/chats/projects/p9')
  assert.equal(store.getState().target?.id, 'u-alice', 'a missing project keeps the mode')
  await win.fetch('/api/chats/projects')
  assert.equal(store.getState().target, null)
  assert.equal(store.getState().ended.name, 'alice')

  const forbidden = harness({ respond: () => ({ ok: false, status: 403 }) })
  forbidden.store.enter(alice)
  await forbidden.win.fetch('/api/chats/projects')
  assert.equal(forbidden.store.getState().target, null)
})

// --- the store: storage and the idle timer ----------------------------------

const memory = () => { const m = new Map(); return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k), m } }

test('store: the person is kept per tab and comes back read-only after a reload', () => {
  const storage = memory()
  const a = storeMod.createViewAsStore({ storage })
  a.enter(alice)
  a.allow()
  assert.equal(JSON.parse(storage.getItem(storeMod.STORAGE_KEY)).id, 'u-alice')
  const reloaded = storeMod.createViewAsStore({ storage })
  assert.equal(reloaded.getState().target.id, 'u-alice')
  assert.equal(reloaded.getState().mode, as.READONLY)
  reloaded.exit()
  assert.equal(storage.getItem(storeMod.STORAGE_KEY), null)
  assert.equal(storeMod.createViewAsStore({ storage }).getState().target, null)
})

test('store: blocked storage is not an error', () => {
  const broken = { getItem() { throw new Error('blocked') }, setItem() { throw new Error('blocked') }, removeItem() { throw new Error('blocked') } }
  const s = storeMod.createViewAsStore({ storage: broken })
  s.enter(alice)
  assert.equal(s.getState().target.id, 'u-alice')
})

test('store: the idle timer returns acting to read-only at the deadline, and input moves it', () => {
  let clock = 1000
  const timers = []
  const s = storeMod.createViewAsStore({
    storage: null, now: () => clock,
    setTimer: (fn, ms) => { const t = { fn, ms }; timers.push(t); return t },
    clearTimer: (t) => { t.cleared = true },
  })
  s.enter(alice)
  assert.equal(timers.length, 0, 'read-only has no timer')
  s.allow()
  assert.equal(timers.at(-1).ms, as.IDLE_MS)
  clock += 60_000
  s.input()
  assert.equal(timers.at(-2).cleared, true)
  assert.equal(timers.at(-1).ms, as.IDLE_MS)
  clock += as.IDLE_MS
  timers.at(-1).fn()
  assert.equal(s.getState().mode, as.READONLY)
  assert.equal(s.getState().reverted, 'idle')
})

test('store: subscribers hear a change once, and not a no-op', () => {
  const s = storeMod.createViewAsStore({ storage: null })
  let n = 0
  const off = s.subscribe(() => { n += 1 })
  s.enter(alice)
  s.revoke()
  s.revoke()
  assert.equal(n, 1)
  off()
  s.exit()
  assert.equal(n, 1)
})
