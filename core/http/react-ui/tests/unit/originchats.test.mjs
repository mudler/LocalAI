import test from 'node:test'
import assert from 'node:assert/strict'
import {
  attention, chatStatus, groupByOrigin, needsYouIds, readTab, resolveOrigin, splitByOrigin, writeTab,
} from '../../src/utils/originChats.js'

const chat = (id, origin, originId, updatedAt, extra = {}) => ({ id, origin, originId, updatedAt, running: false, lastTurn: 'done', ...extra })
const ctx = {
  connectors: [{ id: 'b1', name: 'Team bot', kind: 'chatapp' }, { id: 'b2', name: '', kind: 'chatapp' }],
  connectorKinds: [{ kind: 'chatapp', title: 'Chat App' }],
  triggers: [{ id: 't1', name: 'Morning digest' }],
}

test('origin chats: the person and the others are split by origin', () => {
  const { person, automated } = splitByOrigin([chat('a', '', '', 1), chat('b', 'connector', 'b1', 2), chat('c', 'someday', 'x', 3)])
  assert.deepEqual(person.map(c => c.id), ['a'])
  assert.deepEqual(automated.map(c => c.id), ['b', 'c'])
})

test('origin chats: a connector takes the name of its binding, then the title of its kind', () => {
  assert.equal(resolveOrigin(chat('a', 'connector', 'b1', 1), ctx).title, 'Team bot')
  assert.equal(resolveOrigin(chat('a', 'connector', 'b2', 1), ctx).title, 'Chat App')
  assert.equal(resolveOrigin(chat('a', 'connector', 'gone', 1), ctx).title, '')
  assert.equal(resolveOrigin(chat('a', 'connector', 'b1', 1), ctx).href, '/app/chats/connectors')
})

test('origin chats: a connector links to the page of the add-on that serves its kind', () => {
  const withPage = { ...ctx, connectors: [{ id: 'b1', name: 'Team bot', kind: 'chatapp', addon: 'chatapp' }], connectorKinds: [{ addon: 'chatapp', kind: 'chatapp', title: 'Chat App', page: 'bot' }] }
  assert.equal(resolveOrigin(chat('a', 'connector', 'b1', 1), withPage).href, '/app/addons/chatapp/bot')
})

test('origin chats: a trigger, a job and a page are named by their list or their id', () => {
  assert.equal(resolveOrigin(chat('a', 'trigger', 't1', 1), ctx).title, 'Morning digest')
  assert.equal(resolveOrigin(chat('a', 'trigger', 't1', 1), ctx).href, '/app/triggers/t1')
  assert.equal(resolveOrigin(chat('a', 'job', 'nightly', 1), ctx).title, 'nightly')
  assert.equal(resolveOrigin(chat('a', 'page', 'notes', 1), ctx).href, '/app/pages/notes')
})

test('origin chats: an origin this release does not know gets no title and no link', () => {
  const o = resolveOrigin(chat('a', 'future', 'f1', 1), ctx)
  assert.deepEqual([o.kind, o.title, o.href], ['future', '', ''])
})

test('origin chats: groups go by kind, then by origin, newest first, with counts', () => {
  const chats = [
    chat('a', 'connector', 'b1', 10), chat('b', 'connector', 'b1', 30), chat('c', 'connector', 'b2', 20),
    chat('d', 'trigger', 't1', 5), chat('e', 'future', 'f1', 99), chat('p', '', '', 100),
  ]
  const kinds = groupByOrigin(chats, ctx, new Set())
  assert.deepEqual(kinds.map(k => k.kind), ['trigger', 'connector', 'future'])
  const conn = kinds[1]
  assert.equal(conn.count, 3)
  assert.deepEqual(conn.groups.map(g => g.id), ['b1', 'b2'])
  assert.equal(conn.groups[0].count, 2)
  assert.equal(conn.groups[0].lastActive, 30)
  assert.deepEqual(conn.groups[0].chats.map(c => c.id), ['b', 'a'])
  assert.ok(!JSON.stringify(kinds).includes('"p"'), 'the chat of a person is not grouped')
})

test('origin chats: a running turn and a chat that needs the person are counted apart', () => {
  const chats = [
    chat('r', 'connector', 'b1', 3, { running: true }),
    chat('w', 'connector', 'b1', 2, { lastTurn: 'paused' }),
    chat('n', 'trigger', 't1', 1),
    chat('i', 'trigger', 't1', 0),
  ]
  const needs = new Set(['n'])
  assert.equal(chatStatus(chats[0], needs), 'running')
  assert.equal(chatStatus(chats[1], needs), 'waiting')
  assert.equal(chatStatus(chats[2], needs), 'waiting')
  assert.equal(chatStatus(chats[3], needs), 'idle')
  assert.deepEqual(attention(chats, needs), { waiting: 2, running: 1 })
  const conn = groupByOrigin(chats, ctx, needs).find(k => k.kind === 'connector')
  assert.deepEqual([conn.running, conn.waiting], [1, 1])
})

test('origin chats: only unread items that ask for the person count, and they link to a chat', () => {
  const ids = needsYouIds([
    { kind: 'needs_attention', read: false, link: '/app/chats/c1' },
    { kind: 'run_failed', read: false, link: '/app/chats/c%202?x=1' },
    { kind: 'run_ended', read: false, link: '/app/chats/c3' },
    { kind: 'needs_attention', read: true, link: '/app/chats/c4' },
    { kind: 'needs_attention', read: false, link: '/app/triggers' },
  ])
  assert.deepEqual([...ids].sort(), ['c 2', 'c1'])
})

test('origin chats: the tab is remembered, and a storage that throws does not break it', () => {
  const mem = new Map()
  const storage = { getItem: (k) => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v) }
  assert.equal(readTab(storage), 'mine')
  writeTab('automated', storage)
  assert.equal(readTab(storage), 'automated')
  mem.set('localai_all_chats_tab', 'nonsense')
  assert.equal(readTab(storage), 'mine')
  const broken = { getItem() { throw new Error('denied') }, setItem() { throw new Error('denied') } }
  assert.equal(readTab(broken), 'mine')
  assert.doesNotThrow(() => writeTab('automated', broken))
})
