import assert from 'node:assert/strict'
import test from 'node:test'

import {
  accessSummary, filterCounts, filterUsers, initialOf, inviteState, keyState, timeUntil, userState,
} from './access.js'

const meta = {
  api_features: [{ key: 'chat', default: true }, { key: 'images', default: true }],
  agent_features: [{ key: 'agents', default: false }, { key: 'skills', default: false }],
  general_features: [{ key: 'fine_tuning', default: false }],
}
const users = [
  { id: 1, name: 'alice', email: 'alice@lab.example', role: 'admin', status: 'active' },
  { id: 2, name: 'bob', email: 'bob@lab.example', role: 'user', status: 'active' },
  { id: 3, name: 'dave', email: 'dave@lab.example', role: 'user', status: 'pending' },
  { id: 4, name: '', email: 'erin@lab.example', role: 'user', status: 'disabled' },
]

test('a user waiting for approval is neither active nor disabled', () => {
  assert.equal(userState(users[2]), 'pending')
  assert.equal(userState({ status: '' }), 'pending')
  assert.equal(userState(users[3]), 'disabled')
})

test('filters combine with the search', () => {
  assert.deepEqual(filterUsers(users, { filter: 'pending' }).map(u => u.id), [3])
  assert.deepEqual(filterUsers(users, { filter: 'admins' }).map(u => u.id), [1])
  assert.deepEqual(filterUsers(users, { filter: 'disabled' }).map(u => u.id), [4])
  assert.deepEqual(filterUsers(users, { query: 'ERIN' }).map(u => u.id), [4])
  assert.deepEqual(filterUsers(users, { query: 'lab', filter: 'pending' }).map(u => u.id), [3])
  assert.deepEqual(filterCounts(users), { all: 4, pending: 1, admins: 1, disabled: 1 })
})

test('access summary says what differs from the defaults', () => {
  assert.equal(accessSummary(users[0], meta), 'All access')
  assert.equal(accessSummary({ role: 'user', permissions: { chat: true, images: true, agents: false } }, meta), 'Default access')
  assert.equal(accessSummary({ role: 'user', permissions: { chat: true, images: true, agents: true, skills: true } }, meta), '2 more features')
  assert.equal(accessSummary({ role: 'user', permissions: { chat: false, images: true } }, meta), '1 fewer feature')
  assert.equal(
    accessSummary({ role: 'user', permissions: {}, allowed_models: { enabled: true, models: ['a', 'b'] }, quotas: [{ id: 'q' }] }, meta),
    '2 models only · 1 limit',
  )
})

test('initial of a user falls back to the email', () => {
  assert.equal(initialOf(users[3]), 'E')
  assert.equal(initialOf(users[0]), 'A')
})

test('an invite is open, used or expired', () => {
  const now = Date.parse('2026-10-08T10:00:00Z')
  assert.equal(inviteState({ expiresAt: '2026-10-09T10:00:00Z' }, now), 'open')
  assert.equal(inviteState({ expiresAt: '2026-10-07T10:00:00Z' }, now), 'expired')
  assert.equal(inviteState({ expiresAt: '2026-10-07T10:00:00Z', usedBy: { id: 'x' } }, now), 'used')
})

test('a key is active, paused, paused until a time, or expired', () => {
  const now = Date.parse('2026-10-08T10:00:00Z')
  assert.equal(keyState({}, now), 'active')
  assert.equal(keyState({ disabled: true }, now), 'paused')
  assert.equal(keyState({ pausedUntil: '2026-10-08T12:00:00Z' }, now), 'paused-until')
  assert.equal(keyState({ pausedUntil: '2026-10-08T09:00:00Z' }, now), 'active')
  assert.equal(keyState({ expiresAt: '2026-10-01T00:00:00Z' }, now), 'expired')
})

test('timeUntil reads both directions', () => {
  const now = Date.parse('2026-10-08T10:00:00Z')
  assert.equal(timeUntil('2026-10-14T10:00:00Z', now), 'in 6 days')
  assert.equal(timeUntil('2026-10-06T10:00:00Z', now), '2 days ago')
  assert.equal(timeUntil('2026-10-08T10:30:00Z', now), 'in 30 minutes')
  assert.equal(timeUntil('2026-10-08T13:00:00Z', now), 'in 3 hours')
})
