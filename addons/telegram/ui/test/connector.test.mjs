import test from 'node:test'
import assert from 'node:assert/strict'
import { canSave, connectorPayload, emptyConnector, modeText, senderLine } from '../../../internal/connector-ui/src/connector.js'

test('chat connectors: the token goes only when it was typed', () => {
  const create = connectorPayload({ ...emptyConnector(), name: ' Bot ', token: ' 1:abc ', agent_id: 'a1', allow: ['11', ' 11', '@ada_l', ''] }, 'telegram')
  assert.equal(create.token, '1:abc')
  assert.deepEqual(create.allow, ['11', '@ada_l'])
  assert.equal(create.name, 'Bot')
  assert.equal(create.kind, 'telegram')
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Bot', agent_id: 'a1' }, 'telegram')
  assert.equal('token' in update, false)
})

test('chat connectors: accept any drops the allowlist from the request', () => {
  const body = connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', accept_any: true, allow: ['11'] }, 'telegram')
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

test('chat connectors: a new binding shows progress, one chat per person and sends files', () => {
  const c = emptyConnector()
  assert.equal(c.reply_mode, 'progress')
  assert.equal(c.conversation, 'sender')
  assert.equal(c.send_files, true)
})

test('chat connectors: the request carries the reply mode, the conversation mode and the file switch', () => {
  const base = { ...emptyConnector(), name: 'B', agent_id: 'a' }
  const off = connectorPayload({ ...base, reply_mode: 'stream', conversation: 'thread', send_files: false, window_minutes: 30 }, 'telegram')
  assert.equal(off.reply_mode, 'stream')
  assert.equal(off.conversation, 'thread')
  assert.equal(off.send_files, false)
  assert.equal('window_minutes' in off, false, 'the window goes only with the window mode')
  const win = connectorPayload({ ...base, conversation: 'window', window_minutes: '45' }, 'telegram')
  assert.equal(win.window_minutes, 45)
  assert.equal(win.send_files, true)
})

test('chat connectors: a window needs 1 to 10080 minutes', () => {
  const ok = { ...emptyConnector(), name: 'B', agent_id: 'a', token: '1:x', conversation: 'window', window_minutes: 30 }
  assert.equal(canSave(ok, false), true)
  assert.equal(canSave({ ...ok, window_minutes: 0 }, false), false)
  assert.equal(canSave({ ...ok, window_minutes: 20000 }, false), false)
})
