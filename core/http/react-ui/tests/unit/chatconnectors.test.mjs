import test from 'node:test'
import assert from 'node:assert/strict'
import { acceptsOf, bindingAddress, bindingsOfAgent, duplicateOf, groupConnectors, rowState } from '../../src/utils/chatConnectors.js'

const kinds = [{ addon: 'telegram', kind: 'telegram', title: 'Telegram', page: 'bot' }]

test('chat connectors: a binding goes under the add-on that serves it, with the address of its page', () => {
  const { groups, waiting } = groupConnectors(kinds, [{ id: 'b1', kind: 'telegram', addon: 'telegram' }])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].href, '/app/addons/telegram/bot')
  assert.deepEqual(groups[0].items.map((c) => c.id), ['b1'])
  assert.deepEqual(waiting, [])
})

test('chat connectors: a binding without a running add-on waits, and is not dropped', () => {
  const { groups, waiting } = groupConnectors([], [{ id: 'b1', kind: 'telegram', addon: 'telegram', active: false }])
  assert.deepEqual(groups, [])
  assert.deepEqual(waiting.map((c) => c.id), ['b1'])
})

test('chat connectors: another add-on does not take the bindings of a kind', () => {
  const { groups, waiting } = groupConnectors(kinds, [{ id: 'b1', kind: 'telegram', addon: 'other' }])
  assert.deepEqual(groups[0].items, [])
  assert.equal(waiting.length, 1)
})

test('chat connectors: a kind without a page links to the add-on in the gallery', () => {
  const { groups } = groupConnectors([{ addon: 'slack', kind: 'slack', title: 'Slack' }], [])
  assert.equal(groups[0].href, '/app/addons?detail=slack')
})

test('chat connectors: who a binding accepts', () => {
  assert.deepEqual(acceptsOf({ accept_any: true }), { key: 'anyone' })
  assert.deepEqual(acceptsOf({ allow: [] }), { key: 'nobody' })
  assert.deepEqual(acceptsOf({ allow: ['1', '2'] }), { key: 'listed', count: 2 })
})

test('chat connectors: a kind whose add-on logs in with OAuth, and needs no agent, offers to add a connection here', () => {
  const { groups } = groupConnectors([
    { addon: 'providera', kind: 'providera', title: 'Provider A', auth_kind: 'oauth2', agent_optional: true },
    { addon: 'agentful', kind: 'agentful', title: 'Agentful', auth_kind: 'oauth2' },
    { addon: 'telegram', kind: 'telegram', title: 'Telegram', agent_optional: true },
  ], [])
  assert.deepEqual(groups.map((g) => g.addsLogin), [true, false, false])
})

test('chat connectors: the other binding of a bot that is connected twice', () => {
  assert.equal(duplicateOf({ id: 'a' }), null)
  assert.equal(duplicateOf({ id: 'a', duplicate: { code: 'duplicate_connection', others: [] } }), null)
  assert.deepEqual(duplicateOf({ id: 'a', duplicate: { others: [{ id: 'b', name: 'Team app' }, { id: 'c', name: 'Third' }] } }), { id: 'b', name: 'Team app' })
})

test('chat connectors: a link to a binding opens the page of its add-on on that binding', () => {
  const { groups } = groupConnectors(kinds, [])
  const c = { kind: 'telegram', addon: 'telegram' }
  assert.equal(bindingAddress(groups, c, 'b 2'), '/app/addons/telegram/bot?binding=b%202')
  const noPage = groupConnectors([{ addon: 'slack', kind: 'slack', title: 'Slack' }], []).groups
  assert.equal(bindingAddress(noPage, { kind: 'slack', addon: 'slack' }, 'b2'), '/app/addons?detail=slack&binding=b2')
  assert.equal(bindingAddress(groups, { kind: 'telegram', addon: 'other' }, 'b2'), '', 'no running add-on, no link')
})

const agentKinds = [
  { addon: 'chat-a', kind: 'chat-a', title: 'Chat A', page: 'bots' },
  { addon: 'chat-b', kind: 'room', title: 'Room B', page: 'rooms' },
]

test('agent bindings: lists only the bindings of the agent, from any add-on, with a deep link', () => {
  const items = [
    { id: 'b1', name: 'General', kind: 'chat-a', addon: 'chat-a', agent_id: 'a1', enabled: true, active: true },
    { id: 'b2', name: 'Ops', kind: 'room', addon: 'chat-b', agent_id: 'a1', enabled: true },
    { id: 'b3', name: 'Other', kind: 'chat-a', addon: 'chat-a', agent_id: 'a2', enabled: true },
  ]
  const rows = bindingsOfAgent('a1', agentKinds, items)
  assert.deepEqual(rows.map((r) => [r.id, r.kindTitle, r.state, r.href]), [
    ['b1', 'Chat A', 'ok', '/app/addons/chat-a/bots?binding=b1'],
    ['b2', 'Room B', 'ok', '/app/addons/chat-b/rooms?binding=b2'],
  ])
})

test('agent bindings: not running or answering twice needs attention, switched off is off', () => {
  const items = [
    { id: 'b1', name: 'A', kind: 'chat-a', addon: 'chat-a', agent_id: 'a1', enabled: true, active: false, reason: 'token refused' },
    { id: 'b2', name: 'B', kind: 'chat-a', addon: 'chat-a', agent_id: 'a1', enabled: true, duplicate: { others: [{ id: 'b9', name: 'C' }] } },
    { id: 'b3', name: 'C', kind: 'chat-a', addon: 'chat-a', agent_id: 'a1', enabled: false },
  ]
  const rows = bindingsOfAgent('a1', agentKinds, items)
  assert.deepEqual(rows.map((r) => r.state), ['attention', 'attention', 'off'])
  assert.equal(rows[0].reason, 'token refused')
})

test('agent bindings: a kind no add-on serves links to the add-on in the gallery', () => {
  const rows = bindingsOfAgent('a1', [], [{ id: 'b1', name: 'A', kind: 'room', addon: 'chat-b', agent_id: 'a1', enabled: true }])
  assert.equal(rows[0].kindTitle, 'room')
  assert.equal(rows[0].href, '/app/addons?detail=chat-b')
})

test('agent bindings: an agent with none, or no agent ID, gets an empty list', () => {
  assert.deepEqual(bindingsOfAgent('a1', agentKinds, []), [])
  assert.deepEqual(bindingsOfAgent('', agentKinds, [{ id: 'b1', agent_id: '' }]), [])
  assert.deepEqual(bindingsOfAgent('a1', null, null), [])
})

test('chat connectors: a kind with a declared page has an address that opens its add sheet, and one without has none', () => {
  const { groups } = groupConnectors([...kinds, { addon: 'slack', kind: 'slack', title: 'Slack' }], [])
  assert.equal(groups[0].newHref, '/app/addons/telegram/bot?new=1')
  assert.equal(groups[1].newHref, '')
})

test('chat connectors: a row is running, off or needs attention', () => {
  const ok = { id: 'b', enabled: true, active: true, has_token: true }
  assert.equal(rowState(ok), 'running')
  assert.equal(rowState({ ...ok, enabled: false }), 'off')
  assert.equal(rowState({ ...ok, has_token: false }), 'attention')
  assert.equal(rowState({ ...ok, duplicate: { others: [{ id: 'c', name: 'C' }] } }), 'attention')
  assert.equal(rowState({ ...ok, active: false, enabled: false, reason: 'install it' }), 'attention')
})
