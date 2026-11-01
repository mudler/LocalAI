import test from 'node:test'
import assert from 'node:assert/strict'
import { acceptsOf, groupConnectors } from '../../src/utils/chatConnectors.js'

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
