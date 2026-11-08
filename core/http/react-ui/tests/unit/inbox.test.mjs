import test from 'node:test'
import assert from 'node:assert/strict'
import {
  badgeText, sourceOf, toneOf, iconOf, targetOf, reasonText, dayOf, groupByDay, filterItems,
  hasUnreadFailure, entryVisible, inboxOff, streamWanted,
} from '../../src/utils/inbox.js'

const note = (extra = {}) => ({ id: 1, kind: 'run_ended', status: 'ok', title: 'T', body: '', link: '', read: false, created: '', ...extra })

test('inbox: the badge shows the unread count and stops at 99+', () => {
  assert.equal(badgeText(0), '')
  assert.equal(badgeText(undefined), '')
  assert.equal(badgeText(1), '1')
  assert.equal(badgeText(99), '99')
  assert.equal(badgeText(100), '99+')
})

test('inbox: the source comes from the server, else from the kind', () => {
  assert.equal(sourceOf(note({ source: 'connector' })), 'connector')
  assert.equal(sourceOf(note({ source: 'system' })), 'system')
  // Items stored before sources existed.
  assert.equal(sourceOf(note({ kind: 'run_failed' })), 'trigger')
  assert.equal(sourceOf(note({ kind: 'needs_attention' })), 'system')
  assert.equal(sourceOf(note({ source: 'other', kind: 'run_ended' })), 'trigger')
})

test('inbox: a failure is an error tile, a finished run ok, news accent', () => {
  assert.equal(toneOf(note({ status: 'failed', kind: 'run_failed' })), 'error')
  assert.equal(toneOf(note({ status: 'failed', kind: 'needs_attention' })), 'error')
  assert.equal(toneOf(note()), 'ok')
  assert.equal(toneOf(note({ kind: 'needs_attention' })), 'accent')
  assert.equal(iconOf(note({ source: 'trigger' })), 'bolt')
  assert.equal(iconOf(note({ source: 'connector', status: 'failed' })), 'plug-off')
  assert.equal(iconOf(note({ source: 'system', kind: 'needs_attention' })), 'info')
})

test('inbox: only paths in the app are opened; a connector without one opens its page', () => {
  assert.equal(targetOf(note({ link: '/app/chats/c1' })), '/app/chats/c1')
  assert.equal(targetOf(note({ link: 'https://example.com/x' })), '')
  assert.equal(targetOf(note({ link: '//example.com' })), '')
  assert.equal(targetOf(note({ source: 'connector', kind: 'needs_attention' })), '/app/chats/connectors')
  assert.equal(targetOf(note({ source: 'trigger', kind: 'needs_attention' })), '')
})

test('inbox: a reason starts with a capital', () => {
  assert.equal(reasonText('took longer than 15m'), 'Took longer than 15m')
  assert.equal(reasonText('  '), '')
  assert.equal(reasonText(undefined), '')
})

test('inbox: items group into today, yesterday and earlier in order', () => {
  const now = new Date(2026, 9, 31, 10, 0).getTime()
  const at = (d, h) => new Date(2026, 9, d, h, 0).toISOString()
  assert.equal(dayOf(at(31, 0), now), 'today')
  assert.equal(dayOf(at(30, 23), now), 'yesterday')
  assert.equal(dayOf(at(30, 0), now), 'yesterday')
  assert.equal(dayOf(at(29, 23), now), 'earlier')
  assert.equal(dayOf('', now), 'earlier')
  const items = [note({ id: 4, created: at(31, 9) }), note({ id: 3, created: at(31, 1) }), note({ id: 2, created: at(30, 8) }), note({ id: 1, created: at(2, 8) })]
  const groups = groupByDay(items, now)
  assert.deepEqual(groups.map(g => [g.day, g.items.map(n => n.id)]), [['today', [4, 3]], ['yesterday', [2]], ['earlier', [1]]])
  assert.deepEqual(groupByDay([], now), [])
})

test('inbox: the filters pick unread or failed items', () => {
  const items = [note({ id: 1 }), note({ id: 2, read: true, status: 'failed' }), note({ id: 3, status: 'failed' })]
  assert.deepEqual(filterItems(items, 'all').map(n => n.id), [1, 2, 3])
  assert.deepEqual(filterItems(items, 'unread').map(n => n.id), [1, 3])
  assert.deepEqual(filterItems(items, 'failed').map(n => n.id), [2, 3])
  assert.deepEqual(filterItems(undefined, 'all'), [])
})

test('inbox: the badge is red only when an unread item failed', () => {
  assert.equal(hasUnreadFailure([]), false)
  assert.equal(hasUnreadFailure(undefined), false)
  assert.equal(hasUnreadFailure([note(), note({ id: 2, status: 'failed', read: true })]), false)
  assert.equal(hasUnreadFailure([note(), note({ id: 2, status: 'failed' })]), true)
})

test('inbox: the navbar entry is for an administrator always and for others once they have items', () => {
  const base = { enabled: true, off: false, loaded: true, isAdmin: false, total: 0, everHad: false, open: false }
  // An administrator sees it with an empty inbox; anyone else does not.
  assert.equal(entryVisible({ ...base, isAdmin: true }), true)
  assert.equal(entryVisible(base), false)
  assert.equal(entryVisible({ ...base, total: 3 }), true)
  // It stays after the list was cleared, and while the panel is open.
  assert.equal(entryVisible({ ...base, everHad: true }), true)
  assert.equal(entryVisible({ ...base, open: true }), true)
})

test('inbox: nobody sees the entry when the inbox is off or not read yet', () => {
  const base = { enabled: true, off: false, loaded: true, isAdmin: true, total: 5, everHad: true, open: true }
  assert.equal(entryVisible({ ...base, enabled: false }), false)
  assert.equal(entryVisible({ ...base, off: true }), false)
  assert.equal(entryVisible({ ...base, loaded: false }), false)
})

test('inbox: 403, 404 and 409 mean there is no inbox, other failures are retried', () => {
  for (const s of [403, 404, 409]) assert.equal(inboxOff(s), true)
  for (const s of [undefined, 0, 401, 500, 502]) assert.equal(inboxOff(s), false)
})

test('inbox: the stream is open only for an enabled hook on a visible tab', () => {
  assert.equal(streamWanted({ enabled: true, visible: true, off: false }), true)
  assert.equal(streamWanted({ enabled: false, visible: true, off: false }), false)
  assert.equal(streamWanted({ enabled: true, visible: false, off: false }), false)
  assert.equal(streamWanted({ enabled: true, visible: true, off: true }), false)
})
