import test from 'node:test'
import assert from 'node:assert/strict'
import {
  coordinatorForm, coordinatorFromApi, coordinatorPayload, contactError, contactsError, emptyContact,
  wakeOptions, WAKE_STEPS, CAPS, auditParams, auditFilterActive, coordinatorState, fieldOfError, STARTER_INSTRUCTIONS,
} from '../../src/utils/coordinator.js'

const base = () => ({ ...coordinatorForm(), name: ' ops ', instructions: 'Watch the queue.' })

test('coordinator: a new form has no contacts, no schedule and the starter text', () => {
  const f = coordinatorForm()
  assert.deepEqual(f.contacts, [])
  assert.equal(f.wake_every, 0)
  assert.equal(f.instructions, STARTER_INSTRUCTIONS)
  assert.ok(STARTER_INSTRUCTIONS.length > 50)
})

test('coordinator: the payload names fields as the API does and omits empty optional ones', () => {
  const body = coordinatorPayload(base())
  assert.equal(body.name, 'ops')
  assert.equal(body.instructions, 'Watch the queue.')
  for (const k of ['page', 'wake_every', 'wake_note', 'send_per_minute', 'send_per_day', 'max_helpers', 'max_depth', 'contacts']) {
    assert.equal(k in body, false, `${k} should be left out`)
  }
  // Never sends what a coordinator write does not take.
  for (const k of ['folders', 'connections', 'resources']) assert.equal(k in body, false)
})

test('coordinator: the payload sends contacts, caps, page and schedule as integers', () => {
  const body = coordinatorPayload({
    ...base(), page: ' daily-ops ', wake_every: '3600', wake_note: ' check mail ', send_per_minute: '5', send_per_day: ' 40 ', max_helpers: '4', max_depth: '1',
    contacts: [
      { id: 'sam', name: 'Sam', description: '', kind: 'user', user: ' u1 ', binding: 'stale', chat: 'stale', sender: 'stale' },
      { id: 'ops-team', name: '', description: 'The team', kind: 'external', user: 'stale', binding: 'b1', chat: 'C42', sender: '' },
    ],
  })
  assert.equal(body.page, 'daily-ops')
  assert.equal(body.wake_every, 3600)
  assert.equal(body.wake_note, 'check mail')
  assert.deepEqual([body.send_per_minute, body.send_per_day, body.max_helpers, body.max_depth], [5, 40, 4, 1])
  assert.deepEqual(body.contacts, [
    { id: 'sam', name: 'Sam', kind: 'user', user: 'u1' },
    { id: 'ops-team', description: 'The team', kind: 'external', binding: 'b1', chat: 'C42' },
  ])
})

test('coordinator: a form read from the API keeps its values and round-trips', () => {
  const api = {
    id: 'a9', name: 'ops', description: 'd', instructions: 'i', model: 'm', mcp_servers: [], addons: [], collections: [], skills: [], network: 'off',
    builtins: ['act.files'], coordinator: true, chat_id: 'c1', page: 'ops', wake_every: 3600, wake_note: 'n',
    send_per_minute: 10, send_per_day: 100, max_helpers: 8, max_depth: 2, paused: false,
    contacts: [{ id: 'sam', name: 'Sam', kind: 'user', user: 'u1' }],
  }
  const f = coordinatorFromApi(api)
  assert.equal(f.send_per_minute, '10')
  assert.equal(f.wake_every, 3600)
  const body = coordinatorPayload(f)
  assert.equal(body.page, 'ops')
  assert.equal(body.wake_every, 3600)
  assert.deepEqual(body.contacts, [{ id: 'sam', name: 'Sam', kind: 'user', user: 'u1' }])
  assert.deepEqual(body.builtins, ['act.files'])
})

test('coordinator: contactError refuses an id that is not a slug, a missing user, and a missing binding or chat', () => {
  const ok = { ...emptyContact('user'), id: 'sam', user: 'u1' }
  assert.equal(contactError(ok), '')
  for (const id of ['', 'Sam', 'has space', '-lead', 'a'.repeat(41), 'é']) assert.equal(contactError({ ...ok, id }), 'id', id)
  assert.equal(contactError({ ...ok, user: '  ' }), 'user')
  const ext = { ...emptyContact('external'), id: 'team' }
  assert.equal(contactError(ext), 'binding')
  assert.equal(contactError({ ...ext, binding: 'b1' }), 'chat')
  assert.equal(contactError({ ...ext, binding: 'b1', chat: 'C1' }), '')
  assert.equal(contactError({ ...ok, name: 'x'.repeat(81) }), 'name')
  assert.equal(contactError({ ...ok, description: 'x'.repeat(201) }), 'description')
  assert.equal(contactError({ ...ok, kind: 'fax' }), 'kind')
})

test('coordinator: contactsError finds a repeated id and too many contacts', () => {
  const a = { ...emptyContact('user'), id: 'sam', user: 'u1' }
  assert.deepEqual(contactsError([a, { ...a, user: 'u2' }]), { index: 1, code: 'idTwice' })
  assert.equal(contactsError([a, { ...a, id: 'kim' }]), null)
  assert.deepEqual(contactsError([{ ...a, user: '' }]), { index: 0, code: 'user' })
  const many = Array.from({ length: 51 }, (_, i) => ({ ...a, id: `c${i}` }))
  assert.deepEqual(contactsError(many), { index: -1, code: 'tooMany' })
})

test('coordinator: wake options start at "events only" and none is below 5 minutes', () => {
  const opts = wakeOptions(0)
  assert.equal(opts[0].value, 0)
  assert.equal(opts[0].key, 'events')
  for (const o of opts.slice(1)) assert.ok(o.value >= 300 && o.value <= 86400)
  assert.deepEqual(WAKE_STEPS, [0, 900, 3600, 21600, 86400])
  // A saved value that is not a step stays selectable.
  const odd = wakeOptions(600)
  assert.ok(odd.some(o => o.value === 600 && o.custom))
  assert.equal(wakeOptions(3600).filter(o => o.value === 3600).length, 1)
})

test('coordinator: caps say what the server accepts', () => {
  assert.deepEqual(CAPS.send_per_minute, { def: 10, max: 60 })
  assert.deepEqual(CAPS.send_per_day, { def: 100, max: 5000 })
  assert.deepEqual(CAPS.max_helpers, { def: 8, max: 64 })
  assert.deepEqual(CAPS.max_depth, { def: 2, max: 4 })
})

test('coordinator: audit filters become query parameters, empty ones are dropped', () => {
  assert.deepEqual(auditParams({}), {})
  assert.deepEqual(auditParams({ kind: 'contact', outcome: 'denied', coordinator: 'a1' }), { kind: 'contact', outcome: 'denied', coordinator: 'a1' })
  assert.deepEqual(auditParams({ outcome: 'all', kind: '' }), {})
  const p = auditParams({ since: '2026-11-01', until: '2026-11-03', limit: 25, before: 77 })
  assert.equal(p.limit, 25)
  assert.equal(p.before, 77)
  assert.match(p.since, /^2026-1[01]-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/)
  // `until` is the end of that day, so a one-day filter holds that day.
  assert.ok(new Date(p.until) > new Date('2026-11-03T00:00:00'))
  assert.deepEqual(auditParams({ since: 'garbage' }), {})
  assert.equal(auditFilterActive({ kind: '', outcome: '' }), false)
  assert.equal(auditFilterActive({ outcome: 'denied' }), true)
})

test('coordinator: state is paused, scheduled or idle', () => {
  assert.equal(coordinatorState({ paused: true }), 'paused')
  assert.equal(coordinatorState({ paused: false, next_wake: '2026-11-09T00:00:00Z' }), 'scheduled')
  assert.equal(coordinatorState({}), 'idle')
})

test('coordinator: a 400 reason is tied to the field it names', () => {
  assert.equal(fieldOfError('contact id "Bad": use lowercase'), 'contacts')
  assert.equal(fieldOfError('coordinator: invalid: wake interval: use 0 or 5m0s to 24h0m0s'), 'wake')
  assert.equal(fieldOfError('send per minute: use 0 (default) or 1 to 60'), 'caps')
  assert.equal(fieldOfError('helpers: the highest allowed here is 16'), 'caps')
  assert.equal(fieldOfError('page "X": use a lowercase name'), 'page')
  assert.equal(fieldOfError('something else'), '')
})
