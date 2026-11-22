import assert from 'node:assert/strict'
import test from 'node:test'

import {
  accessInfo, accessSummary, filterCounts, filterUsers, initialOf, inviteState, isInactive, keyState, timeUntil, userState,
} from './access.js'

const meta = {
  api_features: [{ key: 'chat', default: true }, { key: 'images', default: true }],
  agent_features: [{ key: 'collections', default: false }, { key: 'localai_assistant', default: false }],
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
  assert.deepEqual(filterCounts(users), { inactive: 0, all: 4, pending: 1, admins: 1, disabled: 1 })
})

test('access summary says what differs from the defaults', () => {
  assert.equal(accessSummary(users[0], meta), 'All access')
  assert.equal(accessSummary({ role: 'user', permissions: { chat: true, images: true, collections: false } }, meta), 'Default access')
  assert.equal(accessSummary({ role: 'user', permissions: { chat: true, images: true, collections: true, localai_assistant: true } }, meta), '2 more features')
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

const DAY = 86_400_000
const NOW = new Date('2026-11-22T12:00:00Z').getTime()
const ago = (days) => new Date(NOW - days * DAY).toISOString()

test('a person is inactive after 30 days without activity, or never seen on an old account', () => {
  assert.equal(isInactive({ lastActiveAt: ago(31) }, NOW), true)
  assert.equal(isInactive({ lastActiveAt: ago(29) }, NOW), false)
  assert.equal(isInactive({ lastActiveAt: null, createdAt: ago(90) }, NOW), true)
  // A new sign-up has not had the time to be active.
  assert.equal(isInactive({ lastActiveAt: null, createdAt: ago(1) }, NOW), false)
  assert.equal(isInactive({}, NOW), false)
  // The last activity wins over the age of the account.
  assert.equal(isInactive({ lastActiveAt: ago(2), createdAt: ago(400) }, NOW), false)
})

test('the inactive filter and its count', () => {
  const list = [
    { id: 1, role: 'admin', status: 'active', lastActiveAt: ago(0) },
    { id: 2, role: 'user', status: 'active', lastActiveAt: ago(45) },
    { id: 3, role: 'user', status: 'pending', lastActiveAt: null, createdAt: ago(2) },
    { id: 4, role: 'user', status: 'active', lastActiveAt: null, createdAt: ago(60) },
  ]
  assert.deepEqual(filterUsers(list, { filter: 'inactive', now: NOW }).map(u => u.id), [2, 4])
  assert.equal(filterCounts(list, NOW).inactive, 2)
})

test('the Filters popover narrows by sign-in method and by custom access', () => {
  const list = [
    { id: 1, role: 'admin', provider: 'local', status: 'active' },
    { id: 2, role: 'user', provider: 'github', status: 'active', permissions: { chat: true, images: true } },
    { id: 3, role: 'user', provider: 'local', status: 'active', permissions: { chat: true, images: true, collections: true } },
    { id: 4, role: 'user', status: 'active', allowed_models: { enabled: true, models: ['a'] } },
  ]
  assert.deepEqual(filterUsers(list, { provider: 'github' }).map(u => u.id), [2])
  // A user with no provider is a local account.
  assert.deepEqual(filterUsers(list, { provider: 'local' }).map(u => u.id), [1, 3, 4])
  assert.deepEqual(filterUsers(list, { access: 'custom', featureMeta: meta }).map(u => u.id), [3, 4])
})

test('accessInfo gives the numbers and says when everything is at the defaults', () => {
  assert.deepEqual(accessInfo(users[0], meta), { all: true, models: null, more: 0, fewer: 0, limits: 0, isDefault: false })
  assert.equal(accessInfo({ role: 'user', permissions: { chat: true, images: true } }, meta).isDefault, true)
  assert.deepEqual(
    accessInfo({ role: 'user', permissions: {}, allowed_models: { enabled: true, models: ['a', 'b'] }, quotas: [{ id: 'q' }] }, meta),
    { all: false, models: 2, more: 0, fewer: 0, limits: 1, isDefault: false },
  )
})
