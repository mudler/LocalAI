import test from 'node:test'
import assert from 'node:assert/strict'
import { canSave, connectorPayload, emptyConnector, modeText, senderLine } from '../../src/utils/chatConnectors.js'

test('chat connectors: the token goes only when it was typed', () => {
  const create = connectorPayload({ ...emptyConnector(), name: ' Bot ', token: ' 1:abc ', agent_id: 'a1', allow: ['11', ' 11', '@ada_l', ''] })
  assert.equal(create.token, '1:abc')
  assert.deepEqual(create.allow, ['11', '@ada_l'])
  assert.equal(create.name, 'Bot')
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Bot', agent_id: 'a1' })
  assert.equal('token' in update, false)
})

test('chat connectors: accept any drops the allowlist from the request', () => {
  const body = connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', accept_any: true, allow: ['11'] })
  assert.equal(body.accept_any, true)
  assert.deepEqual(body.allow, [])
})

test('chat connectors: who a binding accepts, in words', () => {
  assert.equal(modeText({ accept_any: true }), 'anyone')
  assert.equal(modeText({ allow: [] }), 'nobody yet')
  assert.equal(modeText({ allow: ['1'] }), '1 listed sender')
  assert.equal(modeText({ allow: ['1', '2'] }), '2 listed senders')
})

test('chat connectors: a sender line names the person, the count and the id', () => {
  assert.equal(senderLine({ name: 'Ada', count: 1, id: '11' }), 'Ada · 1 message · 11')
  assert.equal(senderLine({ name: '', count: 3, id: '12' }), '12 · 3 messages · 12')
})

test('chat connectors: saving needs a name, an agent, a token for a new binding, and the warning confirmed for accept any', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x' }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, name: ' ' }, false), false)
  assert.equal(canSave({ ...ok, agent_id: '' }, false), false)
  assert.equal(canSave({ ...ok, token: '' }, false), false)
  assert.equal(canSave({ ...ok, token: '', id: 'b1' }, false), true)
  assert.equal(canSave({ ...ok, accept_any: true }, false), false)
  assert.equal(canSave({ ...ok, accept_any: true }, true), true)
})
